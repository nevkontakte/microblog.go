# 00b — Judge setup: federation harness (part 1 of 3)

Kit prompt: `migration-kit/prompts/00b-judge-setup.md`. Design:
[ADR 0010](../docs/adr/0010-judge-federation-with-paired-instances.md),
[ADR 0013](../docs/adr/0013-run-the-judge-as-a-program.md). This part delivers the harness and
the federation scenarios: every ActivityPub interaction the original supports, listed in
[00b-federation-coverage.md](00b-federation-coverage.md). The rest comes in separate parts,
each its own pull request:

- **2a:** the on-disk swap (Go reads a Python-written `data/`, and the reverse);
- **2b:** the public read surface (`test_public.py`, HTML pages, feeds as normalized XML);
- **2c:** the client protocols (IndieAuth, Micropub, Webmention, C2S);
- **3:** the Mastodon replay set
  ([ADR 0014](../docs/adr/0014-record-the-mastodon-replay-set-from-a-real-server.md)).

The 00b gate closes after part 3.

## Running it

```bash
cd reference/microblog.pub && poetry env use python3.11 && poetry install --no-root && cd -
```

```bash
go run ./cmd/judge -venv "$(cd reference/microblog.pub && poetry env info -p)"
```

Flags: `-run <regexp>` selects scenarios, `-update-golden` re-records the wire (Python–Python
only), `-patch <file>` applies a mutation to the judge's copy of the original, `-timeout` sets
the ceiling for every wait (default 2 min), `-parallel <n>` how many scenarios play at once
(default: the number of CPUs), and `-work <dir>` sets the scratch directory (default
`_judge/`, gitignored). CI runs the same command on every pull request
(`.github/workflows/judge.yml`). Each scenario leaves its
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
| `test_public.py` | 10 | portable | **part 2b** (read surface) |
| `test_httpsig.py` | 5 | internal-bound (`_KEY_CACHE`, `HTTPSigInfo`) | Go unit tests in the port; also covered by the wire's signature checks |
| `test_ldsig.py` | 1 | internal-bound | Go unit test; LD signatures also verified on the wire in part 3 |
| `test_process_outgoing_activities.py` | 7 | internal-bound | Go unit tests; delivery visible in every federation scenario |
| `test_actor.py` | 2 | internal-bound | Go unit tests |
| `test_admin.py` | 1 | internal-bound | Go unit test |
| `test_utils.py` | 1 | internal-bound | Go unit test |

Totals: 55 tests. 37 are portable, 27 of them ported here (26 scenarios) and 10 left for part 2b.
18 are internal-bound. The feasibility report counted 38 and 17: on a closer read,
`test_emoji_are_loaded` asserts on a Python global, so it moves to internal-bound.

The original suite covers only part of what federates. The other 67 scenarios come from an
inventory of every ActivityPub interaction in the original's code
([00b-federation-coverage.md](00b-federation-coverage.md)), each mapped to the scenario that
covers it:

| File | Scenarios | What |
|---|---|---|
| `scenarios_follow.go` | 4 | following between the instances: automatic, unfollow, manual approval and rejection |
| `scenarios_inbox.go` | 1 | beyond its ports: an unknown actor deleting itself, dropped before its key is fetched |
| `scenarios_federation.go` | 18 | posting, replies, likes and boosts (also of objects the instance doesn't have, and of a third party's note), the reply form for an unknown object, deletes, polls, blocks (also following or messaging a peer that blocked me), bookmarks, pins, hiding boosts, remote follow and interaction |
| `scenarios_lookup.go` | 14 | lookup of every kind of query (handle, actor, Note, Article, Question, wrapped object, HTML alternate) and every failure (unknown handle, missing, gone and private objects, unreachable host) |
| `scenarios_inbox_edge.go` | 19 | the rest of the inbox dispatch, and what it rejects or drops (including a Digest header that doesn't match, which the original accepts) |
| `scenarios_endpoints.go` | 7 | the served documents: actor, WebFinger, collections (also hidden), object visibility by signature, deleted objects |
| `scenarios_cli.go` | 4 | moving the account, self-destructing, importing a Mastodon following list, and the Update sent after a profile change |

With the 26 ports, that makes 93 scenarios, the suite `judge.All()` runs.

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

**Clean run on the original (Python–Python):** 91/91 scenarios pass over https, recording the goldens (`-update-golden`).

**Determinism:** a second run against the fresh goldens passes 91/91 with no wire differences, while three
mutation runs shared the machine (four judges on four cores). After the numbering fix (judge bug 6
below), the goldens were re-recorded (91/91) and a rerun again passed 91/91, with a mutation run
sharing the machine. After the owner's later review rounds (error handling, single-step helpers, two
more Update scenarios), the full suite passes 93/93. CI runs the whole suite on every pull request.

