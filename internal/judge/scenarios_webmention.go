package judge

import (
	"cmp"
	"net/url"
)

// WebmentionScenarios test Webmention (app/webmentions.py and
// app/utils/webmentions.py):
//   - The mentions that a site sends to a's endpoint. a checks each mention
//     against the source page.
//   - The mentions that a sends to the pages that its public posts link to.
//
// The sender is the visitor or a fake, so the exchanges are on the wire.
var WebmentionScenarios = []Scenario{
	// A page links to a post, then replies to it, likes it and reposts it.
	// The instance checks each mention against the page, and counts it by
	// its kind (app/webmentions.py:170-196,207-232).
	{Name: "webmention__incoming", Run: func(t *T) {
		post := newPost(t, t.A, url.Values{"content": {"Mention me"}})
		f := t.NewFake("f")
		for _, kind := range []string{"", "u-in-reply-to", "u-like-of", "u-repost-of"} {
			source := f.ID() + "/mention-" + cmp.Or(kind, "plain")
			f.PublishPage(source, mentionPage(source, post, kind))
			t.equal(kind+" status", sendWebmention(t, t.A, source, post).Status, 200)
			t.sawExchange("a", "GET", source, 200)
		}
		t.expectRows(t.A, 4, `SELECT 1 FROM webmention WHERE NOT is_deleted`)
		t.equal("counts", t.one(t.A, `SELECT webmentions_count, replies_count, likes_count, announces_count FROM outbox WHERE ap_id = ?`, post),
			map[string]any{"webmentions_count": int64(1), "replies_count": int64(1), "likes_count": int64(1), "announces_count": int64(1)})
		t.equal("post's page", visit(t, post, acceptHTML).Status, 200)
	}},

	// If a source removes its link to the target, the instance removes the
	// mention. If the source adds the link again, the instance restores the
	// mention (app/webmentions.py:63-75,106-153).
	{Name: "webmention__update", Run: func(t *T) {
		post := newPost(t, t.A, url.Values{"content": {"Mention me"}})
		f := t.NewFake("f")
		source := f.ID() + "/mention"
		f.PublishPage(source, mentionPage(source, post, "u-like-of"))
		t.equal("mention", sendWebmention(t, t.A, source, post).Status, 200)

		f.PublishPage(source, `<html><body><p>No link any more</p></body></html>`)
		t.equal("withdrawn", sendWebmention(t, t.A, source, post).Status, 400)
		t.expectRows(t.A, 1, `SELECT 1 FROM webmention WHERE is_deleted`)
		t.equal("likes after withdrawal", t.one(t.A, `SELECT likes_count FROM outbox WHERE ap_id = ?`, post)["likes_count"], int64(0))

		f.PublishPage(source, mentionPage(source, post, "u-like-of"))
		t.equal("restored", sendWebmention(t, t.A, source, post).Status, 200)
		t.expectRows(t.A, 1, `SELECT 1 FROM webmention WHERE NOT is_deleted`)
		t.equal("likes after restoration", t.one(t.A, `SELECT likes_count FROM outbox WHERE ap_id = ?`, post)["likes_count"], int64(1))
	}},

	// A mention can name an article by the URL of its page or by its id
	// (app/webmentions.py:77-86).
	{Name: "webmention__article", Run: func(t *T) {
		article := newPost(t, t.A, url.Values{"content": {"An article"}, "name": {"An article"}})
		resp := visit(t, article, acceptHTML)
		t.equal("article redirect", resp.Status, 301)
		page := resp.Header.Get("Location")
		f := t.NewFake("f")
		source := f.ID() + "/mention"
		f.PublishPage(source, mentionPage(source, page, ""))
		t.equal("status", sendWebmention(t, t.A, source, page).Status, 200)
		t.expectRows(t.A, 1, `SELECT 1 FROM webmention w JOIN outbox o ON o.id = w.outbox_object_id WHERE o.ap_id = ?`, article)
	}},

	// Mentions that a rejects (app/webmentions.py:43-57,88-95,119-123).
	{Name: "webmention__invalid", Run: func(t *T) {
		post := newPost(t, t.A, url.Values{"content": {"Mention me"}})
		f := t.NewFake("f")
		source := f.ID() + "/mention"
		f.PublishPage(source, `<html><body><p>No link</p></body></html>`)
		for what, target := range map[string]string{"same as source": source, "unknown target": BaseURL(t.A) + "/o/unknown",
			"not linked": post, "not a URL": "judge"} {
			t.equal(what, sendWebmention(t, t.A, source, target).Status, 400)
		}
		t.equal("source not a URL", sendWebmention(t, t.A, "judge", post).Status, 400)
		t.expectRows(t.A, 0, `SELECT 1 FROM webmention`)
	}},

	// BUG(port): a source that is gone fails with a 500. The handler reads
	// the result of the fetch, but after a 404 the result has no value
	// (app/webmentions.py:97-104).
	{Name: "webmention__gone_source", Run: func(t *T) {
		post := newPost(t, t.A, url.Values{"content": {"Mention me"}})
		f := t.NewFake("f")
		t.equal("status", sendWebmention(t, t.A, f.ID()+"/missing", post).Status, 500)
	}},

	// For a public post, a sends a mention to each linked page that has an
	// endpoint in its HTML. For a followers-only post, a sends no mention
	// (app/boxes.py:729-742, app/outgoing_activities.py:203-215).
	{Name: "webmention__outgoing", Run: func(t *T) {
		f := t.NewFake("f")
		page := f.ID() + "/page"
		f.PublishPage(page, `<html><head><link rel="webmention" href="/webmention"></head><body><p>A page</p></body></html>`)
		post := newPost(t, t.A, url.Values{"content": {"See [this page](" + page + ")"}})
		newPost(t, t.A, url.Values{"content": {"Secret: [this page](" + page + ")"}, "visibility": {"FOLLOWERS_ONLY"}})
		t.waitRows(t.A, 1, `SELECT 1 FROM outgoing_activity WHERE webmention_target IS NOT NULL AND is_sent`)
		t.expectRows(t.A, 1, `SELECT 1 FROM outgoing_activity WHERE webmention_target IS NOT NULL`)
		t.equal("webmentions", f.Webmentions(), []url.Values{{"source": {post}, "target": {page}}})
	}},
}
