# 00b — Judge setup: federation harness (part 1 of 3)

Kit prompt: `migration-kit/prompts/00b-judge-setup.md`. Design:
[ADR 0010](../docs/adr/0010-judge-federation-with-paired-instances.md),
[ADR 0013](../docs/adr/0013-run-the-judge-as-a-program.md). This part delivers the harness and
the federation, inbox and outbox scenarios. Part 2 adds the on-disk swap, the read surface
and the client protocols. Part 3 adds the Mastodon replay set
([ADR 0014](../docs/adr/0014-record-the-mastodon-replay-set-from-a-real-server.md)). The 00b
gate closes after part 3.

## Running it

```bash
cd reference/microblog.pub && poetry env use python3.11 && poetry install --no-root && cd -
```

```bash
go run ./cmd/judge -venv "$(cd reference/microblog.pub && poetry env info -p)"
```

Flags: `-run <regexp>` selects scenarios, `-update-golden` re-records the wire (Python–Python
only), `-patch <file>` applies a mutation to the judge's copy of the original, and `-work <dir>`
sets the scratch directory (default `_judge/`, gitignored). Each scenario leaves its
instances' logs, `exchanges.json` (all traffic) and `wire.jsonl` (the normalized comparison)
in `<work>/py-py/<scenario>/`.

## 1. Categorize (receipt)

`grep -cE "^(async )?def test_"` per file in `reference/microblog.pub/tests`. "Portable" means
the behavior is observable through HTTP, the database or the wire, so both implementations can
be judged on it.

| File | Tests | Class | Where it goes |
|---|---|---|---|
| `test_inbox.py` | 9 | portable | `scenarios_inbox.go`, 9 scenarios |
| `test_remote_actor_deletion.py` | 2 | portable | `scenarios_inbox.go`, 2 scenarios |
| `test_outbox.py` | 11 | portable | `scenarios_outbox.go`, 11 scenarios |
| `test_emoji.py` | 4 | 3 portable, 1 internal (`test_emoji_are_loaded` reads a Python global) | `scenarios_outbox.go`, 2 scenarios (`emoji_ap_endpoint` covers both endpoint tests) |
| `test_tags.py` | 2 | portable | `scenarios_outbox.go`, 2 scenarios |
| `test_public.py` | 10 | portable | **part 2** (read surface) |
| `test_httpsig.py` | 5 | internal-bound (`_KEY_CACHE`, `HTTPSigInfo`) | Go unit tests in the port; also covered by the wire's signature checks |
| `test_ldsig.py` | 1 | internal-bound | Go unit test; LD signatures also verified on the wire in part 3 |
| `test_process_outgoing_activities.py` | 7 | internal-bound | Go unit tests; delivery visible in every federation scenario |
| `test_actor.py` | 2 | internal-bound | Go unit tests |
| `test_admin.py` | 1 | internal-bound | Go unit test |
| `test_utils.py` | 1 | internal-bound | Go unit test |

Totals: 55 tests. 37 are portable, 27 of them ported here (26 scenarios) and 10 left for part 2.
18 are internal-bound. The feasibility report counted 38 and 17: on a closer read,
`test_emoji_are_loaded` asserts on a Python global, so it moves to internal-bound.

Beyond the original suite, `scenarios_federation.go` adds 12 scenarios between the two real
instances: follow, unfollow, manual approval (accept and reject), the four visibility levels,
replies, like and undo, announce and undo, delete, poll vote, block and unblock, and remote
lookup.

## 2. Rewrite for portability

- **Setup:** state the original tests seeded directly in the database is reached through real
  interactions. Remote actors are either the other real instance or a signed fake actor
  (`fake.go`) shaped like `tests/factories.py`.
- **Assertions** go through public surfaces only: ActivityPub responses, admin endpoints, the
  SQLite database (its format is part of the contract, ADR 0003), and the wire.
