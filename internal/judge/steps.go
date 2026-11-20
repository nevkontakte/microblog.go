package judge

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/google/go-cmp/cmp"
)

// Waiting on state. Scenarios wait for what they expect to see, never for a
// length of time (scenario.go, Timeout).

// rows runs a query on i and fails the scenario on error.
func (t *T) rows(i Instance, query string, args ...any) []map[string]any {
	r, err := Query(i, query, args...)
	t.Check(err)
	return r
}

// one returns the single row query yields on i, and fails otherwise.
func (t *T) one(i Instance, query string, args ...any) map[string]any {
	r := t.rows(i, query, args...)
	if len(r) != 1 {
		t.Fatalf("%s: %s: got %d rows, want 1", i.Name(), query, len(r))
	}
	return r[0]
}

// rowCount returns a check that query yields exactly n rows on i.
func (t *T) rowCount(i Instance, n int, query string, args ...any) func() error {
	return func() error {
		rows, err := Query(i, query, args...)
		if err != nil {
			return err
		}
		if len(rows) != n {
			return fmt.Errorf("%s: got %d rows, want %d: %s", i.Name(), len(rows), n, query)
		}
		return nil
	}
}

// waitRows waits until query yields exactly n rows on i. Use it for state
// that is expected to change; to assert that something did not happen, wait
// for processing to finish (waitProcessed) and use expectRows.
func (t *T) waitRows(i Instance, n int, query string, args ...any) {
	t.Eventually(fmt.Sprintf("%d rows on %s", n, i.Name()), t.rowCount(i, n, query, args...))
}

// waitRow waits until query yields exactly one row on i, and returns it.
func (t *T) waitRow(i Instance, query string, args ...any) map[string]any {
	t.waitRows(i, 1, query, args...)
	return t.one(i, query, args...)
}

// expectRows checks once that query yields exactly n rows on i.
func (t *T) expectRows(i Instance, n int, query string, args ...any) {
	t.Check(t.rowCount(i, n, query, args...)())
}

// waitProcessed waits until i's incoming worker has processed the activity
// apID. A failed try that a retry fixed keeps its error; settle counts those,
// except lost lock races (lockRace).
func (t *T) waitProcessed(i Instance, apID any) {
	t.waitRows(i, 1, `SELECT 1 FROM incoming_activity WHERE ap_id = ? AND is_processed = 1`, apID)
}

// waitAllProcessed waits until n incoming activities have been processed
// without error on i, for activities without an id of their own.
func (t *T) waitAllProcessed(i Instance, n int) {
	t.waitRows(i, n, `SELECT 1 FROM incoming_activity WHERE is_processed = 1 AND (error IS NULL OR `+lockRace+`)`)
}

// lockRace matches the error an incoming activity keeps when its try lost a
// lock race in the original: the worker reads, then writes in the same
// transaction, and SQLite refuses that upgrade at once, without waiting,
// while another connection is writing. The worker tries again; it isn't a
// processing failure.
const lockRace = `error LIKE '%database is locked%'`

