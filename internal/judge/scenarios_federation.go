package judge

import (
	"fmt"
	"net/url"
	"slices"
	"strings"
)

// FederationScenarios cover the interactions the admin UI can trigger between
// two instances, beyond what the original test suite exercised.
var FederationScenarios = []Scenario{
	{Name: "post__visibility", Run: func(t *T) {
		follow(t, t.A, t.B)
		for _, v := range []string{"PUBLIC", "UNLISTED", "FOLLOWERS_ONLY", "DIRECT"} {
			form := url.Values{"visibility": {v}, "content": {"visible to " + v}}
			if v == "DIRECT" {
				form.Set("content", "hi @a@a.test")
			}
			id := newPost(t, t.B, form)
			t.waitRows(t.A, 1, `SELECT 1 FROM inbox WHERE ap_id = ? AND visibility = ?`, id, v)
			t.waitRows(t.B, 1, `SELECT 1 FROM outbox WHERE ap_id = ? AND visibility = ?`, id, v)
		}
	}},

	{Name: "post__reply", Run: func(t *T) {
		follow(t, t.A, t.B)
		follow(t, t.B, t.A)
		note := newPost(t, t.B, url.Values{"content": {"Hello"}})
		t.waitRows(t.A, 1, `SELECT 1 FROM inbox WHERE ap_id = ?`, note)
		reply := newPost(t, t.A, url.Values{"content": {"Hi back"}, "in_reply_to": {note}})
		t.waitRows(t.B, 1, `SELECT 1 FROM inbox WHERE ap_id = ?
			AND json_extract(ap_object, '$.inReplyTo') = ?`, reply, note)
		t.waitRows(t.B, 1, `SELECT 1 FROM outbox WHERE ap_id = ? AND replies_count = 1`, note)
	}},

	{Name: "like__and_undo", Run: func(t *T) {
		note := remoteNote(t)
		act(t, t.A, "like", url.Values{"ap_object_id": {note}})
		t.waitRows(t.B, 1, `SELECT 1 FROM outbox WHERE ap_id = ? AND likes_count = 1`, note)
		t.waitRows(t.B, 1, `SELECT 1 FROM notifications WHERE notification_type = 'LIKE'`)
		t.waitRows(t.A, 1, `SELECT 1 FROM inbox WHERE ap_id = ? AND liked_via_outbox_object_ap_id IS NOT NULL`, note)

		like := t.one(t.A, `SELECT ap_id FROM outbox WHERE ap_type = 'Like'`)["ap_id"].(string)
		act(t, t.A, "undo", url.Values{"ap_object_id": {like}})
		// BUG(port): the original ends at -1, not 0. Its Undo handler marks the
		// Like deleted, then recounts the remaining likes and subtracts one more
		// (app/boxes.py:1639-1645). The port reproduces this until parity, per
		// the kit's bug-for-bug rule; the fix comes after (prompts/06).
		t.waitRows(t.B, 1, `SELECT 1 FROM outbox WHERE ap_id = ? AND likes_count = -1`, note)
		t.waitRows(t.B, 1, `SELECT 1 FROM notifications WHERE notification_type = 'UNDO_LIKE'`)
		t.waitRows(t.A, 1, `SELECT 1 FROM inbox WHERE ap_id = ? AND liked_via_outbox_object_ap_id IS NULL`, note)
	}},

	{Name: "announce__and_undo", Run: func(t *T) {
		note := remoteNote(t)
		act(t, t.A, "announce", url.Values{"ap_object_id": {note}})
		t.waitRows(t.B, 1, `SELECT 1 FROM outbox WHERE ap_id = ? AND announces_count = 1`, note)
		t.waitRows(t.B, 1, `SELECT 1 FROM notifications WHERE notification_type = 'ANNOUNCE'`)

		announce := t.one(t.A, `SELECT ap_id FROM outbox WHERE ap_type = 'Announce'`)["ap_id"].(string)
		act(t, t.A, "undo", url.Values{"ap_object_id": {announce}})
		t.waitRows(t.B, 1, `SELECT 1 FROM outbox WHERE ap_id = ? AND announces_count = 0`, note)
		t.waitRows(t.B, 1, `SELECT 1 FROM notifications WHERE notification_type = 'UNDO_ANNOUNCE'`)
	}},

	{Name: "delete__note", Run: func(t *T) {
		note := remoteNote(t)
		act(t, t.B, "delete", url.Values{"ap_object_id": {note}})
		t.waitRows(t.B, 1, `SELECT 1 FROM outbox WHERE ap_id = ? AND is_deleted = 1`, note)
		t.waitRows(t.A, 1, `SELECT 1 FROM inbox WHERE ap_id = ? AND is_deleted = 1`, note)
	}},

	{Name: "poll__vote", Run: func(t *T) {
		follow(t, t.A, t.B)
		poll := newPost(t, t.B, url.Values{"content": {"Which?"}, "poll_type": {"oneOf"},
			"poll_duration": {"5"}, "poll_answer_1": {"A"}, "poll_answer_2": {"B"}})
		t.waitRows(t.A, 1, `SELECT 1 FROM inbox WHERE ap_id = ?`, poll)
		act(t, t.A, "vote", url.Values{"in_reply_to": {poll}, "name": {"A"}})
		t.waitRows(t.A, 1, `SELECT 1 FROM inbox WHERE ap_id = ? AND json(voted_for_answers) = json('["A"]')`, poll)
		t.waitRows(t.B, 1, `SELECT 1 FROM poll_answer WHERE name = 'A' AND poll_type = 'oneOf'`)
		pollCounted(t, poll, "oneOf", "A")
	}},

	{Name: "block__and_unblock", Run: func(t *T) {
		follow(t, t.B, t.A)
		act(t, t.A, "block", url.Values{"ap_actor_id": {BaseURL(t.B)}})
		t.waitRows(t.A, 1, `SELECT 1 FROM actor WHERE ap_id = ? AND is_blocked = 1`, BaseURL(t.B))
		t.waitRows(t.B, 1, `SELECT 1 FROM notifications WHERE notification_type = 'BLOCKED'`)
		act(t, t.A, "unblock", url.Values{"ap_actor_id": {BaseURL(t.B)}})
		t.waitRows(t.A, 1, `SELECT 1 FROM actor WHERE ap_id = ? AND is_blocked = 0`, BaseURL(t.B))
		t.waitRows(t.B, 1, `SELECT 1 FROM notifications WHERE notification_type = 'UNBLOCKED'`)
	}},

	// b drops everything from a blocked actor except Undo
	// (app/boxes.py:2328-2330), so a's Follow goes unanswered.
	{Name: "follow__peer_blocked_me", Run: func(t *T) {
		blockedBy(t, t.A, t.B)
		act(t, t.A, "follow", url.Values{"ap_actor_id": {BaseURL(t.B)}})
		t.waitProcessed(t.B, t.one(t.A, `SELECT ap_id FROM outbox WHERE ap_type = 'Follow'`)["ap_id"])
		t.expectRows(t.B, 0, `SELECT 1 FROM inbox WHERE ap_type = 'Follow'`)
		t.expectRows(t.B, 0, `SELECT 1 FROM follower`)
		t.expectRows(t.B, 0, `SELECT 1 FROM outbox WHERE ap_type = 'Accept'`)
	}},

	{Name: "post__direct_to_peer_blocked_me", Run: func(t *T) {
		blockedBy(t, t.A, t.B)
		dm := newPost(t, t.A, url.Values{"content": {"hi @b@b.test"}, "visibility": {"DIRECT"}})
		t.waitProcessed(t.B, dm+"/activity")
		t.expectRows(t.B, 0, `SELECT 1 FROM inbox WHERE ap_id = ?`, dm)
		t.expectRows(t.B, 0, `SELECT 1 FROM notifications WHERE notification_type = 'MENTION'`)
	}},

	// b boosts a note by a third actor that a has never seen: both fetch it
	// (app/boxes.py:287-299, 2112-2212).
	{Name: "announce__third_party_note", Run: func(t *T) {
		c := t.NewFake("c")
		note := c.Note("Hello from c", asPublic)
		c.Publish(note)
		id := note["id"].(string)
		follow(t, t.A, t.B)
		act(t, t.B, "announce", url.Values{"ap_object_id": {id}})
		t.waitRows(t.B, 1, `SELECT 1 FROM inbox WHERE ap_id = ? AND announced_via_outbox_object_ap_id IS NOT NULL`, id)
		// a stores the Announce linked to the fetched Note, in its stream
		// since a follows b (app/boxes.py:2196-2211).
		t.waitRows(t.A, 1, `SELECT 1 FROM inbox AS announce JOIN inbox AS note
			ON announce.relates_to_inbox_object_id = note.id
			WHERE announce.ap_type = 'Announce' AND announce.is_hidden_from_stream = 0
			AND note.ap_id = ? AND note.ap_type = 'Note'`, id)
	}},

	// The original builds an Update's body when it sends it, not when it
	// queues it, so whether b's first Update to a counts one answer or both
	// depends on whether b processed the second vote before sending. Those
	// two deliveries are left out of the comparison; pollCounted checks
	// that a applied them.
	{Name: "poll__vote_any_of", Unstable: isUpdateTo("a"), Run: func(t *T) {
		follow(t, t.A, t.B)
		poll := newPost(t, t.B, url.Values{"content": {"Which?"}, "poll_type": {"anyOf"},
			"poll_duration": {"5"}, "poll_answer_1": {"A"}, "poll_answer_2": {"B"}, "poll_answer_3": {"C"}})
		t.waitRows(t.A, 1, `SELECT 1 FROM inbox WHERE ap_id = ?`, poll)
		act(t, t.A, "vote", url.Values{"in_reply_to": {poll}, "name": {"A", "C"}})
		t.waitRows(t.A, 1, `SELECT 1 FROM inbox WHERE ap_id = ? AND json(voted_for_answers) = json('["A","C"]')`, poll)
		t.waitRows(t.B, 2, `SELECT 1 FROM poll_answer WHERE poll_type = 'anyOf'`)
		pollCounted(t, poll, "anyOf", "A", "C")
	}},

	// Bookmarking an object a doesn't have fetches and stores it
	// (app/admin.py:1068-1083).
	{Name: "bookmark__unknown_object", Run: func(t *T) {
		note := newPost(t, t.B, url.Values{"content": {"Hello"}})
		t.expectRows(t.A, 0, `SELECT 1 FROM inbox WHERE ap_id = ?`, note)
		act(t, t.A, "bookmark", url.Values{"ap_object_id": {note}})
		t.sawExchange("a", "GET", note, 200)
		t.expectRows(t.A, 1, `SELECT 1 FROM inbox WHERE ap_id = ? AND is_bookmarked = 1`, note)
		act(t, t.A, "unbookmark", url.Values{"ap_object_id": {note}})
		t.expectRows(t.A, 1, `SELECT 1 FROM inbox WHERE ap_id = ? AND is_bookmarked = 0`, note)
	}},

	// Liking an object a doesn't have fetches and stores it first
	// (app/boxes.py:252-263).
	{Name: "like__unknown_object", Run: func(t *T) {
		note := newPost(t, t.B, url.Values{"content": {"Hello"}})
		act(t, t.A, "like", url.Values{"ap_object_id": {note}})
		t.sawExchange("a", "GET", note, 200)
		t.expectRows(t.A, 1, `SELECT 1 FROM inbox WHERE ap_id = ? AND liked_via_outbox_object_ap_id IS NOT NULL`, note)
		t.waitRows(t.B, 1, `SELECT 1 FROM outbox WHERE ap_id = ? AND likes_count = 1`, note)
	}},

	// Opening the reply form for an object a doesn't have fetches and stores
	// it (app/admin.py:173-181).
	{Name: "reply__unknown_object", Run: func(t *T) {
		note := newPost(t, t.B, url.Values{"content": {"Hello"}})
		resp, err := t.Client.Get(t.Ctx, BaseURL(t.A)+"/admin/new?in_reply_to="+url.QueryEscape(note), "")
		t.Check(err)
		t.equal("status", resp.Status, 200)
		t.sawExchange("a", "GET", note, 200)
		t.expectRows(t.A, 1, `SELECT 1 FROM inbox WHERE ap_id = ?`, note)
	}},

	// Hiding an actor's boosts is local state, but an unknown actor is
	// fetched first (app/admin.py:968-993).
	{Name: "announces__hide_and_show", Run: func(t *T) {
		act(t, t.A, "hide_announces", url.Values{"ap_actor_id": {BaseURL(t.B)}})
		t.sawExchange("a", "GET", BaseURL(t.B)+"/", 200)
		t.expectRows(t.A, 1, `SELECT 1 FROM actor WHERE ap_id = ? AND are_announces_hidden_from_stream = 1`, BaseURL(t.B))
		act(t, t.A, "show_announces", url.Values{"ap_actor_id": {BaseURL(t.B)}})
		t.expectRows(t.A, 1, `SELECT 1 FROM actor WHERE ap_id = ? AND are_announces_hidden_from_stream = 0`, BaseURL(t.B))
	}},

	{Name: "pin__featured", Run: func(t *T) {
		note := newPost(t, t.A, url.Values{"content": {"Hello"}})
		act(t, t.A, "pin", url.Values{"ap_object_id": {note}})
		featured, err := t.Client.GetAP(t.Ctx, BaseURL(t.A)+"/featured")
		t.Check(err)
		t.equal("featured", []any{featured["totalItems"], itemIDs(featured["orderedItems"])}, []any{float64(1), []any{note}})
		act(t, t.A, "unpin", url.Values{"ap_object_id": {note}})
		featured, err = t.Client.GetAP(t.Ctx, BaseURL(t.A)+"/featured")
		t.Check(err)
		t.equal("featured after unpin", featured["totalItems"], float64(0))
	}},

	// A visitor of a's site follows a from their own account on b: a finds
	// b's subscribe template by WebFinger and sends the visitor there
	// (app/main.py:1190-1209).
	{Name: "remote_follow", Run: func(t *T) {
		resp, err := t.Client.Post(t.Ctx, t.A, "/remote_follow", url.Values{"profile": {"b@b.test"}})
		t.Check(err)
		remoteRedirect(t, resp)
	}},

	// BUG(port): the original sends the visitor to a's profile, not to the
	// object: post_remote_interaction formats the template with ID instead
	// of ap_id (app/main.py:1233-1253).
	{Name: "remote_interaction", Run: func(t *T) {
		note := newPost(t, t.A, url.Values{"content": {"Hello"}})
		resp, err := t.Client.Post(t.Ctx, t.A, "/remote_interaction", url.Values{"profile": {"@b@b.test"}, "ap_id": {note}})
		t.Check(err)
		remoteRedirect(t, resp)
	}},
}

