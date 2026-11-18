# 00 — Feasibility report: microblog.pub (Python) → Go

Surveyed: `tsileo/microblog.pub` @ `9c8693e` (2023-07-14, last upstream commit).
Kit: `anthropics/code-migration-kit-with-claude-code` @ `cf91c9d`.
Hard requirements from the owner: (a) wire compatibility — ActivityPub, federation,
IndieAuth/Micropub/Webmention clients keep working after a binary swap; (b) on-disk
compatibility — existing `data/microblogpub.db` and `data/` files used in place.
Allowed: templates may differ (keep the design); one-time conversion of config/key files.

## The six steps (index)

1. **Create the map and the rules** — dependency map, gap inventory, rulebook.
2. **Stress-test the rules** — bakeoff + pilot; only rule changes survive.
3. **Translate everything** — implementer + 2 adversarial reviewers + fixer per unit, queue on disk.
4. **Compile** — survey build → error queue → fixers (dissolves into Step 3 for cheap typecheckers).
5. **Run it** — hello world, then smoke.
6. **Match behavior** — the judge, run against old and new, until the done-gate.

## The case for leaving (pain in this code, not Python's reputation)

- **Three processes + supervisord for one user.** `misc/supervisord.conf` runs uvicorn,
  `inv process-incoming-activities`, `inv process-outgoing-activities`; Docker adds
  `misc/docker_start.sh`. Go collapses this into one binary with goroutine workers.
- **Dependency rot is already biting.** `pyproject.toml` pins `python = "^3.10"`, pydantic 1,
  greenlet 1.1.3, Pillow 9, httpx 0.23, fastapi 0.78. The install only works here on
  Python 3.11 (3.13 is the system default); upstream has had no commit since 2023-07-14.
  42 runtime deps for a single-user server.
- **Cycle-dodging imports.** 27 function-local `from app…` imports across `app/*.py` and
  `app/utils/*.py` — the module graph is cyclic and held together by import placement.
- **Build-time toolchain**: SCSS via boussole/libsass (`tasks.py:77`, `data/_theme.scss`).
- **Owner-stated (review of this report):** the Python implementation is slow and
  memory-hungry in production, and the owner is far more fluent in Go, so the blog engine
  will be easier to maintain and improve after the move. (Not measured in this survey.)

Counter-case, stated honestly: the code works, is type-clean (mypy: 0 issues in 47 files),
and is small. The case for leaving is operational (deploy/runtime rot, resource use) and about who
maintains it, not correctness.

## Size (gathered)

| Area | Files | Lines |
|---|---|---|
| `app/**/*.py` | 47 | 12,479 (largest: `boxes.py` 2,761, `main.py` 1,728, `admin.py` 1,282, `models.py` 656, `indieauth.py` 552) |
| `app/templates/*.html` (Jinja2) | 24 | 1,767 (37 macros, 20 filters/globals in `app/templates.py`) |
| `tests/*.py` | 15 | 2,389 (55 test functions) |
| `alembic/versions` | 12 migrations | head = `a209f0333f5a` |
| HTTP routes | 70 | `main.py` 29, `admin.py` 30, `indieauth.py` 8, `micropub.py` 2, `webmentions.py` 1 |
| DB tables | 16 | 127 `Column(` declarations in `app/models.py`; 109 `select(` query sites in `app/*.py` |

## Call 1 — Structure-preserving or redesign?

Domain logic translates one-to-one: ActivityPub object handling (`activitypub.py`,
`ap_object.py`, `boxes.py`), HTTP signatures (`httpsig.py`), LD signatures (`ldsig.py`),
webfinger, IndieAuth, Micropub, webmentions, uploads. Each Python module can become one
Go file with the same functions. Three seams cannot translate one-to-one, and the rulebook
must decide each one up front:

- **ORM → SQL.** SQLAlchemy async ORM, relationships, `Enum`/`JSON`/`DateTime(timezone=True)`
  columns (`app/models.py:46-626`). Go gets hand-written SQL over `database/sql` and must
  reproduce SQLAlchemy's SQLite storage formats byte-for-byte (see inventory seeds below).
- **Jinja2 → `html/template`.** 37 macros, `{% block %}` inheritance (78 blocks), 20 custom
  filters. `html/template` has no inheritance; this is a redesign of the view layer,
  which the owner has allowed.
- **FastAPI → `net/http`.** Dependency injection (`Depends`, `Form()`), content negotiation
  by `Accept` header, and the `customization.py` plugin API (Python callables).
