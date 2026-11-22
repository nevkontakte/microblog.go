# Translation Rulebook: Python (microblog.pub) → Go

> **The meta-rule: if two agents could answer a question differently, the answer goes in
> this file.** Every implementer, reviewer and fixer reads this document before they touch
> code. It is read-only inside any loop. Amendments are queued for the owner and applied
> between batches.

Read this whole document before you write any code. It cites the ADRs in `docs/adr/`. An
ADR explains a decision. This file states the rule that follows from it.

Status: **draft** (step 1). The gap inventory (prompt 02) and the stress test (prompt 03)
amend it.

## 0. Scope and posture

- **Structure-preserving, one-to-one** ([0009](../docs/adr/0009-translate-one-to-one-before-refactoring.md)).
  One Python file becomes one Go file, at the path that `migration/depmap/layout.tsv`
  names. Each Python function becomes one Go function with the same name (§4), the same
  parameters in the same order, and the same control flow. Classes keep their methods.
  You are translating, not improving. Refactors wait for parity.
- **Cheap-referee variant.** `go vet ./...` runs inside every unit's loop
  ([0002](../docs/adr/0002-follow-the-migration-kit-process.md)). A draft that doesn't
  vet is not done. `go build` is allowed too. `go test` stays out of the loops.
- **The original is the spec.** When this file and the original disagree on behavior,
  the original wins. When the original is wrong, the BUG rule (§2) applies.
- **No tolerated differences without approval.** Some differences from the original's
  output may be harmless, for example ones that a human user can't notice. The port may
  keep such a difference, and the judge may normalize it, only with the owner's explicit
  approval and an ADR for that case. Until then, the port matches the original.
- **Fidelity before speed.** A slow translation that is faithful is fine. Mark it
  `PERF(port):` with one line on the fast version, and move on.
- **Readable Go** ([0012](../docs/adr/0012-write-code-for-human-readers.md)). Write for a
  human reader. Handle every error. Keep names short and clear.
- **Out of scope:** the files that ADR 0016 excludes, the Python customization hooks
  ([0004](../docs/adr/0004-drop-python-customization-hooks.md)), and developer-only `inv`
  tasks.

## 1. Ecosystem: what we use and what we ban

[0020](../docs/adr/0020-adopt-the-go-dependency-set.md) records the choices. A module that
isn't in this table is banned. Add a row (an amendment) before you import anything else.