// blockedBy makes blocker block i, and waits until i is told.
func blockedBy(t *T, i, blocker Instance) {
	act(t, blocker, "block", url.Values{"ap_actor_id": {BaseURL(i)}})
	t.waitRows(blocker, 1, `SELECT 1 FROM actor WHERE ap_id = ? AND is_blocked = 1`, BaseURL(i))
	t.waitRows(i, 1, `SELECT 1 FROM notifications WHERE notification_type = 'BLOCKED'`)
}

// remoteRedirect checks that a looked up b's subscribe template and sent the
// visitor to it with a's id, by the Refresh header (app/main.py:272-282).
func remoteRedirect(t *T, resp *Response) {
	t.equal("status", resp.Status, 200)
	t.sawExchange("a", "GET", finger(t.B, "b"), 200)
	t.equal("Refresh", resp.Header.Get("Refresh"), "0;url="+BaseURL(t.B)+"/admin/lookup?query="+BaseURL(t.A))
}

// pollCounted waits until b counts a's vote for answers in its poll, and a
// applies the poll's Update (app/boxes.py:2035-2110). votersCount counts
// voters, not answers.
//
// BUG(port): b sends one Update per answer, with an id made of the poll's id
// and the time to the second (app/boxes.py:2098-2099, 839-866). Two Updates
// in the same second share an id, and a drops the second as a duplicate
// (app/boxes.py:2362-2374), so a may show only the first answer counted.
// The wire is the same either way; a's final count depends on timing, so it
// is only required to have applied an Update.
func pollCounted(t *T, poll, pollType string, answers ...string) {
	// counted lists, sorted, the answers with one vote in a box's copy.
	counted := func(i Instance, box string) ([]string, error) {
		rows, err := Query(i, `SELECT json_extract(answer.value, '$.name') AS name
			FROM `+box+`, json_each(`+box+`.ap_object, '$.`+pollType+`') AS answer
			WHERE ap_id = ? AND json_extract(answer.value, '$.replies.totalItems') = 1 ORDER BY name`, poll)
		var names []string
		for _, r := range rows {
			names = append(names, fmt.Sprint(r["name"]))
		}
		return names, err
	}
	t.Eventually("b counts the vote for "+strings.Join(answers, ", "), func() error {
		names, err := counted(t.B, "outbox")
		if err == nil && !slices.Equal(names, answers) {
			err = fmt.Errorf("b counts %v", names)
		}
		return err
	})
	t.waitRows(t.B, 1, `SELECT 1 FROM outbox WHERE ap_id = ? AND json_extract(ap_object, '$.votersCount') = 1`, poll)
	t.waitRows(t.A, 1, `SELECT 1 FROM inbox WHERE ap_id = ? AND json_extract(ap_object, '$.votersCount') = 1`, poll)
	t.waitRows(t.A, len(answers), `SELECT 1 FROM incoming_activity WHERE ap_object ->> '$.type' = 'Update' AND is_processed = 1`)
	names, err := counted(t.A, "inbox")
	t.Check(err)
	if len(names) == 0 || slices.ContainsFunc(names, func(n string) bool { return !slices.Contains(answers, n) }) {
		t.Fatalf("a counts %v of a vote for %v", names, answers)
	}
}