// settle waits until the pair is quiet, so the wire recording holds
// everything the scenario caused, however fast the workers run: every
// delivery sent (or given up on), every incoming activity tried, no fake
// sending. No scenario expects a delivery to fail: one that fails keeps the
// pair unsettled until its retry succeeds, and the extra attempt shows on
// the wire. Incoming activities that fail must number exactly what the
// scenario expects (Scenario.IncomingErrors).
//
// The instances and fakes can't be read at one instant: a delivery can land
// on a after a was read and be marked sent before b is. So the pair counts
// as quiet only when two polls in a row find nothing pending and the same
// state (the newest queue rows, how many are done, how many Accepts the
// fakes started): anything that moved in between changes it.
func (t *T) settle(expectedErrors int) {
	var last string
	t.Eventually("pending deliveries and incoming activities", func() error {
		var state strings.Builder
		for _, i := range []Instance{t.A, t.B} {
			row, err := Query(i, `SELECT
				(SELECT count(*) FROM outgoing_activity WHERE NOT is_sent AND NOT is_errored) AS outgoing,
				(SELECT count(*) FROM incoming_activity WHERE NOT is_processed AND NOT is_errored AND (error IS NULL OR `+lockRace+`)) AS incoming,
				(SELECT coalesce(max(id), 0) FROM outgoing_activity) AS last_outgoing,
				(SELECT coalesce(max(id), 0) FROM incoming_activity) AS last_incoming,
				(SELECT count(*) FROM outgoing_activity WHERE is_sent OR is_errored) AS sent,
				(SELECT count(*) FROM incoming_activity WHERE is_processed OR is_errored OR error IS NOT NULL) AS tried`)
			if err != nil {
				return err
			}
			r := row[0]
			if r["outgoing"] != int64(0) || r["incoming"] != int64(0) {
				last = ""
				return fmt.Errorf("%s has %v deliveries and %v incoming activities pending", i.Name(), r["outgoing"], r["incoming"])
			}
			fmt.Fprintf(&state, "%s:%v/%v/%v/%v ", i.Name(), r["last_outgoing"], r["last_incoming"], r["sent"], r["tried"])
		}
		for _, f := range t.fakes {
			if n := f.sending.Load(); n > 0 {
				last = ""
				return fmt.Errorf("%s is sending %d Accepts", f.Name, n)
			}
			fmt.Fprintf(&state, "%s:%d ", f.Name, f.accepts.Load())
		}
		if state.String() != last {
			last = state.String()
			return fmt.Errorf("not quiet yet: %s", last)
		}
		return nil
	})
	var errors int
	for _, i := range []Instance{t.A, t.B} {
		errors += len(t.rows(i, `SELECT 1 FROM incoming_activity WHERE error IS NOT NULL AND NOT `+lockRace))
	}
	if errors != expectedErrors {
		t.Fatalf("%d incoming activities failed, want %d (see the instances' incoming.log)", errors, expectedErrors)
	}
}

// Assertions.

// equal fails the scenario unless got equals want, showing the difference.
func (t *T) equal(what string, got, want any) {
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("%s (-want +got):\n%s", what, diff)
	}
}

// sameJSON fails the scenario unless the JSON text stored in a database
// column has the same content as want.
func (t *T) sameJSON(what string, stored any, want map[string]any) {
	s, ok := stored.(string)
	if !ok {
		t.Fatalf("%s: stored as %T, not JSON text", what, stored)
	}
	var got, norm any
	t.Check(json.Unmarshal([]byte(s), &got))
	b, err := json.Marshal(want)
	t.Check(err)
	t.Check(json.Unmarshal(b, &norm))
	t.equal(what, got, norm)
}

// expectSchema checks that i's database is still at the original's last
// migration, which is what alembic current reports: the original must be
// able to take the data back.
func (t *T) expectSchema(i Instance) {
	t.equal("alembic version", t.one(i, `SELECT version_num FROM alembic_version`)["version_num"], "a209f0333f5a")
}

// hasSuffix fails the scenario unless s ends with suffix.
func (t *T) hasSuffix(what, s, suffix string) {
	if !strings.HasSuffix(s, suffix) {
		t.Fatalf("%s: %q does not end with %q", what, s, suffix)
	}
}

// sawExchange fails unless the proxy recorded a request from sender for
// exactly url, answered with status.
func (t *T) sawExchange(from, method, url string, status int) {
	t.sawInOrder(from, method, status, url)
}

// sawInOrder fails unless the proxy recorded requests from sender for each
// of urls, in that order, all answered with status.
func (t *T) sawInOrder(from, method string, status int, urls ...string) {
	next := 0
	for _, e := range t.Proxy.Exchanges() {
		if next < len(urls) && e.From == from && e.Method == method && e.URL == urls[next] && e.Status == status {
			next++
		}
	}
	if next < len(urls) {
		t.Fatalf("no %s %s from %s answered %d on the wire (after %v)", method, urls[next], from, status, urls[:next])
	}
}

// isUpdateTo reports deliveries of an Update to instance name's inbox.
func isUpdateTo(name string) func(WireRecord) bool {
	return func(r WireRecord) bool {
		body, _ := r.Body.(map[string]any)
		return r.Method == "POST" && r.URL == "https://"+name+".test/inbox" && body["type"] == "Update"
	}
}

