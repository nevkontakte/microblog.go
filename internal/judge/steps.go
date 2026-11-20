package judge

import (
	"encoding/json"
	"fmt"
	"net/url"
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
