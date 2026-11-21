package judge

import (
	"net/url"
)

// C2SScenarios test the ActivityPub client-to-server publication (POST
// /outbox, app/main.py:635-675), as a client app uses it, with an access
// token. The app is the visitor, so its exchanges are on the wire.
var C2SScenarios = []Scenario{
	// A Create for each audience. The instance answers with the object and
	// its URL. It stores and delivers the object with the visibility that
	// the audience gives.
	{Name: "c2s__create", Run: func(t *T) {
		follow(t, t.B, t.A)
		token := grantToken(t, t.A, "create")
		public, followers := asPublic, BaseURL(t.A)+"/followers"
		for _, c := range []struct {
			content string
			to, cc  []string
			served  int // the status for an anonymous visitor
		}{
			{"Public", []string{public}, []string{followers}, 200},
			{"Unlisted", []string{followers}, []string{public}, 200},
			// BUG(port): the original publishes a followers-only Create as
			// direct (app/main.py:651-656).
			{"Followers only", []string{followers}, nil, 404},
			{"Direct", []string{BaseURL(t.B)}, nil, 404},
		} {
			resp := postJSON(t, BaseURL(t.A)+"/outbox", c2sCreate(t.A, c2sNote(c.content, c.to, c.cc)), bearer(token))
			t.equal(c.content+" status", resp.Status, 201)
			t.equal(c.content+" post", visit(t, resp.Header.Get("Location"), acceptAP).Status, c.served)
		}
		t.equal("visibilities", t.rows(t.A, `SELECT json_extract(ap_object, '$.content') AS content, visibility FROM outbox WHERE ap_type = 'Note' ORDER BY id`), []map[string]any{
			{"content": "<p>Public</p>\n", "visibility": "PUBLIC"}, {"content": "<p>Unlisted</p>\n", "visibility": "UNLISTED"},
			{"content": "<p>Followers only</p>\n", "visibility": "DIRECT"}, {"content": "<p>Direct</p>\n", "visibility": "DIRECT"}})
		// BUG(port): the original uses the client's addressing only for the
		// visibility. The recipients come from the mentions in the content,
		// so a direct Create to b goes to no recipient (app/boxes.py,
		// send_create).
		inbox := BaseURL(t.B) + "/inbox"
		t.equal("queued", t.rows(t.A, `SELECT json_extract(o.ap_object, '$.content') AS content, a.recipient FROM outgoing_activity a
			JOIN outbox o ON o.id = a.outbox_object_id WHERE o.ap_type = 'Note' ORDER BY a.id`),
			[]map[string]any{{"content": "<p>Public</p>\n", "recipient": inbox}, {"content": "<p>Unlisted</p>\n", "recipient": inbox}})
		t.waitRows(t.B, 2, `SELECT 1 FROM inbox WHERE ap_type = 'Note'`)
	}},

	// A reply with a content warning. The reply is sensitive.
	{Name: "c2s__reply", Run: func(t *T) {
		follow(t, t.A, t.B)
		follow(t, t.B, t.A)
		peer := newPost(t, t.B, url.Values{"content": {"From b"}})
		t.waitRows(t.A, 1, `SELECT 1 FROM inbox WHERE ap_id = ?`, peer)
		token := grantToken(t, t.A, "create")
		note := c2sNote("A reply", []string{asPublic}, []string{BaseURL(t.A) + "/followers"})
		note["inReplyTo"], note["summary"], note["sensitive"] = peer, "A warning", true
		t.equal("status", postJSON(t, BaseURL(t.A)+"/outbox", c2sCreate(t.A, note), bearer(token)).Status, 201)
		t.waitRows(t.B, 1, `SELECT 1 FROM inbox WHERE json_extract(ap_object, '$.inReplyTo') = ?
			AND json_extract(ap_object, '$.summary') = 'A warning' AND json_extract(ap_object, '$.sensitive')`, peer)
	}},

	// A client app reads the inbox with a token: the collection and its first
	// page. They list what b delivered (app/main.py:1092-1150).
	{Name: "c2s__read_inbox", Run: func(t *T) {
		follow(t, t.A, t.B)
		post := newPost(t, t.B, url.Values{"content": {"From b"}})
		t.waitRows(t.A, 1, `SELECT 1 FROM inbox WHERE ap_id = ?`, post)
		token := grantToken(t, t.A, "read")
		inbox := BaseURL(t.A) + "/inbox"
		t.equal("no token", visit(t, inbox, acceptAP).Status, 401)
		collection := fetchWithToken(t, inbox, token)
		t.equal("collection", collection.Status, 200)
		t.equal("total", decode(t, collection)["totalItems"], float64(1))
		page := fetchWithToken(t, inbox+"?page=true", token)
		t.equal("page", page.Status, 200)
		t.equal("items", itemIDs(decode(t, page)["orderedItems"]), []any{post + "/activity"})
	}},

	// A publication needs a valid token (app/indieauth.py:468-476).
	{Name: "c2s__unauthorized", Run: func(t *T) {
		create := c2sCreate(t.A, c2sNote("Not posted", []string{asPublic}, nil))
		t.equal("no token", postJSON(t, BaseURL(t.A)+"/outbox", create, "").Status, 401)
		t.equal("unknown token", postJSON(t, BaseURL(t.A)+"/outbox", create, bearer("judge-unknown")).Status, 401)
		t.expectRows(t.A, 0, `SELECT 1 FROM outbox WHERE ap_type = 'Note'`)
	}},

	// BUG(port): an activity that isn't a Create raises ValueError("TODO").
	// A Create by a different actor fails an assert. The two errors give a
	// 500, not a 4xx (app/main.py:646-669).
	{Name: "c2s__unsupported", Run: func(t *T) {
		post := newPost(t, t.A, url.Values{"content": {"Liked"}})
		token := grantToken(t, t.A, "create")
		like := map[string]any{"@context": "https://www.w3.org/ns/activitystreams", "type": "Like",
			"actor": BaseURL(t.A), "object": post}
		t.equal("like", postJSON(t, BaseURL(t.A)+"/outbox", like, bearer(token)).Status, 500)
		create := c2sCreate(t.B, c2sNote("Not posted", []string{asPublic}, nil))
		t.equal("other actor", postJSON(t, BaseURL(t.A)+"/outbox", create, bearer(token)).Status, 500)
		t.expectRows(t.A, 1, `SELECT 1 FROM outbox WHERE ap_type = 'Note'`)
	}},
}
