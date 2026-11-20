package judge

import (
	"fmt"
	"net/url"
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
// apID.
func (t *T) waitProcessed(i Instance, apID any) {
	t.waitRows(i, 1, `SELECT 1 FROM incoming_activity WHERE ap_id = ? AND is_processed = 1`, apID)
}

// Admin actions, and the interactions built from them.

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

// pendingFollow makes a follow b, which approves followers manually, and
// returns the id of b's pending-follower notification.
func pendingFollow(t *T) string {
	act(t, t.A, "follow", url.Values{"ap_actor_id": {BaseURL(t.B)}})
	t.waitProcessed(t.B, t.one(t.A, `SELECT ap_id FROM outbox WHERE ap_type = 'Follow'`)["ap_id"])
	t.expectRows(t.B, 0, `SELECT 1 FROM follower`)
	n := t.one(t.B, `SELECT id FROM notifications WHERE notification_type = 'PENDING_INCOMING_FOLLOWER'`)
	return fmt.Sprint(n["id"])
}