**Mutations** (`internal/judge/testdata/mutations/`, each applied to the judge's copy of the
original, full suite run each time):

| # | Mutation | Caught by |
|---|---|---|
| 1 | Unlisted post addressed as public | `post__visibility`, `ap__collections` |
| 2 | Outgoing requests without a Digest | 48 scenarios: every one with a delivery |
| 3 | Inbox accepts unsigned requests | `inbox_requires_httpsig`, `inbox__incoming_delete_for_unknown_actor`, `inbox__bad_digest`, `inbox__wrong_key` |
| 4 | Undo Like "fixes" the counter (no longer -1) | `like__and_undo`, `inbox__blocked_actor_undo` (the `BUG(port)` assertions) |
| 5 | Like notified as `ANNOUNCE` | `like__and_undo` |
| 6 | `Hashtag` sent as `Tag` | `tags__note_with_tag` |
| 7 | Signature computed over the wrong method | 48 scenarios: every one with a signed delivery |
| 8 | Followers-only objects served to any signed request | `lookup__private_object`, `ap__object_acl` |
| 9 | Activities from a blocked actor processed | `inbox__actor_is_blocked`, `follow__peer_blocked_me`, `post__direct_to_peer_blocked_me`, `inbox__blocked_actor_undo` |
| 10 | A note updated by someone other than its author | `inbox__update_note_wrong_actor` |

10 of 10 caught, in a re-run on the final judge after the owner's review rounds (four judges
sharing four cores): every mutation failed exactly the scenarios listed. Mutations 8 to 10 were
added for the new coverage. The runs before the poll fix
(judge bug 5 below) also failed `poll__vote_any_of`, for an unrelated reason; it isn't listed.
Mutation 7 makes every delivery fail, so each scenario waits out its ceiling; it ran with
`-timeout 90s`.

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
5. Running the mutation batches in parallel (a loaded machine, like CI) made `poll__vote_any_of`
   time out twice. The cause is a race in the original: a vote for two answers makes b send
   two Updates of the poll, whose ids are made of the time to the second, and whose bodies are
   built when sent, not when queued (`app/boxes.py:839-866, 2098-2099`). Depending on timing,
   the first Update counts one answer or both, and a second Update in the same second is
   dropped by a as a duplicate. The scenario now leaves those two deliveries out of the wire
   comparison (`Scenario.Unstable`, the only use) and requires only that a applied an Update;
   the behaviour is marked `BUG(port)`.
6. CI, on its first runs, failed `ap__object_acl` on a renumbered id. Ids were numbered by first
   appearance in the order exchanges completed, and an id an instance makes up (a post's
   conversation context) first showed either in a delivery or in a later fetch, depending on how
   fast the outgoing worker ran. Ids are now numbered in an order that doesn't depend on timing:
   the judge's own requests first, then the other exchanges sorted by content with run-specific
   values masked alike. Every golden was re-recorded.
7. A CI run lost a whole tail of scenarios. The judge chose a free port, closed it, and told
   uvicorn to bind it; in between, an outgoing connection took it as its source port, and the
   instance never started. The judge now binds the socket itself and hands it to uvicorn
   (`--fd`), and a failed setup stops what it started.
8. Another CI run recorded one delivery twice. The original gives up on a receiver after httpx's
   default 5 s (`app/activitypub.py:397`) and sends again; on a loaded runner the receiver's
   answer came later than that. The wire comparison now counts an identical re-sent delivery
   once.
9. A CI run failed `poll__vote_any_of` on an incoming activity that hit `database is locked`.
   That's a race in the original: the incoming worker reads, then writes in the same
   transaction, and SQLite refuses the upgrade at once, without waiting out the 15 s timeout,
   while the web process is writing (`app/incoming_activities.py:110-133`). The worker tried
   again 2 s later and succeeded, but the judge counted every activity that ever had an error,
   and didn't wait for the retry. A lost lock race now counts as pending until it's retried,
   and not as a failure. The port needn't reproduce the race.

