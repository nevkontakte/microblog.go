# Step 1, prompt 01: dependency map

Date: 2026-11-21. Prompt: `migration-kit/prompts/01-dependency-map.md`.

## Result

| Measure | Value | Source |
|---|---|---|
| Files mapped | 58 | `migration/depmap/layout.tsv` |
| File edges | 311 | `edges.tsv`, minus the header |
| Migration batches | 41 | lines of `order.txt`, minus the comment |
| File cycles | 2 (17 files and 2 files) | `cycles.txt` |
| Packages | 28, plus 3 test packages | the directories in `layout.tsv` |
| Package edges | 86 | `package_edges.tsv`, minus the header |
| Package cycles | 0 | `package_cycles.txt` |
| Manifest units | 55 | `tail -n +2 migration/manifest.tsv \| wc -l` |

ADR 0016 sets the scope and the package layout:

- **In scope:** `app/**` (without `app/utils/custom_index_handler.py`), `tasks.py`,
  `scripts/config_wizard.py`, the six test files that exercise internals, and their
  helpers.
- **Out of scope:** the other tests, `alembic/` and `scripts/build_docs.py`. They are
  neither nodes nor edges.

The first version of this PR put all of `app/` in one package. After review, the layout
splits `app/` into layers of separate packages (see "Packages" below).

## Cycles

At the file level:

1. **17 files:** `app/activitypub.py`, `app/actor.py`, `app/ap_object.py`, `app/config.py`,
   `app/customization.py`, `app/database.py`, `app/httpsig.py`, `app/media.py`,
   `app/models.py`, `app/source.py`, `app/templates.py`, `app/utils/emoji.py`,
   `app/utils/highlight.py`, `app/utils/privacy_replace.py`, `app/utils/url.py`,
   `app/utils/webmentions.py`, `app/webfinger.py`.
2. **2 files:** `app/boxes.py`, `app/outgoing_activities.py`.

At the package level: none. The 17-file cycle splits across packages, because two kinds
of its edges don't become package edges (see "Packages"). The core files that remain in
a cycle are all in `internal/app`.

The kit migrates each cycle as one batch. The 17-file batch holds the core of the
application. Most of its edges are deferred imports, inside functions or under
`if TYPE_CHECKING:`. With only the module-level imports (270 edges), one cycle is left:
`app/activitypub.py` ↔ `app/httpsig.py`. The kickoff (prompt 04) can use this to split
the 17-file batch into a module-level order. The map itself keeps all imports, because a
Go file needs the types and functions of a deferred import too.

## Packages

`migration/depmap/layout.tsv` names the Go file of each mapped file. The package of a file
is the directory of its Go file. The layers, from the bottom:

1. Self-contained leaves: `key`, `version`, `emoji`, `datetime`, `text`, `pagination`,
   `favicon`.
2. `config`, on `emoji` and `version`.
3. Services on `config`: `database`, `media`, `urlcheck`, `highlight`, `privacyreplace`,
   `microformats`, `webfinger`, `webmention`, `indieauthclient`, `mastodon`, `workers`,
   `yunohost`.
4. The ActivityPub core, `app`, with 14 files that import each other in cycles.
5. Users of the core: `templates`, `lookup`, `prune`, `stats`.
6. The HTTP server, `web`, with 6 files.
7. The binary, `cmd/microblogpub`. The test helpers are in `apptest`.

`migration/depmap/dropped.tsv` lists the six file edges that the package graph leaves
out, each with its reason:

- Five edges exist only for the customization hooks that the port drops (ADR 0004):
  `config` → `customization`, and the four imports of `customization`'s page handler.
- `utils/emoji` → `activitypub` imports only the type `RawObject`, an alias of
  `dict[str, Any]`.

These edges stay in `edges.tsv`, which describes the source. The script refuses a dropped
edge that isn't in the map.

## The script

`migration/scripts/depmap_python.py` is the kit's mapper with these changes:

- `--include` limits the walk to the translated files.
- String literals that name an in-scope file or an entry point (`"module:attribute"`) are
  edges. Review round 1 found the pattern (see below).
- A pytest file (`test_*.py`) depends on each `conftest.py` in its directory and above,
  because pytest loads those fixtures without an import.
- `--layout` reads the layout table and condenses the file graph to Go packages. A
  `*_test.go` file is a separate node, because it can be an external test package.
- `--drop` reads the dropped edges, which the package graph leaves out.

The kit's Python fixtures still give the expected output. Two runs give byte-identical
output. The command is in the script's docstring. It runs in 0.2 s.