// Admin actions, and the interactions built from them.

// act performs an admin action and fails the scenario on error.
func act(t *T, i Instance, action string, form url.Values) {
	_, err := t.Client.Act(t.Ctx, i, action, form)
	t.Check(err)
}

// newPost publishes a post on i through the admin form and returns its id.
// The form defaults to public visibility.
func newPost(t *T, i Instance, form url.Values, files ...File) string {
	if form.Get("visibility") == "" {
		form.Set("visibility", "PUBLIC")
	}
	id, err := t.Client.Act(t.Ctx, i, "new", form, files...)
	t.Check(err)
	return id // the redirect goes to the new object, whose URL is its id
}

// follow makes from follow to, which accepts automatically, and waits until
// both sides record the relationship.
func follow(t *T, from, to Instance) {
	act(t, from, "follow", url.Values{"ap_actor_id": {BaseURL(to)}})
	t.waitRows(from, 1, `SELECT 1 FROM following WHERE ap_actor_id = ?`, BaseURL(to))
	t.waitRows(to, 1, `SELECT 1 FROM follower WHERE ap_actor_id = ?`, BaseURL(from))
}

// pendingFollower waits until i has processed follower's Follow without
// accepting it, and returns the id of i's pending-follower notification.
func pendingFollower(t *T, i, follower Instance) string {
	t.waitProcessed(i, t.one(follower, `SELECT ap_id FROM outbox WHERE ap_type = 'Follow'`)["ap_id"])
	t.expectRows(i, 0, `SELECT 1 FROM follower WHERE ap_actor_id = ?`, BaseURL(follower))
	n := t.one(i, `SELECT id FROM notifications WHERE notification_type = 'PENDING_INCOMING_FOLLOWER'`)
	return fmt.Sprint(n["id"])
}

// blockedBy makes blocker block i, and waits until i is told.
func blockedBy(t *T, i, blocker Instance) {
	act(t, blocker, "block", url.Values{"ap_actor_id": {BaseURL(i)}})
	t.waitRows(blocker, 1, `SELECT 1 FROM actor WHERE ap_id = ? AND is_blocked = 1`, BaseURL(i))
	t.waitRows(i, 1, `SELECT 1 FROM notifications WHERE notification_type = 'BLOCKED'`)
}

// expectVotesCounted waits until author, whose poll it is, counts one vote
// for exactly answers and one voter (app/boxes.py:2035-2110).
func expectVotesCounted(t *T, author Instance, poll, pollType string, answers ...string) {
	t.Eventually(author.Name()+" counts the vote for "+strings.Join(answers, ", "), func() error {
		names, err := votedAnswers(author, "outbox", poll, pollType)
		if err == nil && !slices.Equal(names, answers) {
			err = fmt.Errorf("%s counts %v", author.Name(), names)
		}
		return err
	})
	t.waitRows(author, 1, `SELECT 1 FROM outbox WHERE ap_id = ? AND json_extract(ap_object, '$.votersCount') = 1`, poll)
}

// expectPollUpdated waits until voter has applied the Updates of poll that
// its vote for answers caused, one per answer.
//
// BUG(port): the author sends one Update per answer, with an id made of the
// poll's id and the time to the second (app/boxes.py:2098-2099, 839-866).
// Two Updates in the same second share an id, and the voter drops the second
// as a duplicate (app/boxes.py:2362-2374), so it may show only the first
// answer counted. The wire is the same either way; the voter's final count
// depends on timing, so it need only count some of answers.
func expectPollUpdated(t *T, voter Instance, poll, pollType string, answers ...string) {
	t.waitRows(voter, 1, `SELECT 1 FROM inbox WHERE ap_id = ? AND json_extract(ap_object, '$.votersCount') = 1`, poll)
	t.waitRows(voter, len(answers), `SELECT 1 FROM incoming_activity WHERE ap_object ->> '$.type' = 'Update' AND is_processed = 1`)
	names, err := votedAnswers(voter, "inbox", poll, pollType)
	t.Check(err)
	if len(names) == 0 || slices.ContainsFunc(names, func(n string) bool { return !slices.Contains(answers, n) }) {
		t.Fatalf("%s counts %v of a vote for %v", voter.Name(), names, answers)
	}
}

