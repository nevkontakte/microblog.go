package judge

import (
	"net/http"
	"net/url"
)

// MicropubScenarios test the Micropub endpoint (app/micropub.py) as a client
// app uses it, with a token that the app got through IndieAuth. The app is
// the visitor, so its exchanges are on the wire.
var MicropubScenarios = []Scenario{
	// A post from a form and a post from JSON. For each post:
	//   - The endpoint answers 201 with the post's URL.
	//   - The instance serves the post at that URL.
	//   - Followers receive the post.
	{Name: "micropub__create", Run: func(t *T) {
		follow(t, t.B, t.A)
		token := grantToken(t, t.A, "create")
		endpoint := BaseURL(t.A) + "/micropub"

		form := postForm(t, endpoint, url.Values{"h": {"entry"}, "content": {"From a form"}}, bearer(token))
		t.equal("form status", form.Status, 201)
		json := postJSON(t, endpoint, map[string]any{"type": []string{"h-entry"},
			"properties": map[string]any{"content": []string{"From JSON"}}}, bearer(token))
		t.equal("JSON status", json.Status, 201)
		// The token can also be in the form (app/indieauth.py:396-400).
		inForm := postForm(t, endpoint, url.Values{"content": {"Token in the form"}, "access_token": {token}}, "")
		t.equal("token in form status", inForm.Status, 201)

		for _, resp := range []*Response{form, json, inForm} {
			t.equal("created post", visit(t, resp.Header.Get("Location"), acceptAP).Status, 200)
		}
		t.waitRows(t.B, 3, `SELECT 1 FROM inbox WHERE ap_type = 'Note'`)
	}},

	// The source of a post, the configuration, and an unknown post
	// (app/micropub.py:20-54).
	{Name: "micropub__query", Run: func(t *T) {
		post := newPost(t, t.A, url.Values{"content": {"Some **markdown**"}})
		token := grantToken(t, t.A, "read")
		endpoint := BaseURL(t.A) + "/micropub"
		query := func(q url.Values) *Response { return fetchWithToken(t, endpoint+"?"+q.Encode(), token) }

		config := query(url.Values{"q": {"config"}})
		t.equal("config status", config.Status, 200)
		t.equal("config", decode(t, config), map[string]any{})

		source := query(url.Values{"q": {"source"}, "url": {post}})
		t.equal("source status", source.Status, 200)
		entry := decode(t, source)
		t.equal("source type", entry["type"], []any{"h-entry"})
		t.equal("source content", objectField(t, entry, "properties")["content"], []any{"Some **markdown**"})

		unknown := query(url.Values{"q": {"source"}, "url": {BaseURL(t.A) + "/o/unknown"}})
		t.equal("unknown status", unknown.Status, 400)
		t.equal("unknown error", decode(t, unknown)["error"], "invalid_request")
	}},

	// The instance removes a deleted post, and followers receive the Delete.
	{Name: "micropub__delete", Run: func(t *T) {
		follow(t, t.B, t.A)
		post := newPost(t, t.A, url.Values{"content": {"Secret, deleted"}})
		t.waitRows(t.B, 1, `SELECT 1 FROM inbox WHERE ap_id = ?`, post)
		token := grantToken(t, t.A, "delete")

		resp := postForm(t, BaseURL(t.A)+"/micropub", url.Values{"action": {"delete"}, "url": {post}}, bearer(token))
		t.equal("status", resp.Status, 200)
		t.waitRows(t.B, 1, `SELECT 1 FROM inbox WHERE ap_id = ? AND is_deleted`, post)
		t.equal("deleted post", visit(t, post, acceptAP).Status, 404)
		t.expectUndisclosed(post, "Secret")
	}},

	// BUG(port): an update answers 200 and changes nothing
	// (app/micropub.py:104-112).
	{Name: "micropub__update", Run: func(t *T) {
		post := newPost(t, t.A, url.Values{"content": {"Before"}})
		token := grantToken(t, t.A, "update")
		resp := postJSON(t, BaseURL(t.A)+"/micropub", map[string]any{"action": "update", "url": post,
			"replace": map[string]any{"content": []string{"After"}}}, bearer(token))
		t.equal("status", resp.Status, 200)
		t.equal("content", decode(t, visit(t, post, acceptAP))["content"], "<p>Before</p>\n")
	}},

	// Each action needs its scope, and each request needs a valid token
	// (app/micropub.py:79-80,98-99,105-106,123-124).
	{Name: "micropub__unauthorized", Run: func(t *T) {
		post := newPost(t, t.A, url.Values{"content": {"Kept"}})
		token := grantToken(t, t.A, "read")
		endpoint := BaseURL(t.A) + "/micropub"
		for _, form := range []url.Values{{"content": {"Not created"}},
			{"action": {"delete"}, "url": {post}}, {"action": {"update"}, "url": {post}}} {
			t.equal(form.Encode()+" status", postForm(t, endpoint, form, bearer(token)).Status, 401)
		}
		t.equal("no token", postForm(t, endpoint, url.Values{"content": {"Not created"}}, "").Status, 401)
		t.equal("unknown token", postForm(t, endpoint, url.Values{"content": {"Not created"}}, bearer("judge-unknown")).Status, 401)
		t.equal("query without token", request(t, http.MethodGet, endpoint+"?q=config", nil, nil).Status, 401)
		t.expectRows(t.A, 1, `SELECT 1 FROM outbox WHERE ap_type = 'Note' AND NOT is_deleted`)
	}},

	// Requests that the endpoint doesn't support
	// (app/micropub.py:83-95,115-122,135-145).
	{Name: "micropub__invalid", Run: func(t *T) {
		token := grantToken(t, t.A, "create", "delete")
		endpoint := BaseURL(t.A) + "/micropub"
		for _, form := range []url.Values{{"h": {"card"}, "name": {"Not an entry"}},
			{"action": {"undelete"}, "url": {BaseURL(t.A) + "/o/unknown"}},
			{"action": {"delete"}, "url": {BaseURL(t.A) + "/o/unknown"}}} {
			t.equal(form.Encode()+" status", postForm(t, endpoint, form, bearer(token)).Status, 400)
		}
		t.expectRows(t.A, 0, `SELECT 1 FROM outbox WHERE ap_type = 'Note'`)
	}},
}
