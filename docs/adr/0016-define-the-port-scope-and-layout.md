# 16. Define the port's scope and package layout

Date: 2026-11-21

## Status

Accepted

## Context

Step 1 of the migration kit builds the dependency map, and the map orders all the
translation work. The map needs two inputs that no record states yet:

- **The scope.** It names the Python files that the port translates. The reference
  checkout holds 79 `.py` files, but not all of them run in a deployed instance.
- **The target packages.** The map condenses the file graph to the Go packages and checks
  that graph for cycles, because Go rejects import cycles between packages.

The 79 files fall into these groups:

| Files | Count | Role |
|---|---|---|
| `app/**/*.py` | 47 | The server and its workers |
| `tasks.py` | 1 | The `inv` command-line tasks that owners run (`inv move-to`, `inv reset-password`, …), plus developer tasks |
| `scripts/config_wizard.py` | 1 | The first-run configuration wizard (`inv configuration-wizard`) |
| `scripts/build_docs.py` | 1 | Builds the project's documentation website |
| `alembic/**/*.py` | 13 | Schema migrations up to head `a209f0333f5a` |
| `tests/*.py` | 16 | The original test suite |

The feasibility report proposes one Go package for all of `app/`, because the Python
modules import each other in cycles. The file graph has a cycle of 17 files. But most of
its edges are imports inside functions or under `if TYPE_CHECKING:`, and two of them exist
only for features that the port drops. Without those two, many modules are
self-contained.

## Decision

### Scope

The port translates 58 files:

- `app/**/*.py`, except `app/utils/custom_index_handler.py`. That file only helps an
  owner's custom index hook, which the port drops
  ([0004](0004-drop-python-customization-hooks.md)), and no file imports it.
- `tasks.py` and `scripts/config_wizard.py`. They become subcommands of the binary with the
  `inv` task names and flags, because the judge drives the command-line tasks by those
  names (`Instance.Command`). The rulebook lists which tasks are owner-facing.
  Developer-only tasks (`lint`, `autoformat`, `tests`, `build-docs`, …) have no Go
  counterpart.
- The six test files that exercise internals, which the judge can't reach:
  `test_httpsig.py`, `test_ldsig.py`, `test_process_outgoing_activities.py`,
  `test_actor.py`, `test_admin.py` and `test_utils.py`. Each becomes a Go unit test next to
  the code it covers, as the feasibility report and
  [0010](0010-judge-federation-with-paired-instances.md) planned. Their helpers
  (`tests/conftest.py`, `tests/factories.py`, `tests/utils.py`) become a test-helper
  package.

The port doesn't translate these files:

- The other test files. The judge ports their tests as scenarios.
- `alembic/`. The port reads and writes the schema at head, and doesn't port the old
  migrations. See "Schema migrations" below.
- `scripts/build_docs.py`. It builds a website about the project, not part of an
  instance.

### Packages

`migration/depmap/layout.tsv` names the Go file of each translated file. The package
graph that the dependency map computes from it has no cycle. The packages form these
layers, from the bottom:

1. **Self-contained leaves**, with no in-scope dependency: `internal/key`,
   `internal/version`, `internal/emoji`, `internal/datetime`, `internal/text`,
   `internal/pagination`, `internal/favicon`.
2. **Configuration:** `internal/config`, on `emoji` and `version`.
3. **Services on the configuration:** `internal/database`, `internal/media`,
   `internal/urlcheck` (`utils/url.py`), `internal/highlight`, `internal/privacyreplace`,
   `internal/microformats`, `internal/webfinger`, `internal/webmention` (sending and
   endpoint discovery), `internal/indieauthclient` (`utils/indieauth.py`),
   `internal/mastodon` (the following-accounts import), `internal/workers`,
   `internal/yunohost`.
4. **The ActivityPub core,** `internal/app`: `activitypub`, `actor`, `ap_object`, `models`,
   `httpsig`, `ldsig`, `source`, `boxes`, `outgoing_activities`, `incoming_activities`,
   `customization`, `uploads`, `utils/facepile`, `utils/opengraph`. These import each
   other in cycles, eager ones included (`activitypub` ↔ `httpsig`, `boxes` ↔
   `outgoing_activities`), so they stay one package.
5. **Users of the core:** `internal/templates`, `internal/lookup`, `internal/prune`,
   `internal/stats`.
6. **The HTTP server,** `internal/web`: `main`, `admin`, `indieauth`, `micropub`,
   `redirect`, and `webmentions` (receiving). `indieauth` uses `admin`, and `micropub`
   uses `indieauth`, so they share a package.
7. **The binary,** `cmd/microblogpub`: `tasks.py` and `scripts/config_wizard.py`.

The unit tests sit next to the code they cover (`internal/app/httpsig_test.go`, …). The
test helpers go to `internal/apptest`, so the tests are external test packages.

Two kinds of file edges don't become package edges. `migration/depmap/dropped.tsv` lists
each one with its reason:

- `config` → `customization`, and the imports of `customization`'s page handler. They
  exist only for the hooks that the port drops
  ([0004](0004-drop-python-customization-hooks.md)). The stream always uses the default
  visibility rule, so `boxes` calls it directly.
- `utils/emoji` → `activitypub`, a type-only import of `RawObject`, an alias of
  `dict[str, Any]`. The Go file uses the map type.

The package names are proposals. The rulebook's naming rules (§4) can rename them, and
the layout table and the manifest then change with them.

### Schema migrations

The port doesn't port the 12 Alembic migrations, but it gets a home for its own future
migrations: `internal/database/migrations`. They continue from Alembic's head
(`a209f0333f5a`), and the port records the current revision in the same
`alembic_version` table. Thus an instance stays at one revision line across the swap.
`inv migrate-db` (`tasks.py`) becomes the subcommand that applies them. The rulebook
decides the migration format and how the binary treats a database older than head.

## Consequences

- The map covers 58 files, and the package graph has 28 packages (plus 3 test packages)
  and no cycle. The compiler never forces a cycle break during translation.
- Most small modules translate and compile in isolation, so translation units can build
  on finished packages instead of on one large package. A one-package build of
  `internal/app` remains for the core, with its 14 files.
- Python names that start with `_` but cross module lines (for example
  `actor._actor_hash`, `source._MENTION_REGEX`) become exported Go names when they cross
  package lines. The rulebook states the rule.
- A move of a file to another package later is a refactor after parity
  ([0009](0009-translate-one-to-one-before-refactoring.md)). Translation keeps one Go file
  for each Python file, so the layout changes only where files live.
- An owner whose database is older than Alembic's head must run the original's
  `inv migrate-db` before the swap, unless the rulebook adds another path.
