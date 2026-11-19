package judge

import (
	"fmt"
	"net/url"
	"strings"
)

// FederationScenarios cover the interactions the admin UI can trigger between
// two instances, beyond what the original test suite exercised.
var FederationScenarios = []Scenario{
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
			id := pendingFollow(t)
			act(t, t.B, "accept_incoming_follow", url.Values{"notification_id": {id}})
			t.waitRows(t.B, 1, `SELECT 1 FROM follower WHERE ap_actor_id = ?`, BaseURL(t.A))
			t.waitRows(t.A, 1, `SELECT 1 FROM following WHERE ap_actor_id = ?`, BaseURL(t.B))
			t.waitRows(t.A, 1, `SELECT 1 FROM notifications WHERE notification_type = 'FOLLOW_REQUEST_ACCEPTED'`)
		}},

	{Name: "follow__manually_rejected",
		Config: map[string]string{"b": "manually_approves_followers = true"},
		Run: func(t *T) {
			id := pendingFollow(t)
			act(t, t.B, "reject_incoming_follow", url.Values{"notification_id": {id}})
			t.waitRows(t.A, 1, `SELECT 1 FROM notifications WHERE notification_type = 'FOLLOW_REQUEST_REJECTED'`)
			t.waitRows(t.A, 0, `SELECT 1 FROM following`)
			t.waitRows(t.B, 0, `SELECT 1 FROM follower`)
		}},

	{Name: "post__visibility", Run: func(t *T) {
		follow(t, t.A, t.B)
		for _, v := range []string{"PUBLIC", "UNLISTED", "FOLLOWERS_ONLY", "DIRECT"} {
			form := url.Values{"visibility": {v}, "content": {"visible to " + v}}
			if v == "DIRECT" {
				form.Set("content", "hi @a@a.test")
			}
			id := newPost(t, t.B, form)
			t.waitRows(t.A, 1, `SELECT 1 FROM inbox WHERE ap_id = ? AND visibility = ?`, id, v)
			t.waitRows(t.B, 1, `SELECT 1 FROM outbox WHERE ap_id = ? AND visibility = ?`, id, v)
		}
	}},

	{Name: "post__reply", Run: func(t *T) {
		follow(t, t.A, t.B)
		follow(t, t.B, t.A)
		note := newPost(t, t.B, url.Values{"content": {"Hello"}})
		t.waitRows(t.A, 1, `SELECT 1 FROM inbox WHERE ap_id = ?`, note)
		reply := newPost(t, t.A, url.Values{"content": {"Hi back"}, "in_reply_to": {note}})
		t.waitRows(t.B, 1, `SELECT 1 FROM inbox WHERE ap_id = ?
			AND json_extract(ap_object, '$.inReplyTo') = ?`, reply, note)
		t.waitRows(t.B, 1, `SELECT 1 FROM outbox WHERE ap_id = ? AND replies_count = 1`, note)
	}},

	{Name: "like__and_undo", Run: func(t *T) {
		note := remoteNote(t)
		act(t, t.A, "like", url.Values{"ap_object_id": {note}})
		t.waitRows(t.B, 1, `SELECT 1 FROM outbox WHERE ap_id = ? AND likes_count = 1`, note)
		t.waitRows(t.B, 1, `SELECT 1 FROM notifications WHERE notification_type = 'LIKE'`)
		t.waitRows(t.A, 1, `SELECT 1 FROM inbox WHERE ap_id = ? AND liked_via_outbox_object_ap_id IS NOT NULL`, note)

		like := t.one(t.A, `SELECT ap_id FROM outbox WHERE ap_type = 'Like'`)["ap_id"].(string)
		act(t, t.A, "undo", url.Values{"ap_object_id": {like}})
		// BUG(port): the original ends at -1, not 0. Its Undo handler marks the
		// Like deleted, then recounts the remaining likes and subtracts one more
		// (app/boxes.py:1639-1645). The port reproduces this until parity, per
		// the kit's bug-for-bug rule; the fix comes after (prompts/06).
		t.waitRows(t.B, 1, `SELECT 1 FROM outbox WHERE ap_id = ? AND likes_count = -1`, note)
		t.waitRows(t.B, 1, `SELECT 1 FROM notifications WHERE notification_type = 'UNDO_LIKE'`)
		t.waitRows(t.A, 1, `SELECT 1 FROM inbox WHERE ap_id = ? AND liked_via_outbox_object_ap_id IS NULL`, note)
	}},

	{Name: "announce__and_undo", Run: func(t *T) {
		note := remoteNote(t)
		act(t, t.A, "announce", url.Values{"ap_object_id": {note}})
		t.waitRows(t.B, 1, `SELECT 1 FROM outbox WHERE ap_id = ? AND announces_count = 1`, note)
		t.waitRows(t.B, 1, `SELECT 1 FROM notifications WHERE notification_type = 'ANNOUNCE'`)

		announce := t.one(t.A, `SELECT ap_id FROM outbox WHERE ap_type = 'Announce'`)["ap_id"].(string)
		act(t, t.A, "undo", url.Values{"ap_object_id": {announce}})
		t.waitRows(t.B, 1, `SELECT 1 FROM outbox WHERE ap_id = ? AND announces_count = 0`, note)
		t.waitRows(t.B, 1, `SELECT 1 FROM notifications WHERE notification_type = 'UNDO_ANNOUNCE'`)
	}},

	{Name: "delete__note", Run: func(t *T) {
		note := remoteNote(t)
		act(t, t.B, "delete", url.Values{"ap_object_id": {note}})
		t.waitRows(t.B, 1, `SELECT 1 FROM outbox WHERE ap_id = ? AND is_deleted = 1`, note)
		t.waitRows(t.A, 1, `SELECT 1 FROM inbox WHERE ap_id = ? AND is_deleted = 1`, note)
	}},

	{Name: "poll__vote", Run: func(t *T) {
		follow(t, t.A, t.B)
		poll := newPost(t, t.B, url.Values{"content": {"Which?"}, "poll_type": {"oneOf"},
			"poll_duration": {"5"}, "poll_answer_1": {"A"}, "poll_answer_2": {"B"}})
		t.waitRows(t.A, 1, `SELECT 1 FROM inbox WHERE ap_id = ?`, poll)
		act(t, t.A, "vote", url.Values{"in_reply_to": {poll}, "name": {"A"}})
		t.waitRows(t.A, 1, `SELECT 1 FROM inbox WHERE ap_id = ? AND voted_for_answers = '["A"]'`, poll)
		t.waitRows(t.B, 1, `SELECT 1 FROM poll_answer WHERE name = 'A' AND poll_type = 'oneOf'`)
		t.waitRows(t.B, 1, `SELECT 1 FROM outbox, json_each(outbox.ap_object, '$.oneOf') AS answer
			WHERE ap_id = ? AND json_extract(answer.value, '$.name') = 'A'
			AND json_extract(answer.value, '$.replies.totalItems') = 1`, poll)
	}},

	{Name: "block__and_unblock", Run: func(t *T) {
		follow(t, t.B, t.A)
		act(t, t.A, "block", url.Values{"ap_actor_id": {BaseURL(t.B)}})
		t.waitRows(t.A, 1, `SELECT 1 FROM actor WHERE ap_id = ? AND is_blocked = 1`, BaseURL(t.B))
		t.waitRows(t.B, 1, `SELECT 1 FROM notifications WHERE notification_type = 'BLOCKED'`)
		act(t, t.A, "unblock", url.Values{"ap_actor_id": {BaseURL(t.B)}})
		t.waitRows(t.A, 1, `SELECT 1 FROM actor WHERE ap_id = ? AND is_blocked = 0`, BaseURL(t.B))
		t.waitRows(t.B, 1, `SELECT 1 FROM notifications WHERE notification_type = 'UNBLOCKED'`)
	}},

	{Name: "lookup__remote_actor", Run: func(t *T) {
		resp, err := t.Client.Get(t.Ctx, BaseURL(t.A)+"/admin/lookup?query="+url.QueryEscape("@b@b.test"), "")
		t.Check(err)
		t.equal("lookup status", resp.Status, 200)
		t.sawRequest("a", "GET", BaseURL(t.B)+"/.well-known/webfinger")
		t.sawRequest("a", "GET", BaseURL(t.B)+"/")
	}},
}