## 4. Reviewer findings

Adversarial reviewers, run as separate agents on `claude-opus-5-5` per the model plan, two per
round, each on a disjoint batch. They assumed the ports were wrong, read only, and had to cite a line in
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

**Batch 3** (the scenarios added for coverage: federation, lookup, command line, and the
inventory): 2 blocking, 7 should-fix, 5 nits. All accepted.

| # | Finding | Fix |
|---|---|---|
| 1 | The lookup page answers 200 whether the lookup worked or not, so "200 and a was fetched" passed when parsing failed. | A successful object lookup must show the object fetch followed by the author fetch, which the original makes only once the object parsed (`app/ap_object.py:332-335`). |
| 2 | Requests were matched by prefix, and `https://b.test/` is a prefix of the WebFinger URL. | Exact URLs everywhere (`sawExchange`, `sawInOrder`). |
| 3 | `remote_interaction` couldn't see its own `BUG(port)`: the fixed URL contains the buggy one. | Exact `Refresh` header for both remote scenarios. |
| 4 | `self_destruct`: the follower branch of the actor Delete never ran. | a and b follow each other; b's Follow of a must end up deleted. |
| 5 | A JSON column compared byte for byte; `votersCount` unchecked. | `json()` comparison; `votersCount` counts voters, and the poll's Update must reach the voter. |
| 6, 7 | `announce__third_party_note` and `move__to_other_account` asserted less than their comments said. | Announce linked to the fetched Note and shown in the stream; the Move's Undo, MOVE notification and profile Update checked. |
| 8–11 | An unordered check, guard checks unlabelled, restart dropping scenario config, bookmark not proven unknown first. | Fixed; restart now adds to the config. |
| 12 | Missing from the inventory: `import-mastodon-following-accounts`, liking an unknown object, the reply form fetching its target, the poll Update, hide/show boosts fetching an unknown actor. | Four scenarios added; the poll Update joined the vote scenarios. |
| 13, 14 | Inventory rows over-claiming; pinning listed as outgoing. | Rows corrected after the fixes; pinning moved to the served endpoints. |

**Batch 4** (inbox edge cases, served endpoints, harness changes, and a timing audit of every
scenario): 2 blocking, 8 should-fix, 5 nits. All accepted except where noted.