- **Wire:** every exchange between instances is normalized, and its signature and digest are
  verified with the sender's key. Each scenario's recording must equal the Python–Python golden
  recording (`internal/judge/testdata/golden/`).

**Adversarial review:** see section 4.

## 3. Validation

**Clean run on the original (Python–Python):** 39/39 scenarios pass over https, recording the goldens (`-update-golden`).

**Determinism:** a second run against the fresh goldens passes 39/39, with no wire differences.

**Mutations** (`internal/judge/testdata/mutations/`, each applied to the judge's copy of the
original, full suite run each time):

| # | Mutation | Caught by |
|---|---|---|
| 1 | Unlisted post addressed as public | `post__visibility` |
| 2 | Outgoing requests without a Digest | 26 scenarios (every one that delivers between instances or to a fake) |
| 3 | Inbox accepts unsigned requests | `inbox_requires_httpsig`, `inbox__incoming_delete_for_unknown_actor` |
| 4 | Undo Like "fixes" the counter (no longer -1) | `like__and_undo` (the `BUG(port)` assertion) |
| 5 | Like notified as `ANNOUNCE` | `like__and_undo` |
| 6 | `Hashtag` sent as `Tag` | `tags__note_with_tag` |
| 7 | Signature computed over the wrong method | 26 scenarios |

7 of 7 caught.

**Judge bugs found and fixed during validation** (the kit's lesson: debug the referee before
believing it):

1. `has_local_mention`: I asserted the original sets it on a mention. It never does; the only
   reference outside the model is a read in `app/prune.py:84`. The scenario now asserts the
   `MENTION` notification.
2. Undo Like leaves `likes_count` at -1 on the liked post. That's a real bug in the original
   (`app/boxes.py:1639-1645`). The judge encodes it, marked `BUG(port)`, per the bug-for-bug rule.
3. Wire flakiness, found by recording twice:
   - empty recordings were compared as one blank line;
   - article short ids (`/articles/<7 hex>/`) weren't masked;
   - one scenario raced a delivery against an actor deletion;
   - fake-actor ids depended on which scenarios ran.

   All four fixed.
4. LD signatures fetch JSON-LD contexts over https (`app/ldsig.py`). The proxy now serves
   captured copies, so runs are offline and byte-stable. At the owner's request, the
   instances themselves now also run over https (`https = true`), behind the proxy's TLS
   termination with a per-run CA, as in production (ADR 0013).

## 4. Reviewer findings

Two adversarial reviewers, run as separate agents on `claude-opus-5-5` per the model plan, each
took a disjoint batch. They assumed the ports were wrong, read only, and had to cite a line in
the original or the port for every finding. All findings were accepted and fixed, except where
noted.

**Batch 1** (`test_inbox.py`, `test_remote_actor_deletion.py`): 2 blocking, 1 should-fix, 2 nits.

| # | Finding | Fix |
|---|---|---|
| 1 | `incoming_delete_for_unknown_actor` took a different path: a self-Delete from an unknown actor is dropped before the key fetch (`app/httpsig.py:210-222`), so the "actor gone" branch (`:249-251`, `:294-296`) never ran. | The scenario now deletes a note from a gone (410) actor, which reaches that branch. The early-drop path keeps its own scenario, `incoming_self_delete_for_unknown_actor`. |
| 2 | `actor_is_blocked` slept 3 s and then asserted that nothing happened. That passes if processing never ran. | It now waits until the Create is processed without error (`incoming_activity.is_processed`), then checks once. |
| 3 | Zero-row checks retried until the count reached zero. | Where something must *not* happen, the scenario waits for processing to finish and checks once (`expectRows`). Zero-row waits remain only where rows are expected to disappear. |
| 4 | `move_activity` filtered inbox rows by type, while the original required the Move to be the only inbox row. | It now asserts the exact set {Accept (from the setup follow), Move}. |
| 5 | `actor_is_blocked` blocks through the admin action, which also removes the follower. | A comment explains why the drop under test is unaffected (`app/boxes.py:2328-2330`). |

