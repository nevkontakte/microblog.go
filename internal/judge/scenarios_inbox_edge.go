package judge

import "net/url"

// InboxEdgeScenarios test the remaining parts of save_to_inbox
// (app/boxes.py:2282-2541): all branches of its dispatch, and the requests
// that it rejects or ignores.
var InboxEdgeScenarios = []Scenario{
	// A Follow and a Create with a signature that names the algorithm
	// "hs2019", as some servers other than Mastodon send it. The original
	// accepts the name, and verifies the signature as RSA-SHA256
	// (app/httpsig.py:79-83, 194).
	{Name: "inbox__hs2019_signature", Run: func(t *T) {
		f := t.NewFake("f")
		resp, err := f.SendHS2019(t.Ctx, t.A, f.Activity("Follow", BaseURL(t.A)))
		t.Check(err)
		t.equal("follow", resp.Status, 202)
		t.waitRows(t.A, 1, `SELECT 1 FROM follower WHERE ap_actor_id = ?`, f.ID())

		create := f.Create(f.Note("Hello", BaseURL(t.A)))
		resp, err = f.SendHS2019(t.Ctx, t.A, create)
		t.Check(err)
		t.equal("post", resp.Status, 202)
		t.waitRows(t.A, 1, `SELECT 1 FROM inbox WHERE ap_id = ?`, objectID(t, create))
	}},

	// The signed string uses the body's real digest (app/httpsig.py:234). Thus
	// the signature fails if it covers a Digest header that doesn't match.
	{Name: "inbox__bad_digest", Run: func(t *T) {
		f := t.NewFake("f")
		resp, err := f.SendBadDigest(t.Ctx, t.A, f.Activity("Follow", BaseURL(t.A)))
		t.Check(err)
		t.equal("status", resp.Status, 401)
		t.expectRows(t.A, 0, `SELECT 1 FROM incoming_activity`)
	}},

	// The other half: the original never compares the Digest header with the
	// body (app/httpsig.py:41-57). Thus it accepts an incorrect header if the
	// signature covers the real digest.
	//
	// BUG(port): a server must reject a Digest header that doesn't match the
	// body (RFC 3230), as Mastodon does. But no unsigned content gets in,
	// because the signature covers the real digest.
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

	// python-federation sends the actor document, without an activity, to
	// tell about a profile change (app/boxes.py:2287-2304).
	{Name: "inbox__actor_payload", Run: func(t *T) {
		f := t.NewFake("f")
		actor := f.actor()
		actor["name"] = "Renamed f"
		send(t, f, t.A, actor)
		t.waitAllProcessed(t.A, 1)
		t.expectRows(t.A, 1, `SELECT 1 FROM actor WHERE ap_id = ? AND json_extract(ap_actor, '$.name') = 'Renamed f'`, f.ID())

		// The instance ignores the actor document of a different actor
		// (app/boxes.py:2300-2304).
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

	// An object without an id is transient. The instance ignores Add and
	// Remove without an id (app/boxes.py:2266-2278, 2321-2323).
	{Name: "inbox__transient_object", Run: func(t *T) {
		f := t.NewFake("f")
		add := f.Activity("Add", f.ID()+"/note/1")
		delete(add, "id")
		add["target"] = f.ID() + "/featured"
		send(t, f, t.A, add)
		t.waitAllProcessed(t.A, 1)
		t.expectRows(t.A, 0, `SELECT 1 FROM inbox`)
	}},

	// The instance accepts an Undo from a blocked actor, and ignores all other
	// activities from that actor (app/boxes.py:2325-2330).
	//
	// BUG(port): the Undo of the Like sets likes_count to -1, as in
	// like__and_undo (app/boxes.py:1639-1645).
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

	// The actor of the Create must be the object's actor
	// (app/boxes.py:1834-1836). The instance records the failure and stores
	// nothing.
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

	// Honk sends replies to more servers with Read. The instance fetches and
	// stores the object (app/boxes.py:1867-1898).
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
		f := t.NewFake("f")
		followFake(t, t.A, f)
		note := f.Note("Hello", BaseURL(t.A))
		create := f.Create(note)
		send(t, f, t.A, create)
		t.waitProcessed(t.A, create["id"])
		edited := withContent(note, "Edited")
		update := f.Activity("Update", edited)
		send(t, f, t.A, update)
		t.waitProcessed(t.A, update["id"])
		t.expectRows(t.A, 1, `SELECT 1 FROM inbox WHERE ap_id = ? AND json_extract(ap_object, '$.content') = 'Edited'
			AND updated_at > created_at`, note["id"])
	}},

	// The instance ignores an Update of an object that it doesn't have. An
	// edit doesn't make the object (app/boxes.py:1791-1795).
	{Name: "inbox__update_unknown_note", Run: func(t *T) {
		f := t.NewFake("f")
		followFake(t, t.A, f)
		note := f.Note("Hello", BaseURL(t.A))
		update := f.Activity("Update", withContent(note, "Edited"))
		send(t, f, t.A, update)
		t.waitProcessed(t.A, update["id"])
		t.expectRows(t.A, 0, `SELECT 1 FROM inbox WHERE ap_id = ?`, note["id"])
	}},

	// The instance updates only actors and a few object types. It ignores an
	// Update of a different type, also for an object that it has
	// (app/boxes.py:1783-1808).
	{Name: "inbox__update_unsupported_type", Run: func(t *T) {
		f := t.NewFake("f")
		followFake(t, t.A, f)
		note := f.Note("Hello", BaseURL(t.A))
		create := f.Create(note)
		send(t, f, t.A, create)
		t.waitProcessed(t.A, create["id"])
		event := withContent(note, "Edited")
		event["type"] = "Event"
		update := f.Activity("Update", event)
		send(t, f, t.A, update)
		t.waitProcessed(t.A, update["id"])
		t.expectRows(t.A, 1, `SELECT 1 FROM inbox WHERE ap_id = ? AND ap_type = 'Note'
			AND json_extract(ap_object, '$.content') = 'Hello'`, note["id"])
	}},

	{Name: "inbox__update_note_wrong_actor", Run: func(t *T) {
		f := t.NewFake("f")
		followFake(t, t.A, f)
		note := f.Note("Hello", BaseURL(t.A))
		create := f.Create(note)
		send(t, f, t.A, create)
		t.waitProcessed(t.A, create["id"])
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
		t.expectRows(t.A, 1, `SELECT 1 FROM inbox WHERE ap_id = ? AND ap_type = 'Undo'`, undo["id"]) // the instance stores the Undo before the dispatch
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

	// The original doesn't support reactions. It keeps a reaction to an
	// object of a, and ignores other reactions (app/boxes.py:2504-2513).
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

	// The instance ignores PeerTube's View (app/boxes.py:2530-2532).
	{Name: "inbox__view", Run: func(t *T) {
		f := t.NewFake("f")
		view := f.Activity("View", f.ID()+"/video/1")
		send(t, f, t.A, view)
		t.waitProcessed(t.A, view["id"])
		t.expectRows(t.A, 0, `SELECT 1 FROM inbox`)
	}},
}
