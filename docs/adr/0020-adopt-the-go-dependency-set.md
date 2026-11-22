# 20. Adopt the Go dependency set

Date: 2026-11-22

## Status

Accepted

## Context

The original depends on 38 Python packages at run time (`pyproject.toml`). The kit's
rulebook template asks to decide once which target modules the translation uses, "or
every agent decides differently and the codebase becomes a museum of styles."

Each dependency falls into one of three groups:

- **The standard library covers it:** asyncio (goroutines and `context`), the HTTP server
  and client (`net/http`), logging (`log/slog`), RSA (`crypto/rsa`), date parsing
  (`time`), tables (`text/tabwriter`), the CLI (`flag`).
- **A well-known Go module covers it:** SQLite, bcrypt, TOML, HTML parsing and
  sanitizing, Markdown, highlighting, microformats, JSON-LD, image formats, blurhash.
- **Only templates or the command line use it**, so its output may differ
  ([0003](0003-keep-wire-and-disk-compatibility.md)): html2text, emoji, humanize,
  tabulate, prompt-toolkit.

Some outputs reach the wire or the disk, and the Go module must reproduce them exactly:

- Markdown HTML in post content ([0021](0021-render-markdown-with-goldmark.md)).
- Pygments lexer aliases in code blocks.
- Blurhash strings of attachments.
- The itsdangerous token format of sessions.
- RSS and Atom feeds, which the judge compares as XML trees.
- LD signatures ([0015](0015-use-json-gold-for-ld-signatures.md)).

## Decision

The port uses the standard library and `golang.org/x/*` wherever they cover a need. All
logging uses `log/slog`. The port adds only these modules:

| Module | For | Replaces |
|---|---|---|
| `modernc.org/sqlite` | the database driver, pure Go ([0017](0017-access-sqlite-through-database-sql.md)) | aiosqlite |
| `github.com/BurntSushi/toml` | `data/profile.toml` | tomli, tomli-w |
| `github.com/yuin/goldmark` | Markdown ([0021](0021-render-markdown-with-goldmark.md)) | mistletoe |
| `github.com/alecthomas/chroma/v2` | code highlighting in templates | Pygments |
| `github.com/microcosm-cc/bluemonday` | HTML sanitizing, with bleach's policy | bleach |
| `willnorris.com/go/microformats` | microformats parsing | mf2py |
| `github.com/piprate/json-gold` | JSON-LD normalization (already in use) | PyLD |
| `github.com/gen2brain/webp` | WebP encoding of thumbnails, without cgo | Pillow's WebP encoder |
| `github.com/buckket/go-blurhash` | blurhash of image attachments | blurhash-python |
| `github.com/dustin/go-humanize` | relative times in templates, sizes in `stats` | humanize |

These Python dependencies become hand-written code, because no module matches their exact
format, or because the need is small:

- itsdangerous's `URLSafeTimedSerializer`, in `internal/config`.
- feedgen's RSS and Atom output, with `encoding/xml`.
- html2text and emoji, as template functions.
- cachetools's caches, as maps with a mutex.

The translation never adds a module that `migration/RULEBOOK.md` §1 doesn't list. An
amendment adds a row first. These are banned:

- cgo;
- web frameworks, ORMs and query builders;
- dependency-injection and logging frameworks;
- CSS-selector and HTML-templating libraries other than `html/template`.

## Consequences

- One binary without cgo, built with `go build` on every platform that Go supports.
- Every module above is a version pin in `go.mod`, added in the first unit that imports
  it.
- The exact-output cases (Markdown, lexer aliases, blurhash, tokens, feeds) are fidelity
  risks. The gap inventory gets one row for each, and the judge or a unit test covers
  each.
- A module may produce output that differs from the original's in a way that nobody
  can notice. The port may keep such a difference, and the judge may normalize it, only
  with the owner's explicit approval and an ADR for that case.
- `github.com/gen2brain/webp` runs libwebp compiled to WebAssembly, through a pure-Go
  runtime. Encoding is slower than native libwebp. Thumbnails are made once for each
  upload, so this is acceptable. A `PERF(port):` marker records it.
