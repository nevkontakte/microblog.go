# 15. Use json-gold for LD signatures

Date: 2026-11-21

## Status

Accepted

## Context

LD signatures (`RsaSignature2017`) sign a JSON-LD document so a server can forward it and the
receiver still knows its author. Mastodon and the original (`app/ldsig.py`) make them alike: an
RSA-SHA256 signature over the SHA-256 hashes of the signature's options and of the document,
each normalized to N-Quads with the URDNA2015 algorithm. The original uses PyLD for that;
Mastodon uses Ruby's `rdf-normalize`.

The judge now needs the same computation in Go, for three things:

- re-signing the Mastodon replay set, whose deliveries carry Mastodon's LD signatures and must
  carry the replay's own ([0014](0014-record-the-mastodon-replay-set-from-a-real-server.md));
- verifying the LD signatures the instances send, on the wire, instead of masking them (the
  deferred reviewer finding in `migration/00b-judge-report.md`, section 4). The original
  LD-signs its public activities so that whoever receives them can forward them, but a
  receiver checks that signature only on a forwarded activity. Between two instances nothing
  is forwarded, so a port that signed wrongly would pass every scenario; the judge has to check
  the signatures itself. It checks every LD-signed body the proxy sees, with the keys it has:
  the instances';
- later, the port itself, which verifies forwarded activities and signs its own.

URDNA2015 is a graph canonicalization algorithm with a W3C test suite. Writing it is a project
of its own, and an implementation that agrees with PyLD and Mastodon on every input is the
whole point. The Go options, as of this date:

1. `github.com/piprate/json-gold`: a JSON-LD 1.1 processor with URDNA2015, used by Go
   ActivityPub and verifiable-credentials projects, Apache-2.0. It brings two small
   dependencies (`github.com/cayleygraph/quad`, `github.com/pquerna/cachecontrol`).
2. Writing URDNA2015 in the repository: no dependency, but thousands of lines to review and
   keep right.
3. Calling the original's PyLD from the judge: no Go dependency, but it ties the judge to the
   Python original, which the judge must stay independent of, and gives the port nothing.

## Decision

- Use `github.com/piprate/json-gold` for JSON-LD normalization, in the judge now and in the port
  when it verifies or makes LD signatures.
- It never fetches contexts from the web: it loads them through a document loader that serves
  the captured copies (`internal/judge/testdata/jsonld`), as the judge's proxy does for the
  instances.

## Consequences

- Its Apache-2.0 license is compatible with the port's AGPL-3.0
  ([0008](0008-license-under-agpl-3.0.md)); the license notice goes with any binary that links
  it.
- The judge can verify Mastodon's own signatures, which checks json-gold against Mastodon's
  canonicalization before the replay relies on it; the Python instances verifying the
  replay's signatures check it against PyLD.
- The port inherits a third-party canonicalizer: a disagreement with PyLD or Mastodon would show
  up in the judge as a bad LD signature on the wire, not silently.