// votedAnswers lists, sorted, the answers with one vote in i's copy of poll,
// which is in box: "outbox" on its author, "inbox" elsewhere.
func votedAnswers(i Instance, box, poll, pollType string) ([]string, error) {
	rows, err := Query(i, `SELECT json_extract(answer.value, '$.name') AS name
		FROM `+box+`, json_each(`+box+`.ap_object, '$.`+pollType+`') AS answer
		WHERE ap_id = ? AND json_extract(answer.value, '$.replies.totalItems') = 1 ORDER BY name`, poll)
	var names []string
	for _, r := range rows {
		names = append(names, fmt.Sprint(r["name"]))
	}
	return names, err
}

// postQuestion publishes a poll on i through the admin form and returns its
// id.
func postQuestion(t *T, i Instance, pollType string, minutes int, answers ...string) string {
	form := url.Values{"content": {"Which?"}, "poll_type": {pollType}, "poll_duration": {fmt.Sprint(minutes)}}
	for n, a := range answers {
		form.Set(fmt.Sprintf("poll_answer_%d", n+1), a)
	}
	return newPost(t, i, form)
}

// expectQuestion checks the poll id as its author i stored it: open, lasting
// minutes, and offering answers as pollType ("oneOf" or "anyOf") alone.
func expectQuestion(t *T, i Instance, id, pollType string, minutes int, answers ...string) {
	other := map[string]string{"oneOf": "anyOf", "anyOf": "oneOf"}[pollType]
	q := t.one(i, `SELECT ap_type,
		json_extract(ap_object, '$.endTime') > strftime('%Y-%m-%dT%H:%M:%SZ', 'now') AS open,
		round((julianday(json_extract(ap_object, '$.endTime')) - julianday(json_extract(ap_object, '$.published'))) * 1440) AS minutes,
		json_extract(ap_object, '$.`+other+`') IS NULL AS single
		FROM outbox WHERE ap_id = ?`, id)
	t.equal("question", []any{q["ap_type"], q["open"], q["minutes"], q["single"]},
		[]any{"Question", int64(1), float64(minutes), int64(1)})
	var names, want []any
	for _, r := range t.rows(i, `SELECT json_extract(value, '$.name') AS name
		FROM outbox, json_each(outbox.ap_object, '$.`+pollType+`') WHERE ap_id = ? ORDER BY name`, id) {
		names = append(names, r["name"])
	}
	for _, a := range answers {
		want = append(want, a)
	}
	t.equal(pollType+" answers", names, want)
}

// page fetches an HTML page and fails unless it is 200.
func page(t *T, url string) string {
	resp, err := t.Client.Get(t.Ctx, url, "text/html")
	t.Check(err)
	if resp.Status != 200 {
		t.Fatalf("GET %s: status %d", url, resp.Status)
	}
	return string(resp.Body)
}

// expectDelivered checks that from queued its object id for to's inbox, and
// that to received it as an apType.
func expectDelivered(t *T, from, to Instance, id, apType string) {
	obj := t.one(from, `SELECT id FROM outbox WHERE ap_id = ?`, id)
	t.equal("recipient", t.one(from, `SELECT recipient FROM outgoing_activity WHERE outbox_object_id = ?`,
		obj["id"])["recipient"], BaseURL(to)+"/inbox")
	t.waitRows(to, 1, `SELECT 1 FROM inbox WHERE ap_id = ? AND ap_type = ?`, id, apType)
}

// outboxTypes returns the types of everything in i's outbox, sorted. The
// original tests asserted on an otherwise empty outbox.
func outboxTypes(t *T, i Instance) []any {
	var types []any
	for _, r := range t.rows(i, `SELECT ap_type FROM outbox ORDER BY ap_type`) {
		types = append(types, r["ap_type"])
	}
	return types
}