- **Package cycles.** The 27 local imports mean the Python modules can't become separate
  Go packages without breaking cycles; one Go package (`internal/app`) mirroring the
  module files is the cheap answer.

**Committed call: structure-preserving per module, with rulebook-decided redesigns at three
seams — forced by `app/models.py` + `app/database.py` (ORM), `app/templates.py` +
`app/templates/*.html` (Jinja), `app/main.py` + `app/admin.py` + `app/customization.py`
(FastAPI DI and Python plugin hooks).**

**Owner ruling (review):** translate one-to-one wherever possible, even where the result
isn't the most idiomatic Go architecture. Cleaning up the architecture comes after the
port is functional. Recorded as
[ADR 0009](../docs/adr/0009-translate-one-to-one-before-refactoring.md).

## Call 2 — What does verification cost?

Baseline, measured here (Python 3.11 venv, cold):

- Build: `poetry install` → **15 s** (deps only; the project-root install fails because
  upstream installs with `--no-root`; not a real failure).
- Typecheck: `poetry run mypy app` → **45 s**, `Success: no issues found in 47 source files`.
- Tests: not run (this prompt forbids test runs).

Target: Go's `go build`/`go vet` on ~15–20k lines in one package — assumption: under
10 s warm, since Go compile time scales roughly linearly with package size and there is
no codegen. If cgo is used (e.g. `mattn/go-sqlite3`), cold builds go up by about a
minute; a pure-Go SQLite driver (`modernc.org/sqlite`) avoids that.

**Committed call: cheap referee — Step 4 dissolves into Step 3 (`go build ./... && go vet ./...`
runs in-loop per unit), forced by the single-package layout chosen for the cycles in
`app/boxes.py`/`app/actor.py`/`app/ap_object.py` et al.** The expensive referee is
Step 6's judge (paired live instances, see call 3), not the compiler.

## Call 3 — Do the tests survive?

Census (grep receipts: `grep -E "^from app|^import app"` and `grep -cE "client\.(get|post)"` per file):

- **Portable as written: 0 of 55.** Every test file imports `app.*` internals. Even the
  HTTP-level tests drive an in-process FastAPI `TestClient`, seed state with
  `tests/factories.py` (SQLAlchemy factories), and mock remotes with `respx`.
- **Portable intent (HTTP assertions, rewrite needed): 38 tests in 6 files** —
  `test_public.py` (10), `test_outbox.py` (11), `test_inbox.py` (9), `test_emoji.py` (4),
  `test_tags.py` (2), `test_remote_actor_deletion.py` (2). Three examples:
  `test_index__ap` (`GET /` with AP `Accept` → actor JSON), `test_followers__ap`
  (`/followers` collection shape), the inbox Follow → Accept flow in `test_inbox.py`.
- **Internal-bound: 17 tests in 6 files** — `test_httpsig.py` (5, calls
  `httpsig._KEY_CACHE`/`HTTPSigInfo`), `test_ldsig.py` (1, calls `ldsig.generate_signature`
  directly), `test_process_outgoing_activities.py` (7, calls `process_next_outgoing_activity`),
  `test_actor.py` (2), `test_admin.py` (1), `test_utils.py` (1). They can't judge the
  port, but the behavior they guard isn't dropped: each becomes a Go unit test next to
  the code it covers, written in the same unit of work as that code (e.g. `test_httpsig.py`
  → `httpsig_test.go`). Where it is visible from outside, it is also covered by the judge.

Too little survives to judge. **Verdict on call 3: build the judge first —
`prompts/00b-judge-setup.md` runs between this gate and Step 1.** Seed scenarios for it:

