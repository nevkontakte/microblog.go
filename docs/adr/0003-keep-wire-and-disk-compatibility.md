# 3. Keep wire and on-disk compatibility with microblog.pub

Date: 2026-11-18

## Status

Accepted

## Context

The owner runs a live microblog.pub instance that federates with other ActivityPub
servers and is used by IndieAuth, Micropub and Webmention clients. The Go binary must
replace the Python one in place.

## Decision

The Go port is a drop-in replacement:

- **Wire:** ActivityPub objects and activities, HTTP and LD signatures, webfinger and
  nodeinfo, IndieAuth/OAuth, Micropub, Webmention, feeds and every public URL behave as
  in `reference/microblog.pub` at `9c8693e`.
- **Disk:** the port opens the existing `data/microblogpub.db` (schema at alembic head
  `a209f0333f5a`) and `data/uploads/`, `data/custom_emoji/`, key and config *in place*,
  reading and writing the same formats SQLAlchemy uses. It never alters the schema.
- **Allowed:** HTML templates may differ as long as the design is kept, and a one-time
  conversion of the config or key files is acceptable.

## Consequences

- Hand-written SQL must reproduce SQLAlchemy's SQLite storage formats exactly (DateTime
  text, Enum names, JSON text, Boolean 0/1). The judge's swap scenario checks this in
  both directions.
- Library swaps (markdown, sanitizer, highlighter, image processing) may change the HTML
  of *new* posts only. Stored content is never re-rendered.
- Media-proxy HMAC URLs stay valid, because they are embedded in already-served and
  cached HTML.
- Admin session cookies and CSRF tokens (`itsdangerous`) are compatible only if it is
  cheap. The cookies expire anyway, so logging in once more after the swap is acceptable,
  and a simpler session format wins over compatibility.
