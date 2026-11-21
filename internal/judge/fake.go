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

// A Fake is a minimal remote actor that the judge controls, at
// https://<name>.test. It sends special activities that a microblog.pub peer
// never sends, as the fixtures of the original test suite did. The fake keeps
// its protocol model small on purpose. The pair, not the fake, is the main
// judge (ADR 0010).
type Fake struct {
	Name        string
	AlsoKnownAs []string
	AutoAccept  bool // answer each Follow with an Accept
	Gone        bool // answer each GET with 410, as for a deleted account

	t       *T // reports the errors of background work
	key     *rsa.PrivateKey
	client  *http.Client
	sending atomic.Int32 // the number of Accepts that background work sends now
	accepts atomic.Int32 // the number of Accepts that the fake started in total

	mu       sync.Mutex
	seq      int // gives numbers to activity ids, so the ids are the same in all runs
	inbox    []map[string]any
	objects  map[string]map[string]any
	pages    map[string]string // the HTML of each page, by URL
	mentions []url.Values      // the webmentions that the fake received, see ServeHTTP
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
		pages:   make(map[string]string),
	}
	t.Proxy.Handle(name+".test", f)
	t.fakes = append(t.fakes, f)
	return f
}

// ID is the actor's id. As with microblog.pub, the id is also the base URL.
func (f *Fake) ID() string { return "https://" + f.Name + ".test" }

// Inbox returns the activities delivered to the fake so far.
func (f *Fake) Inbox() []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]map[string]any(nil), f.inbox...)
}

// Webmentions returns the webmentions that instances sent to the fake's
// endpoint, /webmention, so far. Each webmention has its source and target.
func (f *Fake) Webmentions() []url.Values {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]url.Values(nil), f.mentions...)
}

// Publish makes obj fetchable at its id.
func (f *Fake) Publish(obj map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.objects[obj["id"].(string)] = obj
}

// PublishPage serves html at url, as the web page of an object.
func (f *Fake) PublishPage(url, html string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pages[url] = html
}

// Send delivers activity to inst's inbox, with a signature in Mastodon's
// form.
func (f *Fake) Send(ctx context.Context, inst Instance, activity map[string]any) (*Response, error) {
	return f.post(ctx, BaseURL(inst)+"/inbox", activity, signing{key: f.key})
}

// SendUnsigned delivers activity without an HTTP signature.
func (f *Fake) SendUnsigned(ctx context.Context, inst Instance, activity map[string]any) (*Response, error) {
	return f.post(ctx, BaseURL(inst)+"/inbox", activity, signing{})
}

// SendBadDigest delivers activity with a Digest header that doesn't match the
// body. The HTTP signature over this header is correct.
func (f *Fake) SendBadDigest(ctx context.Context, inst Instance, activity map[string]any) (*Response, error) {
	return f.post(ctx, BaseURL(inst)+"/inbox", activity, signing{key: f.key, signedDigest: zeroDigest})
}

// SendWrongDigestHeader delivers activity with a signature over the body's
// real digest, and with a Digest header that doesn't match the body.
func (f *Fake) SendWrongDigestHeader(ctx context.Context, inst Instance, activity map[string]any) (*Response, error) {
	return f.post(ctx, BaseURL(inst)+"/inbox", activity, signing{key: f.key, sentDigest: zeroDigest})
}

var zeroDigest = "SHA-256=" + base64.StdEncoding.EncodeToString(make([]byte, 32))

// SendWrongKey delivers activity with a signature from a key that isn't the
// fake's key.
func (f *Fake) SendWrongKey(ctx context.Context, inst Instance, activity map[string]any) (*Response, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}
	return f.post(ctx, BaseURL(inst)+"/inbox", activity, signing{key: key})
}

// Fetch fetches an ActivityPub object as the fake. If signed is true, the
// request has an HTTP signature, as servers use to fetch objects with
// restricted visibility.
func (f *Fake) Fetch(ctx context.Context, url string, signed bool) (*Response, error) {
	var key *rsa.PrivateKey
	if signed {
		key = f.key
	}
	return f.fetch(ctx, url, key)
}

// FetchWrongKey fetches url with a signature from a key that isn't the fake's
// key.
func (f *Fake) FetchWrongKey(ctx context.Context, url string) (*Response, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}
	return f.fetch(ctx, url, key)
}

func (f *Fake) fetch(ctx context.Context, url string, key *rsa.PrivateKey) (*Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/activity+json")
	req.Header.Set("Date", time.Now().UTC().Format(http.TimeFormat))
	if key != nil {
		if err := f.sign(req, key, "(request-target) host date"); err != nil {
			return nil, err
		}
	}
	return f.do(req)
}

// signing tells how post signs a request:
//   - Without a key, post doesn't sign the request.
//   - post signs the body's digest, or signedDigest if it isn't empty.
//   - After the signature, sentDigest replaces the Digest header if it isn't
//     empty.
type signing struct {
	key                      *rsa.PrivateKey
	signedDigest, sentDigest string
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
		if s.signedDigest != "" {
			req.Header.Set("Digest", s.signedDigest)
		}
		if err := f.sign(req, s.key, "(request-target) host date digest content-type"); err != nil {
			return nil, err
		}
		if s.sentDigest != "" {
			req.Header.Set("Digest", s.sentDigest)
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
	return signRequest(req, key, f.ID()+"#main-key", "rsa-sha256", headers)
}

// signRequest adds a draft-cavage HTTP signature with key over the given
// headers, in that order. The signature names keyID and algorithm.
//
// signRequest always signs with RSASSA-PKCS1-v1_5 and SHA-256, which
// "rsa-sha256" names. The draft gives a different definition of "hs2019":
// the algorithm that the key's metadata names. For an RSA key, that is
// RSASSA-PSS with SHA-512 (draft-cavage-http-signatures-12, appendix E.2).
// But servers send "hs2019" with the same RSA-SHA256 signature. The original
// verifies all signatures with RSA-SHA256, and ignores the algorithm
// (app/httpsig.py:79-83, 194).
func signRequest(req *http.Request, key *rsa.PrivateKey, keyID, algorithm, headers string) error {
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
	req.Header.Set("Signature", fmt.Sprintf(`keyId="%s",algorithm="%s",headers="%s",signature="%s"`,
		keyID, algorithm, headers, base64.StdEncoding.EncodeToString(sig)))
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
			f.sending.Add(1) // before the fake answers the Follow, see settle
			f.accepts.Add(1)
			go func() {
				defer f.sending.Add(-1)
				f.accept(activity)
			}()
		}
	case r.Method == http.MethodPost && r.URL.Path == "/webmention":
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		f.mu.Lock()
		f.mentions = append(f.mentions, r.PostForm)
		f.mu.Unlock()
		w.WriteHeader(http.StatusAccepted)
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
		obj, page := f.objects[id], f.pages[id]
		f.mu.Unlock()
		if page != "" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			if _, err := io.WriteString(w, page); err != nil {
				panic(err) // fails the run, see Proxy.exchange
			}
			return
		}
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

// Activity makes an activity from the fake, in the same shape as the
// factories of the original test suite (tests/factories.py).
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

// asPublic is the public collection. Address an object to it to make the
// object public.
const asPublic = "https://www.w3.org/ns/activitystreams#Public"

// Note makes a Note by the fake, with the current time as its publication
// time, addressed to to.
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

// Create puts obj in a Create activity, as tests/factories.py does.
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