**Batch 2** (`test_outbox.py`, `test_emoji.py`, `test_tags.py`, and the wire normalization): 2
blocking, 6 should-fix, 4 nits, 1 process note.

| # | Finding | Fix |
|---|---|---|
| 1 | `send_delete__reverts_side_effects` dropped the original's bogus counter, so a decrement passed as a recount. | The scenario seeds `replies_count = 5` (new `Exec` helper, for state the original seeded and no interaction can produce) and still expects 1. |
| 2 | "Exactly one outbox row" became "one row matching a filter". | The whole outbox is now checked: exactly the new object, or {Accept, object} where a follow is set up. |
| 3 | The attachment URL wasn't pinned, and the attachment count wasn't checked. | It now asserts `/attachments/<sha256>/<filename>`, which existing posts link to (`app/models.py:245-251`), and exactly one attachment. |
| 4 | The CW-only post checked the JSON attachment, not the database rows. | It now also checks the `upload` and `outbox_object_attachment` rows. |
| 5, 6 | Polls: no check that the other poll type is absent, and a fixed duration. | Both added: the other type must be absent, the original's durations (5 and 10) are used, and `endTime − published` must equal the duration. |
| 7 | `test_emoji_are_loaded` was dropped silently. | It's now documented in the scenario: the endpoint's 200 covers it. |
| 8 | **Wire:** every id masked to the same `<id>`, so a reference to the wrong object passed. | Ids are now numbered by first appearance, `<id1>`, `<id2>`, and so on. |
| 9 | **Wire:** timestamp masking hid the format. | Timestamps keep their shape (digits become 9), so a missing zone or a different precision is a difference. |
| 10 | **Wire:** the response content type wasn't compared. | Now recorded (`respType`). |
| 11 | **Wire:** the keyId and the Date header weren't checked. | The keyId is now part of the signature record, and Date must parse as an HTTP date in GMT. |
| 12 | **Wire:** the multiset comparison ignored delivery order. | Each sender's POSTs to each receiver are now recorded in order. |
| 13 | The golden files were being rewritten during the review. | That was the stopped re-record. They were regenerated after these fixes. |

**PR review** (Copilot, on the pull request): 4 findings, all accepted.

| # | Finding | Fix |
|---|---|---|
| 1 | Instances were stopped as soon as a scenario's checks passed, before queued deliveries went out: `inbox__move_activity`'s recording lacked the Undo and the new Follow. A faster worker would have changed the wire. | Before stopping, the judge now waits until neither instance has an untried delivery or an unprocessed incoming activity, and no fake has an Accept in flight (`settle` in `run.go`). Failed work waiting for a retry counts as done. |
| 2 | The CSRF token was found with a regexp over the original's exact markup, while admin HTML may differ in the port. | The `<input>` is now found by its `name` attribute with an HTML tokenizer (`golang.org/x/net/html`). |
| 3 | The proxy didn't close hijacked tunnel connections on `Close`, so they outlived their scenario. | The proxy tracks tunnels, closes them and waits for them to finish. |
| 4 | Ids were numbered by first appearance anywhere, admin HTML included, so a change to an admin page could renumber the wire. | Only the URL and `Location` of the judge's own exchanges count; bodies count only on the wire between peers. |


## Not covered yet (parts 2 and 3)

- The on-disk swap (Go reads a Python-written `data/` and the reverse), the public read
  surface (`test_public.py`, feeds as normalized XML), and IndieAuth/Micropub/Webmention.
- The Mastodon replay set, including `hs2019` and LD-signed forwarded activities.
- Moving an account and self-destructing, which are CLI commands in the original
  (`inv move-to`, `inv self-destruct`), and profile updates (sent when the outgoing worker
  restarts). The incoming side of Move and Delete is covered here.