// pendingFollow makes a follow b, which approves followers manually, and
// returns the id of b's pending-follower notification.
func pendingFollow(t *T) string {
	act(t, t.A, "follow", url.Values{"ap_actor_id": {BaseURL(t.B)}})
	t.waitRows(t.B, 1, `SELECT 1 FROM notifications WHERE notification_type = 'PENDING_INCOMING_FOLLOWER'`)
	t.waitRows(t.B, 0, `SELECT 1 FROM follower`)
	n := t.one(t.B, `SELECT id FROM notifications WHERE notification_type = 'PENDING_INCOMING_FOLLOWER'`)
	return fmt.Sprint(n["id"])
}

// remoteNote makes a follow b, posts a note on b, and returns its id once a
// has received it.
func remoteNote(t *T) string {
	follow(t, t.A, t.B)
	note := newPost(t, t.B, url.Values{"content": {"Hello"}})
	t.waitRows(t.A, 1, `SELECT 1 FROM inbox WHERE ap_id = ?`, note)
	return note
}

// sawRequest fails unless the proxy recorded a request from sender whose URL
// starts with prefix.
func (t *T) sawRequest(from, method, prefix string) {
	for _, e := range t.Proxy.Exchanges() {
		if e.From == from && e.Method == method && strings.HasPrefix(e.URL, prefix) {
			return
		}
	}
	t.Fatalf("no %s %s* from %s on the wire", method, prefix, from)
}
