# 2. Follow the migration kit process

Date: 2026-11-18

## Status

Accepted

## Context

The rewrite covers about 12.5k lines of Python (47 modules, 70 HTTP routes, 16 tables) and
must not break a live, federating instance. A one-shot rewrite would have no exit
condition and no way to show that the result behaves like the original. The owner chose
the [Claude Code Migration Kit](https://github.com/anthropics/code-migration-kit-with-claude-code)
for this. Its feasibility step (`migration/00-feasibility-report.md`) found that 0 of the 55
existing tests can judge the Go port as written.

## Decision

We follow the kit's six-step process. The kit is pinned as the `migration-kit/` submodule,
and its `CLAUDE.md` is imported by ours. Concretely:

- Every step ends at a sign-off gate, and nothing continues on inferred approval.
- A parity judge is built and validated (kit prompt `00b`) **before** Step 1.
- Step 4 (compile) is dissolved into Step 3: Go's typecheck is cheap, so `go build` and
  `go vet` run in every unit's loop. `go test` stays out of the loops.
- The Python original is pinned read-only at `reference/microblog.pub/` and is the
  executable spec.

## Consequences

- Progress is slower per step but resumable from disk and reviewable at every gate.
- Translation can't start until the judge exists, so it front-loads harness work
  (two servers plus a fake fediverse peer).
- Deviations from the kit must be logged in the rulebook's deviation log.
