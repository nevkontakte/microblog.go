package judge

import (
	"fmt"
	"net/url"
	"slices"
)

// MastodonScenarios replay what a real Mastodon server sent a microblog.pub
// instance (testdata/mastodon.json, recorded by cmd/record-mastodon) to the
// instance under judgment, signed again by the replay (replay.go). The
// replayed server's exchanges are on the wire as "m".
var MastodonScenarios = []Scenario{
	{Name: "mastodon__follow", Run: func(t *T) {
		mastodon := t.NewReplay()
		followEachOther(t, mastodon)
		t.Eventually("a's Accept and Follow reaching Mastodon", func() error {
			if got := activityTypes(mastodon.Inbox()); !slices.Equal(got, []string{"Accept", "Follow"}) {
				return fmt.Errorf("Mastodon got %v", got)
			}
			return nil
		})
	}},

	{Name: "mastodon__posts", Run: func(t *T) {
		mastodon := t.NewReplay()
		followEachOther(t, mastodon)
		for _, step := range []string{"post_public", "post_unlisted", "post_private", "post_direct",
			"post_mention_hashtag", "post_content_warning", "post_image", "post_poll"} {
			mastodon.Play(t.A, mastodon.Step(step))
		}
		t.equal("stored", t.rows(t.A, `SELECT ap_type, visibility, json_extract(ap_object, '$.summary') AS summary,
			json_array_length(json_extract(ap_object, '$.attachment')) AS attachments
			FROM inbox WHERE ap_type IN ('Note', 'Question') ORDER BY id`), []map[string]any{
			{"ap_type": "Note", "visibility": "PUBLIC", "summary": nil, "attachments": int64(0)},
			{"ap_type": "Note", "visibility": "UNLISTED", "summary": nil, "attachments": int64(0)},
			{"ap_type": "Note", "visibility": "FOLLOWERS_ONLY", "summary": nil, "attachments": int64(0)},
			{"ap_type": "Note", "visibility": "DIRECT", "summary": nil, "attachments": int64(0)},
			{"ap_type": "Note", "visibility": "PUBLIC", "summary": nil, "attachments": int64(0)},
			{"ap_type": "Note", "visibility": "PUBLIC", "summary": "A warning", "attachments": int64(0)},
			{"ap_type": "Note", "visibility": "PUBLIC", "summary": nil, "attachments": int64(1)},
			{"ap_type": "Question", "visibility": "PUBLIC", "summary": nil, "attachments": int64(0)},
		})
		t.expectRows(t.A, 2, `SELECT 1 FROM notifications WHERE notification_type = 'MENTION'`)
	}},

	{Name: "mastodon__interactions", Run: func(t *T) {
		mastodon := t.NewReplay()
		followEachOther(t, mastodon)
		mastodon.SetPlaceholder("a_note", newPost(t, t.A, url.Values{"content": {"Hello from microblog.pub"}}))
		for _, step := range []string{"like", "undo_like", "boost", "undo_boost", "reply"} {
			mastodon.Play(t.A, mastodon.Step(step))
		}
		// BUG(port): the undone like leaves likes_count at -1, as in
		// like__and_undo (app/boxes.py:1381-1395).
		t.equal("counts", t.one(t.A, `SELECT likes_count, announces_count, replies_count FROM outbox WHERE ap_type = 'Note'`),
			map[string]any{"likes_count": int64(-1), "announces_count": int64(0), "replies_count": int64(1)})
		t.equal("notifications", notificationTypes(t, t.A), []string{"ANNOUNCE", "FOLLOW_REQUEST_ACCEPTED", "LIKE",
			"MENTION", "NEW_FOLLOWER", "UNDO_ANNOUNCE", "UNDO_LIKE"})
	}},

	{Name: "mastodon__vote", Run: func(t *T) {
		mastodon := t.NewReplay()
		followEachOther(t, mastodon)
		mastodon.SetPlaceholder("a_question", postQuestion(t, t.A, "oneOf", 60, "Yes", "No"))
		mastodon.Play(t.A, mastodon.Step("vote"))
		t.equal("votes", t.rows(t.A, `SELECT name FROM poll_answer`), []map[string]any{{"name": "Yes"}})
	}},

	{Name: "mastodon__edit_pin_delete", Run: func(t *T) {
		mastodon := t.NewReplay()
		followEachOther(t, mastodon)
		for _, step := range []string{"post_public", "edit", "pin", "unpin", "delete"} {
			mastodon.Play(t.A, mastodon.Step(step))
		}
		t.equal("post", t.one(t.A, `SELECT json_extract(ap_object, '$.content') AS content, is_deleted FROM inbox WHERE ap_type = 'Note'`),
			map[string]any{"content": "<p>Hello again from Mastodon</p>", "is_deleted": int64(1)})
		// The pin and unpin (Add, Remove) are processed and dropped.
		t.equal("stored", activityTypes(t.rows(t.A, `SELECT ap_type AS type FROM inbox ORDER BY id`)),
			[]string{"Follow", "Accept", "Create", "Note", "Update", "Delete"})
	}},

	{Name: "mastodon__profile", Run: func(t *T) {
		mastodon := t.NewReplay()
		followEachOther(t, mastodon)
		mastodon.Play(t.A, mastodon.Step("update_profile"))
		t.equal("actor", t.one(t.A, `SELECT json_extract(ap_actor, '$.name') AS name, json_extract(ap_actor, '$.summary') AS summary
			FROM actor WHERE ap_id = ?`, mastodon.Set.Actor), map[string]any{"name": "Mastodon user", "summary": "<p>Recorded for the judge</p>"})
	}},

	{Name: "mastodon__unfollow_block", Run: func(t *T) {
		mastodon := t.NewReplay()
		followEachOther(t, mastodon)
		for _, step := range []string{"unfollow", "block", "unblock"} {
			mastodon.Play(t.A, mastodon.Step(step))
		}
		// Mastodon's block also rejects a's follow, which ends it.
		t.expectRows(t.A, 0, `SELECT 1 FROM follower`)
		t.expectRows(t.A, 0, `SELECT 1 FROM following`)
		t.equal("notifications", notificationTypes(t, t.A), []string{"BLOCKED", "FOLLOW_REQUEST_ACCEPTED",
			"FOLLOW_REQUEST_REJECTED", "NEW_FOLLOWER", "UNBLOCKED", "UNFOLLOW"})
	}},

	{Name: "mastodon__move", Run: func(t *T) {
		mastodon := t.NewReplay()
		followEachOther(t, mastodon)
		move := mastodon.Step("move")
		mastodon.Serve(move)
		mastodon.PlayOne(t.A, move, 0) // the Move, which makes a follow the new account
		t.waitRows(t.A, 2, `SELECT 1 FROM outbox WHERE ap_type = 'Follow'`)
		mastodon.SetPlaceholder("a_follow_alias", lastFollow(t, t.A))
		mastodon.PlayOne(t.A, move, 1)
		mastodon.PlayOne(t.A, move, 2)
		target := stringField(t, mastodon.Activity(move, 0), "target")
		t.equal("following", t.rows(t.A, `SELECT ap_actor_id FROM following`), []map[string]any{{"ap_actor_id": target}})
		t.expectRows(t.A, 1, `SELECT 1 FROM actor WHERE ap_id = ? AND json_extract(ap_actor, '$.movedTo') = ?`, mastodon.Set.Actor, target)
		t.expectRows(t.A, 1, `SELECT 1 FROM notifications WHERE notification_type = 'MOVE'`)
	}},

	{Name: "mastodon__delete_account", Run: func(t *T) {
		mastodon := t.NewReplay()
		followEachOther(t, mastodon)
		mastodon.Play(t.A, mastodon.Step("delete_account"))
		t.expectRows(t.A, 1, `SELECT 1 FROM actor WHERE ap_id = ? AND is_deleted = 1`, mastodon.Set.Actor)
		t.expectRows(t.A, 0, `SELECT 1 FROM follower`)
		t.expectRows(t.A, 0, `SELECT 1 FROM following`)
	}},

	// A reply to a's note is forwarded by a to its followers, here b, with
	// Mastodon's LD signature, which b checks (app/boxes.py:2001-2025,
	// 2333-2366).
	{Name: "mastodon__forwarded_reply", Run: func(t *T) {
		mastodon := t.NewReplay()
		follow(t, t.B, t.A)
		followEachOther(t, mastodon)
		note := newPost(t, t.A, url.Values{"content": {"Hello from microblog.pub"}})
		mastodon.SetPlaceholder("a_note", note)
		t.waitRows(t.B, 1, `SELECT 1 FROM inbox WHERE ap_id = ?`, note)
		reply := mastodon.Step("reply")
		mastodon.Play(t.A, reply)
		t.waitRows(t.B, 1, `SELECT 1 FROM inbox WHERE ap_id = ?`, objectID(t, mastodon.Activity(reply, 0)))
	}},

	// A post forwarded by another server than its author's, which a trusts
	// for its LD signature.
	{Name: "mastodon__forwarded_by_third_party", Run: func(t *T) {
		mastodon := t.NewReplay()
		mastodon.Serve(mastodon.Step("follow")) // Mastodon's actor, for its key
		activity := mastodon.Activity(mastodon.Step("post_mention_hashtag"), 0)
		mastodon.Resign(activity)
		send(t, t.NewFake("f"), t.A, activity)
		t.waitProcessed(t.A, activity["id"])
		t.expectRows(t.A, 1, `SELECT 1 FROM inbox WHERE ap_id = ? AND ap_actor_id = ?`, objectID(t, activity), mastodon.Set.Actor)
		t.expectRows(t.A, 1, `SELECT 1 FROM notifications WHERE notification_type = 'MENTION'`)
	}},

	// A forwarded post whose LD signature fails, which a fetches from its
	// author's server instead (app/boxes.py:2342-2363).
	{Name: "mastodon__forwarded_bad_signature", Run: func(t *T) {
		mastodon := t.NewReplay()
		mastodon.Serve(mastodon.Step("follow"))       // Mastodon's actor, for its key
		mastodon.Serve(mastodon.Step("accept_again")) // the post's activity, as Mastodon serves it
		activity := mastodon.Activity(mastodon.Step("post_mention_hashtag"), 0)
		mastodon.Resign(activity)
		objectField(t, activity, "object")["content"] = "Altered after signing"
		send(t, t.NewFake("f"), t.A, activity)
		t.waitProcessed(t.A, activity["id"])
		t.expectRows(t.A, 1, `SELECT 1 FROM inbox WHERE ap_id = ? AND json_extract(ap_object, '$.content') LIKE '<p>Hello %'`,
			objectID(t, activity))
		t.expectRows(t.A, 0, `SELECT 1 FROM inbox WHERE json_extract(ap_object, '$.content') LIKE '%Altered%'`)
	}},
}

