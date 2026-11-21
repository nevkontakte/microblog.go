# Recent Mastodon can't resolve a microblog.pub account on first contact

Status: open, for after parity (marker: `TODO(port)` in `internal/judge/mastodon.go`).
Found while recording the Mastodon replay set (part 3a of `migration/00b-judge-report.md`),
2026-11-21. This note keeps what the investigation found, so that nobody has to repeat it.

## Symptom

A Mastodon server on v4.4.27 or later that has never seen a microblog.pub instance can't use
it:

- A search for `@user@host` finds nothing.
- It refuses the instance's signed deliveries, a Follow included, with
  `401 Unable to fetch key JSON at https://host#main-key`.

A server that already knows the account keeps working. Search finds the account in the
server's own database, and the server verifies deliveries with the key it stored earlier. So
existing federation continues. That is why the owner's instance still federates with the
servers it knows. A known account probably can't refresh its profile by a fetch either, but
pushed Updates still arrive. (Not tested.)

## Cause

microblog.pub's actor id is the bare origin, `https://host`, with no path
(`app/config.py`: `ID = f"{_SCHEME}://{DOMAIN}"`). Its WebFinger `self` link is the same
(`app/main.py:1276`).

Since v4.4.27, when Mastodon fetches an actor or a key, it requires that the response's URL
equal the one it asked for. `app/helpers/json_ld_helper.rb`,
`fetch_resource_without_id_validation`:

```ruby
body_to_json(response.body_with_limit) if response.code == 200 && valid_activitypub_content_type?(response) && (allow_redirects || response.uri.to_s == uri)
```

Mastodon's `Request` normalizes the URL before it sends (`app/lib/request.rb`,
`normalize_preserving_url_encodings`), and Addressable's normalization adds the empty path:
`https://host` becomes `https://host/`. `response.uri` is the URL it sent. So for a bare origin,
`response.uri.to_s` is `https://host/`, `uri` is `https://host`, and the fetch returns nothing.

The callers that fetch actors and keys (`fetch_remote_actor_service.rb`,
`fetch_remote_key_service.rb` and others) use `fetch_resource` without `allow_redirects`, so
they all take this path.

## Evidence

- **Mastodon's own HTTP client:** `Request.new(:get, 'http://127.0.0.1:18080').perform`, run
  with `rails runner` in the v4.7.6 image against a local server, reports `response.uri` as
  `http://127.0.0.1:18080/`, with or without a proxy. So the judge's proxy is not the cause.
- **A fresh Mastodon v4.7.6, unpatched:** the search for `@a@a.test` finds nothing. a's Follow
  gets `401 Unable to fetch key JSON at https://a.test#main-key`.
- **Bisection of the release images** (`ghcr.io/mastodon/mastodon`): the comparison is in
  v4.4.27 and not in v4.4.26. It is also in v4.5.22, v4.6.12 and v4.7.6, and not in v4.4.0,
  v4.3.8, v4.2.5 or v4.2.4.
- **Mastodon's `main`** (checked 2026-11-21) still has the comparison unchanged.

## Origin of the change

- Commit [fb81d7e](https://github.com/mastodon/mastodon/commit/fb81d7eba77d57102dfe6a897d156fd35a20a979)
  by Claire, "Fix cross-domain redirects breaking origin-based security model", 2026-10-28.
  It changes 13 files: every caller of `fetch_resource`. It has no public pull request, as is
  usual for a security fix.
- Advisory [GHSA-288h-6j8h-6x5j](https://github.com/mastodon/mastodon/security/advisories/GHSA-288h-6j8h-6x5j),
  "Insufficient ActivityPub identifier verification", rated critical. Patched in 4.4.27,
  4.5.20, 4.6.10, 4.7.4 and 5.0.0-alpha.2. Its text: "A flaw in how ActivityPub identifiers
  are authenticated allows for attackers to impersonate remote actors." It gives no technical
  detail and mentions no compatibility impact.
- The mechanism, from the commit: Mastodon trusts a document's id by the origin it fetched it
  from. Before the fix, `fetch_resource` followed redirects. So a fetch of `https://victim/…`
  could end on an attacker's server, which served a document that claimed the victim's id.
  The fix disables redirects for these fetches, and adds the exact string comparison above.
  The comparison breaks bare-origin ids by accident.

## Options

1. **Fix upstream (preferred).** Compare the normalized forms of the two URLs. `https://host`
   and `https://host/` are the same URL under RFC 3986's normalization, so this keeps the
   security property. Report it to Mastodon. Since it touches a security fix, perhaps
   privately to their security contact first. The owner decides whether and how to report it.
2. **Change microblog.pub's actor id to one with a path** (`https://host/` or
   `https://host/actor`). Mastodon compares ids as strings, so that's a new identity. The
   instance would have to move its followers with a Move from the old id to the new one, as an
   account migration does. The servers that follow the old id know it already, so they can
   process the Move. Other software's support for Move varies. This is invasive.

Things that don't work without a new id:

- A WebFinger `self` link of `https://host/` passes the URL comparison, but then fails
  `json['id'] == uri` (`fetch_resource`), because the actor's id stays `https://host`.
- A `keyId` with a path (`https://host/key`) gets the key document. But Mastodon then fetches
  the key's owner, `https://host`, which fails.

## How the judge works around it

`internal/judge/mastodon_patch.rb` is a Rails initializer that the recorder mounts into
Mastodon. It normalizes the URL before the fetch, so Mastodon accepts microblog.pub's actor.
It changes only what Mastodon accepts, never what it sends. Without it, the recording can't
start.
