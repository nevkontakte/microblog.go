package judge

import "net/url"

// InboxEdgeScenarios cover the rest of save_to_inbox (app/boxes.py:2282-2541):
// every branch of its dispatch, and the requests it rejects or drops.
var InboxEdgeScenarios = []Scenario{
	// The signed string uses the body's real digest (app/httpsig.py:234), so
	// a Digest header that doesn't match fails the signature.
	{Name: "inbox__bad_digest", Run: func(t *T) {
		f := t.NewFake("f")
		resp, err := f.SendBadDigest(t.Ctx, t.A, f.Activity("Follow", BaseURL(t.A)))
		t.Check(err)
		t.equal("status", resp.Status, 401)
		t.expectRows(t.A, 0, `SELECT 1 FROM incoming_activity`)
	}},

	// The other half: the Digest header itself is never compared to the
	// body (app/httpsig.py:41-57), so a wrong header on a request signed over
	// the real digest is accepted.
	{Name: "inbox__digest_header_ignored", Run: func(t *T) {
		f := t.NewFake("f")
		follow := f.Activity("Follow", BaseURL(t.A))
		resp, err := f.SendWrongDigestHeader(t.Ctx, t.A, follow)
		t.Check(err)
		t.equal("status", resp.Status, 202)
		t.waitProcessed(t.A, follow["id"])
		t.expectRows(t.A, 1, `SELECT 1 FROM follower WHERE ap_actor_id = ?`, f.ID())
	}},

	{Name: "inbox__wrong_key", Run: func(t *T) {
		f := t.NewFake("f")
		resp, err := f.SendWrongKey(t.Ctx, t.A, f.Activity("Follow", BaseURL(t.A)))
		t.Check(err)
		t.equal("status", resp.Status, 401)
		t.expectRows(t.A, 0, `SELECT 1 FROM incoming_activity`)
	}},

	{Name: "inbox__blocked_server",
		Config: map[string]string{"a": `blocked_servers = [{hostname = "f.test", reason = "judge"}]`},
		Run: func(t *T) {
			f := t.NewFake("f")
			resp, err := f.Send(t.Ctx, t.A, f.Activity("Follow", BaseURL(t.A)))
			t.Check(err)
			t.equal("status", resp.Status, 403)
			t.expectRows(t.A, 0, `SELECT 1 FROM incoming_activity`)
		}},

	// python-federation sends the actor itself to announce a profile change
	// (app/boxes.py:2287-2304).
	{Name: "inbox__actor_payload", Run: func(t *T) {
		f := t.NewFake("f")
		actor := f.actor()
		actor["name"] = "Renamed f"
		send(t, f, t.A, actor)
		t.waitAllProcessed(t.A, 1)
		t.expectRows(t.A, 1, `SELECT 1 FROM actor WHERE ap_id = ? AND json_extract(ap_actor, '$.name') = 'Renamed f'`, f.ID())

		// Someone else's actor is ignored (app/boxes.py:2300-2304).
		g := t.NewFake("g")
		other := g.actor()
		other["name"] = "Renamed g"
		send(t, f, t.A, other)
		t.waitAllProcessed(t.A, 2)
		t.expectRows(t.A, 0, `SELECT 1 FROM actor WHERE json_extract(ap_actor, '$.name') = 'Renamed g'`)
	}},

	{Name: "inbox__update_actor", Run: func(t *T) {
		f := t.NewFake("f")
		actor := f.actor()
		delete(actor, "@context")
		actor["name"] = "Renamed f"
		update := f.Activity("Update", actor)
		send(t, f, t.A, update)
		t.waitProcessed(t.A, update["id"])
		t.expectRows(t.A, 1, `SELECT 1 FROM actor WHERE ap_id = ? AND json_extract(ap_actor, '$.name') = 'Renamed f'`, f.ID())
	}},

	// An object without an id is transient; Add and Remove are dropped
	// (app/boxes.py:2266-2278, 2321-2323).
	{Name: "inbox__transient_object", Run: func(t *T) {
		f := t.NewFake("f")
		add := f.Activity("Add", f.ID()+"/note/1")
		delete(add, "id")
		add["target"] = f.ID() + "/featured"
		send(t, f, t.A, add)
		t.waitAllProcessed(t.A, 1)
		t.expectRows(t.A, 0, `SELECT 1 FROM inbox`)
	}},

	// A blocked actor's Undo still goes through; anything else is dropped
	// (app/boxes.py:2325-2330). BUG(port): undoing the Like leaves
	// likes_count at -1, as in like__and_undo (app/boxes.py:1639-1645).
	{Name: "inbox__blocked_actor_undo", Run: func(t *T) {
		f := t.NewFake("f")
		note := newPost(t, t.A, url.Values{"content": {"Hello"}})
		like := f.Activity("Like", note)
		send(t, f, t.A, like)
		t.waitRows(t.A, 1, `SELECT 1 FROM outbox WHERE ap_id = ? AND likes_count = 1`, note)
		act(t, t.A, "block", url.Values{"ap_actor_id": {f.ID()}})

		again := f.Activity("Like", note)
		send(t, f, t.A, again)
		t.waitProcessed(t.A, again["id"])
		t.expectRows(t.A, 0, `SELECT 1 FROM inbox WHERE ap_id = ?`, again["id"])

		undo := f.Activity("Undo", like)
		send(t, f, t.A, undo)
		t.waitProcessed(t.A, undo["id"])
		t.expectRows(t.A, 1, `SELECT 1 FROM outbox WHERE ap_id = ? AND likes_count = -1`, note)
		t.expectRows(t.A, 1, `SELECT 1 FROM inbox AS liked JOIN inbox AS undo ON liked.undone_by_inbox_object_id = undo.id
			WHERE liked.ap_id = ? AND liked.is_deleted = 1 AND undo.ap_id = ?`, like["id"], undo["id"])
		t.expectRows(t.A, 1, `SELECT 1 FROM notifications WHERE notification_type = 'UNDO_LIKE'`)
	}},

	{Name: "inbox__duplicate_activity", Run: func(t *T) {
		f := t.NewFake("f")
		followFake(t, t.A, f)
		create := f.Create(f.Note("Hello", BaseURL(t.A)))
		send(t, f, t.A, create)
		send(t, f, t.A, create)
		t.waitRows(t.A, 2, `SELECT 1 FROM incoming_activity WHERE ap_id = ? AND is_processed = 1 AND error IS NULL`, create["id"])
		t.expectRows(t.A, 1, `SELECT 1 FROM inbox WHERE ap_id = ?`, create["id"])
	}},

	// The Create's actor must be the object's (app/boxes.py:1834-1836); the
	// failure is recorded and nothing is stored.
	{Name: "inbox__create_actor_mismatch", IncomingErrors: 1, Run: func(t *T) {
		f := t.NewFake("f")
		g := t.NewFake("g")
		followFake(t, t.A, f)
		create := f.Create(g.Note("Hello", BaseURL(t.A)))
		create["actor"] = f.ID()
		send(t, f, t.A, create)
		t.waitRows(t.A, 1, `SELECT 1 FROM incoming_activity WHERE ap_id = ? AND error IS NOT NULL`, create["id"])
		t.expectRows(t.A, 0, `SELECT 1 FROM inbox WHERE ap_type IN ('Create', 'Note')`)
	}},

	// Honk propagates replies with Read; the object is fetched and stored
	// (app/boxes.py:1867-1898).
	{Name: "inbox__read_activity", Run: func(t *T) {
		f := t.NewFake("f")
		g := t.NewFake("g")
		note := g.Note("Hello from g", asPublic)
		g.Publish(note)
		read := f.Activity("Read", note["id"])
		send(t, f, t.A, read)
		t.waitProcessed(t.A, read["id"])
		t.expectRows(t.A, 1, `SELECT 1 FROM inbox WHERE ap_id = ? AND ap_type = 'Note' AND actor_id = ?`,
			note["id"], t.actorRowID(t.A, g.ID()))
	}},

	{Name: "inbox__update_note", Run: func(t *T) {
		f, note := followedNote(t)
		edited := withContent(note, "Edited")
		update := f.Activity("Update", edited)
		send(t, f, t.A, update)
		t.waitProcessed(t.A, update["id"])
		t.expectRows(t.A, 1, `SELECT 1 FROM inbox WHERE ap_id = ? AND json_extract(ap_object, '$.content') = 'Edited'`, note["id"])
	}},

	{Name: "inbox__update_note_wrong_actor", Run: func(t *T) {
		_, note := followedNote(t)
		g := t.NewFake("g")
		update := g.Activity("Update", withContent(note, "Edited"))
		send(t, g, t.A, update)
		t.waitProcessed(t.A, update["id"])
		t.expectRows(t.A, 1, `SELECT 1 FROM inbox WHERE ap_id = ? AND json_extract(ap_object, '$.content') = 'Hello'`, note["id"])
	}},

	{Name: "inbox__undo_unknown", Run: func(t *T) {
		f := t.NewFake("f")
		undo := f.Activity("Undo", f.ID()+"/like/99")
		send(t, f, t.A, undo)
		t.waitProcessed(t.A, undo["id"])
		t.expectRows(t.A, 1, `SELECT 1 FROM inbox WHERE ap_id = ? AND ap_type = 'Undo'`, undo["id"]) // stored before dispatch
		t.expectRows(t.A, 0, `SELECT 1 FROM notifications`)
	}},

	{Name: "inbox__accept_unknown", Run: func(t *T) {
		f := t.NewFake("f")
		accept := f.Activity("Accept", BaseURL(t.A)+"/o/0123456789abcdef0123456789abcdef")
		send(t, f, t.A, accept)
		t.waitProcessed(t.A, accept["id"])
		t.expectRows(t.A, 1, `SELECT 1 FROM inbox WHERE ap_id = ? AND ap_type = 'Accept'`, accept["id"])
		t.expectRows(t.A, 0, `SELECT 1 FROM following`)
		t.expectRows(t.A, 0, `SELECT 1 FROM notifications`)
	}},

	// Reactions are unsupported: kept when they target a's object, dropped
	// otherwise (app/boxes.py:2504-2513).
	{Name: "inbox__emoji_react", Run: func(t *T) {
		f := t.NewFake("f")
		note := newPost(t, t.A, url.Values{"content": {"Hello"}})
		known := f.Activity("EmojiReact", note)
		known["content"] = "👍"
		unknown := f.Activity("EmojiReact", f.ID()+"/note/42")
		unknown["content"] = "👍"
		send(t, f, t.A, known)
		send(t, f, t.A, unknown)
		t.waitProcessed(t.A, known["id"])
		t.waitProcessed(t.A, unknown["id"])
		t.equal("reactions kept", t.one(t.A, `SELECT ap_id FROM inbox WHERE ap_type = 'EmojiReact'`)["ap_id"], known["id"])
	}},

	// PeerTube's View is dropped (app/boxes.py:2530-2532).
	{Name: "inbox__view", Run: func(t *T) {
		f := t.NewFake("f")
		view := f.Activity("View", f.ID()+"/video/1")
		send(t, f, t.A, view)
		t.waitProcessed(t.A, view["id"])
		t.expectRows(t.A, 0, `SELECT 1 FROM inbox`)
	}},
}
