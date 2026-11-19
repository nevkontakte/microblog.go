# 13. Run the judge as a program

Date: 2026-11-19

## Status

Accepted

## Context

The judge ([0010](0010-judge-federation-with-paired-instances.md)) starts real server
processes (the Python original today, the Go port later), routes them through a recording
proxy, and runs scenarios that take seconds each. It must be pointed at different
implementations on each side of a pair, and it produces artifacts (logs, recordings,
golden-file updates). The repository's settings deny `go test` inside agent loops; the kit
reserves test runs for the judge and the orchestrator.

Building the harness also settled several mechanisms that are worth recording:

- Instances get fake hostnames (`a.test`, `b.test`) on the default port, and every outgoing
  request goes through the judge's proxy, which needs no DNS. Separate ports on `localhost`
  don't work: `app/actor.py:39-41` builds webfinger URLs from the hostname alone.
- Instances run with `https = true`, as in production. The proxy terminates TLS for every
  host with certificates from a per-run CA that the instances are told to trust, and
  forwards plain http with `X-Forwarded-Proto: https`, like a reverse proxy in front of a
  production instance. It also serves captured JSON-LD contexts
  (`internal/judge/testdata/jsonld`), so LD signatures are computed offline from identical
  bytes. This supersedes the `https = false` detail in point 5 of
  [ADR 0010](0010-judge-federation-with-paired-instances.md). Instances still run with
  `debug = true`, which is what lets them reach local hosts.
- `PYTHONHASHSEED=0`: `app/actor.py:39` iterates over a set, and the wire must not change
  between runs.
- A Docker-based runner (real hostnames through container networking) was considered and
  set aside for now. It would still need the recording proxy, the CA and the context server,
  and it adds a hard dependency on a container daemon.

## Decision

- The judge is a command, `go run ./cmd/judge`, backed by `internal/judge`. It is not a
  `go test` suite. `go test` stays reserved for the port's unit tests.
- Scenarios assert on ActivityPub responses, admin-endpoint results and the database. They
  never assert on admin HTML, which the port may render differently. Public HTML is
  checked only where the original test did (emoji rendering, tag pages).
- The admin action endpoints and their form fields are part of the judged surface: the port
  must keep them, so the judge can drive both implementations the same way.
- Each scenario's traffic between instances is normalized and compared, as a multiset, to a
  golden Python–Python recording in `internal/judge/testdata/golden`. Signatures and digests
  are verified cryptographically and recorded as `ok`/`bad`, not masked.
  `-update-golden` rewrites the recordings, and only a Python–Python run may do that.
- Bugs in the original are judged bug-for-bug and marked `BUG(port)` where the judge
  encodes them (the kit's rule; fixed after parity).
- The judge is validated by mutation: `-patch` applies a patch from
  `internal/judge/testdata/mutations` to the judge's copy of the original, and the run must fail.
- Scenarios wait on state (database rows, recorded deliveries), never on time. Timeouts are
  ceilings for a stuck run, set by `-timeout`. Before stopping a pair, the judge waits until
  every delivery is sent and every incoming activity tried, so the wire recording doesn't
  depend on how fast the workers run.
- The judge runs on every pull request in GitHub Actions (`.github/workflows/judge.yml`). A pull
  request is ready for review only once it passes.

## Consequences

- Running the judge needs the original's Python environment (Python 3.11, `poetry install
  --no-root` in `reference/microblog.pub`). A full run takes tens of minutes, mostly
  instance start-up; CI runs it on every pull request.
- A Go instance only needs to implement `judge.Impl`, so one harness judges every pairing.
- Golden recordings are reviewable text. A change to them is a change to the expected wire
  behavior, and needs a reason in review.
- The admin endpoints can't be redesigned until after parity.
