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
be fresh. LD signatures are embedded in the payload and stay valid only while the signer's
key can be fetched, and keeping the recording actors' private keys would mean committing
private keys, throwaway or not.

## Decision

- The main source is **a real Mastodon server**, pinned to a release, run once via
  containers. Through the recording proxy it follows, posts at each visibility level,
  likes, boosts, replies, votes, deletes, edits, moves, and forwards a reply. The captured
  payloads are committed as fixtures, with the Mastodon version.
- **Mastodon's own spec fixtures** supplement it for cases that are awkward to trigger live.
- The owner's production database is **not** used.
- No private key is committed. Everything is **re-signed at replay time** with the fake
  actor's own key: it serves the captured actor documents with its public key in place of
  the recording one, signs each request in Mastodon's exact HTTP signature format, and
  recomputes each LD signature over the captured payload in the same form
  (`RsaSignature2017`, same `creator` and `created`). The `keyId`, the served actor
  document and both signatures then agree.

## Consequences

- The fixtures are genuine Mastodon output and contain neither personal data nor keys.
- The judge needs an LD signer (JSON-LD canonicalization, URDNA2015), working from the
  captured contexts it already serves offline. Replays test that the receiver verifies
  Mastodon-shaped LD signatures, not the exact bytes Mastodon produced.
- Recording needs a container runtime once. This environment has the Docker client but no
  running daemon, so the recording may have to happen on the owner's machine or in a
  session whose environment provides one.
- The replay set ages with Mastodon. It is re-recorded when a Mastodon change matters,
  noting the new version.
