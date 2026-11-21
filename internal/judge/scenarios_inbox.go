package judge

import (
	"encoding/json"
	"net/url"
)

// InboxScenarios port tests/test_inbox.py and
// tests/test_remote_actor_deletion.py. A fake actor sends the activities
// that the original fixtures made manually. The original wrote some states
// directly into the database. These scenarios make the same states with
// real interactions.
var InboxScenarios = []Scenario{
	{Name: "inbox_requires_httpsig", Run: func(t *T) {
		f := t.NewFake("f")
		resp, err := f.SendUnsigned(t.Ctx, t.A, map[string]any{})
		t.Check(err)
		t.equal("status", resp.Status, 401)
		var body struct{ Detail string }
		t.Check(json.Unmarshal(resp.Body, &body))
		t.equal("detail", body.Detail, "Invalid HTTP sig")
	}},

	{Name: "inbox_incoming_follow_request", Run: func(t *T) {
		f := t.NewFake("f")
		followAct := f.Activity("Follow", BaseURL(t.A))
		send(t, f, t.A, followAct)

		t.waitProcessed(t.A, followAct["id"])
		actor := t.one(t.A, `SELECT id, ap_id FROM actor`)
		t.equal("saved actor", actor["ap_id"], f.ID())
		inbox := t.one(t.A, `SELECT id, ap_object FROM inbox`)
		t.sameJSON("inbox Follow", inbox["ap_object"], followAct)
		follower := t.one(t.A, `SELECT ap_actor_id, actor_id, inbox_object_id FROM follower`)
		t.equal("follower", follower, map[string]any{
			"ap_actor_id": f.ID(), "actor_id": actor["id"], "inbox_object_id": inbox["id"]})
		accept := t.one(t.A, `SELECT id, ap_type, activity_object_ap_id FROM outbox`)
		t.equal("outbox Accept", []any{accept["ap_type"], accept["activity_object_ap_id"]},
			[]any{"Accept", followAct["id"]})
		outgoing := t.one(t.A, `SELECT outbox_object_id FROM outgoing_activity`)
		t.equal("outgoing Accept", outgoing["outbox_object_id"], accept["id"])
	}},

	{Name: "inbox_incoming_follow_request__manually_approves_followers",
		Config: map[string]string{"a": "manually_approves_followers = true"},
		Run: func(t *T) {
			f := t.NewFake("f")
			followAct := f.Activity("Follow", BaseURL(t.A))
			send(t, f, t.A, followAct)

			t.waitProcessed(t.A, followAct["id"])
			t.expectRows(t.A, 1, `SELECT 1 FROM notifications WHERE notification_type = 'PENDING_INCOMING_FOLLOWER'`)
			t.equal("saved actor", t.one(t.A, `SELECT ap_id FROM actor`)["ap_id"], f.ID())
			t.sameJSON("inbox Follow", t.one(t.A, `SELECT ap_object FROM inbox`)["ap_object"], followAct)
			t.expectRows(t.A, 0, `SELECT 1 FROM follower`)
		}},

	{Name: "inbox_accept_follow_request", Run: func(t *T) {
		f := t.NewFake("f")
		followFake(t, t.A, f)

		followRow := t.one(t.A, `SELECT id FROM outbox WHERE ap_type = 'Follow'`)
		accept := t.one(t.A, `SELECT ap_type, relates_to_outbox_object_id, actor_id FROM inbox`)
		t.equal("inbox Accept", accept, map[string]any{
			"ap_type": "Accept", "relates_to_outbox_object_id": followRow["id"], "actor_id": t.actorRowID(t.A, f.ID())})
		t.equal("following", t.one(t.A, `SELECT ap_actor_id FROM following`)["ap_actor_id"], f.ID())
	}},

	{Name: "inbox__create_from_follower", Run: func(t *T) {
		f := t.NewFake("f")
		fakeFollows(t, f, t.A)
		create := f.Create(f.Note("Hello", BaseURL(t.A)))
		send(t, f, t.A, create)

		t.waitRows(t.A, 1, `SELECT 1 FROM inbox WHERE ap_type = 'Note'`)
		t.equal("inbox Create", t.one(t.A, `SELECT ap_id FROM inbox WHERE ap_type = 'Create'`)["ap_id"], create["id"])
		t.equal("inbox Note", t.one(t.A, `SELECT ap_id FROM inbox WHERE ap_type = 'Note'`)["ap_id"],
			create["object"].(map[string]any)["id"])
	}},

	{Name: "inbox__create_already_deleted_object", Run: func(t *T) {
		f := t.NewFake("f")
		fakeFollows(t, f, t.A)
		note := f.Note("Hello", BaseURL(t.A))
		send(t, f, t.A, f.Activity("Delete", note["id"]))
		t.waitRows(t.A, 1, `SELECT 1 FROM inbox WHERE ap_type = 'Delete'`)

		create := f.Create(note)
		send(t, f, t.A, create)
		t.waitProcessed(t.A, create["id"])
		saved := t.one(t.A, `SELECT ap_id, is_deleted FROM inbox WHERE ap_type = 'Create'`)
		t.equal("inbox Create", saved, map[string]any{"ap_id": create["id"], "is_deleted": int64(1)})
		t.expectRows(t.A, 0, `SELECT 1 FROM inbox WHERE ap_type = 'Note'`)
	}},

	{Name: "inbox__actor_is_blocked", Run: func(t *T) {
		f := t.NewFake("f")
		fakeFollows(t, f, t.A)
		// The original test only sets actor.is_blocked. The admin's block
		// action also sends a Reject and removes the follower. The instance
		// ignores the activity (app/boxes.py:2328-2330) before the follower
		// logic, so the difference doesn't matter here.
		act(t, t.A, "block", url.Values{"ap_actor_id": {f.ID()}})
		t.waitRows(t.A, 1, `SELECT 1 FROM actor WHERE ap_id = ? AND is_blocked = 1`, f.ID())

		create := f.Create(f.Note("Hello", BaseURL(t.A)))
		send(t, f, t.A, create)
		t.waitProcessed(t.A, create["id"])
		t.expectRows(t.A, 0, `SELECT 1 FROM inbox WHERE ap_type != 'Follow'`)
	}},

	{Name: "inbox__move_activity", Run: func(t *T) {
		old := t.NewFake("old")
		followFake(t, t.A, old)
		followID := t.one(t.A, `SELECT ap_id FROM outbox WHERE ap_type = 'Follow'`)["ap_id"]
		moved := t.NewFake("new")
		moved.AlsoKnownAs = []string{old.ID()}

		move := old.Activity("Move", old.ID())
		move["target"] = moved.ID()
		send(t, old, t.A, move)

		t.waitProcessed(t.A, move["id"])
		var types []any
		for _, r := range t.rows(t.A, `SELECT ap_type FROM inbox ORDER BY ap_type`) {
			types = append(types, r["ap_type"])
		}
		t.equal("inbox objects", types, []any{"Accept", "Move"}) // the Accept answers a's Follow of the old account
		inboxMove := t.one(t.A, `SELECT id, actor_id FROM inbox WHERE ap_type = 'Move'`)
		t.equal("Move actor", inboxMove["actor_id"], t.actorRowID(t.A, old.ID()))
		t.waitRows(t.A, 0, `SELECT 1 FROM following`)
		t.waitRows(t.A, 1, `SELECT 1 FROM outbox WHERE ap_type = 'Undo' AND activity_object_ap_id = ?`, followID)
		t.waitRows(t.A, 1, `SELECT 1 FROM outbox WHERE ap_type = 'Follow' AND activity_object_ap_id = ?`, moved.ID())
		notif := t.one(t.A, `SELECT actor_id, inbox_object_id FROM notifications WHERE notification_type = 'MOVE'`)
		t.equal("MOVE notification", notif, map[string]any{
			"actor_id": t.actorRowID(t.A, moved.ID()), "inbox_object_id": inboxMove["id"]})
	}},

	{Name: "inbox__block_activity", Run: func(t *T) {
		f := t.NewFake("f")
		followFake(t, t.A, f)
		send(t, f, t.A, f.Activity("Block", BaseURL(t.A)))

		t.waitRows(t.A, 1, `SELECT 1 FROM notifications WHERE notification_type = 'BLOCKED'`)
		t.equal("saved actor", t.one(t.A, `SELECT ap_id FROM actor`)["ap_id"], f.ID())
		block := t.one(t.A, `SELECT id FROM inbox WHERE ap_type = 'Block'`)
		notif := t.one(t.A, `SELECT actor_id, inbox_object_id FROM notifications WHERE notification_type = 'BLOCKED'`)
		t.equal("BLOCKED notification", notif, map[string]any{
			"actor_id": t.actorRowID(t.A, f.ID()), "inbox_object_id": block["id"]})
	}},

	// The original test replaces the signature check with a mock that gives
	// "actor is gone" (app/httpsig.py:249-251). The instance then answers 202
	// and stores nothing (:294-296). Here the actor is really gone: the URL
	// of its key answers 410. The instance writes incoming_activity rows
	// during the request, so a check directly after the response is
	// sufficient.
	{Name: "inbox__incoming_delete_for_unknown_actor", Run: func(t *T) {
		f := t.NewFake("f")
		f.Gone = true
		send(t, f, t.A, f.Activity("Delete", f.ID()+"/note/1"))
		t.expectRows(t.A, 0, `SELECT 1 FROM incoming_activity`)
	}},

	// If an unknown actor deletes itself, the instance ignores the Delete
	// before it fetches the key (app/httpsig.py:210-222).
	{Name: "inbox__incoming_self_delete_for_unknown_actor", Run: func(t *T) {
		f := t.NewFake("f")
		send(t, f, t.A, f.Activity("Delete", f.ID()))
		t.expectRows(t.A, 0, `SELECT 1 FROM incoming_activity`)
	}},

	{Name: "inbox__incoming_delete_for_known_actor", Run: func(t *T) {
		f := t.NewFake("f")
		followFake(t, t.A, f)
		fakeFollows(t, f, t.A)
		actorID := t.actorRowID(t.A, f.ID())

		send(t, f, t.A, f.Activity("Delete", f.ID()))
		t.waitRows(t.A, 1, `SELECT 1 FROM actor WHERE id = ? AND is_deleted = 1`, actorID)
		t.waitRows(t.A, 0, `SELECT 1 FROM inbox WHERE actor_id = ? AND is_deleted = 0`, actorID)
		t.waitRows(t.A, 0, `SELECT 1 FROM following`)
		t.waitRows(t.A, 0, `SELECT 1 FROM follower`)
	}},
}