// itemIDs returns the ids of a collection's items, which may be inlined.
func itemIDs(items any) []any {
	var ids []any
	for _, it := range items.([]any) {
		if obj, ok := it.(map[string]any); ok {
			it = obj["id"]
		}
		ids = append(ids, it)
	}
	return ids
}

// lookup queries i's admin lookup page.
func lookup(t *T, i Instance, query string) *Response {
	resp, err := t.Client.Get(t.Ctx, BaseURL(i)+"/admin/lookup?query="+url.QueryEscape(query), "")
	t.Check(err)
	return resp
}

// expectLookedUp checks that i's lookup of object id fetched it and then,
// once it parsed, its author at authorID (app/ap_object.py:332-335), and
// stored neither.
func expectLookedUp(t *T, i Instance, id, authorID string) {
	t.sawInOrder(i.Name(), "GET", 200, id, authorID+"/")
	t.expectRows(i, 0, `SELECT 1 FROM inbox WHERE ap_id = ?`, id)
}

// finger is the WebFinger URL for user on i, as the original requests it.
func finger(i Instance, user string) string {
	return BaseURL(i) + "/.well-known/webfinger?resource=" + url.QueryEscape("acct:"+user+"@"+Host(i))
}

// remoteRedirect checks that from looked up to's subscribe template and sent
// the visitor to it with from's id, by the Refresh header
// (app/main.py:272-282).
func remoteRedirect(t *T, from, to Instance, resp *Response) {
	t.equal("status", resp.Status, 200)
	t.sawExchange(from.Name(), "GET", finger(to, to.Name()), 200)
	t.equal("Refresh", resp.Header.Get("Refresh"), "0;url="+BaseURL(to)+"/admin/lookup?query="+BaseURL(from))
}

// restart lets the pair settle, stops i, and starts it again on its data
// with config added to its profile.toml lines (a key set again overrides
// the earlier value), as an admin editing the profile would. It returns the
// new instance, which also replaces i in t. Until the new instance's address
// is routed, its host still points at the stopped one; nothing is pending by
// then. The admin session survives: its cookie is signed with the same
// secret.
func (t *T) restart(i Instance, config string) Instance {
	t.settle(0)
	t.Check(i.Stop())
	spec := t.specs[i.Name()]
	spec.Config += "\n" + config
	t.specs[i.Name()] = spec
	inst, err := t.start(i.Name())
	t.Check(err)
	t.Check(t.waitReady(inst))
	if i == t.A {
		t.A = inst
	} else {
		t.B = inst
	}
	return inst
}

// swap stops i and starts the peer's implementation on i's data, as an owner
// swapping binaries would: same directory, no migration step. It returns the
// new instance, which also replaces i in t. On a Python pair it is a restart;
// with the port on one side it is the swap test.
func (t *T) swap(i Instance) Instance {
	peer := map[string]string{"a": "b", "b": "a"}[i.Name()]
	t.impls[i.Name()] = t.impls[peer]
	return t.restart(i, "")
}

// addCustomEmoji puts an image in i's data/custom_emoji, which an
// implementation loads when it starts (app/utils/emoji.py:14-40).
func addCustomEmoji(t *T, i Instance, file string, data []byte) {
	t.Check(os.WriteFile(filepath.Join(i.DataDir(), "custom_emoji", file), data, 0o644))
}

// Steps with fake actors.

// followFake makes i follow f, which must accept automatically.
func followFake(t *T, i Instance, f *Fake) {
	f.AutoAccept = true
	act(t, i, "follow", url.Values{"ap_actor_id": {f.ID()}})
	t.waitRows(i, 1, `SELECT 1 FROM following WHERE ap_actor_id = ?`, f.ID())
}

// fakeFollows makes f follow i, and waits until f has received i's Accept.
func fakeFollows(t *T, f *Fake, i Instance) {
	follow := f.Activity("Follow", BaseURL(i))
	send(t, f, i, follow)
	t.waitRows(i, 1, `SELECT 1 FROM follower WHERE ap_actor_id = ?`, f.ID())
	t.Eventually("Accept delivered to "+f.ID(), func() error {
		for _, a := range f.Inbox() {
			if a["type"] == "Accept" && a["object"] == follow["id"] {
				return nil
			}
		}
		return fmt.Errorf("no Accept of %v yet", follow["id"])
	})
}

