package judge

import (
	"encoding/json"
	"net/url"
)

// EndpointScenarios cover the ActivityPub documents an instance serves
// (app/main.py, app/activitypub.py:118-163), fetched the way a remote server
// fetches them: unauthenticated, or signed by a fake actor.
var EndpointScenarios = []Scenario{
	{Name: "ap__actor_document",
		Config: map[string]string{"a": `also_known_as = "https://old.test"`},
		Run: func(t *T) {
			base := BaseURL(t.A)
			actor := fetchAP(t, t.NewFake("f"), base, false, 200)
			want := map[string]any{
				"type": "Person", "id": base, "preferredUsername": "a", "url": base + "/",
				"inbox": base + "/inbox", "outbox": base + "/outbox", "featured": base + "/featured",
				"followers": base + "/followers", "following": base + "/following",
				"manuallyApprovesFollowers": false, "alsoKnownAs": []any{"https://old.test"},
			}
			got := map[string]any{}
			for field := range want {
				got[field] = actor[field]
			}
			t.equal("actor document", got, want)
			key := actor["publicKey"].(map[string]any)
			t.equal("publicKey", []any{key["id"], key["owner"]}, []any{base + "#main-key", base})
			// The key on disk is the one served: existing keys keep working.
			if !publicKey(t, key["publicKeyPem"].(string)).Equal(instanceKey(t, t.A)) {
				t.Fatalf("publicKeyPem is not the public half of data/key.pem")
			}
		}},

	{Name: "ap__webfinger", Run: func(t *T) {
		f := t.NewFake("f")
		finger := BaseURL(t.A) + "/.well-known/webfinger?resource="
		resp, err := f.Fetch(t.Ctx, finger+url.QueryEscape("acct:a@a.test"), false)
		t.Check(err)
		t.equal("status", resp.Status, 200)
		var jrd struct {
			Subject string
			Links   []struct{ Rel, Type, Href string }
		}
		t.Check(json.Unmarshal(resp.Body, &jrd))
		t.equal("subject", jrd.Subject, "acct:a@a.test")
		self := false
		for _, l := range jrd.Links {
			self = self || (l.Rel == "self" && l.Type == "application/activity+json" && l.Href == BaseURL(t.A))
		}
		t.equal("self link", self, true)
		resp, err = f.Fetch(t.Ctx, finger+url.QueryEscape(BaseURL(t.A)), false) // by actor id (app/main.py:1259-1263)
		t.Check(err)
		t.equal("by actor id", resp.Status, 200)
		resp, err = f.Fetch(t.Ctx, finger+url.QueryEscape("acct:nobody@a.test"), false)
		t.Check(err)
		t.equal("unknown account", resp.Status, 404)
	}},

	// Collections show the latest items first; the outbox only public notes,
	// wrapped in their Create (app/main.py:388-632).
	{Name: "ap__collections", Run: func(t *T) {
		f := t.NewFake("f")
		g := t.NewFake("g")
		follow(t, t.B, t.A)
		fakeFollows(t, f, t.A)
		follow(t, t.A, t.B)
		followFake(t, t.A, g)
		first := newPost(t, t.A, url.Values{"content": {"first"}})
		newPost(t, t.A, url.Values{"content": {"unlisted"}, "visibility": {"UNLISTED"}})
		second := newPost(t, t.A, url.Values{"content": {"second"}})
		for path, newestFirst := range map[string][]any{
			"/followers": {f.ID(), BaseURL(t.B)},
			"/following": {g.ID(), BaseURL(t.B)},
		} {
			coll := fetchAP(t, f, BaseURL(t.A)+path, false, 200)
			t.equal(path+" totalItems", coll["totalItems"], float64(2))
			page := fetchAP(t, f, coll["first"].(string), false, 200)
			t.equal(path+" items", itemIDs(page["orderedItems"]), newestFirst)
		}
		outbox := fetchAP(t, f, BaseURL(t.A)+"/outbox", false, 200)
		var types, objects []any
		for _, it := range outbox["orderedItems"].([]any) {
			create := it.(map[string]any)
			types = append(types, create["type"])
			objects = append(objects, create["object"].(map[string]any)["id"])
		}
		t.equal("outbox", []any{outbox["totalItems"], types, objects},
			[]any{float64(2), []any{"Create", "Create"}, []any{second, first}})
	}},

	{Name: "ap__hidden_collections",
		Config: map[string]string{"a": "hides_followers = true\nhides_following = true"},
		Run: func(t *T) {
			f := t.NewFake("f")
			follow(t, t.B, t.A)
			follow(t, t.A, t.B)
			// An IndieAuth token shows them; that belongs to part 2c.
			for _, path := range []string{"/followers", "/following"} {
				coll := fetchAP(t, f, BaseURL(t.A)+path, false, 200)
				t.equal(path, []any{coll["totalItems"], coll["first"], coll["orderedItems"]}, []any{float64(1), nil, nil})
			}
		}},

	// Followers-only objects are served to followers, direct ones to their
	// audience, by HTTP signature (app/main.py:706-742).
	{Name: "ap__object_acl", Run: func(t *T) {
		f := t.NewFake("f")
		stranger := t.NewFake("g")
		fakeFollows(t, f, t.A)
		followers := newPost(t, t.A, url.Values{"content": {"hi followers"}, "visibility": {"FOLLOWERS_ONLY"}})
		direct := newPost(t, t.A, url.Values{"content": {"hi @f@f.test"}, "visibility": {"DIRECT"}})
		for _, c := range []struct {
			who    *Fake
			id     string
			signed bool
			status int
		}{
			{f, followers, false, 404}, {f, followers, true, 200}, {stranger, followers, true, 404},
			{f, direct, false, 404}, {f, direct, true, 200}, {stranger, direct, true, 404},
		} {
			fetchAP(t, c.who, c.id, c.signed, c.status)
			fetchAP(t, c.who, c.id+"/activity", c.signed, c.status)
		}
		// BUG(port): the signature isn't checked for objects. The keyId's
		// owner counts as the signer even when the signature is invalid
		// (app/httpsig.py:256-273), so anyone claiming to be a follower gets
		// followers-only and direct objects.
		for _, id := range []string{followers, direct} {
			resp, err := f.FetchWrongKey(t.Ctx, id)
			t.Check(err)
			t.equal("signed by the wrong key: "+id, resp.Status, 200)
		}
	}},

	{Name: "ap__deleted_object", Run: func(t *T) {
		note := newPost(t, t.A, url.Values{"content": {"Hello"}})
		act(t, t.A, "delete", url.Values{"ap_object_id": {note}})
		f := t.NewFake("f")
		fetchAP(t, f, note, false, 404)
		fetchAP(t, f, note+"/activity", false, 404)
	}},

	{Name: "ap__object_activity", Run: func(t *T) {
		note := newPost(t, t.A, url.Values{"content": {"Hello"}})
		create := fetchAP(t, t.NewFake("f"), note+"/activity", false, 200)
		t.equal("activity", []any{create["type"], create["id"], create["object"].(map[string]any)["id"]},
			[]any{"Create", note + "/activity", note})
	}},
}
