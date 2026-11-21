package judge

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// mastodonReplaySet holds what a real Mastodon server sent a microblog.pub
// instance (mastodon_record.go).
//
//go:embed testdata/mastodon.json
var mastodonReplaySet []byte

// A Replay plays the Mastodon server of the replay set on t's network. It
// serves the documents that Mastodon served, and delivers what Mastodon
// delivered, step by step. It replaces the ids of a with the ids of the
// replaying instance.
//
// The Replay signs everything again with its own key. The actor documents
// that it serves hold this key in place of Mastodon's key. The Replay makes
// these signatures:
//   - HTTP signatures in Mastodon's exact form.
//   - LD signatures over the delivered payload, with the same creator and
//     creation time.
//
// See docs/adr/0014-record-the-mastodon-replay-set-from-a-real-server.md.
type Replay struct {
	Set ReplaySet

	t      *T
	key    *rsa.PrivateKey
	client *http.Client

	mu           sync.Mutex
	placeholders map[string]string   // the ids of the replaying instance, by placeholder name
	documents    map[string]Document // the documents that the server serves now, by URL
	inbox        []map[string]any    // the activities that instances sent to the server
	idless       map[string]int      // the number of deliveries without an id, by instance
}

// NewReplay puts the replay set's Mastodon server on t's network.
func (t *T) NewReplay() *Replay {
	var set ReplaySet
	t.Check(json.Unmarshal(mastodonReplaySet, &set))
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	t.Check(err)
	host := must(url.Parse(set.Actor)).Host
	addr, err := t.Proxy.Listen(strings.TrimSuffix(host, ".test"))
	t.Check(err)
	proxy := must(url.Parse("http://" + addr))
	r := &Replay{
		Set: set, t: t, key: key,
		client: &http.Client{
			Transport: &http.Transport{Proxy: http.ProxyURL(proxy), TLSClientConfig: &tls.Config{RootCAs: t.Proxy.CertPool()}},
			Timeout:   Timeout,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		placeholders: map[string]string{},
		documents:    map[string]Document{},
		idless:       map[string]int{},
	}
	t.Proxy.Handle(host, r)
	return r
}

// SetPlaceholder sets id as the value of the placeholder {{name}}, in all
// locations of the replay set. id is the id of an object of the replaying
// instance.
func (r *Replay) SetPlaceholder(name, id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.placeholders[name] = id
}

// Step returns the recorded step with the given name.
func (r *Replay) Step(name string) ReplayStep {
	for _, s := range r.Set.Steps {
		if s.Name == name {
			return s
		}
	}
	r.t.Fatalf("the replay set has no step %s", name)
	return ReplayStep{}
}

// Serve adds the documents that a fetched during step to the documents that
// the server serves. A new document replaces an old document at the same URL.
// The server keeps the documents of the earlier steps, because Mastodon
// continued to serve them. The replay set records only what a fetched during
// each step. Thus, at a step, Mastodon served all the documents that the
// replay set recorded up to that step, each at its latest version.
func (r *Replay) Serve(step ReplayStep) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, d := range step.Documents {
		r.documents[d.URL] = d
	}
}

// Activity returns the step's n-th delivery, with the ids of the replaying
// instance in place of the placeholders.
func (r *Replay) Activity(step ReplayStep, n int) map[string]any {
	if n >= len(step.Deliveries) {
		r.t.Fatalf("step %s has %d deliveries, not %d", step.Name, len(step.Deliveries), n+1)
	}
	body, err := r.fillPlaceholders(step.Deliveries[n].Activity)
	r.t.Check(err)
	var activity map[string]any
	r.t.Check(json.Unmarshal(body, &activity))
	return activity
}

// fillPlaceholders replaces the placeholders in body with their ids. It fails
// if a placeholder has no id.
func (r *Replay) fillPlaceholders(body json.RawMessage) (json.RawMessage, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := string(body)
	for name, id := range r.placeholders {
		s = strings.ReplaceAll(s, "{{"+name+"}}", id)
	}
	if i := strings.Index(s, "{{"); i >= 0 {
		return nil, fmt.Errorf("unset placeholder in the replay set: %.40s", s[i:])
	}
	return json.RawMessage(s), nil
}

// Play plays a step to i. It serves the step's documents. Then it delivers
// the step's activities in order. For each activity, it checks that i
// accepts the activity, and waits until i processed it.
func (r *Replay) Play(i Instance, step ReplayStep) {
	r.Serve(step)
	for n := range step.Deliveries {
		r.PlayOne(i, step, n)
	}
}

// PlayOne delivers the step's n-th activity to i. It checks that i accepts
// the activity, and waits until i processed it. PlayOne sets the ids of the
// placeholders at this time. Thus an earlier delivery can make the object
// whose id a later delivery needs.
func (r *Replay) PlayOne(i Instance, step ReplayStep, n int) {
	activity := r.Activity(step, n)
	resp := r.Send(i, activity, step.Deliveries[n])
	r.t.equal(fmt.Sprintf("%s delivery %d", step.Name, n+1), resp.Status, http.StatusAccepted)
	if id, ok := activity["id"]; ok {
		r.t.waitProcessed(i, id)
		return
	}
	// Mastodon sends some activities without an id, such as the Add of a
	// pinned post. The original keeps them under a hash of the payload. Wait
	// until i processed one more of those activities.
	r.mu.Lock()
	r.idless[i.Name()]++
	n = r.idless[i.Name()]
	r.mu.Unlock()
	r.t.waitRows(i, n, `SELECT 1 FROM incoming_activity WHERE ap_id NOT LIKE 'http%' AND is_processed = 1`)
}

