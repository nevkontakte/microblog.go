package judge

import (
	"bytes"
	"net/url"
)

// IndieAuthScenarios judge the authorization server (app/indieauth.py): how
// a client app registers, gets the admin's consent, redeems its code for
// tokens, refreshes and revokes them, and what a token unlocks. The client
// app is the visitor, so its exchanges are on the wire.
var IndieAuthScenarios = []Scenario{
	{Name: "indieauth__discovery", Run: func(t *T) {
		visit(t, BaseURL(t.A)+"/.well-known/oauth-authorization-server", acceptAP)
	}},

	// The whole flow, ending with a token that sees what only the admin and
	// followers may see, until it is revoked.
	{Name: "indieauth__authorization_code", Run: func(t *T) {
		const redirectURI = "https://app.test/callback"
		secret := newPost(t, t.A, url.Values{"content": {"Secret for followers"}, "visibility": {"FOLLOWERS_ONLY"}})
		clientID, clientSecret := registerClient(t, t.A, redirectURI)
		verifier, challenge := pkce("judge-code-verifier-0123456789abcdef0123456789")

		callback := approve(t, t.A, clientID, redirectURI, challenge, "read", "create")
		t.equal("callback", callback.Scheme+"://"+callback.Host+callback.Path, redirectURI)
		t.equal("state", callback.Query().Get("state"), "judge-state")
		t.equal("issuer", callback.Query().Get("iss"), BaseURL(t.A)+"/")

		resp := redeemCode(t, t.A, clientID, redirectURI, callback.Query().Get("code"), verifier)
		t.equal("token status", resp.Status, 200)
		tokens := decode(t, resp)
		token := stringField(t, tokens, "access_token")
		t.equal("introspection", introspect(t, t.A, clientID, clientSecret, token)["active"], true)
		t.equal("outbox shows the followers-only post", bytes.Contains(fetchWithToken(t, BaseURL(t.A)+"/outbox", token).Body, []byte(secret)), true)
		t.equal("followers-only post", fetchWithToken(t, secret, token).Status, 200)

		resp = refresh(t, t.A, clientID, stringField(t, tokens, "refresh_token"))
		t.equal("refresh status", resp.Status, 200)
		refreshed := stringField(t, decode(t, resp), "access_token")
		// BUG(port): a refresh token used twice raises ValueError, a 500
		// rather than invalid_grant (app/indieauth.py:318-319).
		t.equal("reused refresh token", refresh(t, t.A, clientID, stringField(t, tokens, "refresh_token")).Status, 500)

		revoke(t, t.A, refreshed)
		t.equal("revoked introspection", introspect(t, t.A, clientID, clientSecret, refreshed)["active"], false)
		t.equal("revoked token's view of the post", fetchWithToken(t, secret, refreshed).Status, 404)
	}},

	// A code is redeemed once, by the client it was issued to, for the
	// redirect URI it was issued for, within two minutes
	// (app/indieauth.py:191-227).
	{Name: "indieauth__rejected_codes", Run: func(t *T) {
		const redirectURI = "https://app.test/callback"
		clientID, _ := registerClient(t, t.A, redirectURI)
		other, _ := registerClient(t, t.A, redirectURI)
		verifier, challenge := pkce("judge-code-verifier-0123456789abcdef0123456789")
		code := func() string { return approve(t, t.A, clientID, redirectURI, challenge, "read").Query().Get("code") }
		invalidGrant := func(what string, resp *Response) {
			t.equal(what+" status", resp.Status, 400)
			t.equal(what+" error", decode(t, resp)["error"], "invalid_grant")
		}

		used := code()
		t.equal("first redemption", redeemCode(t, t.A, clientID, redirectURI, used, verifier).Status, 200)
		invalidGrant("reused code", redeemCode(t, t.A, clientID, redirectURI, used, verifier))
		invalidGrant("wrong redirect URI", redeemCode(t, t.A, clientID, "https://app.test/other", code(), verifier))
		invalidGrant("wrong client", redeemCode(t, t.A, other, redirectURI, code(), verifier))
		invalidGrant("unknown code", redeemCode(t, t.A, clientID, redirectURI, "judge-unknown-code", verifier))

		expired := code()
		t.Check(Exec(t.A, `UPDATE indieauth_authorization_request SET created_at = datetime(created_at, '-121 seconds') WHERE code = ?`, expired))
		invalidGrant("expired code", redeemCode(t, t.A, clientID, redirectURI, expired, verifier))

		// A code without scopes grants nothing (app/indieauth.py:295).
		unscoped := approve(t, t.A, clientID, redirectURI, challenge).Query().Get("code")
		invalidGrant("code without scopes", redeemCode(t, t.A, clientID, redirectURI, unscoped, verifier))

		// BUG(port): the PKCE verifier is never checked against the
		// challenge (app/indieauth.py:191-227 takes it and ignores it), so a
		// stolen code is redeemable without it. A security hole, kept until
		// parity per ADR 0013.
		t.equal("wrong verifier", redeemCode(t, t.A, clientID, redirectURI, code(), "judge-wrong-verifier").Status, 200)
	}},

	// An access token lasts an hour (app/indieauth.py:333,372-376).
	{Name: "indieauth__token_expiry", Run: func(t *T) {
		const redirectURI = "https://app.test/callback"
		secret := newPost(t, t.A, url.Values{"content": {"Secret for followers"}, "visibility": {"FOLLOWERS_ONLY"}})
		clientID, clientSecret := registerClient(t, t.A, redirectURI)
		verifier, challenge := pkce("judge-code-verifier-0123456789abcdef0123456789")
		code := approve(t, t.A, clientID, redirectURI, challenge, "read").Query().Get("code")
		token := stringField(t, decode(t, redeemCode(t, t.A, clientID, redirectURI, code, verifier)), "access_token")

		t.Check(Exec(t.A, `UPDATE indieauth_access_token SET created_at = datetime(created_at, '-3601 seconds') WHERE access_token = ?`, token))
		t.equal("expired introspection", introspect(t, t.A, clientID, clientSecret, token)["active"], false)
		t.equal("expired token's view of the post", fetchWithToken(t, secret, token).Status, 404)
		t.expectUndisclosed(secret)
	}},

	// Introspection answers only the client the token was issued to, and
	// only with its secret (app/indieauth.py:500-552).
	{Name: "indieauth__introspection_auth", Run: func(t *T) {
		const redirectURI = "https://app.test/callback"
		clientID, clientSecret := registerClient(t, t.A, redirectURI)
		other, otherSecret := registerClient(t, t.A, redirectURI)
		verifier, challenge := pkce("judge-code-verifier-0123456789abcdef0123456789")
		code := approve(t, t.A, clientID, redirectURI, challenge, "read").Query().Get("code")
		token := stringField(t, decode(t, redeemCode(t, t.A, clientID, redirectURI, code, verifier)), "access_token")

		t.equal("other client", introspect(t, t.A, other, otherSecret, token)["active"], false)
		wrongSecret := postForm(t, BaseURL(t.A)+"/token_introspection", url.Values{"token": {token}},
			basicAuth(clientID, clientSecret+"0"))
		t.equal("wrong secret", wrongSecret.Status, 401)
	}},

	// An app that isn't registered is identified by its URL: the consent
	// page names it from the h-app its page publishes
	// (app/utils/indieauth.py:25-53).
	{Name: "indieauth__client_url", Run: func(t *T) {
		f := t.NewFake("f")
		app := f.ID() + "/app"
		f.PublishPage(app, `<html><body><div class="h-app"><a class="u-url p-name" href="`+app+`">Judge app</a></div></body></html>`)
		resp, err := t.Client.Get(t.Ctx, BaseURL(t.A)+"/auth?"+url.Values{"client_id": {app}, "redirect_uri": {app + "/callback"},
			"state": {"judge-state"}, "response_type": {"code"}, "scope": {"read"}, "me": {BaseURL(t.A) + "/"}}.Encode(), acceptHTML)
		t.Check(err)
		t.equal("consent page", resp.Status, 200)
		t.sawExchange("a", "GET", app, 200)
	}},

	// A token sees the followers and following an instance hides
	// (app/main.py:460-480,525-545).
	{Name: "indieauth__hidden_collections", Config: map[string]string{"a": "hides_followers = true\nhides_following = true"}, Run: func(t *T) {
		follow(t, t.B, t.A)
		follow(t, t.A, t.B)
		const redirectURI = "https://app.test/callback"
		clientID, _ := registerClient(t, t.A, redirectURI)
		verifier, challenge := pkce("judge-code-verifier-0123456789abcdef0123456789")
		code := approve(t, t.A, clientID, redirectURI, challenge, "read").Query().Get("code")
		token := stringField(t, decode(t, redeemCode(t, t.A, clientID, redirectURI, code, verifier)), "access_token")
		for _, path := range []string{"/followers", "/following"} {
			t.equal(path+" items", decode(t, visit(t, BaseURL(t.A)+path+"?page=true", acceptAP))["orderedItems"], nil)
			t.equal(path+" items with a token", decode(t, fetchWithToken(t, BaseURL(t.A)+path+"?page=true", token))["orderedItems"],
				[]any{BaseURL(t.B)})
			visit(t, BaseURL(t.A)+path, acceptAP)
			fetchWithToken(t, BaseURL(t.A)+path, token)
		}
	}},
}