| Python | Go | Rule |
|---|---|---|
| asyncio, `async`/`await` | plain functions, goroutines | No async runtime. See §2, "Async". |
| FastAPI, Starlette, uvicorn, asgiref, python-multipart | `net/http` | [0018](../docs/adr/0018-serve-http-with-the-standard-library.md). `http.ServeMux` with method and path patterns. No web framework. |
| httpx (HTTP/1.1 and HTTP/2) | `net/http` client | [0018](../docs/adr/0018-serve-http-with-the-standard-library.md). One shared client, built in `internal/app/activitypub.go`. See §2, "HTTP client". |
| SQLAlchemy 1.4 (async ORM), aiosqlite | `database/sql` + `modernc.org/sqlite` | [0017](../docs/adr/0017-access-sqlite-through-database-sql.md). Hand-written SQL. No ORM, no query builder. See §2, "Database". |
| alembic | `internal/database/migrations` | [0017](../docs/adr/0017-access-sqlite-through-database-sql.md). The old migrations aren't ported. |
| Jinja2 | `html/template` | Templates may differ if the design is kept ([0003](../docs/adr/0003-keep-wire-and-disk-compatibility.md)). Jinja filters and globals become a `template.FuncMap`. Embedding per [0005](../docs/adr/0005-embed-templates-with-wholesale-override.md). |
| pydantic | structs + explicit validation | See §2, "Models". |
| pycryptodome (RSA) | `crypto/rsa`, `crypto/x509`, `crypto/sha256` | PKCS#1 v1.5 signatures, PKCS#1 PEM keys. |
| bcrypt | `golang.org/x/crypto/bcrypt` | Reads the existing `$2b$` hashes. |
| itsdangerous `URLSafeTimedSerializer` | hand-written in `internal/config` | Byte-identical tokens, so admin sessions survive the swap. See §2, "Signed tokens". |
| tomli, tomli-w | `github.com/BurntSushi/toml` | Reads and writes `data/profile.toml`. |
| mistletoe | `github.com/yuin/goldmark` | [0021](../docs/adr/0021-render-markdown-with-goldmark.md). The HTML must equal mistletoe's for the same input. |
| Pygments | `github.com/alecthomas/chroma/v2` | Lexer lookup by alias, and highlighting in templates. See §2, "Code blocks". |
| bleach | `github.com/microcosm-cc/bluemonday` | The policy reproduces bleach's allowed tags, attributes and protocols from `app/templates.py`. |
| bs4 + html5lib | `golang.org/x/net/html` | Parse and walk the tree directly. No CSS-selector library. |
| mf2py | `willnorris.com/go/microformats` | |
| PyLD | `github.com/piprate/json-gold` | [0015](../docs/adr/0015-use-json-gold-for-ld-signatures.md). Contexts come from the captured copies, never the web. |
| Pillow | `image`, `image/jpeg`, `image/png`, `image/gif`, `golang.org/x/image/webp`, `golang.org/x/image/draw` | Decode and resize. |
| Pillow (WebP encoding) | `github.com/gen2brain/webp` | The standard library has no WebP encoder. This module needs no cgo. |
| blurhash-python | `github.com/buckket/go-blurhash` | The hash must equal Python's for the same image (inventory). |
| feedgen | `encoding/xml` | Hand-written RSS and Atom. The judge compares feeds as XML trees. |
| html2text | hand-written over `golang.org/x/net/html` | Only templates use it. |
| emoji | hand-written | Only templates use it. |
| humanize | `github.com/dustin/go-humanize` | Only templates and `stats` use it. |
| python-dateutil | `time` | `time.Parse` with the layouts that the call sites need. |
| cachetools (`LFUCache`, `TTLCache`) | hand-written in the package that uses it | A map with a mutex, and the same size and TTL. |
| Pebble (process pool with timeout) | `context.WithTimeout` + goroutine | |
| loguru | `log/slog` | Same messages and levels. See §2, "Logging". |
| invoke | `flag` + subcommands in `cmd/microblogpub` | [0019](../docs/adr/0019-run-the-server-and-the-workers-in-one-process.md). Subcommand names are the `inv` task names, except the two worker tasks, which become flags of `serve`. |
| prompt-toolkit | `bufio` + `golang.org/x/term` | |
| tabulate | `text/tabwriter` | |
| supervisor, Brotli, greenlet, boussole, libsass | none | One process ([0019](../docs/adr/0019-run-the-server-and-the-workers-in-one-process.md)). `net/http` handles compression. SCSS is gone ([0011](../docs/adr/0011-replace-scss-with-plain-css.md)). |

Banned everywhere: cgo, `unsafe`, `reflect` outside `encoding/*`, global `init()` functions
that read files or the network, and any web framework, ORM or dependency-injection
library.

## 2. Constructs with no equivalent: one canonical mapping each

Every Python construct that Go lacks gets exactly one translation. An implementer who
invents a second mapping for a listed construct breaks a rule. A reviewer who sees an
unlisted construct flags it for this table.

### Types and values