// Send delivers activity to i's inbox in the same form as d. It signs again
// with the replay's key:
//   - It replaces the LD signature of activity, if there is one. The new
//     signature has the same creator and creation time.
//   - It makes an HTTP signature with the key id, algorithm and signed
//     headers of d. It sends the extra headers that this signature covers
//     with their recorded values.
func (r *Replay) Send(i Instance, activity map[string]any, d Delivery) *Response {
	r.Resign(activity)
	body, err := json.Marshal(activity)
	r.t.Check(err)
	req, err := http.NewRequestWithContext(r.t.Ctx, http.MethodPost, BaseURL(i)+"/inbox", bytes.NewReader(body))
	r.t.Check(err)
	req.Header.Set("Content-Type", d.ContentType)
	req.Header.Set("Date", time.Now().UTC().Format(http.TimeFormat))
	for k, v := range d.Headers {
		req.Header.Set(k, v)
	}
	digest := sha256.Sum256(body)
	req.Header.Set("Digest", "SHA-256="+base64.StdEncoding.EncodeToString(digest[:]))
	algorithm, headers := signatureForm(d.Signature)
	r.t.Check(signRequest(req, r.key, signatureKeyID(d.Signature), algorithm, headers))
	resp, err := r.client.Do(req)
	r.t.Check(err)
	defer func() { r.t.Check(resp.Body.Close()) }()
	respBody, err := io.ReadAll(resp.Body)
	r.t.Check(err)
	return &Response{Status: resp.StatusCode, Header: resp.Header, Body: respBody}
}

// Resign signs the LD signature of activity again with the replay's key. The
// new signature has the same creator and creation time. Resign doesn't change
// an activity without an LD signature.
func (r *Replay) Resign(activity map[string]any) {
	sig, ok := activity["signature"].(map[string]any)
	if !ok {
		return
	}
	created, err := time.Parse(time.RFC3339, stringField(r.t, sig, "created"))
	r.t.Check(err)
	r.t.Check(ldSign(activity, stringField(r.t, sig, "creator"), created, r.key))
}

// signatureKeyID reads the key id from the recorded parameters of a
// signature (signatureParams).
func signatureKeyID(signature string) string {
	for _, part := range strings.Split(signature, ",") {
		if name, value, _ := strings.Cut(part, "="); name == "keyId" {
			return strings.Trim(value, `"`)
		}
	}
	return ""
}

// Inbox returns the activities that instances delivered to the replayed
// server so far.
func (r *Replay) Inbox() []map[string]any {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]map[string]any(nil), r.inbox...)
}

// ServeHTTP serves the documents that Serve added so far. Actors have the
// replay's public key in place of Mastodon's key. ServeHTTP also accepts
// deliveries to all inboxes.
func (r *Replay) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if req.Method == http.MethodPost && strings.HasSuffix(req.URL.Path, "/inbox") {
		var activity map[string]any
		if err := json.NewDecoder(req.Body).Decode(&activity); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		r.mu.Lock()
		r.inbox = append(r.inbox, activity)
		r.mu.Unlock()
		w.WriteHeader(http.StatusAccepted)
		return
	}
	r.mu.Lock()
	d, ok := r.documents[req.URL.String()] // the proxy forwards the absolute URL
	r.mu.Unlock()
	if !ok {
		http.NotFound(w, req)
		return
	}
	body, err := r.fillPlaceholders(d.Body)
	if err != nil { // a scenario served a document before it set the placeholders of the document
		r.t.Errorf("serving %s: %v", req.URL, err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	body = r.withReplayKey(body)
	var text string
	if json.Unmarshal(body, &text) == nil {
		body = []byte(text) // the recorder kept a body that isn't JSON as a string, for example a redirect's page
	}
	if d.ContentType != "" {
		w.Header().Set("Content-Type", d.ContentType)
	}
	if d.Location != "" {
		w.Header().Set("Location", d.Location)
	}
	w.WriteHeader(d.Status)
	if _, err := w.Write(body); err != nil {
		panic(err) // fails the run, see Proxy.exchange
	}
}

// withReplayKey puts the replay's public key in place of Mastodon's key in an
// actor document. It returns other bodies without a change.
func (r *Replay) withReplayKey(body json.RawMessage) json.RawMessage {
	var doc map[string]any
	if json.Unmarshal(body, &doc) != nil {
		return body
	}
	key, ok := doc["publicKey"].(map[string]any)
	if !ok {
		return body
	}
	pub := must(x509.MarshalPKIXPublicKey(&r.key.PublicKey))
	key["publicKeyPem"] = string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pub}))
	return must(json.Marshal(doc))
}