// itemIDs returns the ids of a collection's items, which may be inlined.
func itemIDs(items any) []any {
	var ids []any
	for _, it := range items.([]any) {
		if obj, ok := it.(map[string]any); ok {
			it = obj["id"]
		}
		ids = append(ids, it)
	}
	return ids
}

// remoteNote makes a follow b, posts a note on b, and returns its id once a
// has received it.
func remoteNote(t *T) string {
	follow(t, t.A, t.B)
	note := newPost(t, t.B, url.Values{"content": {"Hello"}})
	t.waitRows(t.A, 1, `SELECT 1 FROM inbox WHERE ap_id = ?`, note)
	return note
}

// sawExchange fails unless the proxy recorded a request from sender for
// exactly url, answered with status.
func (t *T) sawExchange(from, method, url string, status int) {
	t.sawInOrder(from, method, status, url)
}

// sawInOrder fails unless the proxy recorded requests from sender for each
// of urls, in that order, all answered with status.
func (t *T) sawInOrder(from, method string, status int, urls ...string) {
	next := 0
	for _, e := range t.Proxy.Exchanges() {
		if next < len(urls) && e.From == from && e.Method == method && e.URL == urls[next] && e.Status == status {
			next++
		}
	}
	if next < len(urls) {
		t.Fatalf("no %s %s from %s answered %d on the wire (after %v)", method, urls[next], from, status, urls[:next])
	}
}

// finger is the WebFinger URL for user on i, as the original requests it.
func finger(i Instance, user string) string {
	return BaseURL(i) + "/.well-known/webfinger?resource=" + url.QueryEscape("acct:"+user+"@"+Host(i))
}

// isUpdateTo reports deliveries of an Update to instance name's inbox.
func isUpdateTo(name string) func(WireRecord) bool {
	return func(r WireRecord) bool {
		body, _ := r.Body.(map[string]any)
		return r.Method == "POST" && r.URL == "https://"+name+".test/inbox" && body["type"] == "Update"
	}
}
