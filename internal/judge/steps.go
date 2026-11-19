package judge

import (
	"encoding/json"
	"fmt"
	"net/url"
	"reflect"
)

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
// apID without error.
func (t *T) waitProcessed(i Instance, apID any) {
	t.waitRows(i, 1, `SELECT 1 FROM incoming_activity WHERE ap_id = ? AND is_processed = 1 AND error IS NULL`, apID)
}

// equal fails the scenario if got != want.
func (t *T) equal(what string, got, want any) {
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s: got %#v, want %#v", what, got, want)
	}
}

// sameJSON fails the scenario unless the JSON text stored in a database
// column has the same content as want.
func (t *T) sameJSON(what string, stored any, want map[string]any) {
	var got, norm any
	s, _ := stored.(string)
	t.Check(json.Unmarshal([]byte(s), &got))
	b, _ := json.Marshal(want)
	json.Unmarshal(b, &norm)
	t.equal(what, got, norm)
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

// act performs an admin action and fails the scenario on error.
func act(t *T, i Instance, action string, form url.Values) {
	_, err := t.Client.Act(t.Ctx, i, action, form)
	t.Check(err)
}

// follow makes from follow to, which accepts automatically, and waits until
// both sides record the relationship.
func follow(t *T, from, to Instance) {
	act(t, from, "follow", url.Values{"ap_actor_id": {BaseURL(to)}})
	t.waitRows(from, 1, `SELECT 1 FROM following WHERE ap_actor_id = ?`, BaseURL(to))
	t.waitRows(to, 1, `SELECT 1 FROM follower WHERE ap_actor_id = ?`, BaseURL(from))
}

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
