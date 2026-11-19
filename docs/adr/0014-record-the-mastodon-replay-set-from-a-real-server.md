# 14. Record the Mastodon replay set from a real server

Date: 2026-11-19

## Status

Accepted

## Context

Two microblog.pub instances federating with each other can't produce what only other
servers send: `hs2019` signatures, LD-signed forwarded activities, and Mastodon-specific
object shapes ([0010](0010-judge-federation-with-paired-instances.md)). The judge needs a
set of such inputs to replay to both implementations. Three sources were considered:

1. Recording a real Mastodon server, run once, talking to a Python instance through the
   judge proxy.
2. The JSON fixtures in Mastodon's own test suite.
3. Incoming activities stored in the owner's production database.

Replays can't reuse captured HTTP signatures, because they cover a `Date` header that must
be fresh. LD signatures are embedded in the payload and stay valid as long as the signing
actor's key can still be fetched.

## Decision

- The main source is **a real Mastodon server**, pinned to a release, run once via
  containers. Through the recording proxy it follows, posts at each visibility level,
  likes, boosts, replies, votes, deletes, edits, moves, and forwards a reply. The captured
  payloads are committed as fixtures, with the Mastodon version.
- **Mastodon's own spec fixtures** supplement it for cases that are awkward to trigger live.
- The owner's production database is **not** used.
- On replay, the fake actor re-signs each payload with its own key, in Mastodon's exact
  signature format, and serves the captured actor documents. LD-signed payloads keep their
  original signatures, and the fake serves the original signer's key.

## Consequences

- The fixtures are genuine Mastodon output, signatures included, and contain no personal data.
- Recording needs a container runtime once. This environment has the Docker client but no
  running daemon, so the recording may have to happen on the owner's machine or in a
  session whose environment provides one.
- The replay set ages with Mastodon. It is re-recorded when a Mastodon change matters,
  noting the new version.