// followEachOther has the replayed Mastodon user and a follow each other, as the
// recording began: Mastodon's Follow, which a accepts, then a's, which
// Mastodon accepts.
func followEachOther(t *T, mastodon *Replay) {
	mastodon.Play(t.A, mastodon.Step("follow"))
	t.waitRows(t.A, 1, `SELECT 1 FROM follower WHERE ap_actor_id = ?`, mastodon.Set.Actor)
	act(t, t.A, "follow", url.Values{"ap_actor_id": {mastodon.Set.Actor}})
	mastodon.SetPlaceholder("a_follow", lastFollow(t, t.A))
	mastodon.Play(t.A, mastodon.Step("accept"))
	t.waitRows(t.A, 1, `SELECT 1 FROM following WHERE ap_actor_id = ?`, mastodon.Set.Actor)
}

// objectID is the id of the object an activity carries.
func objectID(t *T, activity map[string]any) string {
	return stringField(t, objectField(t, activity, "object"), "id")
}

// activityTypes lists the types of activities, in order.
func activityTypes(activities []map[string]any) []string {
	var types []string
	for _, a := range activities {
		types = append(types, fmt.Sprint(a["type"]))
	}
	return types
}

// notificationTypes lists i's notifications by type, sorted.
func notificationTypes(t *T, i Instance) []string {
	var types []string
	for _, n := range t.rows(i, `SELECT notification_type FROM notifications ORDER BY notification_type`) {
		types = append(types, fmt.Sprint(n["notification_type"]))
	}
	return types
}
