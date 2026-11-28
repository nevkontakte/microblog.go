# 23. Store Go error text instead of tracebacks

Date: 2026-11-23

## Status

Accepted

## Context

When the incoming or the outgoing worker fails to process an activity, the original
stores the Python traceback in the activity's `error` column
(`traceback.format_exc()`, `app/incoming_activities.py:127`, `:133`,
`app/outgoing_activities.py:243`, `:260`). Both columns are nullable strings
(`app/models.py:352`, `:382`).

Go has no traceback for an error value, so the port can't write the same text. The gap
inventory found no code or template in the original that reads either column. The
judge reads one: it treats an incoming activity whose `error` contains
`database is locked` as a lost SQLite lock race, not a failure
(`internal/judge/steps.go`, `lockRace`).

The rulebook allows a tolerated difference only with the owner's explicit approval and
an ADR (`migration/RULEBOOK.md` §0). The owner approved this one during the inventory
review ([#40](https://github.com/nevkontakte/microblog.go/pull/40)): "Yes, storing Go
trace is reasonable."

## Decision

- At each of the four sites, the port stores the Go error's text, `err.Error()`, where
  the original stores the traceback. A `NULL` stays `NULL`, and every other column that
  these handlers write stays exact.
- The stored text must keep the underlying error's message, so that wrapping doesn't
  hide it. For a busy database, `modernc.org/sqlite` puts SQLite's own message,
  `database is locked`, into its error text, so the judge's lock-race check keeps
  working.
- The judge doesn't compare the `error` column's text. It still checks whether the
  column is `NULL` and the lock-race match above.

## Consequences

- The admin who reads the column in the database sees a Go error chain instead of a
  Python traceback. The original shows the column nowhere, so no page changes.
- A database moved from the original to the port keeps its old tracebacks. Both read
  either text, because neither parses it.
- If a later change shows the column to the admin, the text is a Go error, and that
  change decides whether it needs more detail, such as a stack trace.
