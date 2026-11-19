package judge

import (
	"fmt"
	"net/url"
	"strings"
)

// OutboxScenarios port tests/test_outbox.py, tests/test_emoji.py and
// tests/test_tags.py. The remote actor of the original fixtures is the real
// instance b, so every delivery is also checked on the receiving side.
var OutboxScenarios = []Scenario{
	{Name: "outbox__no_activities", Run: func(t *T) {
		outbox, err := t.Client.GetAP(t.Ctx, BaseURL(t.A)+"/outbox")
		t.Check(err)
		t.equal("totalItems", outbox["totalItems"], float64(0))
		t.equal("orderedItems", outbox["orderedItems"], []any{})
	}},

	{Name: "send_follow_request", Run: func(t *T) {
		redirect := BaseURL(t.A) + "/"
		loc, err := t.Client.Act(t.Ctx, t.A, "follow", url.Values{"ap_actor_id": {BaseURL(t.B)}, "redirect_url": {redirect}})
		t.Check(err)
		t.equal("redirect", loc, redirect)
		followRow := t.one(t.A, `SELECT id, ap_type, activity_object_ap_id FROM outbox`)
		t.equal("outbox Follow", []any{followRow["ap_type"], followRow["activity_object_ap_id"]}, []any{"Follow", BaseURL(t.B)})
		t.equal("outgoing Follow", t.one(t.A, `SELECT outbox_object_id, recipient FROM outgoing_activity`),
			map[string]any{"outbox_object_id": followRow["id"], "recipient": BaseURL(t.B) + "/inbox"})
		t.waitRows(t.B, 1, `SELECT 1 FROM follower WHERE ap_actor_id = ?`, BaseURL(t.A))
	}},

	{Name: "send_delete__reverts_side_effects", Run: func(t *T) {
		follow(t, t.A, t.B)
		follow(t, t.B, t.A)
		note := newPost(t, t.B, url.Values{"content": {"Hello"}})
		t.waitRows(t.A, 1, `SELECT 1 FROM inbox WHERE ap_id = ?`, note)
		newPost(t, t.A, url.Values{"content": {"reply 1"}, "in_reply_to": {note}})
		reply2 := newPost(t, t.A, url.Values{"content": {"reply 2"}, "in_reply_to": {note}})
		t.waitRows(t.A, 1, `SELECT 1 FROM inbox WHERE ap_id = ? AND replies_count = 2`, note)
		t.waitRows(t.B, 1, `SELECT 1 FROM outbox WHERE ap_id = ? AND replies_count = 2`, note)
		// A drifted counter, as the original seeds: deleting must recount the
		// replies (app/boxes.py:230-245), not decrement.
		t.Check(Exec(t.A, `UPDATE inbox SET replies_count = 5 WHERE ap_id = ?`, note))

		redirect := BaseURL(t.A) + "/"
		loc, err := t.Client.Act(t.Ctx, t.A, "delete", url.Values{"ap_object_id": {reply2}, "redirect_url": {redirect}})
		t.Check(err)
		t.equal("redirect", loc, redirect)
		del := t.one(t.A, `SELECT id, activity_object_ap_id FROM outbox WHERE ap_type = 'Delete'`)
		t.equal("Delete object", del["activity_object_ap_id"], reply2)
		t.equal("outgoing Delete recipient", t.one(t.A,
			`SELECT recipient FROM outgoing_activity WHERE outbox_object_id = ?`, del["id"])["recipient"],
			BaseURL(t.B)+"/inbox")
		t.waitRows(t.A, 1, `SELECT 1 FROM inbox WHERE ap_id = ? AND replies_count = 1`, note)
		t.waitRows(t.B, 1, `SELECT 1 FROM outbox WHERE ap_id = ? AND replies_count = 1`, note)
	}},

	{Name: "send_create_activity__no_content", Run: func(t *T) {
		resp, err := t.Client.Post(t.Ctx, t.A, "/admin/actions/new",
			url.Values{"visibility": {"PUBLIC"}, "redirect_url": {BaseURL(t.A) + "/"}})
		t.Check(err)
		t.equal("status", resp.Status, 422)
	}},

	{Name: "send_create_activity__with_attachment", Run: func(t *T) {
		id := newPost(t, t.A, url.Values{"content": {"hello"}},
			File{Field: "files", Name: "attachment.txt", Data: []byte("hello")})
		note := t.one(t.A, `SELECT id, ap_id, ap_type, json_extract(ap_object, '$.summary') AS summary,
			json_extract(ap_object, '$.content') AS content, json_array_length(ap_object, '$.attachment') AS n,
			json_extract(ap_object, '$.attachment[0].type') AS type,
			json_extract(ap_object, '$.attachment[0].url') AS url FROM outbox`)
		t.equal("note", []any{note["ap_id"], note["ap_type"], note["summary"], note["content"], note["n"], note["type"]},
			[]any{id, "Note", nil, "<p>hello</p>\n", int64(1), "Document"})

		upload := t.one(t.A, `SELECT id, content_hash FROM upload`)
		t.equal("content hash", upload["content_hash"], "324dcf027dd4a30a932c441f365a25e86b173defa4b8e58948253471b81b72cf")
		// Existing posts link to attachments by this URL (app/models.py:245-251).
		attachment := BaseURL(t.A) + "/attachments/" + upload["content_hash"].(string) + "/attachment.txt"
		t.equal("attachment URL", note["url"], attachment)
		resp, err := t.Client.Get(t.Ctx, attachment, "")
		t.Check(err)
		t.equal("attachment", []any{resp.Status, string(resp.Body)}, []any{200, "hello"})
		t.equal("attachment row", t.one(t.A, `SELECT upload_id, outbox_object_id, filename FROM outbox_object_attachment`),
			map[string]any{"upload_id": upload["id"], "outbox_object_id": note["id"], "filename": "attachment.txt"})
	}},

	{Name: "send_create_activity__no_content_with_cw_and_attachments", Run: func(t *T) {
		id := newPost(t, t.A, url.Values{"content_warning": {"cw"}},
			File{Field: "files", Name: "attachment.txt", Data: []byte("hello")})
		note := t.one(t.A, `SELECT id, ap_id, ap_type, json_extract(ap_object, '$.summary') AS summary,
			json_extract(ap_object, '$.content') AS content, json_array_length(ap_object, '$.attachment') AS n FROM outbox`)
		t.equal("note", note, map[string]any{"id": note["id"], "ap_id": id, "ap_type": "Note",
			"summary": nil, "content": "<p>cw</p>\n", "n": int64(1)})
		upload := t.one(t.A, `SELECT id FROM upload`)
		t.equal("attachment row", t.one(t.A, `SELECT upload_id, outbox_object_id FROM outbox_object_attachment`),
			map[string]any{"upload_id": upload["id"], "outbox_object_id": note["id"]})
	}},

	{Name: "send_create_activity__no_followers_and_with_mention", Run: func(t *T) {
		id := newPost(t, t.A, url.Values{"content": {"hi @b@b.test"}})
		note := t.one(t.A, `SELECT id, ap_type FROM outbox`)
		t.equal("ap_type", note["ap_type"], "Note")
		t.equal("outgoing", t.one(t.A, `SELECT outbox_object_id, recipient FROM outgoing_activity`),
			map[string]any{"outbox_object_id": note["id"], "recipient": BaseURL(t.B) + "/inbox"})
		// The original never sets inbox.has_local_mention; the notification is
		// the observable effect of a mention.
		t.waitRows(t.B, 1, `SELECT 1 FROM notifications n JOIN inbox i ON n.inbox_object_id = i.id
			WHERE n.notification_type = 'MENTION' AND i.ap_id = ?`, id)
	}},

	{Name: "send_create_activity__with_followers", Run: func(t *T) {
		follow(t, t.B, t.A)
		id := newPost(t, t.A, url.Values{"content": {"hi followers"}})
		t.equal("outbox", outboxTypes(t, t.A), []any{"Accept", "Note"}) // the Accept is b's follow
		note := t.one(t.A, `SELECT id, ap_type FROM outbox WHERE ap_id = ?`, id)
		t.equal("outgoing recipient", t.one(t.A, `SELECT recipient FROM outgoing_activity WHERE outbox_object_id = ?`,
			note["id"])["recipient"], BaseURL(t.B)+"/inbox")
		t.waitRows(t.B, 1, `SELECT 1 FROM inbox WHERE ap_id = ? AND ap_type = 'Note'`, id)
	}},

	{Name: "send_create_activity__question__one_of", Run: func(t *T) {
		question(t, "oneOf", 5, []string{"A", "B"})
	}},
	{Name: "send_create_activity__question__any_of", Run: func(t *T) {
		question(t, "anyOf", 10, []string{"A", "B", "C", "D"})
	}},

	{Name: "send_create_activity__article", Run: func(t *T) {
		follow(t, t.B, t.A)
		id := newPost(t, t.A, url.Values{"content": {"hi followers"}, "name": {"Article"}})
		t.equal("outbox", outboxTypes(t, t.A), []any{"Accept", "Article"})
		article := t.one(t.A, `SELECT id, ap_type, json_extract(ap_object, '$.name') AS name FROM outbox WHERE ap_id = ?`, id)
		t.equal("article", []any{article["ap_type"], article["name"]}, []any{"Article", "Article"})
		t.equal("outgoing recipient", t.one(t.A, `SELECT recipient FROM outgoing_activity WHERE outbox_object_id = ?`,
			article["id"])["recipient"], BaseURL(t.B)+"/inbox")
		t.waitRows(t.B, 1, `SELECT 1 FROM inbox WHERE ap_id = ? AND ap_type = 'Article'`, id)
	}},

	// Covers test_emoji_ap_endpoint and test_emoji_ap_endpoint__not_found.
	// test_emoji_are_loaded reads a Python global; the 200 below shows the
	// emoji were loaded.
	{Name: "emoji_ap_endpoint", Run: func(t *T) {
		resp, err := t.Client.Get(t.Ctx, BaseURL(t.A)+"/e/goose_honk", "application/activity+json")
		t.Check(err)
		t.equal("status", resp.Status, 200)
		t.equal("content type", resp.Header.Get("Content-Type"), "application/activity+json")
		emoji, err := t.Client.GetAP(t.Ctx, BaseURL(t.A)+"/e/goose_honk")
		t.Check(err)
		t.equal("type", emoji["type"], "Emoji")
		resp, err = t.Client.Get(t.Ctx, BaseURL(t.A)+"/e/goose_honk2", "application/activity+json")
		t.Check(err)
		t.equal("unknown emoji status", resp.Status, 404)
	}},

	{Name: "emoji_note_with_emoji", Run: func(t *T) {
		id := newPost(t, t.A, url.Values{"content": {"😺 :goose_honk:"}})
		t.equal("outbox", t.one(t.A, `SELECT ap_id FROM outbox`)["ap_id"], id)
		tags := t.one(t.A, `SELECT ap_type, json_array_length(ap_object, '$.tag') AS n,
			json_extract(ap_object, '$.tag[0].type') AS type, json_extract(ap_object, '$.tag[0].name') AS name,
			json_extract(ap_object, '$.tag[0].icon.url') AS url FROM outbox WHERE ap_id = ?`, id)
		t.equal("emoji tag", []any{tags["ap_type"], tags["n"], tags["type"], tags["name"]},
			[]any{"Note", int64(1), "Emoji", ":goose_honk:"})
		html := page(t, id)
		for _, want := range []string{tags["url"].(string), fmt.Sprintf("/static/twemoji/%x.svg", '😺')} {
			if !strings.Contains(html, want) {
				t.Fatalf("HTML of %s does not contain %q", id, want)
			}
		}
	}},

	{Name: "tags__no_tags", Run: func(t *T) {
		resp, err := t.Client.Get(t.Ctx, BaseURL(t.A)+"/t/nope", "application/activity+json")
		t.Check(err)
		t.equal("status", resp.Status, 404)
	}},

	{Name: "tags__note_with_tag", Run: func(t *T) {
		id := newPost(t, t.A, url.Values{"content": {"Hello #testing"}})
		t.equal("outbox", t.one(t.A, `SELECT ap_id FROM outbox`)["ap_id"], id)
		tag := t.one(t.A, `SELECT ap_type, json_array_length(ap_object, '$.tag') AS n,
			json_extract(ap_object, '$.tag[0].type') AS type, json_extract(ap_object, '$.tag[0].name') AS name
			FROM outbox WHERE ap_id = ?`, id)
		t.equal("hashtag", tag, map[string]any{"ap_type": "Note", "n": int64(1), "type": "Hashtag", "name": "#testing"})
		if html := page(t, BaseURL(t.A)+"/t/testing"); !strings.Contains(html, "Hello") {
			t.Fatalf("tag page does not show the note")
		}
		coll, err := t.Client.GetAP(t.Ctx, BaseURL(t.A)+"/t/testing")
		t.Check(err)
		t.equal("tag collection", []any{coll["totalItems"], coll["orderedItems"]}, []any{float64(1), []any{id}})
	}},
}

