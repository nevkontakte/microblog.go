package judge

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// A Fake is a minimal remote actor under the judge's control, living at
// https://<name>.test. It sends crafted activities that a microblog.pub peer
// never would, as the original test suite's fixtures did, and later replays
// captured Mastodon traffic. It keeps the protocol model small on purpose:
// the paired instances, not the fake, are the main judge (ADR 0010).
type Fake struct {
	Name        string
	AlsoKnownAs []string
	AutoAccept  bool // answer Follow with Accept
	Gone        bool // answer every GET with 410, like a deleted account

	t       *T // reports what goes wrong in the background
	key     *rsa.PrivateKey
	client  *http.Client
	sending atomic.Int32 // Accepts being sent in the background
	accepts atomic.Int32 // Accepts started, ever

	mu      sync.Mutex
	seq     int // numbers activity ids, so they are the same on every run
	inbox   []map[string]any
	objects map[string]map[string]any
}

// NewFake creates a fake actor and puts it on t's network.
func (t *T) NewFake(name string) *Fake {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	t.Check(err)
	addr, err := t.Proxy.Listen(name)
	t.Check(err)
	proxy, err := url.Parse("http://" + addr)
	t.Check(err)
	f := &Fake{
		Name: name,
		t:    t,
		key:  key,
		client: &http.Client{
			Transport: &http.Transport{Proxy: http.ProxyURL(proxy), TLSClientConfig: &tls.Config{RootCAs: t.Proxy.CertPool()}},
			Timeout:   Timeout,
		},
		objects: make(map[string]map[string]any),
	}
	t.Proxy.Handle(name+".test", f)
	t.fakes = append(t.fakes, f)
	return f
}

// ID is the actor's id, also its base URL, as with microblog.pub.
func (f *Fake) ID() string { return "https://" + f.Name + ".test" }

// Inbox returns the activities delivered to the fake so far.
func (f *Fake) Inbox() []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]map[string]any(nil), f.inbox...)
}

// Publish makes obj fetchable at its id.
func (f *Fake) Publish(obj map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.objects[obj["id"].(string)] = obj
}

// Send delivers activity to inst's inbox, signed like Mastodon signs.
func (f *Fake) Send(ctx context.Context, inst Instance, activity map[string]any) (*Response, error) {
	return f.post(ctx, BaseURL(inst)+"/inbox", activity, signing{key: f.key})
}

// SendUnsigned delivers activity without an HTTP signature.
func (f *Fake) SendUnsigned(ctx context.Context, inst Instance, activity map[string]any) (*Response, error) {
	return f.post(ctx, BaseURL(inst)+"/inbox", activity, signing{})
}

// signing says how post signs a request: not at all without a key.
type signing struct {
	key *rsa.PrivateKey
}

func (f *Fake) post(ctx context.Context, inbox string, activity map[string]any, s signing) (*Response, error) {
	body, err := json.Marshal(activity)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, inbox, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/activity+json")
	req.Header.Set("Date", time.Now().UTC().Format(http.TimeFormat))
	if s.key != nil {
		digest := sha256.Sum256(body)
		req.Header.Set("Digest", "SHA-256="+base64.StdEncoding.EncodeToString(digest[:]))
		if err := f.sign(req, s.key, "(request-target) host date digest content-type"); err != nil {
			return nil, err
		}
	}
	return f.do(req)
}

func (f *Fake) do(req *http.Request) (_ *Response, err error) {
	resp, err := f.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, resp.Body.Close()) }()
	body, err := io.ReadAll(resp.Body)
	return &Response{Status: resp.StatusCode, Header: resp.Header, Body: body}, err
}

// sign adds a draft-cavage HTTP signature with key over the given headers.
func (f *Fake) sign(req *http.Request, key *rsa.PrivateKey, headers string) error {
	var lines []string
	for _, h := range strings.Fields(headers) {
		switch h {
		case "(request-target)":
			lines = append(lines, h+": "+strings.ToLower(req.Method)+" "+req.URL.RequestURI())
		case "host":
			lines = append(lines, "host: "+req.URL.Host)
		default:
			lines = append(lines, h+": "+req.Header.Get(h))
		}
	}
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if err != nil {
		return err
	}
	req.Header.Set("Signature", fmt.Sprintf(`keyId="%s#main-key",algorithm="rsa-sha256",headers="%s",signature="%s"`,
		f.ID(), headers, base64.StdEncoding.EncodeToString(sig)))
	return nil
}