1. **Swap test (the owner's real requirement).** Python creates `data/` (DB + uploads + key);
   Go starts on that exact directory with no migration step; then the reverse (Go-written DB
   read by Python, which must still pass `alembic current` = `a209f0333f5a`).
2. **Read surface diff.** Same seeded `data/`, both servers: every public GET with AP
   `Accept` headers (`/`, `/o/{id}`, `/o/{id}/activity`, `/outbox`, `/followers`,
   `/following`, `/featured`, `/t/{tag}`, `/.well-known/webfinger`, `/.well-known/nodeinfo`,
   `/nodeinfo`, `/feed.json`, `/feed.atom`, `/feed.rss`). JSON (including `/feed.json`) is
   diffed after normalization. `/feed.atom` and `/feed.rss` always return XML
   (`app/main.py:1711-1727`), so they are diffed as normalized XML: parsed, attribute
   order and insignificant whitespace ignored, generator/build timestamps masked.
   HTML is checked for status, content type, and key microformats only.
3. **Paired instances (owner's design; the core of the judge).** Two live microblog.pub
   instances federate with each other. A scenario triggers an interaction on one, such as
   posting at each visibility level, follow/unfollow, accept/reject, like, announce, reply,
   poll and vote, mention, delete, update, profile update, block, or move, then checks the
   observable results on either instance. The suite is validated first on a Python–Python
   pair. Then it runs with each side swapped for Go in turn (Go–Python, Python–Go), and
   finally Go–Go. A recording proxy between the instances captures every HTTP exchange so
   that the actual payloads and signature headers can also be diffed against the
   Python–Python run. Otherwise a mixed pair could pass with payloads that the lenient
   Python receiver tolerates but Mastodon would reject. Practicalities found in the source:
   Python refuses localhost/private URLs unless `debug = true` (`app/utils/url.py:50-80`),
   and debug mode otherwise only adds verbose logs and drops the HSTS header
   (`app/main.py:154`), so the pair runs in debug mode on two local ports with
   `https = false`. Delivery is asynchronous (the outgoing worker with retries), so
   assertions poll with a deadline.
   A small replay set of captured real Mastodon payloads covers inputs a microblog.pub peer
   never sends: `hs2019` signatures, LD-signed forwarded activities, and Mastodon-specific
   object shapes. Recorded as
   [ADR 0010](../docs/adr/0010-judge-federation-with-paired-instances.md).
4. **LD-signature parity.** A doc signed by Python verifies under Go and the reverse
   (JSON-LD URDNA2015 via a Go library must match PyLD byte-for-byte).
5. **Client protocols.** IndieAuth code flow + token/introspect/revoke, OAuth client
   registration, Micropub create, Webmention receive — token rows written by one
   implementation must be accepted by the other.
6. **Admin session carry-over (nice to have).** Ideally a session cookie and CSRF token
   minted by Python (`itsdangerous.URLSafeTimedSerializer`, `app/config.py:245-252`) are
   accepted by Go. Per the owner, logging in once more after the swap is acceptable: keep the
   format only if it costs little, and drop this scenario otherwise. Media-proxy HMAC URLs
   (`app/media.py`) do appear in federated and cached HTML, so they stay compatible.
7. **DB-state diff.** After each scenario, dump both DBs table by table and diff rows,
   with timestamps and generated IDs normalized.

Judge validation per 00b: N/N clean on Python, then mutate Python (flip visibility enum,
drop Digest header, change the DateTime format) and confirm each one is caught.

## Inventory seeds (compat sites the gap inventory must cover)

- **SQLite formats written by SQLAlchemy 1.4** (`app/models.py`): `DateTime(timezone=True)`
  stored as text `YYYY-MM-DD HH:MM:SS.ffffff` with no offset (assumption, verify in 00b
  scenario 1); `Enum(VisibilityEnum)` stores member **names** (`PUBLIC`), not values
  (`public`) (assumption, same check); `JSON` as text; Boolean as 0/1. The `alembic_version`
  table must be left at head; Go must never alter the schema unless a future migration is ported.
- **Keys**: `data/key.pem` is PKCS#1 PEM from pycryptodome `exportKey("PEM")`
  (`app/key.py:13`) — Go `crypto/x509.ParsePKCS1PrivateKey` reads it directly, so no
  conversion is needed. `keyId = {actor}#main-key` (`app/key.py:47`).
- **Config**: `data/profile.toml`, pydantic `Config` (`app/config.py:93-135`). Go reads the
  same TOML; `admin_password` is bcrypt `$2b$` (`golang.org/x/crypto/bcrypt` reads it).
  Probably no conversion needed.
- **Python-only customization (cannot carry over as-is)**: `data/custom_routes.py`
  (`app/config.py:231`), `data/stream.py` (`app/config.py:283`), Jinja overrides in
  `data/templates/` (`app/templates.py:42`). **Decided:** the hooks are dropped (ADR 0004;
  the owner uses neither), and template overrides are replaced by the wholesale override
  (ADR 0005).
- **SCSS theme**: `data/_theme.scss` compiled by libsass (`tasks.py:87`). **Owner ruling:** port
  the styles to plain CSS (custom properties replace Sass variables), removing the Sass
  toolchain entirely. Theme customization then goes through the wholesale asset override of
  ADR 0005. Recorded as [ADR 0011](../docs/adr/0011-replace-scss-with-plain-css.md).
- **Library swaps that change output for new content only** (stored HTML is untouched):
  mistletoe→goldmark, bleach→bluemonday, Pygments→chroma, feedgen→gorilla/feeds or
  hand-built, mf2py→`willnorris.com/go/microformats`, Pillow+blurhash→`x/image` + `go-blurhash`,
  PyLD→`piprate/json-gold`.
- **HTTP signatures**: `rsa-sha256`/`hs2019` accepted. Outgoing requests sign two header
  lists (`app/httpsig.py:320-332`): with a body, a `Digest: SHA-256=…` header is added and
  `(request-target) user-agent host date digest content-type` is signed; without a body
  (signed GETs), there is no Digest and `(request-target) user-agent host date accept` is
  signed.

## The six steps, instantiated

| Step | In this repo | Kit prompt | Units × per-unit |
|---|---|---|---|
| 0b Judge | Docker-free harness: Python server + Go server + fake fediverse; scenarios 1–7 above | `prompts/00b-judge-setup.md` | ~7 scenario groups × unknown |
| 1 Map/rules | From the repo root, run `python3 -I migration-kit/scripts/depmap_python.py --root reference/microblog.pub --out migration/depmap`. The source root must be the reference checkout: with `--root .` the modules are named `reference.microblog.pub.app.*`, upstream `from app…` imports resolve to nothing, and a trial run found 1 edge instead of 349. The Python mapper fits as-is; rulebook decides the 3 seams + storage formats; inventory from the seeds above | `prompts/01`, `02`, `templates/RULEBOOK.md` | 47 modules surveyed |
| 2 Stress test | Bakeoff on the nastiest files: `httpsig.py`, `ldsig.py`, `models.py`+one `boxes.py` slice, `templates/object.html` | `prompts/03-stress-test.md` | 4 files × 2 translators |
| 3 Translate | 47 `.py` → `internal/app/*.go` (one package); 24 templates → Go templates; `boxes.py`/`main.py`/`admin.py` split into ~4 units each | `prompts/04-translation-kickoff.md` + `scripts/queue_runner.mjs` | ~80 units × (impl + 2 reviewers + fixer) |
| 4 Compile | Dissolved into Step 3 (in-loop `go build`/`go vet`) — the human edits `.claude/settings.json` at the gate | (dissolved) | — |
| 5 Run | `microblogpub serve` on a copy of a real `data/`; smoke: `/`, webfinger, a signed inbox Follow | — | 1 |
| 6 Match | Run the 00b judge against both until N/N; re-run on Python → zero inherited failures | judge + `prompts/06-post-parity.md` | scenarios × rounds |

## Cost & duration (bands only)

Drivers: ~80 translation units × 4 agents per unit (implementer, 2 reviewers, fixer) ×
a cheap in-loop referee, plus a judge that is expensive to build (two live servers and a
fake federation peer).

- **Tokens: tens of millions (band 10M–60M).** Most of it is in Step 3; the judge build and
  Step 6 burndown are the next largest. The big units (`boxes.py`, `main.py`, `admin.py`)
  are at the top of the band.
- **In API dollars** (list prices as of 2026-11, per 1M input/output tokens: Opus 5.5 $4/$20,
  Sonnet 5.5 $2/$10, cache reads $0.20/$0.10): mostly Sonnet volume with Opus review, and
  input-heavy agent work, puts the band at roughly **$20–$250**. Subscription plans don't
  publish weekly limits in tokens, so this can't be converted into "N Pro weeks" from the
  repo. Measure it instead: run one batch, read the usage meter, and extrapolate per unit.

| Step | Wall clock | Your attention |
|---|---|---|
| 0b judge | hours | 20–40 min (sign-off on scenarios + mutation run) |
| 1 map/rules | 1–3 h | 30–60 min (the seam decisions are yours) |
| 2 stress test | 1–2 h | 20–30 min (amendments) |
| 3 translate | several hours to a day of agent time | 15 min per batch gate |
| 4 compile | dissolved | 5 min (settings.json edit) |
| 5 run | < 1 h | 10 min |
| 6 match | hours to days | 30–90 min (triage review, final swap test on *your* data) |

## Model plan (your decision at this gate)

| Phase | Recommended | Why |
|---|---|---|
| Rulebook authorship + amendments | `claude-opus-5-5` | One-time; a wrong storage-format or signature rule replicates into every file. |
| Judge build (00b) + its reviewers | `claude-opus-5-5` | The judge is the exit condition. A comparator bug produces false verdicts. |
| Skeptic reviewers (Step 1/3) | `claude-opus-5-5` for `httpsig/ldsig/models/boxes/indieauth`; `claude-sonnet-5-5` elsewhere | Protocol and storage sites are where a silent divergence breaks federation. |
| Translation implementers | `claude-sonnet-5-5` | High volume, and two reviewers plus `go build` stand behind them. |
| Fixers | `claude-sonnet-5-5` | The compiler and the judge are the real referees. |

## Verdict

**Migrate — but build the judge first** (`prompts/00b-judge-setup.md` before Step 1). The
architecture translates file by file. The owner's two hard requirements are both testable
mechanically: the swap test and the protocol scenarios. No construct found here is
impossible in Go.

**Fact that would change it:** none known. The top risk used to be whether
`piprate/json-gold`'s URDNA2015 normalization of real Mastodon/Pleroma activities matches
PyLD byte-for-byte; a mismatch would break LD signatures in both directions. Owner ruling:
if no Go library matches, we port our own canonicalization, which makes it a cost risk and
not a verdict risk. Note that PyLD does not use local contexts here:
`app/ldsig.py:21-36` fetches every context over the network and only rewrites
`https://w3id.org/identity/v1` to a raw.githubusercontent.com copy. **Check in < 1 day:**
capture the context documents once, then serve those identical bytes to both PyLD and the Go
loader, so that fetch failures or drifting remote contexts can't pass for a
canonicalization mismatch. Then sign 20 real captured activities with
`ldsig.generate_signature`, verify them with a 50-line Go program, and diff the canonical
N-Quads. The Go port will need the same rewrite, ideally with the contexts embedded.

Owner-side question, answered in review: `data/custom_routes.py` and `data/stream.py` are
not used; template overrides are covered by ADR 0005.

## What I read / what I ran

Read: kit `README.md`, `CLAUDE.md`, `RUN-NOTES.md`, `skill/SKILL.md`, `prompts/00`, `00b`;
source `pyproject.toml`, `app/config.py`, `app/key.py`, `app/database.py`,
`app/customization.py` (first 80 lines), `misc/supervisord.conf`, `tests/test_public.py`
(first 40 lines), `data/tests.toml`; grep-sampled `app/models.py`, `app/httpsig.py`,
`app/ldsig.py`, `app/uploads.py`, `app/source.py`, `app/activitypub.py`, `tasks.py`, all
`tests/test_*.py` headers. **Skipped (not read in full):** `boxes.py`, `main.py`,
`admin.py`, `indieauth.py`, `templates.py`, all Jinja templates, all alembic migrations
except the head revision line.
Ran: `wc -l`/`grep -c` counts above; `poetry install` on Python 3.11 (15 s); `poetry run mypy app`
(45 s, clean). No tests run. `git status`: the source tree is unchanged. The only writes
are this report and `migration/cost-log.tsv`.

## Gate 0 sign-off (owner decisions, 2026-11-18)

1. **Model plan: approved as proposed** (table above).
2. **Python customization hooks:**
   - `data/custom_routes.py` and `data/stream.py` are **dropped**. The Go port has no
     plugin hooks. The stream uses the default visibility callback only.
   - **Templates and static assets are embedded in the binary** (`embed.FS`). To
     customize, the user dumps *all* templates and static assets to a directory (a CLI
     subcommand) and passes a flag that loads *all* of them from that directory instead
     of the embedded copy. This is a wholesale switch with no per-template overrides, so
     Jinja overrides in `data/templates/` are not read.
3. **Ported code lives in `nevkontakte/microblog.go`.** Nothing is ever committed to
   `tsileo/microblog.pub`; it is pinned read-only at `reference/microblog.pub`.
4. **Stop between steps; one PR per self-contained unit of work**, reviewed by the owner.
   Nothing goes straight to `main`.
5. **`.claude/settings.json`**: installed by the owner via PR #1 (Go-adapted kit template;
   Step 4 dissolved into Step 3). Git policy: PR branches may be rebased and pushed with
   `--force-with-lease`, `main` advances only by PR (GitHub's PR merge commits are fine,
   but no merges between branches), and review feedback is addressed by amending commits.

6. **Review of this report (2026-11-19):**
   - Translate one-to-one first and refactor after parity (ADR 0009).
   - Judge federation with paired instances (ADR 0010).
   - Replace SCSS with plain CSS (ADR 0011).
   - Write code for human readers (ADR 0012).
   - Admin session compatibility is nice to have, not required (ADR 0003).
   - Internal-only Python tests come over as Go unit tests.
   - A JSON-LD library mismatch is solved by porting our own canonicalization.

**Next step:** `migration-kit/prompts/00b-judge-setup.md` (build and validate the parity
judge), as its own PR.