// send delivers a signed activity from f to i and expects 202 Accepted.
func send(t *T, f *Fake, i Instance, activity map[string]any) {
	resp, err := f.Send(t.Ctx, i, activity)
	t.Check(err)
	t.equal("inbox POST status", resp.Status, 202)
}

// actorRowID returns the id of the actor row for apID on i.
func (t *T) actorRowID(i Instance, apID string) any {
	return t.one(i, `SELECT id FROM actor WHERE ap_id = ?`, apID)["id"]
}

// withContent returns a copy of note, without its context, with new content.
func withContent(note map[string]any, content string) map[string]any {
	edited := map[string]any{}
	for k, v := range note {
		if k != "@context" {
			edited[k] = v
		}
	}
	edited["content"] = content
	return edited
}

// fetchAP fetches url as f, expects status, and returns the decoded body
// when it is 200.
func fetchAP(t *T, f *Fake, url string, signed bool, status int) map[string]any {
	resp, err := f.Fetch(t.Ctx, url, signed)
	t.Check(err)
	if resp.Status != status {
		t.Fatalf("GET %s as %s (signed: %v): status %d, want %d", url, f.Name, signed, resp.Status, status)
	}
	var obj map[string]any
	if status == 200 {
		t.Check(json.Unmarshal(resp.Body, &obj))
	}
	return obj
}

// servedState fetches, as f, what remote servers and readers see of i: its
// WebFinger, actor and collections, each object in ids with its activity,
// and each of i's attachments. Two calls compare equal if nothing visible changed.
func servedState(t *T, f *Fake, i Instance, ids ...string) map[string]any {
	base := BaseURL(i)
	state := map[string]any{}
	resp, err := f.Fetch(t.Ctx, base+"/.well-known/webfinger?resource=acct:"+i.Name()+"@"+Host(i), false)
	t.Check(err)
	state["webfinger"] = string(resp.Body)
	for _, path := range []string{"", "/outbox", "/followers", "/following", "/featured"} {
		doc := fetchAP(t, f, base+path, false, 200)
		state[path] = doc
		if first, ok := doc["first"].(string); ok {
			state[path+" first"] = fetchAP(t, f, first, false, 200)
		}
	}
	for _, id := range ids {
		state[id] = fetchAP(t, f, id, false, 200)
		state[id+"/activity"] = fetchAP(t, f, id+"/activity", false, 200)
	}
	for _, r := range t.rows(i, `SELECT u.content_hash || '/' || a.filename AS path
		FROM upload u JOIN outbox_object_attachment a ON a.upload_id = u.id`) {
		path := r["path"].(string)
		resp, err := t.Client.Get(t.Ctx, base+"/attachments/"+path, "*/*")
		t.Check(err)
		t.equal("attachment "+path, resp.Status, 200)
		state["attachment "+path] = string(resp.Body)
	}
	return state
}

// publicKey parses a PEM-encoded RSA public key.
func publicKey(t *T, pemText string) *rsa.PublicKey {
	block, _ := pem.Decode([]byte(pemText))
	if block == nil {
		t.Fatalf("not a PEM key: %q", pemText)
	}
	key, err := x509.ParsePKIXPublicKey(block.Bytes)
	t.Check(err)
	rsaKey, ok := key.(*rsa.PublicKey)
	if !ok {
		t.Fatalf("not an RSA key: %T", key)
	}
	return rsaKey
}

// instanceKey returns the public half of the key in i's data/key.pem.
func instanceKey(t *T, i Instance) *rsa.PublicKey {
	b, err := os.ReadFile(filepath.Join(i.DataDir(), "key.pem"))
	t.Check(err)
	block, _ := pem.Decode(b)
	if block == nil {
		t.Fatalf("data/key.pem is not PEM")
	}
	key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	t.Check(err)
	return &key.PublicKey
}
