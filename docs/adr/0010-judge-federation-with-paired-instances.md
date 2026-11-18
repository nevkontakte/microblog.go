# 10. Judge federation with paired instances

Date: 2026-11-19

## Status

Accepted

## Context

Federation compatibility is the paramount requirement ([0003](0003-keep-wire-and-disk-compatibility.md)),
and none of the existing Python tests can judge the port, because all of them import
Python internals. A hand-written fake peer would encode our own understanding of the
protocol. That understanding is exactly what is in question.

## Decision

The judge's core is a pair of live instances that federate with each other:

1. A scenario suite drives real ActivityPub interactions between two microblog.pub
   instances: posting at every visibility level, follow/unfollow, accept/reject, likes,
   announces, replies, polls and votes, mentions, deletes, updates, profile updates, blocks,
   moves, and viewing remote profiles. It asserts the observable results on either side.
2. The suite must first pass on a **Python–Python** pair. That validates the suite itself.
3. Then it runs with **each side swapped for Go in turn** (Go–Python, Python–Go), and
   finally **Go–Go**.
4. A recording proxy between the instances captures every exchange. Payloads and signature
   headers are diffed against the Python–Python run, so a lenient receiver can't hide a
   sender that differs from Python.
5. Instances run with `debug = true` and `https = false` on local ports, because Python
   rejects localhost/private URLs otherwise (`app/utils/url.py:50-80`). Assertions poll with
   a deadline, because delivery is asynchronous.

Around that core, the judge also covers what federation between two microblog.pub
instances can't reach:

- the on-disk swap in both directions;
- feeds and public pages;
- IndieAuth, Micropub and Webmention;
- a replay set of captured real Mastodon payloads (such as `hs2019` signatures and
  LD-signed forwarded activities).

Python tests that only exercise internals are ported as Go unit tests next to the code
they cover.

## Consequences

- The judge tests the actual protocol, and validates itself on the original pair before
  anything is translated.
- The harness has to orchestrate up to six Python processes per pairing (each instance
  runs a web server and two workers; a Go instance is one binary), plus the proxy, across
  four pairings. It is the most expensive part of
  step 0b.
- Interop with servers other than microblog.pub is only as good as microblog.pub's own
  behavior plus the Mastodon replay set.
- The Go port must honor the same `debug` semantics, at least for allowing local URLs.
