package judge

import "net/url"

// SwapScenarios test the on-disk swap (ADR 0010). An instance stops, and the
// peer's implementation starts with the same data, as when an owner replaces
// the binary. On a Python pair, these scenarios record what a swap must
// keep. With the port on one side, they are the swap test, in the two
// directions.
var SwapScenarios = []Scenario{
	// All that remote servers and readers see stays the same: the key, the
	// actor, the collections, the objects and the attachments.
	{Name: "swap__served_state", Run: func(t *T) {
		follow(t, t.B, t.A)
		follow(t, t.A, t.B)
		note := newPost(t, t.A, url.Values{"content": {"Hello"}},
			File{Field: "files", Name: "attachment.txt", Data: []byte("hello")})
		f := t.NewFake("f")
		before := servedState(t, f, t.A, note)
		t.swap(t.A)
		t.equal("served state", servedState(t, f, t.A, note), before)
		t.expectSchema(t.A)
	}},

	// The swapped instance continues to send and receive. It signs its
	// deliveries with the same key, and accepts activities about its old
	// posts.
	{Name: "swap__federates", Run: func(t *T) {
		follow(t, t.B, t.A)
		old := newPost(t, t.A, url.Values{"content": {"Before"}})
		t.waitRows(t.B, 1, `SELECT 1 FROM inbox WHERE ap_id = ?`, old)
		t.swap(t.A)
		note := newPost(t, t.A, url.Values{"content": {"After"}})
		t.waitRows(t.B, 1, `SELECT 1 FROM inbox WHERE ap_id = ?`, note)
		act(t, t.B, "like", url.Values{"ap_object_id": {old}})
		t.waitRows(t.A, 1, `SELECT 1 FROM outbox WHERE ap_id = ? AND likes_count = 1`, old)
	}},

	// The receiver is the swapped instance. It still knows the actors that it
	// follows, and accepts their deliveries.
	{Name: "swap__peer", Run: func(t *T) {
		follow(t, t.B, t.A)
		t.swap(t.B)
		note := newPost(t, t.A, url.Values{"content": {"Hello"}})
		t.waitRows(t.B, 1, `SELECT 1 FROM inbox WHERE ap_id = ?`, note)
		t.expectSchema(t.B)
	}},

	// The new implementation also uses the custom emoji in data/custom_emoji.
	{Name: "swap__custom_emoji", Run: func(t *T) {
		addCustomEmoji(t, t.A, "judge_star.png", []byte("not really a PNG"))
		t.swap(t.A)
		base := BaseURL(t.A)
		emoji, err := t.Client.GetAP(t.Ctx, base+"/e/judge_star")
		t.Check(err)
		delete(emoji, "@context") // the same in all documents
		t.equal("emoji", emoji, map[string]any{
			"type": "Emoji", "name": ":judge_star:", "updated": "1970-01-01T00:00:00Z", "id": base + "/e/judge_star",
			"icon": map[string]any{"mediaType": "image/png", "type": "Image", "url": base + "/custom_emoji/judge_star.png"},
		})
		resp, err := t.Client.Get(t.Ctx, base+"/custom_emoji/judge_star.png", "*/*")
		t.Check(err)
		t.equal("image", []any{resp.Status, string(resp.Body)}, []any{200, "not really a PNG"})
	}},
}
