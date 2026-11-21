package judge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

// RecordOptions configure a recording of the Mastodon replay set.
type RecordOptions struct {
	Impl Impl   // starts the instance Mastodon talks to
	Work string // directory for the instances and their logs
	Out  string // the replay set file to write
}

// mastodonUser is the Mastodon account that does everything the replay set
// records; mastodonAlias is the one it moves to.
const (
	mastodonUser  = "m"
	mastodonAlias = "m2"
)

// recording is the state of a recording: the network, instance a, the
// Mastodon server, and the placeholders for a's objects Mastodon refers to.
type recording struct {
	t        *T
	mastodon *Mastodon
	steps    []recordedStep
	// placeholders maps the id of each object of a's that Mastodon refers to
	// to its placeholder's name, so a replay can create its own and fill them
	// in.
	placeholders map[string]string
	recorded     int    // exchanges already assigned to a step
	actor        string // the id of Mastodon's user
}

// RecordMastodon records what a real Mastodon server sends a microblog.pub
// instance while its user follows, posts, likes, boosts, replies, votes,
// edits, deletes and moves, and writes it as the replay set
// (docs/adr/0014-record-the-mastodon-replay-set-from-a-real-server.md).
func RecordMastodon(ctx context.Context, opts RecordOptions) (err error) {
	dir, err := filepath.Abs(opts.Work)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	proxy, err := NewProxy()
	if err != nil {
		return err
	}
	t := &T{Ctx: ctx, Proxy: proxy, impls: map[string]Impl{}, specs: map[string]Spec{}}
	r := &recording{t: t, placeholders: map[string]string{}}
	defer func() {
		err = errors.Join(err, writeJSON(filepath.Join(dir, "exchanges.json"), proxy.Exchanges()))
		if t.A != nil {
			err = errors.Join(err, t.A.Stop())
		}
		if r.mastodon != nil {
			err = errors.Join(err, r.mastodon.Stop())
		}
		err = errors.Join(err, proxy.Close())
	}()
	caFile := filepath.Join(dir, "ca.pem")
	spec := func(name string) (Spec, error) {
		addr, err := proxy.Listen(name)
		if err != nil {
			return Spec{}, err
		}
		s := Spec{Name: name, Dir: filepath.Join(dir, name), ProxyAddr: addr, CAFile: caFile}
		return s, os.MkdirAll(s.Dir, 0o755)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := proxy.WriteCA(caFile); err != nil {
		return err
	}
	judgeAddr, err := proxy.Listen("judge")
	if err != nil {
		return err
	}
	t.Client = NewClient(judgeAddr, proxy.CertPool())

	sa, err := spec("a")
	if err != nil {
		return err
	}
	t.impls["a"], t.specs["a"] = opts.Impl, sa
	if t.A, err = t.start("a"); err != nil {
		return err
	}
	if err := t.waitReady(t.A); err != nil {
		return err
	}
	if err := t.Client.Login(ctx, t.A); err != nil {
		return err
	}
	sm, err := spec("m")
	if err != nil {
		return err
	}
	if r.mastodon, err = StartMastodon(ctx, sm, mastodonUser, mastodonAlias); err != nil {
		return err
	}
	proxy.Route(r.mastodon.Host(), r.mastodon.Addr())

	if !t.run(r.script) {
		return fmt.Errorf("recording failed:\n%s", strings.Join(t.Log(), "\n"))
	}
	set, err := r.replaySet()
	if err != nil {
		return err
	}
	return writeJSON(opts.Out, set)
}

// script is what the recording does, step by step: everything a Mastodon
// user does that reaches a microblog.pub instance.
func (r *recording) script(t *T) {
	r.actor = stringField(t, r.api("GET", "/api/v1/accounts/verify_credentials", nil), "uri")
	a := stringField(t, r.search("@a@a.test", "accounts"), "id")

	r.step("follow", func() { r.api("POST", "/api/v1/accounts/"+a+"/follow", nil) })
	act(t, t.A, "follow", url.Values{"ap_actor_id": {r.actor}})
	r.addFollowPlaceholder("a_follow")
	r.step("accept", func() {}) // Mastodon accepts a's Follow by itself

	var public string
	r.step("post_public", func() { public = r.post("Hello from Mastodon", "public") })
	r.step("post_unlisted", func() { r.post("Unlisted", "unlisted") })
	r.step("post_private", func() { r.post("For followers only", "private") })
	r.step("post_direct", func() { r.post("@a@a.test Just for you", "direct") })
	r.step("post_mention_hashtag", func() { r.post("Hello @a@a.test, about #judge", "public") })
	r.step("post_content_warning", func() {
		r.api("POST", "/api/v1/statuses", url.Values{"status": {"Behind a warning"}, "spoiler_text": {"A warning"},
			"sensitive": {"true"}, "visibility": {"public"}})
	})
	r.step("post_image", func() {
		media, err := r.mastodon.Upload(t.Ctx, t.Client, mastodonUser, File{Name: "picture.png", Data: smallPNG()})
		t.Check(err)
		r.api("POST", "/api/v1/statuses", url.Values{"status": {"A picture"}, "media_ids[]": {media}, "visibility": {"public"}})
	})
	r.step("post_poll", func() {
		r.api("POST", "/api/v1/statuses", url.Values{"status": {"Which?"}, "poll[options][]": {"A", "B"},
			"poll[expires_in]": {"86400"}, "visibility": {"public"}})
	})

	note := newPost(t, t.A, url.Values{"content": {"Hello from microblog.pub"}})
	r.addPlaceholder(note, "a_note")
	noteID := stringField(t, r.search(note, "statuses"), "id")
	r.step("like", func() { r.api("POST", "/api/v1/statuses/"+noteID+"/favourite", nil) })
	r.step("undo_like", func() { r.api("POST", "/api/v1/statuses/"+noteID+"/unfavourite", nil) })
	r.step("boost", func() { r.api("POST", "/api/v1/statuses/"+noteID+"/reblog", nil) })
	r.step("undo_boost", func() { r.api("POST", "/api/v1/statuses/"+noteID+"/unreblog", nil) })
	r.step("reply", func() {
		r.api("POST", "/api/v1/statuses", url.Values{"status": {"@a@a.test A reply"}, "in_reply_to_id": {noteID}, "visibility": {"public"}})
	})

	question := postQuestion(t, t.A, "oneOf", 60, "Yes", "No")
	r.addPlaceholder(question, "a_question")
	poll := stringField(t, objectField(t, r.search(question, "statuses"), "poll"), "id")
	r.step("vote", func() { r.api("POST", "/api/v1/polls/"+poll+"/votes", url.Values{"choices[]": {"0"}}) })

	r.step("edit", func() { r.api("PUT", "/api/v1/statuses/"+public, url.Values{"status": {"Hello again from Mastodon"}}) })
	r.step("pin", func() { r.api("POST", "/api/v1/statuses/"+public+"/pin", nil) })
	r.step("unpin", func() { r.api("POST", "/api/v1/statuses/"+public+"/unpin", nil) })
	r.step("delete", func() { r.api("DELETE", "/api/v1/statuses/"+public, nil) })
	r.step("update_profile", func() {
		r.api("PATCH", "/api/v1/accounts/update_credentials", url.Values{"display_name": {"Mastodon user"}, "note": {"Recorded for the judge"}})
	})
	r.step("unfollow", func() { r.api("POST", "/api/v1/accounts/"+a+"/unfollow", nil) })
	r.step("block", func() { r.api("POST", "/api/v1/accounts/"+a+"/block", nil) })
	r.step("unblock", func() { r.api("POST", "/api/v1/accounts/"+a+"/unblock", nil) })

	// The block ended a's follow, and a Move reaches only followers. A moved
	// account can't use the API any more, so these come last.
	act(t, t.A, "follow", url.Values{"ap_actor_id": {r.actor}})
	r.addFollowPlaceholder("a_follow_again")
	r.step("accept_again", func() {})
	r.step("move", func() { r.rails(moveAccount) })
	// a follows the account Mastodon's user moved to by itself.
	r.addFollowPlaceholder("a_follow_alias")
	r.step("delete_account", func() { r.rails(deleteAccount) })
}

// moveAccount moves Mastodon's user to its alias as the migration settings
// page does (app/controllers/settings/migrations_controller.rb), after the
// alias names it as an old account, which receivers check.
const moveAccount = `
m = Account.find_local('` + mastodonUser + `')
m2 = Account.find_local('` + mastodonAlias + `')
m2.update!(also_known_as: [ActivityPub::TagManager.instance.uri_for(m)])
migration = m.migrations.build(acct: m2.acct, target_account: m2)
migration.save!(validate: false)
MoveService.new.call(migration)
`

// deleteAccount deletes Mastodon's user as its owner would, which tells
// every server it knows.
const deleteAccount = `DeleteAccountService.new.call(Account.find_local('` + mastodonUser + `'), reserve_username: false)`

// step runs do, waits until Mastodon has delivered something, the network is
// quiet and a has processed every delivery, and records what was exchanged
// meanwhile, including what a fetched from Mastodon.
func (r *recording) step(name string, do func()) {
	t := r.t
	before := r.deliveryCount()
	do()
	t.Eventually("a delivery for step "+name, func() error {
		if r.deliveryCount() == before {
			return errors.New("Mastodon has delivered nothing")
		}
		return nil
	})
	r.settle()
	t.waitAllProcessed(t.A, r.deliveryCount())
	all := t.Proxy.Exchanges()
	r.steps = append(r.steps, recordedStep{name, slices.Clone(all[r.recorded:])})
	r.recorded = len(all)
}

// deliveryCount counts what Mastodon has delivered to a's inbox so far.
func (r *recording) deliveryCount() int {
	n := 0
	for _, e := range r.t.Proxy.Exchanges() {
		if e.From == "m" && e.Method == "POST" && e.URL == BaseURL(r.t.A)+"/inbox" {
			n++
		}
	}
	return n
}

// settle waits until the proxy has seen no new exchange for five seconds:
// Mastodon delivers from background jobs, and some of them follow others.
func (r *recording) settle() {
	n := -1
	for n != len(r.t.Proxy.Exchanges()) {
		n = len(r.t.Proxy.Exchanges())
		select {
		case <-r.t.Ctx.Done():
			r.t.Check(r.t.Ctx.Err())
		case <-time.After(5 * time.Second):
		}
	}
}

// addPlaceholder has the replay set refer to a's object id by name.
func (r *recording) addPlaceholder(id, name string) { r.placeholders[id] = name }

// addFollowPlaceholder has the replay set refer to the Follow a sent last by
// name.
func (r *recording) addFollowPlaceholder(name string) {
	follow := r.t.one(r.t.A, `SELECT ap_id FROM outbox WHERE ap_type = 'Follow' ORDER BY id DESC LIMIT 1`)
	r.addPlaceholder(stringField(r.t, follow, "ap_id"), name)
}

// post posts a status with the given visibility on Mastodon and returns its
// id there.
func (r *recording) post(text, visibility string) string {
	return stringField(r.t, r.api("POST", "/api/v1/statuses", url.Values{"status": {text}, "visibility": {visibility}}), "id")
}

// search resolves query on Mastodon, fetching what it names, and returns the
// one result of kind it finds: an account or a status, as Mastodon's API
// shows it.
func (r *recording) search(query, kind string) map[string]any {
	found := r.api("GET", "/api/v2/search?"+url.Values{"q": {query}, "resolve": {"true"}, "type": {kind}}.Encode(), nil)
	results, ok := found[kind].([]any)
	if !ok || len(results) != 1 {
		r.t.Fatalf("searching for %s found %v", query, found)
	}
	result, ok := results[0].(map[string]any)
	if !ok {
		r.t.Fatalf("searching for %s found %v", query, results[0])
	}
	return result
}

// api calls Mastodon's API as its user.
func (r *recording) api(method, path string, params url.Values) map[string]any {
	v, err := r.mastodon.API(r.t.Ctx, r.t.Client, mastodonUser, method, path, params)
	r.t.Check(err)
	obj, ok := v.(map[string]any)
	if !ok {
		r.t.Fatalf("%s %s answered %v", method, path, v)
	}
	return obj
}

// rails runs Ruby code on Mastodon.
func (r *recording) rails(code string) {
	_, err := r.mastodon.RunRails(r.t.Ctx, code)
	r.t.Check(err)
}

// recordedStep is what the proxy saw during one step of a recording.
type recordedStep struct {
	name      string
	exchanges []Exchange
}

// replaySet keeps what the replay needs from the recorded exchanges: what
// Mastodon delivered to a, and what a fetched from Mastodon. The ids of a's
// objects become {{name}} placeholders.
func (r *recording) replaySet() (ReplaySet, error) {
	set := ReplaySet{Mastodon: MastodonImage, Actor: r.actor}
	served := func(url string) bool { // by Mastodon, over https or http
		return strings.HasPrefix(url, "https://"+r.mastodon.Host()+"/") || strings.HasPrefix(url, "http://"+r.mastodon.Host()+"/")
	}
	for _, st := range r.steps {
		step := ReplayStep{Name: st.name}
		for _, e := range st.exchanges {
			switch {
			case e.From == "m" && e.Method == http.MethodPost && e.URL == BaseURL(r.t.A)+"/inbox":
				body, err := r.withPlaceholders(e.ReqBody)
				if err != nil {
					return ReplaySet{}, fmt.Errorf("step %s: %w", st.name, err)
				}
				step.Deliveries = append(step.Deliveries, Delivery{
					ContentType: e.ReqHeader.Get("Content-Type"),
					Signature:   signatureParams(e.ReqHeader.Get("Signature")),
					Headers:     extraSignedHeaders(e),
					Activity:    body,
				})
			case e.From == "a" && e.Method == http.MethodGet && served(e.URL):
				body, err := r.withPlaceholders(e.RespBody)
				if err != nil {
					return ReplaySet{}, fmt.Errorf("step %s: %w", st.name, err)
				}
				step.Documents = append(step.Documents, Document{
					URL: e.URL, Status: e.Status, ContentType: e.RespHeader.Get("Content-Type"),
					Location: e.RespHeader.Get("Location"), Body: body,
				})
			}
		}
		set.Steps = append(set.Steps, step)
	}
	return set, nil
}

// withPlaceholders replaces the ids of a's objects in a JSON body with their
// names, and fails if any other id of a's object is left. A body that isn't
// JSON is kept as a string.
func (r *recording) withPlaceholders(body string) (json.RawMessage, error) {
	if body == "" {
		return nil, nil
	}
	for id, name := range r.placeholders {
		body = strings.ReplaceAll(body, id, "{{"+name+"}}")
	}
	if id := objectOfA.FindString(body); id != "" {
		return nil, fmt.Errorf("%s has no name in the replay set: %.300s", id, body)
	}
	var indented bytes.Buffer
	if json.Indent(&indented, []byte(body), "", "  ") != nil {
		return json.Marshal(body) // not JSON, e.g. an empty redirect
	}
	return indented.Bytes(), nil
}

// objectOfA matches the id of an object of a's.
var objectOfA = regexp.MustCompile(`https://a\.test/o/[0-9a-f]{32}`)

// signatureParams keeps what describes an HTTP signature: the key's id,
// which the replay keeps while swapping the key, the algorithm and the signed
// headers. It drops the signature value, which only that request can carry.
func signatureParams(header string) string {
	var kept []string
	for _, part := range strings.Split(header, ",") {
		if name, _, _ := strings.Cut(part, "="); name == "keyId" || name == "algorithm" || name == "headers" {
			kept = append(kept, part)
		}
	}
	return strings.Join(kept, ",")
}

// extraSignedHeaders returns the headers e's HTTP signature covers beyond
// those every delivery has, such as Mastodon's collection-synchronization:
// the replay must send them as recorded for its own signature to cover them.
func extraSignedHeaders(e Exchange) map[string]string {
	_, headers := signatureForm(e.ReqHeader.Get("Signature"))
	extra := map[string]string{}
	for _, h := range strings.Fields(headers) {
		switch h {
		case "(request-target)", "host", "date", "digest", "content-type":
		default:
			extra[h] = e.ReqHeader.Get(h)
		}
	}
	if len(extra) == 0 {
		return nil
	}
	return extra
}

// signatureForm reads the algorithm and the signed headers from an HTTP
// signature, or from what signatureParams keeps of one.
func signatureForm(signature string) (algorithm, headers string) {
	for _, part := range strings.Split(signature, ",") {
		name, value, _ := strings.Cut(part, "=")
		value = strings.Trim(value, `"`)
		switch name {
		case "algorithm":
			algorithm = value
		case "headers":
			headers = value
		}
	}
	return algorithm, headers
}

// ReplaySet is the Mastodon replay set: what a real Mastodon server sent a
// microblog.pub instance, step by step (mastodon_record.go).
type ReplaySet struct {
	Mastodon string       `json:"mastodon"` // the image recorded
	Actor    string       `json:"actor"`    // the id of Mastodon's user
	Steps    []ReplayStep `json:"steps"`
}

// ReplayStep is what one step of the recording exchanged.
type ReplayStep struct {
	Name       string     `json:"name"`
	Deliveries []Delivery `json:"deliveries"`          // to a's inbox, in order
	Documents  []Document `json:"documents,omitempty"` // fetched by a meanwhile
}

// Delivery is an activity Mastodon delivered.
type Delivery struct {
	ContentType string            `json:"contentType"`
	Signature   string            `json:"signature"`         // the HTTP signature's key id, algorithm and headers
	Headers     map[string]string `json:"headers,omitempty"` // signed headers beyond the usual ones
	Activity    json.RawMessage   `json:"activity"`
}

// Document is what Mastodon served for a request of a's.
type Document struct {
	URL         string          `json:"url"`
	Status      int             `json:"status"`
	ContentType string          `json:"contentType,omitempty"`
	Location    string          `json:"location,omitempty"`
	Body        json.RawMessage `json:"body,omitempty"`
}
