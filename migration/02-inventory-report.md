# Step 1, prompt 02: gap inventory

Date: 2026-11-28. Prompt: `migration-kit/prompts/02-gap-inventory.md`.

## Result

```
$ wc -l migration/inventory.tsv
702 migration/inventory.tsv
```

The file holds the header and 701 rows. The rows cover all 708 sites of
`migration/inventory/sites.tsv`, because the first 20 rows cover 27 sites between them.
`migration/scripts/inventory_merge.py` builds the file from the shards in
`migration/inventory/shards/`. It dedupes on (file, symbol, source_construct), sorts by
file and line, and fails on a key with two different rows. No key had two.

| Status | Rows |
|---|---:|
| confirmed | 699 |
| unknown (still contested after the one revision) | 2 |

| Classification | Rows | Classification | Rows |
|---|---:|---|---:|
| WRITE_THROUGH | 98 | ASCII_BYTES | 19 |
| NULL_COLUMN | 61 | WRITE_VISIBLE | 17 |
| ROUTE_IMPLICIT | 58 | ROUTE_PLAIN | 16 |
| ERROR_MATCH | 47 | DATETIME_LAYOUT | 14 |
| ASSERT_PANIC | 46 | EXACT_PORT | 10 |
| BUG_CRASH | 41 | DATETIME_PARSE | 10 |
| ERROR_TYPE | 41 | SESSION_PER_CALL | 8 |
| CATCH_ALL | 38 | EXACT_LIBRARY | 8 |
| HTTP_ERROR | 35 | ORDER_IRRELEVANT | 7 |
| OK_RESULT | 32 | UNKNOWN | 5 |
| ZERO_VALUE | 30 | SHARED_OBJECT | 4 |
| DEFAULT_ARG | 28 | SESSION_LONG_LIVED | 3 |
| POINTER | 20 | ORDER_MATTERS | 3 |
| | | NOT_A_ROW, EXACT_TOLERATED | 1 each |

The rows touch 47 files. The most rows are in `app/boxes.py` (153), `app/admin.py` (78),
`app/models.py` (67) and `app/main.py` (66).

## How the rows were checked

- **Classifiers:** 27 shards, on the model plan's tiers (Opus for the protocol files,
  Sonnet elsewhere). The merge renamed four spellings that the classifiers chose to the
  guide's names (`ORM_WRITE_THROUGH` to `WRITE_THROUGH`, and three more).
- **Reviews:** two skeptic reviewers per group, in separate contexts, gave 1,362
  verdicts on the 681 shard rows. They refuted 276. The 181 rows that at least one
  reviewer refuted went back to their classifier for one revision. Both reviewers then
  re-checked only those rows: 15 of the 364 final verdicts refuted.
- **After the re-check:** a row whose content was still contested became UNKNOWN. Where
  the final refute was only a wrong line number and both versions agreed on the content,
  I checked the line in the source, fixed the citation and confirmed the row. That
  happened in six rows; `migration/inventory/reviews/` has each verdict. One edit went
  further than the reviewers asked; see "Judgement calls" below.
- **Citation drift:** the Sonnet classifiers cited lines one to four off in about half
  of their refuted rows. It is a model habit, not a rulebook defect, so no amendment is
  queued for it. Every row that a reviewer touched now cites lines that I or a reviewer
  opened.

## UNKNOWN rows

| Row | Status | Question |
|---|---|---|
| `app/boxes.py` L1122, `_handle_delete_activity` parameters | unknown | The translation (`*InboxObject` and `*Actor`, nil for `None`) isn't disputed. The final review disputes only the row's reasoning about one `is_from_db` test, which can never be false here. No question for you. |
| `app/boxes.py` L2480, `db_session.add(following)` on Accept | unknown | The original's error paths around this insert depend on SQLAlchemy's pending-rollback state, which amendment 8 proposes to reproduce. The row is right if you accept amendment 8. |
| `tasks.py` L203, `embed_version` | confirmed | Not ported, by your answer to question 1. Classified UNKNOWN only because no NONE value fits a generator's yield type. |
| `app/config.py` L32, `VERSION_COMMIT` | confirmed | Decided by your answer to question 1, and reclassified ERROR_MATCH. |
| `app/utils/highlight.py` L38, guessing a lexer | confirmed | Classified UNKNOWN by design: Chroma's `lexers.Analyse` uses other heuristics than Pygments' `guess_lexer`. The audit fixed the row (no error path: Pygments always falls back to plain text). See question 2. |