Before the reviews, I checked five files by hand against their import lines:
`app/key.py`, `app/ldsig.py`, `app/webfinger.py`, `app/utils/emoji.py` and `tasks.py`. The
script matched all five.

## Reviews

Each round had two skeptic reviewers, in separate contexts with disjoint samples of 20
files. Half of each sample was random, from non-overlapping halves of the file list. The
other half was adversarial:

- **Reviewer 1:** the largest files.
- **Reviewer 2:** files with guarded or unusual imports: empty `__init__.py` files,
  `TYPE_CHECKING` blocks, `try/except ImportError`, and `from app import module` forms.
  This reviewer also checked that the out-of-scope files add no edges.

Per the model plan, a reviewer with a protocol file (`httpsig`, `ldsig`, `models`, `boxes`,
`indieauth`) ran on `claude-opus-5-5`. The others ran on `claude-sonnet-5-5`. Each reviewer
listed the dependencies from the source, with line numbers, and never saw the map. I
compared their rows with the map in both directions.

| Round | Reviewer | Model | Rows | Missed by the map | Invented by the map |
|---|---|---|---|---|---|
| 1 | A (largest) | opus | 142 | 2 non-import edges | 0 |
| 1 | B (guarded imports) | sonnet | 82 | 0 | 0 |
| 2 | C | opus | 89 | 0 | 0 |
| 2 | D | opus | 118 | 0 | 1, not confirmed (below) |
| 3 | E (test files) | opus | 104 | 0 | 0 |
| 3 | F (test files) | opus | 92 | 0 | 0 |

- **Round 1, two misses, confirmed in the source:** `tasks.py:100` starts
  `uvicorn app.main:app`, and `tasks.py:174` runs `python scripts/config_wizard.py`. Both
  reach an in-scope file through a subprocess, not an import. I fixed the pattern in the
  script, not the two edges: string literals that name an in-scope file or an entry point
  are now edges. In the whole scope, the pattern matches exactly these two strings.
- **Round 2** sampled again, with the 9 files that round 1 didn't cover. Both reviewers
  agreed with the map on every row except one.
- **The unconfirmed row:** the map has `app/config.py` → `app/__init__.py`, from
  `app/config.py:31`, `from app._version import VERSION_COMMIT`. `app/_version.py` is
  generated and not on disk. My instructions to reviewer D contradicted each other on this
  case. In Python, the import loads the `app` package first, so the edge is real. The
  target file is empty, so the edge has no effect on the order.

- **Round 3** followed the layout change. The scope gained the test files, and the script
  gained the `conftest.py` rule, so two new reviewers checked samples that held all 10
  test files. Both agreed with the map on every row. Reviewer E also noted that
  `tasks.py:126` runs `pytest tests`. That is the developer-only `tests` task, which the
  port drops, and it names a directory, not a file. So it isn't an edge.

Rounds 2 and 3 are clean, so the map stands.

Reviewers also reported dependencies that leave the scope. None of them is an edge:

- `tasks.py` runs `alembic` and `scripts/build_docs.py`, which import `app` modules.
- `app/config.py` imports the owner's `data/custom_routes.py` and `data/stream.py`.

## The manifest

`migration/manifest.tsv` comes from `migration-kit/scripts/make_manifest.py`. Each row
of the layout table is one substitution, `--sub <source>=<Go file>`. I then removed the
rows of the three empty `__init__.py` files (Go file `-`), because they have nothing to
translate. This leaves 55 units, and the manifest equals the layout table without those
rows.

The Go names are a proposal for the rulebook's naming rules (§4). If the rulebook changes
them, the layout table changes, and the map and the manifest are generated again. The
feasibility report also expects that `boxes.py`, `main.py` and `admin.py` split into
about four units each. That split belongs to the kickoff (prompt 04), not to the map.

## Next step: the draft rulebook, then the gap inventory

Prompt 02 needs a draft `RULEBOOK.md` first. Estimates, from the counts above (55 units,
12,479 lines in `app/`, 2 file cycles, 5 protocol files) and from the cost log of the earlier steps:

- **Draft rulebook** (Opus, one session): about 3 h wall-clock and 2 h active. It needs
  decisions with ADRs: the database layer for SQLAlchemy and Alembic's head, the HTTP
  router for FastAPI, async and the workers, and errors for exceptions. Your attention:
  about 45 min for the review, plus the library decisions.
- **Gap inventory** (prompt 02): about 2 h wall-clock and 1.5 h active, with two
  reviewers. Your attention: about 30 min.
