package judge

import (
	"net/url"
)

// FederationScenarios test the interactions between two instances that the
// admin UI can start. The original test suite doesn't test all of them.
var FederationScenarios = []Scenario{
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
		follow(t, t.A, t.B)
		note := newPost(t, t.B, url.Values{"content": {"Hello"}})
		t.waitRows(t.A, 1, `SELECT 1 FROM inbox WHERE ap_id = ?`, note)
		act(t, t.A, "like", url.Values{"ap_object_id": {note}})
		t.waitRows(t.B, 1, `SELECT 1 FROM outbox WHERE ap_id = ? AND likes_count = 1`, note)
		t.waitRows(t.B, 1, `SELECT 1 FROM notifications WHERE notification_type = 'LIKE'`)
		t.waitRows(t.A, 1, `SELECT 1 FROM inbox WHERE ap_id = ? AND liked_via_outbox_object_ap_id IS NOT NULL`, note)

		like := t.one(t.A, `SELECT ap_id FROM outbox WHERE ap_type = 'Like'`)["ap_id"].(string)
		act(t, t.A, "undo", url.Values{"ap_object_id": {like}})
		// BUG(port): the original stops at -1, not 0. Its Undo handler marks
		// the Like as deleted, then counts the remaining likes and subtracts
		// one more (app/boxes.py:1639-1645). The port keeps this bug until
		// parity, as the kit's bug-for-bug rule tells. The fix comes after
		// parity (prompts/06).
		t.waitRows(t.B, 1, `SELECT 1 FROM outbox WHERE ap_id = ? AND likes_count = -1`, note)
		t.waitRows(t.B, 1, `SELECT 1 FROM notifications WHERE notification_type = 'UNDO_LIKE'`)
		t.waitRows(t.A, 1, `SELECT 1 FROM inbox WHERE ap_id = ? AND liked_via_outbox_object_ap_id IS NULL`, note)
	}},

	{Name: "announce__and_undo", Run: func(t *T) {
		follow(t, t.A, t.B)
		note := newPost(t, t.B, url.Values{"content": {"Hello"}})
		t.waitRows(t.A, 1, `SELECT 1 FROM inbox WHERE ap_id = ?`, note)
		act(t, t.A, "announce", url.Values{"ap_object_id": {note}})
		t.waitRows(t.B, 1, `SELECT 1 FROM outbox WHERE ap_id = ? AND announces_count = 1`, note)
		t.waitRows(t.B, 1, `SELECT 1 FROM notifications WHERE notification_type = 'ANNOUNCE'`)

		announce := t.one(t.A, `SELECT ap_id FROM outbox WHERE ap_type = 'Announce'`)["ap_id"].(string)
		act(t, t.A, "undo", url.Values{"ap_object_id": {announce}})
		t.waitRows(t.B, 1, `SELECT 1 FROM outbox WHERE ap_id = ? AND announces_count = 0`, note)
		t.waitRows(t.B, 1, `SELECT 1 FROM notifications WHERE notification_type = 'UNDO_ANNOUNCE'`)
	}},

	{Name: "delete__note", Run: func(t *T) {
		follow(t, t.A, t.B)
		note := newPost(t, t.B, url.Values{"content": {"Hello"}})
		t.waitRows(t.A, 1, `SELECT 1 FROM inbox WHERE ap_id = ?`, note)
		act(t, t.B, "delete", url.Values{"ap_object_id": {note}})
		t.waitRows(t.B, 1, `SELECT 1 FROM outbox WHERE ap_id = ? AND is_deleted = 1`, note)
		t.waitRows(t.A, 1, `SELECT 1 FROM inbox WHERE ap_id = ? AND is_deleted = 1`, note)
	}},

	{Name: "poll__vote", Run: func(t *T) {
		follow(t, t.A, t.B)
		poll := postQuestion(t, t.B, "oneOf", 5, "A", "B")
		t.waitRows(t.A, 1, `SELECT 1 FROM inbox WHERE ap_id = ?`, poll)
		act(t, t.A, "vote", url.Values{"in_reply_to": {poll}, "name": {"A"}})
		t.waitRows(t.A, 1, `SELECT 1 FROM inbox WHERE ap_id = ? AND json(voted_for_answers) = json('["A"]')`, poll)
		t.waitRows(t.B, 1, `SELECT 1 FROM poll_answer WHERE name = 'A' AND poll_type = 'oneOf'`)
		expectVotesCounted(t, t.B, poll, "oneOf", "A")
		expectPollUpdated(t, t.A, poll, "oneOf", "A")
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

	// b ignores all activities from a blocked actor, except Undo
	// (app/boxes.py:2328-2330). Thus a's Follow gets no answer.
	{Name: "follow__peer_blocked_me", Run: func(t *T) {
		blockedBy(t, t.A, t.B)
		act(t, t.A, "follow", url.Values{"ap_actor_id": {BaseURL(t.B)}})
		t.waitProcessed(t.B, t.one(t.A, `SELECT ap_id FROM outbox WHERE ap_type = 'Follow'`)["ap_id"])
		t.expectRows(t.B, 0, `SELECT 1 FROM inbox WHERE ap_type = 'Follow'`)
		t.expectRows(t.B, 0, `SELECT 1 FROM follower`)
		t.expectRows(t.B, 0, `SELECT 1 FROM outbox WHERE ap_type = 'Accept'`)
	}},

	{Name: "post__direct_to_peer_blocked_me", Run: func(t *T) {
		blockedBy(t, t.A, t.B)
		dm := newPost(t, t.A, url.Values{"content": {"hi @b@b.test"}, "visibility": {"DIRECT"}})
		t.waitProcessed(t.B, dm+"/activity")
		t.expectRows(t.B, 0, `SELECT 1 FROM inbox WHERE ap_id = ?`, dm)
		t.expectRows(t.B, 0, `SELECT 1 FROM notifications WHERE notification_type = 'MENTION'`)
	}},

	// b boosts a note by a third actor that a doesn't know. a and b both
	// fetch the note (app/boxes.py:287-299, 2112-2212).
	{Name: "announce__third_party_note", Run: func(t *T) {
		c := t.NewFake("c")
		note := c.Note("Hello from c", asPublic)
		c.Publish(note)
		id := note["id"].(string)
		follow(t, t.A, t.B)
		act(t, t.B, "announce", url.Values{"ap_object_id": {id}})
		t.waitRows(t.B, 1, `SELECT 1 FROM inbox WHERE ap_id = ? AND announced_via_outbox_object_ap_id IS NOT NULL`, id)
		// a stores the Announce with a link to the fetched Note. The
		// Announce is in a's stream, because a follows b
		// (app/boxes.py:2196-2211).
		t.waitRows(t.A, 1, `SELECT 1 FROM inbox AS announce JOIN inbox AS note
			ON announce.relates_to_inbox_object_id = note.id
			WHERE announce.ap_type = 'Announce' AND announce.is_hidden_from_stream = 0
			AND note.ap_id = ? AND note.ap_type = 'Note'`, id)
	}},

	// The original makes the body of an Update when it sends the Update, not
	// when it adds the Update to the queue. If b processed the second vote
	// before the send, b's first Update to a counts the two answers.
	// Otherwise, it counts one answer. The comparison ignores these two
	// deliveries. expectPollUpdated checks that a applied them.
	{Name: "poll__vote_any_of", Unstable: isUpdateTo("a"), Run: func(t *T) {
		follow(t, t.A, t.B)
		poll := postQuestion(t, t.B, "anyOf", 5, "A", "B", "C")
		t.waitRows(t.A, 1, `SELECT 1 FROM inbox WHERE ap_id = ?`, poll)
		act(t, t.A, "vote", url.Values{"in_reply_to": {poll}, "name": {"A", "C"}})
		t.waitRows(t.A, 1, `SELECT 1 FROM inbox WHERE ap_id = ? AND json(voted_for_answers) = json('["A","C"]')`, poll)
		t.waitRows(t.B, 2, `SELECT 1 FROM poll_answer WHERE poll_type = 'anyOf'`)
		expectVotesCounted(t, t.B, poll, "anyOf", "A", "C")
		expectPollUpdated(t, t.A, poll, "anyOf", "A", "C")
	}},

	// If a bookmarks an object that it doesn't have, a fetches and stores the
	// object (app/admin.py:1068-1083).
	{Name: "bookmark__unknown_object", Run: func(t *T) {
		note := newPost(t, t.B, url.Values{"content": {"Hello"}})
		t.expectRows(t.A, 0, `SELECT 1 FROM inbox WHERE ap_id = ?`, note)
		act(t, t.A, "bookmark", url.Values{"ap_object_id": {note}})
		t.sawExchange("a", "GET", note, 200)
		t.expectRows(t.A, 1, `SELECT 1 FROM inbox WHERE ap_id = ? AND is_bookmarked = 1`, note)
		act(t, t.A, "unbookmark", url.Values{"ap_object_id": {note}})
		t.expectRows(t.A, 1, `SELECT 1 FROM inbox WHERE ap_id = ? AND is_bookmarked = 0`, note)
	}},

	// If a likes an object that it doesn't have, a first fetches and stores
	// the object (app/boxes.py:252-263).
	{Name: "like__unknown_object", Run: func(t *T) {
		note := newPost(t, t.B, url.Values{"content": {"Hello"}})
		act(t, t.A, "like", url.Values{"ap_object_id": {note}})
		t.sawExchange("a", "GET", note, 200)
		t.expectRows(t.A, 1, `SELECT 1 FROM inbox WHERE ap_id = ? AND liked_via_outbox_object_ap_id IS NOT NULL`, note)
		t.waitRows(t.B, 1, `SELECT 1 FROM outbox WHERE ap_id = ? AND likes_count = 1`, note)
	}},

	// If a opens the reply form for an object that it doesn't have, a fetches
	// and stores the object (app/admin.py:173-181).
	{Name: "reply__unknown_object", Run: func(t *T) {
		note := newPost(t, t.B, url.Values{"content": {"Hello"}})
		resp, err := t.Client.Get(t.Ctx, BaseURL(t.A)+"/admin/new?in_reply_to="+url.QueryEscape(note), "")
		t.Check(err)
		t.equal("status", resp.Status, 200)
		t.sawExchange("a", "GET", note, 200)
		t.expectRows(t.A, 1, `SELECT 1 FROM inbox WHERE ap_id = ?`, note)
	}},

	// The setting that hides an actor's boosts is local state. But a fetches
	// an unknown actor first (app/admin.py:968-993).
	{Name: "announces__hide_and_show", Run: func(t *T) {
		act(t, t.A, "hide_announces", url.Values{"ap_actor_id": {BaseURL(t.B)}})
		t.sawExchange("a", "GET", BaseURL(t.B)+"/", 200)
		t.expectRows(t.A, 1, `SELECT 1 FROM actor WHERE ap_id = ? AND are_announces_hidden_from_stream = 1`, BaseURL(t.B))
		act(t, t.A, "show_announces", url.Values{"ap_actor_id": {BaseURL(t.B)}})
		t.expectRows(t.A, 1, `SELECT 1 FROM actor WHERE ap_id = ? AND are_announces_hidden_from_stream = 0`, BaseURL(t.B))
	}},

	{Name: "pin__featured", Run: func(t *T) {
		note := newPost(t, t.A, url.Values{"content": {"Hello"}})
		act(t, t.A, "pin", url.Values{"ap_object_id": {note}})
		featured, err := t.Client.GetAP(t.Ctx, BaseURL(t.A)+"/featured")
		t.Check(err)
		t.equal("featured", []any{featured["totalItems"], itemIDs(featured["orderedItems"])}, []any{float64(1), []any{note}})
		act(t, t.A, "unpin", url.Values{"ap_object_id": {note}})
		featured, err = t.Client.GetAP(t.Ctx, BaseURL(t.A)+"/featured")
		t.Check(err)
		t.equal("featured after unpin", featured["totalItems"], float64(0))
	}},

	// A visitor of a's site follows a from the visitor's own account on b. a
	// finds b's subscribe template with WebFinger, and sends the visitor to
	// it (app/main.py:1190-1209).
	{Name: "remote_follow", Run: func(t *T) {
		resp, err := t.Client.Post(t.Ctx, t.A, "/remote_follow", url.Values{"profile": {"b@b.test"}})
		t.Check(err)
		remoteRedirect(t, t.A, t.B, resp)
	}},

	// BUG(port): the original sends the visitor to a's profile, not to the
	// object. post_remote_interaction fills the template with ID in place of
	// ap_id (app/main.py:1233-1253).
	{Name: "remote_interaction", Run: func(t *T) {
		note := newPost(t, t.A, url.Values{"content": {"Hello"}})
		resp, err := t.Client.Post(t.Ctx, t.A, "/remote_interaction", url.Values{"profile": {"@b@b.test"}, "ap_id": {note}})
		t.Check(err)
		remoteRedirect(t, t.A, t.B, resp)
	}},
}