| # | Finding | Fix |
|---|---|---|
| 1 | The settle barrier read a, then b, then the fakes: a delivery landing between the reads could be missed. | The pair counts as quiet only after two polls in a row that find nothing pending and the same state (newest queue rows, done counts, Accepts started). |
| 2 | The profile Update after a restart was never waited for, so its delivery depended on timing. | Waited for, and for b to process it. |
| 3, 4 | Any failed incoming activity counted as done; `waitProcessed` required no error, which a successful retry doesn't clear. | `Scenario.IncomingErrors` states the failures a scenario expects; settle fails on any other. `waitProcessed` waits for processing only. |
| 5 | `self_destruct` zero-row check (same as batch 3, #4). | Fixed there. |
| 6 | A follower signing with the wrong key still gets followers-only and direct objects: the original takes the keyId's owner as the signer even when the signature is invalid (`app/httpsig.py:256-273`). | Encoded as `BUG(port)` in `ap__object_acl`. It's a security bug; the owner decided the port keeps it until parity (ADR 0009). |
| 7 | The served `publicKeyPem` was never compared with the key on disk. | Compared with `data/key.pem`. |
| 8, 9 | Unknown Undo/Accept: storage unchecked. Blocked actor's Undo: only the counter checked. | Storage, the undone Like, the notification, and a dropped non-Undo now checked. |
| 10 | One follower and one followee can't show ordering. | Two of each, newest first. |
| 11 | The Digest header is never compared to the body; only the signed digest matters. | `inbox__digest_header_ignored`: a wrong header on a correctly signed request is accepted, marked `BUG(port)` (owner review): fixed after parity. |
| 12 | Unexercised branches. | Someone else's actor payload and WebFinger by actor id added. The `save_to_inbox` blocked-server check is unreachable from outside (the signature check answers 403 first), and the token bypass of hidden collections belongs to 2c. |
| 13–15 | Read's author unchecked; restart config (as batch 3); `waitReady` ignored cancellation. | Fixed. |

**Copilot, on the stack:**

| # | Finding | Fix |
|---|---|---|
| 1 | Proxy: a stalled tunneled request could block `Close`; tunnels could register after it; Host wasn't recorded; HEAD responses got a body; a comment claimed arrival order. | All fixed; the comment now says completion order, which ids no longer depend on (judge bug 6). |
| 2 | Only the modulus of the served key was compared with `data/key.pem`. | The whole public key is compared. |
| 3 | A relative `-venv` broke, since the CSS compiler runs in another directory. | The path is made absolute. |
| 4 | Poll scenarios checked how many answers were counted, not which. | The counted answers must be exactly the ones voted for. |
| 5 | Id numbering depended on the order in which a serializer writes JSON members, which the port may change. | Bodies are put in canonical form (sorted members) before ids are numbered; every golden was re-recorded. |
| 6 | LD signatures are masked, not verified. | Deferred to part 3, which judges the LD-signed traffic (forwarded activities); the owner left the call to us. Between two instances of the original, an LD signature can only be wrong if the port computes it wrong, and part 1 has no port yet. |

**Owner review** (on the first pull request, which this stack replaces):

| # | Finding | Fix |
|---|---|---|
| 1 | Too large to review. | Split into a stack of pull requests, one per layer or scenario group. |
| 2 | The judge should run on every pull request. | `.github/workflows/judge.yml` runs `go vet` and the judge; a pull request is ready for review only once it passes (ADR 0013). |
| 3 | Coverage: every ActivityPub interaction, lookups of each object type, and error cases. | The coverage inventory and 67 scenarios beyond the ported suite (section 1). |
| 4 | No scenario may assume that work completes in a fixed time. | Audit below. |
| 5 | Part 2 should be three parts. | 2a, 2b, 2c (top of this report). |
| 6 | Proxy fields, `from` and the plain http path were undocumented; JSON-LD code sat in `tls.go`; `Listen` could register a server after `Close`. | Documented; `jsonld.go`; `Listen` refuses once closing. |
| 7 | Helpers were spread over scenario files and `run.go`. | Every helper lives in `steps.go`, grouped by kind: waits, assertions, admin actions, fake-actor steps. Scenario files hold only scenarios. |
| 8 | `T.run` hand-rolled a goroutine join. | `sync.WaitGroup.Go`. |
| 9 | Assertion failures printed both values whole. | `equal` prints a `go-cmp` diff. The wire comparison stays a multiset diff of normalized lines, since its order is not meaningful. |
| 10 | Errors were ignored throughout the judge. | Every error is handled. Those only a judge bug can cause panic (`must`). The proxy collects errors from its goroutines, including a handler's panic, and the scenario fails; a sender hanging up is the one quiet case. Stopping an instance reports a process that exited by itself. |
| 11 | The poll helper bundled following, posting and checking, with fixed roles. | `postQuestion`, `expectQuestion` and `expectDelivered` take the instances as arguments; the vote and lookup scenarios reuse `postQuestion`. Voting itself is `poll__vote` and `poll__vote_any_of`. |
| 12 | Other helpers also folded several actions, or fixed which instance plays which role. | Every helper is now one action or one check, taking the instances as arguments: `remoteNote` and `followedNote` are spelled out in their scenarios, `pendingFollow` became `pendingFollower`, `pollCounted` became `expectVotesCounted` and `expectPollUpdated`, `lookupObject` became `expectLookedUp`, and `remoteRedirect` takes both instances. |
| 13 | Receiving edits must be judged. | `inbox__update_note` also checks `updated_at`; `inbox__update_unknown_note` and `inbox__update_unsupported_type` cover the two branches of the Update handler no scenario reached. |
| 14 | The ignored Digest header is a bug. | Marked `BUG(port)` in `inbox__digest_header_ignored`, to fix after parity. The wrong-key ACL bug (Copilot finding 6) is kept until parity too, by the owner's decision. |
| 15 | Field-by-field checks of a document stop at the first difference. | `ap__actor_document` and `ap__collections` compare the fields they check in one `go-cmp` diff. |
| 16 | Scenario directories must be deleted at the end and never shared. | Each scenario's directory is named after it and emptied before it starts; it is now deleted once the scenario passes (a failing one's is kept for debugging), and two scenarios with the same name stop the run. Only a restart reuses an instance's directory, within its scenario. |