func (f *Fake) actor() map[string]any {
	pub := must(x509.MarshalPKIXPublicKey(&f.key.PublicKey))
	return map[string]any{
		"@context":          []any{"https://www.w3.org/ns/activitystreams", "https://w3id.org/security/v1"},
		"type":              "Person",
		"id":                f.ID(),
		"preferredUsername": f.Name,
		"name":              "Fake " + f.Name,
		"inbox":             f.ID() + "/inbox",
		"outbox":            f.ID() + "/outbox",
		"followers":         f.ID() + "/followers",
		"following":         f.ID() + "/following",
		"alsoKnownAs":       f.AlsoKnownAs,
		"publicKey": map[string]any{
			"id":           f.ID() + "#main-key",
			"owner":        f.ID(),
			"publicKeyPem": string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pub})),
		},
	}
}

func (f *Fake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet && f.Gone {
		http.Error(w, "gone", http.StatusGone)
		return
	}
	id := f.ID() + r.URL.Path
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/inbox":
		var activity map[string]any
		if err := json.NewDecoder(r.Body).Decode(&activity); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		f.mu.Lock()
		f.inbox = append(f.inbox, activity)
		f.mu.Unlock()
		w.WriteHeader(http.StatusAccepted)
		if f.AutoAccept && activity["type"] == "Follow" {
			f.sending.Add(1) // before the Follow is answered, see settle
			f.accepts.Add(1)
			go func() {
				defer f.sending.Add(-1)
				f.accept(activity)
			}()
		}
	case r.URL.Path == "/" || r.URL.Path == "":
		writeAP(w, f.actor())
	case r.URL.Path == "/.well-known/webfinger":
		w.Header().Set("Content-Type", "application/jrd+json")
		err := json.NewEncoder(w).Encode(map[string]any{
			"subject": fmt.Sprintf("acct:%s@%s.test", f.Name, f.Name),
			"links":   []any{map[string]any{"rel": "self", "type": "application/activity+json", "href": f.ID()}},
		})
		if err != nil {
			panic(err) // fails the run, see Proxy.exchange
		}
	case r.URL.Path == "/outbox" || r.URL.Path == "/followers" || r.URL.Path == "/following":
		writeAP(w, map[string]any{
			"@context": "https://www.w3.org/ns/activitystreams", "id": id,
			"type": "OrderedCollection", "totalItems": 0, "orderedItems": []any{},
		})
	default:
		f.mu.Lock()
		obj := f.objects[id]
		f.mu.Unlock()
		if obj == nil {
			http.NotFound(w, r)
			return
		}
		writeAP(w, obj)
	}
}

func (f *Fake) accept(follow map[string]any) {
	actor, ok := follow["actor"].(string)
	if !ok {
		f.t.Errorf("%s got a Follow without an actor: %v", f.Name, follow)
		return
	}
	accept := f.Activity("Accept", follow["id"])
	accept["to"] = []any{actor}
	resp, err := f.post(context.Background(), actor+"/inbox", accept, signing{key: f.key})
	if err == nil && resp.Status >= 300 {
		err = fmt.Errorf("status %d: %s", resp.Status, resp.Body)
	}
	if err != nil {
		f.t.Errorf("%s accepting a Follow from %s: %v", f.Name, actor, err)
	}
}

// Activity builds an activity from the fake, shaped like the original test
// suite's factories (tests/factories.py).
func (f *Fake) Activity(typ string, object any) map[string]any {
	f.mu.Lock()
	f.seq++
	n := f.seq
	f.mu.Unlock()
	return map[string]any{
		"@context": "https://www.w3.org/ns/activitystreams",
		"type":     typ,
		"id":       fmt.Sprintf("%s/%s/%d", f.ID(), strings.ToLower(typ), n),
		"actor":    f.ID(),
		"object":   object,
	}
}

// asPublic is the public collection, for addressing objects to everyone.
const asPublic = "https://www.w3.org/ns/activitystreams#Public"

// Note builds a Note by the fake, published now, addressed to to.
func (f *Fake) Note(content string, to ...string) map[string]any {
	note := f.Activity("Note", nil)
	delete(note, "actor")
	delete(note, "object")
	note["attributedTo"] = f.ID()
	note["content"] = content
	note["to"] = to
	note["cc"] = []any{}
	note["published"] = time.Now().UTC().Truncate(time.Second).Format(time.RFC3339)
	return note
}

// Create wraps obj in a Create activity, as tests/factories.py does.
func (f *Fake) Create(obj map[string]any) map[string]any {
	inner := make(map[string]any, len(obj))
	for k, v := range obj {
		if k != "@context" {
			inner[k] = v
		}
	}
	return map[string]any{
		"@context":  "https://www.w3.org/ns/activitystreams",
		"type":      "Create",
		"id":        obj["id"].(string) + "/activity",
		"actor":     obj["attributedTo"],
		"to":        obj["to"],
		"cc":        obj["cc"],
		"published": obj["published"],
		"object":    inner,
	}
}

func writeAP(w http.ResponseWriter, obj any) {
	w.Header().Set("Content-Type", "application/activity+json")
	if err := json.NewEncoder(w).Encode(obj); err != nil {
		panic(err) // fails the run, see Proxy.exchange
	}
}