| Python | Go | Notes |
|---|---|---|
| `None` for "no value" of a scalar or struct | pointer (`*T`) when the value can be absent in the source | Never a sentinel value (`""`, `0`, `-1`) for "absent", unless the source uses that sentinel. |
| `str \| None` field from a nullable DB column | `*string` in the row struct | `sql.NullString` only inside the scan code of `internal/database`. |
| `RawObject` (`dict[str, Any]`) | `app.RawObject`, `type RawObject = map[string]any` | An alias, so packages below `app` use `map[string]any` directly. JSON numbers decode as `float64`. Use the `json.Number` decoder only where the source compares integers (inventory). |
| `dict` with fixed keys, used as a record | a struct | |
| `dict` iterated where order matters (output, wire, templates) | a slice of pairs, or keys sorted as the source's insertion order requires | Python dicts keep insertion order. Go maps don't. The inventory lists each site. |
| `list[T]` | `[]T` | `nil` and empty are the same unless the source tells `None` from `[]`. |
| `set[T]` | `map[T]struct{}` | Iterate in sorted order if the order reaches output. |
| `tuple` return | multiple return values | |
| `enum.Enum` (`VisibilityEnum`, …) | `type X string` with constants | The constant's value is the Python member **name** (`"PUBLIC"`), because SQLAlchemy stores names ([0003](../docs/adr/0003-keep-wire-and-disk-compatibility.md)). A method gives the Python `.value` where the source uses it. |
| `int` | `int64` for DB columns and IDs, `int` otherwise | |
| `float` | `float64` | |
| `bool` truthiness (`if x:`) | an explicit test: `x != ""`, `len(x) > 0`, `x != nil` | Write the test that the source's type implies. `0`, `""`, `[]`, `{}` and `None` are all false in Python. |
| `datetime` (aware, UTC) | `time.Time` in UTC | `app/utils/datetime.py`'s `now()` becomes `datetime.Now()`. |
| `str` indexing and slicing, `len(str)` | `[]rune` when the source counts characters | Python counts code points. Go's `len` counts bytes. Use bytes only where the text is ASCII by construction (hashes, IDs). |
| f-string, `%`, `.format` | `fmt.Sprintf` or `+` | Keep the exact output text. |
| `re` | `regexp` | No source regex uses lookaround or backreferences, so RE2 accepts them all. Compile at package level with `regexp.MustCompile`. Note `re.match` (anchored at the start) versus `re.search`. |
| `json.dumps` | `encoding/json` | Go sorts map keys and escapes `<`, `>`, `&` by default. Use an encoder with `SetEscapeHTML(false)`, because Python doesn't escape them. For the DB JSON columns, see §2, "Database". |

### Control flow and errors

| Python | Go | Notes |
|---|---|---|
| a function that can raise | returns `error` last | A function that can't fail in the source returns no error. |
| `raise SomeError(...)` | `return ..., &SomeError{...}` or a wrapped sentinel | Each Python exception class becomes a Go error type with the same name and the `Error` suffix only if the name lacks it (`FetchError`, `ObjectIsGoneError`, `InvalidURLError`). Subclassing becomes an `Unwrap` to the parent's type, so `errors.As` matches as `except` does. |
| `except X:` | `if errors.As(err, &x)` / `errors.Is` | Catch exactly what the source catches. |
| `except Exception:` (log and continue) | `if err != nil { log; continue }` | Python also catches bugs here (`KeyError`, `TypeError`). The Go code can't, so a type assertion or map lookup that can fail in that block must check its `ok` result and take the same path. |
| `try` / `finally` | `defer` | |
| `with` (context manager) | an explicit open, then `defer` close | |
| `assert cond` | `if !cond { panic("assertion failed: …") }` | An assert marks a programming error. The original runs with asserts on. Two recovery points keep the original's results: the web server's recover middleware turns a panic into a 500, as an uncaught `AssertionError` is. Each worker recovers a panic in one message, and marks that message as failed. Nothing else recovers panics. |
| `raise HTTPException(status, detail)` | `return &web.HTTPError{Status: status, Detail: detail}` | The handler wrapper writes the status and the body that FastAPI writes. |
| a bare `KeyError`/`IndexError` that reaches a handler | the same 500 | Where the source would crash on bad input, the Go code returns an error that becomes a 500 too. Mark the site `BUG(port):`. |
| default arguments, keyword arguments | explicit parameters at every call site | For a function with many optional parameters, an options struct named `<Func>Options`. |
| decorators (`@router.get`, `@lru_cache`) | explicit registration in the same file, or a hand-written cache | |
| generators (`yield`) | `iter.Seq[T]` or `iter.Seq2[T, error]` | The translated files have only two generators, and both are framework protocols. FastAPI's `get_db_session` becomes an explicit session (§2, "Database"). httpx's `auth_flow` in `app/httpsig.py` becomes a function that signs the request in place. |