// question posts a poll on a, which b follows, and checks it on both sides.
func question(t *T, pollType string, minutes int, answers []string) {
	follow(t, t.B, t.A)
	form := url.Values{"content": {"hi followers"}, "poll_type": {pollType}, "poll_duration": {fmt.Sprint(minutes)}}
	for i, a := range answers {
		form.Set(fmt.Sprintf("poll_answer_%d", i+1), a)
	}
	id := newPost(t, t.A, form)
	t.equal("outbox", outboxTypes(t, t.A), []any{"Accept", "Question"})
	other := map[string]string{"oneOf": "anyOf", "anyOf": "oneOf"}[pollType]
	q := t.one(t.A, `SELECT id, ap_type,
		json_extract(ap_object, '$.endTime') > strftime('%Y-%m-%dT%H:%M:%SZ', 'now') AS open,
		round((julianday(json_extract(ap_object, '$.endTime')) - julianday(json_extract(ap_object, '$.published'))) * 1440) AS minutes,
		json_extract(ap_object, '$.`+other+`') IS NULL AS single
		FROM outbox WHERE ap_id = ?`, id)
	t.equal("question", []any{q["ap_type"], q["open"], q["minutes"], q["single"]},
		[]any{"Question", int64(1), float64(minutes), int64(1)})
	var names []any
	for _, r := range t.rows(t.A, `SELECT json_extract(value, '$.name') AS name
		FROM outbox, json_each(outbox.ap_object, '$.`+pollType+`') WHERE ap_id = ? ORDER BY name`, id) {
		names = append(names, r["name"])
	}
	var want []any
	for _, a := range answers {
		want = append(want, a)
	}
	t.equal(pollType+" answers", names, want)
	t.equal("outgoing recipient", t.one(t.A, `SELECT recipient FROM outgoing_activity WHERE outbox_object_id = ?`,
		q["id"])["recipient"], BaseURL(t.B)+"/inbox")
	t.waitRows(t.B, 1, `SELECT 1 FROM inbox WHERE ap_id = ? AND ap_type = 'Question'`, id)
}

// page fetches an HTML page and fails unless it is 200.
func page(t *T, url string) string {
	resp, err := t.Client.Get(t.Ctx, url, "text/html")
	t.Check(err)
	if resp.Status != 200 {
		t.Fatalf("GET %s: status %d", url, resp.Status)
	}
	return string(resp.Body)
}

// outboxTypes returns the types of everything in i's outbox, sorted. The
// original tests asserted on an otherwise empty outbox.
func outboxTypes(t *T, i Instance) []any {
	var types []any
	for _, r := range t.rows(i, `SELECT ap_type FROM outbox ORDER BY ap_type`) {
		types = append(types, r["ap_type"])
	}
	return types
}
