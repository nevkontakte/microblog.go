package judge

import (
	"fmt"
	"net/url"
	"strings"
)

// PublicScenarios judge the public read surface (app/main.py): what
// anonymous browsers, feed readers and remote servers fetching without a
// signature are served, and what they must never be. The visitor's
// exchanges are on the wire, so every page, document and feed is compared
// with the original's (HTML by its microformats, feeds as XML trees).
var PublicScenarios = []Scenario{
	// One post of every kind, and every surface that lists or serves posts.
	// Followers-only, direct and deleted posts must not show anywhere, by
	// id or text; the rest show as the original shows them.
	{Name: "public__visibility", Run: func(t *T) {
		follow(t, t.B, t.A)
		follow(t, t.A, t.B)
		peer := newPost(t, t.B, url.Values{"content": {"From b"}})
		t.waitRows(t.A, 1, `SELECT 1 FROM inbox WHERE ap_id = ?`, peer)

		attach := func(text string) File { return File{Field: "files", Name: "attachment.txt", Data: []byte(text)} }
		public := newPost(t, t.A, url.Values{"content": {"Public #judge"}}, attach("public attachment"))
		unlisted := newPost(t, t.A, url.Values{"content": {"Unlisted #judge"}, "visibility": {"UNLISTED"}})
		followers := newPost(t, t.A, url.Values{"content": {"Secret for followers #judge"}, "visibility": {"FOLLOWERS_ONLY"}},
			attach("followers attachment"))
		direct := newPost(t, t.A, url.Values{"content": {"Secret for @b@b.test #judge"}, "visibility": {"DIRECT"}})
		article := newPost(t, t.A, url.Values{"content": {"Article #judge"}, "name": {"An article"}})
		poll := postQuestion(t, t.A, "oneOf", 5, "A", "B")
		reply := newPost(t, t.A, url.Values{"content": {"A reply"}, "in_reply_to": {peer}})
		pinned := newPost(t, t.A, url.Values{"content": {"Pinned"}})
		act(t, t.A, "pin", url.Values{"ap_object_id": {pinned}})
		deleted := newPost(t, t.A, url.Values{"content": {"Secret, deleted #judge"}}, attach("deleted attachment"))
		act(t, t.A, "pin", url.Values{"ap_object_id": {deleted}})
		act(t, t.A, "delete", url.Values{"ap_object_id": {deleted}})

		base := BaseURL(t.A)
		for _, path := range []string{"/", "/outbox", "/featured", "/followers", "/followers?page=true",
			"/following", "/following?page=true", "/t/judge", "/.well-known/nodeinfo", "/nodeinfo",
			"/.well-known/webfinger?resource=acct:a@a.test"} {
			visit(t, base+path, acceptAP)
		}
		for _, path := range []string{"/", "/articles", "/t/judge", "/followers", "/following",
			"/feed.json", "/feed.rss", "/feed.atom", "/robots.txt"} {
			visit(t, base+path, acceptHTML)
		}
		for _, id := range []string{public, unlisted, followers, direct, article, poll, reply, pinned, deleted} {
			visit(t, id, acceptAP)
			visit(t, id+"/activity", acceptAP)
			if resp := visit(t, id, acceptHTML); resp.Status == 301 { // an article's page
				visit(t, resp.Header.Get("Location"), acceptHTML)
			}
		}
		t.expectUndisclosed(followers, direct, deleted, "Secret")

		// BUG(port): an attachment is served to anyone who has its URL,
		// whatever its post's visibility, and after the post is deleted
		// (app/main.py:1533-1551). The URL holds the content's hash.
		for _, r := range t.rows(t.A, `SELECT o.ap_id, '/attachments/' || u.content_hash || '/' || a.filename AS path
			FROM outbox o JOIN outbox_object_attachment a ON a.outbox_object_id = o.id JOIN upload u ON u.id = a.upload_id`) {
			t.equal("attachment of "+r["ap_id"].(string), visit(t, base+r["path"].(string), "").Status, 200)
		}
	}},

	// Requests for what doesn't exist.
	{Name: "public__not_found", Run: func(t *T) {
		base := BaseURL(t.A)
		missing := base + "/o/0123456789abcdef0123456789abcdef"
		for _, c := range []struct {
			url, accept string
			status      int
		}{
			{missing, acceptAP, 404},
			{missing, acceptHTML, 404},
			{missing + "/activity", acceptAP, 404},
			{base + "/articles/0123456/no-such-article", acceptHTML, 404},
			{base + "/t/nothing", acceptAP, 404},
			{base + "/t/nothing", acceptHTML, 404},
			{base + "/attachments/" + strings.Repeat("0", 64) + "/f.txt", "", 404},
			{base + "/attachments/thumbnails/" + strings.Repeat("0", 64) + "/f.png", "image/webp", 404},
			{base + "/e/nothing", acceptAP, 404},
			{base + "/.well-known/webfinger", acceptAP, 422},
		} {
			t.equal(c.accept+" "+c.url, visit(t, c.url, c.accept).Status, c.status)
		}
	}},

	// An image's thumbnail is served as WebP to those who accept it.
	// BUG(port): a request without an Accept header fails with 500
	// (app/main.py:1569).
	{Name: "public__thumbnail", Run: func(t *T) {
		newPost(t, t.A, url.Values{"content": {"An image"}},
			File{Field: "files", Name: "image.png", ContentType: "image/png", Data: smallPNG()})
		upload := t.one(t.A, `SELECT content_hash, has_thumbnail FROM upload`)
		thumbnail := BaseURL(t.A) + "/attachments/thumbnails/" + upload["content_hash"].(string) + "/image.png"
		t.equal("has thumbnail", upload["has_thumbnail"], int64(1))
		t.equal("as WebP", visit(t, thumbnail, "image/webp,*/*").Header.Get("Content-Type"), "image/webp")
		t.equal("as the original", visit(t, thumbnail, "image/png").Header.Get("Content-Type"), "image/png")
		t.equal("without Accept", visit(t, thumbnail, "").Status, 500)
	}},

	// Every admin page and action sends an anonymous visitor to the login
	// page, and acts on nothing (app/admin.py:52-88).
	{Name: "public__admin_requires_login", Run: func(t *T) {
		base := BaseURL(t.A)
		for _, path := range []string{"/admin/lookup", "/admin/new", "/admin/bookmarks", "/admin/stream",
			"/admin/inbox", "/admin/direct_messages", "/admin/outbox", "/admin/notifications", "/admin/object",
			"/admin/profile", "/admin/logout"} {
			resp := visit(t, base+path, acceptHTML)
			login, _, _ := strings.Cut(resp.Header.Get("Location"), "?")
			t.equal(path, []any{resp.Status, login}, []any{302, base + "/admin/login"})
		}
		for _, action := range []string{"new", "follow", "block", "delete", "like", "announce", "pin", "vote"} {
			resp, err := t.Visitor.post(t.Ctx, t.A, "/admin/actions/"+action, "", url.Values{
				"content": {"Not me"}, "ap_actor_id": {BaseURL(t.B)}, "ap_object_id": {base}})
			t.Check(err)
			t.equal(action, resp.Status, 302)
		}
		t.expectRows(t.A, 0, `SELECT 1 FROM outbox`)
		t.equal("login page", visit(t, base+"/admin/login", acceptHTML).Status, 200)
	}},

	// The media proxy serves remote media under URLs it signs. URLs minted
	// by the original appear in cached and federated HTML, so the port must
	// accept them (feasibility report, call #5). The original's media proxy
	// doesn't go through HTTPS_PROXY, so the media is fetched from b's own
	// address, which debug mode allows.
	// BUG(port): a forged or expired URL fails with 500, not a 4xx: the
	// signature error isn't handled (app/main.py:1378-1388).
	{Name: "public__media_proxy", Run: func(t *T) {
		newPost(t, t.B, url.Values{"content": {"An image"}},
			File{Field: "files", Name: "image.png", ContentType: "image/png", Data: smallPNG()})
		path := "/attachments/" + t.one(t.B, `SELECT content_hash FROM upload`)["content_hash"].(string) + "/image.png"
		image := "http://" + t.B.Addr() + path
		valid := mediaURL(t.A, image, today()+7)
		resp := visit(t, valid, "image/png")
		t.equal("valid", []any{resp.Status, string(resp.Body)}, []any{200, string(visit(t, BaseURL(t.B)+path, "").Body)})
		t.equal("resized", visit(t, valid+"/50", "image/webp").Header.Get("Content-Type"), "image/webp")
		forged := strings.Replace(valid, fmt.Sprintf("/%d/", today()+7), fmt.Sprintf("/%d/", today()+8), 1)
		t.equal("forged", visit(t, forged, "image/png").Status, 500)
		t.equal("expired", visit(t, mediaURL(t.A, image, today()-1), "image/png").Status, 500)
	}},

	// Collections page by 20, newest first, and link to the next page.
	{Name: "public__collection_pages", Run: func(t *T) {
		for n := range 21 {
			fakeFollows(t, t.NewFake(fmt.Sprintf("f%02d", n)), t.A)
		}
		page := fetchAP(t, t.NewFake("reader"), BaseURL(t.A)+"/followers?page=true", false, 200)
		t.equal("first page", len(page["orderedItems"].([]any)), 20)
		next := visit(t, page["next"].(string), acceptAP)
		t.equal("next page", next.Status, 200)
	}},

	// tests/test_public.py: the index, and the followers and following pages,
	// shown or hidden.
	{Name: "public__index", Run: func(t *T) {
		base := BaseURL(t.A)
		resp := visit(t, base+"/", acceptHTML)
		t.equal("HTML", []any{resp.Status, strings.HasPrefix(resp.Header.Get("Content-Type"), "text/html")}, []any{200, true})
		for _, accept := range []string{"application/activity+json", "application/activity+json; charset=utf-8",
			"application/ld+json", `application/ld+json; profile="https://www.w3.org/ns/activitystreams"`} {
			resp := visit(t, base+"/", accept)
			t.equal(accept, []any{resp.Status, resp.Header.Get("Content-Type")}, []any{200, "application/activity+json"})
		}
	}},
	{Name: "public__collections_html",
		Config: map[string]string{"b": "hides_followers = true\nhides_following = true"},
		Run: func(t *T) {
			follow(t, t.A, t.B)
			follow(t, t.B, t.A)
			for _, path := range []string{"/followers", "/following"} {
				t.equal("shown "+path, visit(t, BaseURL(t.A)+path, acceptHTML).Status, 200)
				t.equal("hidden "+path, visit(t, BaseURL(t.B)+path, acceptHTML).Status, 404)
			}
		}},
}