### Async

- `async def f(...)` becomes `func f(ctx context.Context, ...)`. `await` disappears.
  Every function that does I/O (database, HTTP, files) takes `ctx` first.
- `asyncio.wait_for(x, timeout=t)` becomes `context.WithTimeout`.
- The workers' loops (`app/utils/workers.py`) become goroutines that the server starts
  ([0019](../docs/adr/0019-run-the-server-and-the-workers-in-one-process.md)). Keep the
  source's sleeps (0.5 s after a message, 2 s when idle) and its order of work.
- FastAPI `BackgroundTasks` close the media proxy's HTTP client after the response
  (`app/main.py:1399`, `:1468`). That is the only use, and it becomes a `defer`.

### Database

[0017](../docs/adr/0017-access-sqlite-through-database-sql.md) explains these rules.

- **Sessions.** `AsyncSession` becomes `*database.Session`. It wraps one connection and
  begins a transaction at its first statement, as SQLAlchemy's autobegin does.
  `commit()` → `Commit()`, `close()` without a commit rolls back.
- **Write-through.** The source's async session runs with `autoflush=True` (a probe of
  `app/database.py:24` confirms it), so a change reaches the database before the next
  query. The Go session writes each change at once:
  - An attribute assignment on a loaded row becomes a struct assignment, followed by
    `session.Update(ctx, row, "column", …)` with the changed columns. Several
    assignments in a row share one `Update` call, placed before the next statement that
    reads the database.
  - `session.add(new)` becomes `session.Insert(ctx, row)`, which also fills the row's
    ID. Defaults (`default=now`, `default=False`) are set by `Insert` as SQLAlchemy sets
    them.
  - `flush()` has no counterpart and disappears. `refresh(row)` becomes a new load of
    the row.
- **No identity map.** The source's session returns the same object for each load of
  one row, and keeps it after a commit with its old values (`expire_on_commit=False`).
  Each Go load gives a new struct with the database's current values. So:
  - where the source changes a row through one reference and reads it through another,
    pass the one Go struct along;
  - where the source reads a stale cached object, reproduce the stale read and mark it
    `BUG(port):`.

  The inventory lists each site
  ([0017](../docs/adr/0017-access-sqlite-through-database-sql.md)).
- **Queries.** A SQLAlchemy `select(...)` becomes one SQL string in the same file, next
  to the function that runs it, with `?` parameters. `joinedload` becomes a JOIN or a
  second query, as the inventory says. Keep the source's `ORDER BY` and `LIMIT` exactly.
- **Row structs.** Each model class in `app/models.py` becomes a struct in
  `internal/app/models.go`, with the same field names in MixedCaps. Each field has a
  `db:"column_name"` tag. Scanning lives in one function per model.
- **Formats on disk.** These are observations, from databases that the original wrote
  in judge runs, and from SQLAlchemy 1.4.48's own SQLite type processors:
  - `DateTime(timezone=True)`: text `YYYY-MM-DD HH:MM:SS.ffffff`, always with six
    fractional digits (`.000000` too). SQLAlchemy drops the offset: it stores the wall
    clock of the value as given, and reads it back as a naive datetime. The values are
    UTC because the original creates them only with `now()` and `parse_isoformat()`
    (`app/utils/datetime.py`). The Go codec writes UTC in that format, and reads the text
    as UTC.
  - `Enum`: the member name as text (`DIRECT`, `UNDO_LIKE`).
  - `Boolean`: integer `0` or `1`. `None` is NULL.
  - `JSON`: text, as Python's `json.dumps` writes it: `", "` and `": "` separators,
    non-ASCII characters escaped as `\uXXXX`, `<` not escaped, floats with a fraction
    (`1.0`), and keys in insertion order. `None` in a nullable JSON column is SQL NULL.
    [0017](../docs/adr/0017-access-sqlite-through-database-sql.md) decides that the Go
    codec writes the same values, not the same bytes.
  - The database is in WAL mode, and `alembic_version` holds `a209f0333f5a`.

  The codecs live in `internal/database`. Never format these by hand at a call site.