## Joint audit

Three adversarial Opus auditors read the rulebook together with the inventory, one each
for the NONE rows (171), the EXCEPT rows (248) and the rest (282). They found 30
conflicts, each with the rule, the row and the Go code that goes wrong. Two independent
Opus verifiers then ruled on every finding, with REJECTED as the default.

| Side | Found | Confirmed | Amended | Rejected |
|---|---:|---:|---:|---:|
| Inventory | 15 | 8 | 6 | 1 |
| Rulebook | 15 | 6 | 6 | 3 |

The findings and the verdicts are in `migration/inventory/audit/`.

**Inventory side, applied:** a Sonnet fixer applied the 14 confirmed and amended fixes
to the shards, and the merge rebuilt the inventory (20 lines changed). The main ones:

- `app/admin.py` L1096, L1112, L1128: an unknown `ap_object_id` in a form is bad input,
  so these are `BUG_CRASH` with a returned error, not panics.
- `app/activitypub.py` L210: without a charset, httpx decodes JSON by its byte pattern
  (a UTF-8 BOM is stripped, UTF-16 and UTF-32 are decoded). Go must do the same before
  `json.Unmarshal`, with the standard library only.
- `app/admin.py` L103: a DNS timeout from `check_url` is not an httpx timeout in the
  original, so the Go test excludes it.
- `app/actor.py` L120, L126, L132: a non-map `icon` returns an error that reaches
  `fetch_actor`'s catch-all, which logs and keeps the cached actor, as the original does.
- `app/main.py` L286, L459: query parameters parse as pydantic 1.10 does (`int()`, and
  the full boolean word lists), not as `strconv`.
- `app/boxes.py` L2205, L2607: two constructors set `is_hidden_from_stream = true`.
- `app/webmentions.py` L145: the row demanded Python's JSON bytes, against ADR 0017.
- Three signatures (`Fetch`, `FetchNextOutgoingActivity`, `OutboxObject.URL`) now agree
  with the rows that call them.

**Rulebook side, queued:** amendments 9 to 20 in `migration/inventory/amendments.md`.
I did not apply them. They join amendments 1 to 8 from the fan-out.

| # | Section | Change |
|---|---|---|
| 1 | §4 | A private name that must be exported and collides with a public one keeps a suffix chosen in its row. |
| 2 | §4 | A home for `AnyboxObject`: an interface next to the two models, returned as a nil interface for "none". |
| 3 | §2 | `session.Exec` for an update from a SQL expression (`announces_count + 1`), which write-through would write stale. |
| 4 | §2 HTTP | `web.HTTPError` gets a `Header` field, for a redirect raised from a dependency. |
| 5 | §2 | `pyLower`: Python's `str.lower`, for the federated hashtag URLs. |
| 6 | §2 | Go error types for httpx's errors, and `isHTTPError`. |
| 7 | §2 | `database.ExecNoTx` for `VACUUM`, which can't run in a transaction. |
| 8 | §2 | The session keeps the first failed write until `Rollback`, as SQLAlchemy's pending-rollback state does. |
| 9 | §2 HTTP | A 500 is written outside the app's middleware, without its headers, as Starlette's `ServerErrorMiddleware` does. |
| 10 | §2 | A panic in a worker takes the source's `except Exception` path: retry with backoff, not a permanent failure. |
| 11 | §2 HTTP | httpx timeouts are per phase. Never set `http.Client.Timeout`: it would cut off the media proxy's streams. |
| 12 | §2 | No Go `except` translation catches a context error, because Python's never catch `CancelledError`. |
| 13 | §1 | A `None` that Jinja prints is printed as `None` in Go, with `BUG(port):`. |
| 14 | §2 | A nil slice that reaches a nullable JSON column is SQL NULL, where the original stores `[]`. |
| 15 | §4 | The base class `Actor` becomes the interface `BaseActor`, as `app/models.py:26` names it. |
| 16 | §2 | Python's `\w`, `\d`, `\s` and `\b` are Unicode-aware; RE2's are ASCII. |
| 17 | §1 | `golang.org/x/text/unicode/norm` for NFKC in `slugify`, which federates in Article URLs. |
| 18 | §2 HTTP | On a route with a form, FastAPI parses the body before every dependency, the 0-RTT check included. |
| 19 | §2 HTTP | `verify_csrf_token` with a missing token defers its 422 entry to the route's validation. |
| 20 | §2 | The lexer table is keyed by lower-cased Pygments alias, generated from Pygments 2.15.1. |

