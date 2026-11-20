package judge

import (
	"net/url"
	"strings"
)

// LookupScenarios cover /admin/lookup (app/lookup.py, app/admin.py:91-158)
// for every kind of query it accepts and every way a lookup fails. The page
// is admin HTML, which the port may render differently, and it answers 200
// whether the lookup worked or not. So the scenarios check what the
// instance fetched, and in which order: an object's author is fetched only
// once the object parsed (app/ap_object.py:332-335).
//
// A lookup never stores what it finds; the checks that nothing was stored
// guard the port, not the original.
var LookupScenarios = []Scenario{
	{Name: "lookup__remote_actor", Run: func(t *T) {
		t.equal("status", lookup(t, t.A, "@b@b.test").Status, 200)
		t.sawInOrder("a", "GET", 200, finger(t.B, "b"), BaseURL(t.B)+"/")
	}},

	{Name: "lookup__actor_url", Run: func(t *T) {
		t.equal("status", lookup(t, t.A, BaseURL(t.B)).Status, 200)
		t.sawExchange("a", "GET", BaseURL(t.B)+"/", 200)
	}},

	{Name: "lookup__note", Run: func(t *T) {
		lookupObject(t, newPost(t, t.B, url.Values{"content": {"Hello"}}))
	}},

	{Name: "lookup__article", Run: func(t *T) {
		lookupObject(t, newPost(t, t.B, url.Values{"content": {"Hello"}, "name": {"Article"}}))
	}},

	{Name: "lookup__question", Run: func(t *T) {
		lookupObject(t, newPost(t, t.B, url.Values{"content": {"Which?"}, "poll_type": {"oneOf"},
			"poll_duration": {"5"}, "poll_answer_1": {"A"}, "poll_answer_2": {"B"}}))
	}},

	// Some servers serve an object wrapped in its Create (app/lookup.py:44-47).
	{Name: "lookup__object_wrapped_in_create", Run: func(t *T) {
		f := t.NewFake("f")
		create := f.Create(f.Note("Hello", asPublic))
		f.Publish(create)
		t.equal("status", lookup(t, t.A, create["id"].(string)).Status, 200)
		t.sawInOrder("a", "GET", 200, create["id"].(string), f.ID()+"/")
		t.expectRows(t.A, 0, `SELECT 1 FROM inbox`)
	}},

	// A web page that links to its ActivityPub representation
	// (app/lookup.py:25-36).
	{Name: "lookup__html_alternate", Run: func(t *T) {
		f := t.NewFake("f")
		note := f.Note("Hello", asPublic)
		f.Publish(note)
		page := f.ID() + "/@f/1"
		f.PublishPage(page, `<html><head><link rel="alternate" type="application/activity+json" href="`+
			note["id"].(string)+`"></head><body><p>Hello</p></body></html>`)
		t.equal("status", lookup(t, t.A, page).Status, 200)
		t.sawInOrder("a", "GET", 200, page, note["id"].(string), f.ID()+"/")
	}},

	{Name: "lookup__known_actor", Run: func(t *T) {
		follow(t, t.A, t.B)
		resp := lookup(t, t.A, "@b@b.test")
		t.equal("status", resp.Status, 302)
		t.hasSuffix("redirect", resp.Header.Get("Location"), "/admin/profile?actor_id="+BaseURL(t.B))
	}},

	{Name: "lookup__known_object", Run: func(t *T) {
		note := remoteNote(t)
		resp := lookup(t, t.A, note)
		t.equal("status", resp.Status, 302)
		loc, _, _ := strings.Cut(resp.Header.Get("Location"), "#")
		t.hasSuffix("redirect", loc, "/admin/object?ap_id="+note)
	}},

	{Name: "lookup__unknown_handle", Run: func(t *T) {
		t.equal("status", lookup(t, t.A, "@nobody@b.test").Status, 200)
		t.sawExchange("a", "GET", finger(t.B, "nobody"), 404)
		t.expectRows(t.A, 0, `SELECT 1 FROM actor`)
	}},

	{Name: "lookup__missing_object", Run: func(t *T) {
		missing := BaseURL(t.B) + "/o/0123456789abcdef0123456789abcdef"
		t.equal("status", lookup(t, t.A, missing).Status, 200)
		t.sawExchange("a", "GET", missing, 404)
	}},

	{Name: "lookup__gone_object", Run: func(t *T) {
		f := t.NewFake("f")
		f.Gone = true
		gone := f.ID() + "/note/1"
		t.equal("status", lookup(t, t.A, gone).Status, 200)
		t.sawExchange("a", "GET", gone, 410)
	}},

	// a doesn't follow b, so b's followers-only note isn't served to it
	// (app/main.py:706-742).
	{Name: "lookup__private_object", Run: func(t *T) {
		note := newPost(t, t.B, url.Values{"content": {"Hello"}, "visibility": {"FOLLOWERS_ONLY"}})
		t.equal("status", lookup(t, t.A, note).Status, 200)
		t.sawExchange("a", "GET", note, 404)
	}},

	{Name: "lookup__unreachable_host", Run: func(t *T) {
		t.equal("status", lookup(t, t.A, "@x@nowhere.test").Status, 200)
		t.sawExchange("a", "CONNECT", "nowhere.test:443", 502)
		t.expectRows(t.A, 0, `SELECT 1 FROM actor`)
	}},
}