- **Schema.** On an empty database, the port creates the schema at Alembic's head
  ([0017](../docs/adr/0017-access-sqlite-through-database-sql.md)). It never changes the
  schema except through
  `internal/database/migrations`.

### HTTP

- **Server.** Each FastAPI route becomes a method on `*web.Server`, registered in
  `internal/web/main.go` with the same method and path. Keep status codes, headers,
  content types and redirect targets exactly.
- **Middleware.** Cross-cutting behavior follows the standard library's middleware
  pattern: a function that wraps one `http.Handler` (or the mux) in another, for example
  `middleware.Cached(server.ServeImage)`. Middleware lives in `internal/web/middleware`.
  - A FastAPI dependency that only lets a request through or refuses it becomes
    middleware: `user_session_or_redirect`, `verify_csrf_token`,
    `httpsig.enforce_httpsig`, `basic_auth`, `_check_0rtt_early_data`.
  - A dependency that gives the handler a value becomes an explicit call at the top of
    the handler: `get_db_session`, `httpsig.httpsig_checker`.
  - `verify_access_token` refuses a request and gives a value. It becomes middleware
    that stores the token's data in the request context, and a typed accessor for the
    handler.
  - The app-wide middleware of `app/main.py` and the recover middleware of §2 ("assert")
    wrap the mux.
