# 19. Run the server and the workers in one process

Date: 2026-11-22

## Status

Accepted

## Context

The original runs three processes under supervisord (`misc/supervisord.conf`):

- uvicorn with the web app,
- `inv process-incoming-activities`,
- `inv process-outgoing-activities`.

They share the SQLite database and nothing else. The workers poll their queue tables
(`incoming_activity`, `outgoing_activity`), with a sleep of 0.5 s after a message and 2 s
when the queue is empty (`app/utils/workers.py`).

The feasibility report names this setup as a cost of the original, and expects one Go
binary. The owner swaps one binary for the three processes. The judge starts an instance
through the implementation's `Impl`, so it doesn't depend on the process count.

## Decision

- `microblogpub serve` (the default) runs the web server and both workers in one process.
  Each worker is a goroutine that runs the translated loop of `app/utils/workers.py`,
  with the same queries, order and sleeps.
- Three flags of `serve` choose what runs: `--enable-web`, `--enable-incoming-worker`
  and `--enable-outgoing-worker`. All three are on by default. This is the only code path
  that starts a worker. `inv process-incoming-activities` and
  `inv process-outgoing-activities` have no subcommands. `serve --enable-web=false
  --enable-outgoing-worker=false` does what the first one did.
- The other `inv` tasks become subcommands with the same names and flags (`migrate-db`,
  `move-to`, …).
- All parts share one connection pool. The sessions
  ([0017](0017-access-sqlite-through-database-sql.md)) stay as in the original:
  - Each request has its own session.
  - Each worker keeps one session for its whole life, and commits after each message
    (`app/utils/workers.py:48`).

  This is for correctness, not speed:
  - A session's transaction must commit or roll back as one unit. If two parts shared
    one, a failure in one would roll back the other's work.
  - A `*sql.Tx` isn't safe for concurrent use.
  - SQLite allows one writer at a time, so a shared session would gain no speed.
- On SIGTERM or SIGINT, the server stops accepting requests, the workers finish their
  current message, and the process exits, as the original's workers do.

## Consequences

- One binary and one process replace supervisord. Docker and systemd setups get simpler.
- SQLite allows one writer at a time, so the busy timeout
  ([0017](0017-access-sqlite-through-database-sql.md)) handles the contention between
  sessions that file locks handled between processes.
- A crash in a worker would stop the server too. So a worker recovers from a panic in one
  message, marks that message as failed, and continues, as the original does for a
  Python exception in one message.
- The workers' timing stays as it was, so the judge's settle logic works unchanged.
