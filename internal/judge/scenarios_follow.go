package judge

import (
	"net/url"
)

// FollowScenarios test follows between the two instances:
//   - Automatic acceptance.
//   - The end of a follow.
//   - Manual approval, with acceptance or rejection.
var FollowScenarios = []Scenario{
	{Name: "follow", Run: func(t *T) {
		follow(t, t.A, t.B)
		t.waitRows(t.B, 1, `SELECT 1 FROM notifications WHERE notification_type = 'NEW_FOLLOWER'`)
	}},

	{Name: "unfollow", Run: func(t *T) {
		follow(t, t.A, t.B)
		followID := t.one(t.A, `SELECT ap_id FROM outbox WHERE ap_type = 'Follow'`)["ap_id"].(string)
		act(t, t.A, "undo", url.Values{"ap_object_id": {followID}})
		t.waitRows(t.A, 0, `SELECT 1 FROM following`)
		t.waitRows(t.B, 0, `SELECT 1 FROM follower`)
		t.waitRows(t.B, 1, `SELECT 1 FROM notifications WHERE notification_type = 'UNFOLLOW'`)
	}},

	{Name: "follow__manually_approved",
		Config: map[string]string{"b": "manually_approves_followers = true"},
		Run: func(t *T) {
			act(t, t.A, "follow", url.Values{"ap_actor_id": {BaseURL(t.B)}})
			id := pendingFollower(t, t.B, t.A)
			act(t, t.B, "accept_incoming_follow", url.Values{"notification_id": {id}})
			t.waitRows(t.B, 1, `SELECT 1 FROM follower WHERE ap_actor_id = ?`, BaseURL(t.A))
			t.waitRows(t.A, 1, `SELECT 1 FROM following WHERE ap_actor_id = ?`, BaseURL(t.B))
			t.waitRows(t.A, 1, `SELECT 1 FROM notifications WHERE notification_type = 'FOLLOW_REQUEST_ACCEPTED'`)
		}},

	{Name: "follow__manually_rejected",
		Config: map[string]string{"b": "manually_approves_followers = true"},
		Run: func(t *T) {
			act(t, t.A, "follow", url.Values{"ap_actor_id": {BaseURL(t.B)}})
			id := pendingFollower(t, t.B, t.A)
			act(t, t.B, "reject_incoming_follow", url.Values{"notification_id": {id}})
			reject := t.one(t.B, `SELECT ap_id FROM outbox WHERE ap_type = 'Reject'`)["ap_id"]
			t.waitProcessed(t.A, reject)
			t.expectRows(t.A, 1, `SELECT 1 FROM notifications WHERE notification_type = 'FOLLOW_REQUEST_REJECTED'`)
			t.expectRows(t.A, 0, `SELECT 1 FROM following`)
			t.expectRows(t.B, 0, `SELECT 1 FROM follower`)
		}},
}