- **Client.** httpx calls become calls on the shared client. Keep the source's timeout
  (httpx's default is 5 s), redirect policy, headers (User-Agent from `config`), and
  HTTP/2 use.
- **Responses that federate** keep their exact JSON shape. The judge compares them.

### Models

- A pydantic model becomes a struct with JSON tags that equal the Python field names or
  aliases. Validation that the model performs becomes a `Validate() error` method,
  called where the source constructs the model.
- `app/config.py`'s `Config` becomes `config.Config`. Its defaults and validators keep
  their exact behavior.

### Module state

- Module-level constants computed at import time (`config.ID`, `BASE_URL`, `LOCAL_ACTOR`,
  …) become package-level variables that an explicit `Load` or `Init` function sets.
  `main` calls them in the import order of the source. No `init()` reads a file.
- Module-level caches become package-level values with a mutex.

### Signed tokens

`itsdangerous.URLSafeTimedSerializer` signs the session cookie and CSRF tokens with
`CONFIG.secret` and a salt (`app/config.py:245-252`). The port reproduces its format
exactly:
- the compact JSON payload, compressed with zlib when that is shorter (a leading `.`),
- base64url without padding,
- the timestamp,
- the HMAC-SHA1 signature, with itsdangerous's default key derivation from the secret
  and the salt.

 Existing admin sessions then survive the swap
([0003](../docs/adr/0003-keep-wire-and-disk-compatibility.md)). The media proxy's
`hmac_sha256` URLs are a wire format, so the same exactness applies to them.

### Code blocks

`app/source.py` writes a fenced code block as
`<pre><code data-microblogpub-lexer="ALIAS">` without highlighting. `ALIAS` is
Pygments' first alias of the lexer. The Go code takes it from a table that maps
Pygments' lexer names to their first alias, generated from the Pygments version in the
original's `poetry.lock` (2.15.1). Chroma only highlights at display time, in templates.

### Logging

`logger.info(f"...")` becomes `slog.Info("...")` with the same message text. Keep the
level. Don't add log lines. `logger.exception` becomes `slog.Error` with the error as an
attribute.

### The rules from the kit

**The BUG rule:** when the original's behavior is itself defective, reproduce it
bug-for-bug and mark the site `BUG(port):` with a one-line description and, where one
exists, the judge scenario that shows it. The judge asserts the defective behavior. Fixes
ship after parity, in a flagged change (`migration-kit/prompts/06-post-parity.md`).

**The error-kind rule:** a Python function can fail in two different ways that Go must
keep apart:
- An exception that the source lets propagate becomes a returned `error`.
- A `None` or `False` that the source returns for "not found" or "invalid" stays a
  value (`nil`, `false`), not an error.

The inventory records which kind each site is.

**The UNKNOWN rule:** when neither this file nor the inventory decides a case, translate
to the most conservative representation that Go offers. Mark it `TODO(port):` with the
open question, and keep moving.

## 3. The sanctioned escape hatch

Go needs no `unsafe` for this port. The escape hatch is dynamic data:

- `any` is allowed only in `RawObject` values and in the JSON-LD code. A type assertion
  on such a value always uses the two-result form (`v, ok := x.(string)`), and takes the
  path that the source takes when the value has another type.
- Every deferred decision carries a marker: `TODO(port):`, `PERF(port):`, `BUG(port):`.
  The format is exact, because later steps grep for it.

## 4. Naming and output paths

These rules make the run resumable, because a unit is done when its output file exists.

- **Paths.** `migration/depmap/layout.tsv` maps each source file to its Go file, and
  `migration/manifest.tsv` lists the work. One source file, one Go file. A file that the
  kickoff (prompt 04) splits into several units still becomes one Go file.
- **Packages.** The package name is the last element of the directory
  (`internal/urlcheck` → `package urlcheck`). `cmd/microblogpub` is `package main`.
- **Tests.** A Python test file becomes the `*_test.go` file that the layout names, in
  the external test package (`package app_test`). Test helpers live in
  `internal/apptest`. Each Python test function becomes `TestName` with the Python name
  in MixedCaps (`test_fetch_actor` → `TestFetchActor`).
- **Functions and variables.** snake_case becomes MixedCaps. A leading `_` (private in
  Python) becomes an unexported name (`_get_public_key` → `getPublicKey`). A private
  name that another Go package uses becomes exported without the underscore
  (`actor._actor_hash` → `actor.ActorHash`) (ADR 0016).
- **Constants.** `UPPER_CASE` module constants become MixedCaps (`BASE_URL` → `BaseURL`).
  Initialisms stay upper case: `ID`, `URL`, `HTTP`, `JSON`, `HTML`, `AP` (`ap_id` → `APID`).
- **Classes.** A class becomes a type with the same name. Its methods keep their names,
  in MixedCaps.
- **Collisions.** If two Python names map to one Go name in a package, the second keeps a
  suffix from its source file (`utils/indieauth.py`'s `get_client_id_data` lives in its
  own package, so this is rare). Record each collision in this section.
- **Home of shared types:**

| Type | Home |
|---|---|
| `RawObject`, `VisibilityEnum`, `AS_PUBLIC`, `AP_CONTENT_TYPE` | `internal/app/activitypub.go` |
| model structs (`Actor` row, `InboxObject`, `OutboxObject`, …) | `internal/app/models.go` |
| `Actor` interface, `LocalActor`, `RemoteActor` | `internal/app/actor.go` |
| `Session`, the DB codecs | `internal/database/database.go` |
| `HTTPError` | `internal/web/main.go` |
| `Config`, the signed-token serializer | `internal/config/config.go` |

## 5. The gap inventory

Python leaves several decisions implicit that Go makes explicit:
- nullability
- the exceptions a call can raise
- dict order
- the ORM's flush and identity semantics
- text length in code points
- JSON number types

Implementers don't decide these. They look them up in `migration/inventory.tsv`, one row
per site (prompt 02). If a site isn't in the inventory, that is an inventory bug. Flag it,
apply the UNKNOWN rule, and keep moving.

## 6. Per-file obligations

- Every Go file starts with a comment that names its source file:
  `// Translated from app/boxes.py.`
- Every translated file ends with a status trailer:

  ```
  // PORT STATUS: confidence=[high|medium|low] todos=[N]
  ```

  Reviewers check the trailer against the file's `TODO(port)` count. A trailer that
  undercounts is a finding.
- Comments follow ASD-STE100 in strict mode, as the judge's comments do.

## 7. Deviation log

Every departure from the documented process gets one line here: skipped passes, waived
checks, untriggered model defaults. It is written at gates, by the owner or with the
owner's sign-off.

| ID | Date | Deviation | Sanctioned by |
|---|---|---|---|
| (none yet) | | | |
