package judge

import (
	"fmt"
	"net/url"
	"strings"
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
// delivery sent (or given up on), every incoming activity tried. No scenario
// expects a delivery to fail: one that fails keeps the pair unsettled until
// its retry succeeds, and the extra attempt shows on the wire. Incoming
// activities that fail must number exactly what the scenario expects
// (Scenario.IncomingErrors).
//
// The instances can't be read at one instant: a delivery can land on a after
// a was read and be marked sent before b is. So the pair counts as quiet only
// when two polls in a row find nothing pending and the same state (the newest
// queue rows, how many are done): anything that moved in between changes it.
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