The timing audit went through every wait in the judge:

- **Waits on time.** None remain. Timeouts are ceilings for a stuck run, all set by one
  `-timeout` (default 2 min; CI uses 5), including instance start-up and HTTP requests, which
  had fixed 30 s and 60 s bounds.
- **Checks that something did not happen.** Three waited for a row count to reach zero when
  it already was zero, so they passed before the event they meant to observe:
  `follow__manually_rejected` (twice) and `pendingFollow`. They now wait until the activity is
  processed, then check once.
- **Reads after a wait on a related row.** `inbox_incoming_follow_request` read the outbox and
  the delivery queue once the follower row appeared. They are written in the same transaction,
  but the scenario now waits for the activity to be processed, which doesn't depend on that.
- **Stopping the pair.** Before stopping, the judge waits until every delivery is sent and
  every incoming activity tried (Copilot finding 1). A delivery that fails and waits for a
  retry now keeps the pair from settling, rather than being left off the recording.
- **Proxy tunnels** bound their route when opened, so after a restart a reused connection
  reached the stopped process. Routes are now looked up per request.


## Not covered yet

- 2a, 2b, 2c and 3, listed at the top. The coverage inventory marks which interactions wait
  for 2c (Webmention delivery, C2S) and 3 (`hs2019`, LD-signed forwarded activities).

## Part 2a: the on-disk swap

`scenarios_swap.go` judges the swap test: an instance stops, and the peer's implementation
starts on its `data/` with no migration step (`t.swap`). On the Python pair a swap is a
restart, so these scenarios pin what a swap must keep; with the port on one side of a
Go–Python or Python–Go pairing, they are the swap test in each direction.

| Scenario | Checks after the swap |
|---|---|
| `swap__served_state` | WebFinger, actor (key included), collections, a post, its activity and its attachment are served unchanged; the schema is still the original's last migration |
| `swap__federates` | New posts are delivered, signed with the kept key; a like of an old post is counted |
| `swap__peer` | The swapped receiver still follows its peer and takes its deliveries |
| `swap__custom_emoji` | An emoji in `data/custom_emoji` is served |

The schema check reads `alembic_version`, which is what `alembic current` reports
(`a209f0333f5a`), so it holds for any implementation that ran on the data.

**Clean run:** 4/4 pass, recording the goldens, and 4/4 again against them.

**Mutations,** run on the swap scenarios:

| # | Mutation | Caught by |
|---|---|---|
| 11 | The web server replaces `data/key.pem` whenever it starts | `swap__served_state`, `swap__federates`, `swap__peer` |
| 12 | Uploads go to a temporary directory and are lost on stop | `swap__served_state` |
| 13 | `data/custom_emoji` isn't loaded | `swap__custom_emoji` |
| 14 | A new migration moves the schema past the original's last one | `swap__served_state`, `swap__peer` |

4 of 4 caught.

## Part 2b: the public read surface

`scenarios_public.go` judges what anonymous visitors (browsers, feed readers, servers fetching
without a signature) are served, and what they must never be. A third proxy listener,
`visitor`, carries an anonymous client, so its exchanges are on the wire like the rest. Its
response bodies are compared as `asJSON` keeps them: JSON as it is, feeds as XML trees
(attribute order and whitespace ignored, RFC 822 dates masked), HTML by its microformats (each
`h-entry`, `h-card` and `h-feed` with its own `u-url`), since templates may differ.

| Scenario | Judges |
|---|---|
| `public__visibility` | One post of each kind (public, unlisted, followers-only, direct, article, poll, reply, pinned, pinned then deleted), and every surface that lists or serves posts: home page, outbox, featured, followers, following, tag pages, NodeInfo, WebFinger, the JSON, RSS and Atom feeds, `robots.txt`, and each post as ActivityPub, as its activity and as a page. No response holds the id or text of a followers-only, direct or deleted post |
| `public__not_found` | Unknown objects, articles, tags, attachments, thumbnails and emoji; WebFinger without a resource |
| `public__thumbnail` | An image's thumbnail, as WebP and as the original |
| `public__admin_requires_login` | Every admin page and action sends an anonymous visitor to the login page and changes nothing |
| `public__media_proxy` | Media proxy URLs the judge signs itself: valid, resized, forged and expired |
| `public__collection_pages` | Followers past 20 page, with a working next link |
| `public__index`, `public__collections_html` | The rest of `tests/test_public.py`: the index as HTML and under each accepted ActivityPub type, and followers and following pages, shown and hidden |

