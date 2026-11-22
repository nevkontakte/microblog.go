# 17. Access SQLite through database/sql

Date: 2026-11-22

## Status

Accepted

## Context

The original uses SQLAlchemy 1.4's async ORM over aiosqlite (`app/database.py`,
`app/models.py`). Sixteen of the translated files run queries. The port must read and
write the existing `data/microblogpub.db` without a conversion
([0003](0003-keep-wire-and-disk-compatibility.md)), and its code must translate
one-to-one from the original ([0009](0009-translate-one-to-one-before-refactoring.md)).

Three things decide the data layer:

- **Formats.** SQLAlchemy writes aware datetimes as text without an offset, enums as
  member names, JSON as text, and booleans as 0 or 1. Any Go layer must write the same.
- **Session semantics.** The server and the workers use the async session
  (`app/database.py:24`). It runs with SQLAlchemy's default `autoflush=True` and with
  `expire_on_commit=False`, which a probe of the session confirms. (The sync
  `SessionLocal` sets `autoflush=False`, but no file in `app/` or `tasks.py` uses it.) A
  change to a loaded row reaches the database before the next query, at `flush()`, or at
  `commit()`, whichever comes first. Some behavior depends on this. The `likes_count` of
  -1 after an Undo of a Like (`app/boxes.py:1609`, `1639-1645`), which the judge asserts,
  is an example: the recount sees the Like that the handler just marked deleted, and the
  handler then subtracts one more.
- **The identity map.** A SQLAlchemy session keeps one Python object for each database
  row that it loaded, keyed by the row's primary key. If the code loads the same row
  twice in one session, by any query, it gets the same object back. A change to that
  object is visible through every reference, before any flush. With
  `expire_on_commit=False`, the session also keeps the objects after a commit, with their
  old values. A later query for the same row then returns the cached object, not the
  database's current values. The outgoing worker shows the effect: it keeps one session
  for its whole life, so it can send a poll as it was before a vote changed it in the
  database (the cause of the `mastodon__vote` fix,
  [#37](https://github.com/nevkontakte/microblog.go/pull/37)).
- **Readability.** A reviewer compares each Go query with the SQLAlchemy expression it
  translates. The closer the Go query reads to SQL, the easier that check is.

These are the options:

1. An ORM (GORM, ent, Bun). Each has its own session and loading rules, which differ
   from SQLAlchemy's in ways that are hard to see. Its type mapping would need overrides
   for every format above.
2. A query builder (squirrel) or generated code (sqlc). sqlc needs a schema file and a
   generation step. A builder hides the SQL that a reviewer wants to see.
3. `database/sql` with hand-written SQL, and a small session type that reproduces the
   flush semantics.

## Decision

The port uses `database/sql` with the `modernc.org/sqlite` driver, and hand-written SQL.

- `modernc.org/sqlite` is pure Go, so the binary needs no cgo. The judge already uses it.
- `internal/database` holds:
  - the connection setup, with the 15 s busy timeout that `app/database.py` sets. WAL
    mode needs no setup: an Alembic migration (`6286262bb466`) turned it on, and SQLite
    keeps it in the database file;
  - `Session`, which begins a transaction at its first statement. It writes each change
    when the code makes it (write-through): an INSERT for a new row, which also fills
    the row's ID, and an UPDATE of the changed columns for a changed row. Because the
    original flushes before every query, a later query sees the same data in both. The
    session has no `Flush()`, because nothing waits for one;
  - the codecs for SQLAlchemy's formats. No call site formats a datetime, enum, JSON
    value or boolean for the database by hand.
- Each query is a SQL string next to the function that runs it, with `?` parameters.
  The SQL keeps the original's filters, order and limits.
- The Go session has no identity map: each load gives a new struct with the database's
  current values. Two consequences follow:
  - Where the original changes an object and reads the change through another reference
    to the same row, the Go code passes the one struct along instead.
  - Where the original reads a stale cached object, as the outgoing worker does, the
    Go code reproduces the stale read with a `BUG(port):` marker, so the judge sees the
    original's behavior.

  The gap inventory lists each such site.
- **JSON columns hold the same values, not the same bytes.** Python writes JSON with
  `json.dumps`'s defaults: `", "` and `": "` separators, non-ASCII escaped, and keys in
  insertion order. Go maps have no insertion order, so byte-identical JSON would need an
  ordered map type for every JSON value. The Go codec writes compact JSON with sorted
  keys and without HTML escaping. A number that Python wrote as `1.0` comes back as `1`,
  because Go decodes JSON numbers in a `RawObject` as `float64`. Both
  implementations read either form, the judge reads the columns with `json_extract`, and
  no user sees these bytes. This is a tolerated difference, so it needs the owner's
  explicit approval (`migration/RULEBOOK.md` §0). The owner approved it in the review
  of this ADR ([#39](https://github.com/nevkontakte/microblog.go/pull/39)).

Schema migrations, in the place that
[0016](0016-define-the-port-scope-and-layout.md) gives them
(`internal/database/migrations`):

- The port is self-sufficient: it can set up a new instance without the original.
  - Migration `0000` creates the whole schema at Alembic's head (`a209f0333f5a`) on an
    empty database, and writes that revision to `alembic_version`. Its SQL is the
    schema that the original's `inv migrate-db` creates, read from `sqlite_master`. A
    unit test compares the two schemas.
  - A new instance runs `configuration-wizard`, which writes `data/profile.toml` and
    the key, and then `migrate-db`, as with the original.
- Each later migration is a numbered SQL file, embedded in the binary.
- Each file names the Alembic-style revision that it creates, and the revision it
  follows. The first one follows `a209f0333f5a`.
- `migrate-db` applies the pending files in one transaction each, and updates
  `alembic_version`. On an empty database it starts with `0000`. The server refuses to start on a database whose revision it
  doesn't know, with a message that names `migrate-db`. On a database older than
  Alembic's head, the message tells the owner to run the original's `inv migrate-db`
  first.

## Consequences

- The SQL that the port runs is visible in the code, so reviewers and the judge's
  `Query` checks read the same language.
- The session type stays small, because write-through needs no record of changes. It
  still needs its own unit tests before the translation starts.
- The order of statements inside a transaction differs from SQLAlchemy's, which sorts
  its flush by table dependencies. Nobody outside the transaction can see that order.
  A constraint error can come from a different line, though. The inventory checks each
  write that can fail on a constraint.
- Without an identity map, two loads of one row give two structs. The inventory must find
  each site where the original mutates one object and reads it through another reference.
- No ORM means more lines of scanning code. One scan function per model keeps it in one
  place.
- The port can't migrate a database from before Alembic's head. That stays the original's
  job. The port can create a new database, so it doesn't need the original for a new
  instance.