Amendments 17 and 18 need your decision as well as your approval:

- **17** tolerates a difference for characters newer than Unicode 14.0 (rulebook §0 needs
  your approval and an ADR), or marks them `BUG(port)`.
- **18** and **19** refine ADR 0018: the CSRF check and the body parse are not pure
  middleware. The ADR's decision stands; the order changes.

**Rejected by the verifiers, left open in the inventory:**

- `AUDIT-NONE-8`, `app/webfinger.py` L12: Python's order of `{"http", "https"}` is random
  per process, so both orders are the original's. The row tries https first, with a
  `TODO(port)`.
- `AUDIT-NONE-11`, `tests/factories.py` L25 and four more: the rows say to use
  `github.com/google/uuid`, which §1 bans. The fix is in the rows (`crypto/rand`), not
  the rule. I left it for the stress test, because the verifier ruled only on the rule.
- `AUDIT-REST-5`, `app/utils/workers.py`: the auditor questioned ADR 0017's stale-read
  rule. Changing it would supersede the ADR, which is your decision.
- `AUDIT-EXCEPT-4`: the one `assert` in `app/` (`app/main.py:647`) has no row of its own.
  The rulebook decides it (`panic`), so the verifier found no defect.

## Judgement calls to check

- **`app/webfinger.py` L80:** both reviewers said no caller can pass an `acct:` prefix.
  They missed `app/utils/mastodon.py:23`, which passes a handle from an imported CSV
  file unchanged. I corrected the evidence myself.
- **Citation-only refutes after the re-check:** I fixed and confirmed six rows instead of
  marking them UNKNOWN (see above). Strictly, the process makes a still-refuted row
  UNKNOWN.
- **One verdict file used shard names** in its `file` column. I mapped each verdict back to
  its row by the line key within its shard. Every row with a shared key was confirmed by
  both reviewers, so the mapping can't hide a dispute.
- **ADR 0023** (store Go error text instead of a traceback) is new in this PR. It records
  your approval from the session.

## Questions for you

The owner answered questions 1 to 3 on #40.

1. **`VERSION_COMMIT` and `embed_version`:** answered. The port takes the commit from the
   VCS data that the Go toolchain embeds (`-buildvcs=auto`), its first 8 characters, as
   `git rev-parse --short=8` gives, and `dev` without VCS data, as the original's
   fallback. No `embed-version` task. Rows `app/config.py` L32 and `tasks.py` L203 say so.
2. **A code fence whose language Pygments doesn't know:** open. With no language, the
   original doesn't highlight at all (`app/utils/highlight.py`, the `else` branch). It calls
   `guess_lexer` only when `data-microblogpub-lexer` names an alias that its Pygments
   doesn't know, which only a remote instance with another Pygments version can send.
   The highlighted HTML is drawn when a page renders (`app/templates.py:321`); it doesn't
   federate. Chroma's `lexers.Analyse` can guess another lexer than Pygments there.
3. **`inv self-destruct` without a "yes":** answered. Port it as it is, `BUG(port)`, and
   fix it after parity. The row already says so.
4. **Amendments 1 to 20:** open. Apply, change or reject each. Amendment 17 also needs
   the Unicode decision.

## Next step: the stress test (prompt 03)

`.claude/settings.json` exists, so prompt 03's guardrail check passes. The stress test
picks three files by the rulebook sections and inventory rows they exercise. The top
candidates by rows are `app/boxes.py` (153 rows, 2,761 lines), `app/admin.py` (78, 1,282)
and `app/main.py` (66, 1,728).

Estimate, from those counts: two translators and a diff inspector over about 5,800
lines and 300 inventory rows.

- **Wall clock:** about 5 hours, at this step's pace of roughly 100 rows per review
  hour. A smaller third file (`app/models.py` or `app/actor.py`) would cut it by an hour.
- **Your attention:** about 45 minutes at the gate, for the diff inspector's verdicts and
  the rule changes they propose.

## Cost

`migration/cost-log.tsv`, line `1b-inventory`, from `migration/scripts/session_cost.py`:
206 active minutes, 522,628,989 tokens, 49 subagents, about $209. The phase starts at the
previous line (the rulebook draft), so it includes the rulebook's review in
[#39](https://github.com/nevkontakte/microblog.go/pull/39). The model
column names the orchestrator; the classifiers, reviewers and fixer ran on Opus or Sonnet
as the model plan sets them, each with its model passed explicitly.