**Found in the original,** marked `BUG(port)` (kept until parity, as for every bug the
original has):

- An attachment is served to anyone who has its URL, whatever its post's visibility, and after
  the post is deleted (`app/main.py:1533-1551`). The URL holds the content's hash, so it can't
  be guessed, but deleting a post doesn't take its attachments down.
- A forged or expired media proxy URL fails with 500, not a 4xx: the signature error isn't
  handled (`app/main.py:1378-1388`).
- A thumbnail requested without an `Accept` header fails with 500 (`app/main.py:1569`).
- Also pinned by the goldens: the outbox never lists polls (`app/main.py:604`).

**Judge fixes on the way:**

- The proxy now opens a connection per request to an instance. After a 500, uvicorn closes the
  connection, and a reused one failed the next request with a 502 from the proxy (seen in
  `public__media_proxy` under load).
- Multipart boundaries are masked; the original's media proxy ignores `HTTPS_PROXY`, so proxied
  media come from b's own address, which debug mode allows.

**Clean run:** 8/8 pass, recording the goldens, and again against them; `public__media_proxy`
and `public__admin_requires_login` also passed six runs at once. The full suite, now 105
scenarios, passed twice in a row (297 s and 282 s, four scenarios at a time).

**Mutations,** run on the public scenarios:

| # | Mutation | Caught by |
|---|---|---|
| 15 | The outbox lists followers-only posts | `public__visibility` |
| 16 | The feeds list deleted posts | `public__visibility` |
| 17 | The home page lists unlisted posts | `public__visibility` |
| 18 | An admin page is served without a session | `public__admin_requires_login` |
| 19 | The media proxy accepts expired URLs | `public__media_proxy` |
| 20 | Tag pages list followers-only posts | `public__visibility` |

6 of 6 caught.

## Part 2c: client protocols — IndieAuth

`scenarios_indieauth.go` judges the authorization server (`app/indieauth.py`) as a third-party
app uses it. The app is the `visitor`, so its exchanges are on the wire; tokens, client secrets
and their timestamps are masked. The admin's consent is the one step a browser takes, through
the judge's logged-in client.

| Scenario | Judges |
|---|---|
| `indieauth__discovery` | The authorization server metadata |
| `indieauth__authorization_code` | Registration (RFC 7591), consent with state and issuer, code redemption with PKCE, introspection, a token unlocking the outbox and a followers-only post, refresh, and revocation |
| `indieauth__rejected_codes` | A code reused, redeemed for another redirect URI or by another client, unknown, older than two minutes, or granting no scope: each `invalid_grant` |
| `indieauth__token_expiry` | An access token older than an hour is inactive and unlocks nothing |
| `indieauth__introspection_auth` | Introspection answers only the token's own client, and only with its secret |
| `indieauth__client_url` | An unregistered app named by its URL: the consent page fetches its `h-app` |
| `indieauth__hidden_collections` | A token sees the followers and following an instance hides |

**Found in the original,** marked `BUG(port)`:

- **Security:** the PKCE verifier is never checked against the challenge
  (`app/indieauth.py:191-227` takes it and ignores it), so an intercepted code is redeemable
  without it. Kept until parity per ADR 0013; worth fixing first after it.
- A refresh token used twice raises `ValueError`: a 500, not `invalid_grant`
  (`app/indieauth.py:318-319`). So does an unknown grant type.
- Also pinned, without a marker since it's a TODO rather than a bug: a token's scopes are never
  checked; any token unlocks every private object (`app/main.py:719`).

**Clean run:** 7/7 pass, recording the goldens, and again against them.

**Mutations,** run on the IndieAuth scenarios:

| # | Mutation | Caught by |
|---|---|---|
| 21 | Access tokens never expire | `indieauth__token_expiry` |
| 22 | Revoked access tokens stay valid | `indieauth__authorization_code` |
| 23 | A code can be redeemed more than once | `indieauth__rejected_codes` |
| 24 | A code is redeemed for any redirect URI | `indieauth__rejected_codes` |

4 of 4 caught.
