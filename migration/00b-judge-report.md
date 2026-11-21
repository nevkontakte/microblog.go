# 00b — Judge setup: federation harness (part 1 of 3)

Kit prompt: `migration-kit/prompts/00b-judge-setup.md`. Design:
[ADR 0010](../docs/adr/0010-judge-federation-with-paired-instances.md),
[ADR 0013](../docs/adr/0013-run-the-judge-as-a-program.md). This part delivers the harness and
the federation scenarios. They cover every ActivityPub interaction that the original supports,
listed in [00b-federation-coverage.md](00b-federation-coverage.md). The rest comes in separate
parts, each its own pull request:

- **2a:** the on-disk swap (Go reads a `data/` that Python wrote, and the reverse).
- **2b:** the public read surface (`test_public.py`, HTML pages, feeds as normalized XML).
- **2c:** the client protocols (IndieAuth, Micropub, Webmention, C2S).
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

Flags:

- `-run <regexp>` selects scenarios.
- `-update-golden` records the wire again (Python–Python only).
- `-patch <file>` applies a mutation to the judge's copy of the original.
- `-timeout` sets the ceiling for every wait (default 2 min).
- `-parallel <n>` sets how many scenarios play at once (default: the number of CPUs).
- `-work <dir>` sets the scratch directory (default `_judge/`, gitignored).

CI runs the same command on every pull request (`.github/workflows/judge.yml`). Each scenario
leaves its instances' logs in `<work>/py-py/<scenario>/`. It also leaves `exchanges.json` (all
traffic) and `wire.jsonl` (the normalized comparison) there.

## 1. Categorize (receipt)

`grep -cE "^(async )?def test_"` per file in `reference/microblog.pub/tests`. "Portable" means
that HTTP, the database or the wire shows the behavior, so the judge can judge both
implementations on it.

| File | Tests | Class | Where it goes |
|---|---|---|---|
| `test_inbox.py` | 9 | portable | `scenarios_inbox.go`, 9 scenarios |
| `test_remote_actor_deletion.py` | 2 | portable | `scenarios_inbox.go`, 2 scenarios |
| `test_outbox.py` | 11 | portable | `scenarios_outbox.go`, 11 scenarios |
| `test_emoji.py` | 4 | 3 portable, 1 internal (`test_emoji_are_loaded` reads a Python global) | `scenarios_outbox.go`, 2 scenarios (`emoji_ap_endpoint` covers both endpoint tests) |
| `test_tags.py` | 2 | portable | `scenarios_outbox.go`, 2 scenarios |
| `test_public.py` | 10 | portable | **part 2b** (read surface) |
| `test_httpsig.py` | 5 | internal-bound (`_KEY_CACHE`, `HTTPSigInfo`) | Go unit tests in the port. The wire's signature checks also cover them |
| `test_ldsig.py` | 1 | internal-bound | Go unit test. Part 3 also verifies LD signatures on the wire |
| `test_process_outgoing_activities.py` | 7 | internal-bound | Go unit tests. Every federation scenario shows delivery |
| `test_actor.py` | 2 | internal-bound | Go unit tests |
| `test_admin.py` | 1 | internal-bound | Go unit test |
| `test_utils.py` | 1 | internal-bound | Go unit test |

Totals: 55 tests.

- 37 are portable: 27 of them ported here (26 scenarios), and 10 left for part 2b.
- 18 are internal-bound.

The feasibility report counted 38 and 17. On a closer read, `test_emoji_are_loaded` asserts
on a Python global, so it moves to internal-bound.

The original suite covers only part of what federates. The other 67 scenarios come from an
inventory of every ActivityPub interaction in the original's code
([00b-federation-coverage.md](00b-federation-coverage.md)). The inventory maps each
interaction to the scenario that covers it:

| File | Scenarios | What |
|---|---|---|
| `scenarios_follow.go` | 4 | following between the instances: automatic, unfollow, manual approval and rejection |
| `scenarios_inbox.go` | 1 | beyond its ports: an unknown actor that deletes itself, dropped before the instance fetches its key |
| `scenarios_federation.go` | 18 | Posting and replies. Likes and boosts, also of objects the instance doesn't have and of a third party's note. The reply form for an unknown object. Deletes, polls, bookmarks, pins, hiding boosts, remote follow and interaction. Blocks, also following or messaging a peer that blocked me |
| `scenarios_lookup.go` | 14 | Lookup of every kind of query: handle, actor, Note, Article, Question, wrapped object, HTML alternate. Every failure: unknown handle, missing, gone and private objects, unreachable host |
| `scenarios_inbox_edge.go` | 19 | the rest of the inbox dispatch, and what it rejects or drops (including a Digest header that doesn't match, which the original accepts) |
| `scenarios_endpoints.go` | 7 | the served documents: actor, WebFinger, collections (also hidden), object visibility by signature, deleted objects |
| `scenarios_cli.go` | 4 | moving the account, self-destructing, importing a Mastodon following list, and the Update an instance sends after a profile change |

With the 26 ports, that makes 93 scenarios. `judge.All()` runs that suite.

## 2. Rewrite for portability

- **Setup:** the original tests seeded state directly in the database. The judge reaches that
  state through real interactions. A remote actor is either the other real instance, or a
  signed fake actor (`fake.go`) shaped like `tests/factories.py`.
- **Assertions** use public surfaces only: ActivityPub responses, admin endpoints, the
  SQLite database (its format is part of the contract, ADR 0003), and the wire.
- **Wire:** the judge normalizes every exchange between instances, and verifies its signature
  and digest with the sender's key. Each scenario's recording must equal the Python–Python
  golden recording (`internal/judge/testdata/golden/`).

**Adversarial review:** see section 4.

## 3. Validation

**Clean run on the original (Python–Python):** 91/91 scenarios pass over https, and record
the goldens (`-update-golden`).

**Determinism:**

1. A second run against the fresh goldens passes 91/91 with no wire differences. Three mutation
   runs shared the machine meanwhile (four judges on four cores).
2. After the numbering fix (judge bug 6 below), I recorded the goldens again (91/91). A new
   run passed 91/91 again, with a mutation run on the same machine.
3. After the owner's later review rounds (error handling, single-step helpers, two more Update
   scenarios), the full suite passes 93/93. CI runs the whole suite on every pull request.

**Mutations** (`internal/judge/testdata/mutations/`). The judge applies each one to its copy of
the original and runs the full suite:

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

The judge caught 10 of 10, in a new run on the final judge after the owner's review rounds
(four judges shared four cores). Every mutation failed exactly the scenarios listed.

- Mutations 8 to 10 came with the new coverage.
- The runs before the poll fix (judge bug 5 below) also failed `poll__vote_any_of`, for an
  unrelated reason. The table doesn't list it.
- Mutation 7 makes every delivery fail, so each scenario waits until its ceiling. It ran with
  `-timeout 90s`.

**Judge bugs found and fixed during validation** (the kit's lesson: debug the referee before
you believe it):

1. `has_local_mention`: I asserted that the original sets it on a mention. It never does. The
   only reference outside the model is a read in `app/prune.py:84`. The scenario now asserts
   the `MENTION` notification.
2. Undo Like leaves `likes_count` at -1 on the liked post. That's a real bug in the original
   (`app/boxes.py:1639-1645`). The judge encodes it, marked `BUG(port)`, per the bug-for-bug
   rule.
3. Wire flakiness, found by recording twice:
   - The judge compared empty recordings as one blank line.
   - It didn't mask article short ids (`/articles/<7 hex>/`).
   - One scenario raced a delivery against an actor deletion.
   - Fake-actor ids depended on which scenarios ran.

   I fixed all four.
4. LD signatures fetch JSON-LD contexts over https (`app/ldsig.py`). The proxy now serves
   captured copies, so runs are offline and byte-stable. At the owner's request, the
   instances themselves now also run over https (`https = true`), as in production. They sit
   behind the proxy's TLS termination, with a per-run CA (ADR 0013).
5. Mutation batches in parallel (a loaded machine, like CI) made `poll__vote_any_of` time out
   twice. The cause is a race in the original (`app/boxes.py:839-866, 2098-2099`):
   - A vote for two answers makes b send two Updates of the poll.
   - Their ids come from the time to the second.
   - b builds their bodies when it sends them, not when it queues them.
   - So the first Update counts one answer or both, depending on timing. If a second Update
     comes in the same second, a drops it as a duplicate.

   The scenario now leaves those two deliveries out of the wire comparison
   (`Scenario.Unstable`, its only use). It only requires that a applied an Update. I marked
   the behavior `BUG(port)`.
6. CI, on its first runs, failed `ap__object_acl` on an id with a new number. The judge
   numbered ids by first appearance, in the order the exchanges completed. An id that an
   instance makes up (a post's conversation context) first showed in a delivery or in a later
   fetch. Which one depended on how fast the outgoing worker ran. The judge now numbers ids in
   an order that doesn't depend on timing. Its own requests come first. The other exchanges
   follow, sorted by content, with run-specific values masked alike. I recorded every golden
   again.
7. A CI run lost a whole tail of scenarios. The judge chose a free port, closed it, and told
   uvicorn to bind it. In between, an outgoing connection took the port as its source port,
   and the instance never started. The judge now binds the socket itself and hands it to
   uvicorn (`--fd`). A failed setup now stops what it started.
8. Another CI run recorded one delivery twice. The original gives up on a receiver after
   httpx's default 5 s (`app/activitypub.py:397`) and sends again. On a loaded runner, the
   receiver's answer came later than that. The wire comparison now counts an identical
   delivery that the sender sent again only once.
9. A CI run failed `poll__vote_any_of` on an incoming activity that hit `database is locked`.
   That's a race in the original (`app/incoming_activities.py:110-133`):
   - The incoming worker reads, then writes in the same transaction.
   - While the web process writes, SQLite refuses that upgrade at once, without waiting
     for the 15 s timeout.
   - The worker tried again 2 s later and succeeded.

   But the judge counted every activity that ever had an error, and didn't wait for the
   retry. A lost lock race now counts as pending until the worker tries again, not as a
   failure. The port needn't reproduce the race.

## 4. Reviewer findings

Adversarial reviewers ran as separate agents on `claude-opus-5-5`, per the model plan. Each
round had two of them, each on a separate batch. They assumed that the ports were wrong, only
read, and had to cite a line in the original or the port for every finding. I accepted and
fixed all findings, except where noted.

**Batch 1** (`test_inbox.py`, `test_remote_actor_deletion.py`): 2 blocking, 1 should-fix, 2 nits.

| # | Finding | Fix |
|---|---|---|
| 1 | `incoming_delete_for_unknown_actor` took a different path. The original drops a self-Delete from an unknown actor before the key fetch (`app/httpsig.py:210-222`), so the "actor gone" branch (`:249-251`, `:294-296`) never ran. | The scenario now deletes a note from a gone (410) actor, which reaches that branch. The early-drop path keeps its own scenario, `incoming_self_delete_for_unknown_actor`. |
| 2 | `actor_is_blocked` slept 3 s and then asserted that nothing happened. That passes if processing never ran. | It now waits until the instance processed the Create without error (`incoming_activity.is_processed`), then checks once. |
| 3 | Zero-row checks tried again until the count reached zero. | Where something must *not* happen, the scenario waits for processing to finish and checks once (`expectRows`). Zero-row waits remain only where rows must disappear. |
| 4 | `move_activity` filtered inbox rows by type. The original required that the Move be the only inbox row. | It now asserts the exact set {Accept (from the setup follow), Move}. |
| 5 | `actor_is_blocked` blocks through the admin action, which also removes the follower. | A comment explains why this doesn't affect the drop under test (`app/boxes.py:2328-2330`). |

**Batch 2** (`test_outbox.py`, `test_emoji.py`, `test_tags.py`, and the wire normalization):
2 blocking, 6 should-fix, 4 nits, 1 process note.

| # | Finding | Fix |
|---|---|---|
| 1 | `send_delete__reverts_side_effects` dropped the original's bogus counter, so a decrement passed as a recount. | The scenario seeds `replies_count = 5` and still expects 1. It uses a new `Exec` helper, for state that the original seeded and no interaction can produce. |
| 2 | "Exactly one outbox row" became "one row that matches a filter". | The scenario now checks the whole outbox: exactly the new object, or {Accept, object} where it sets up a follow. |
| 3 | The scenario didn't pin the attachment URL, and didn't check the attachment count. | It now asserts `/attachments/<sha256>/<filename>`, which existing posts link to (`app/models.py:245-251`). It also asserts exactly one attachment. |
| 4 | The CW-only post checked the JSON attachment, not the database rows. | It now also checks the `upload` and `outbox_object_attachment` rows. |
| 5, 6 | Polls: no check that the other poll type is absent, and a fixed duration. | Both added. The other type must be absent. The scenarios use the original's durations (5 and 10), and `endTime − published` must equal the duration. |
| 7 | The port dropped `test_emoji_are_loaded` silently. | The scenario now documents it: the endpoint's 200 covers it. |
| 8 | **Wire:** the judge masked every id to the same `<id>`, so a reference to the wrong object passed. | The judge now numbers ids by first appearance: `<id1>`, `<id2>`, and so on. |
| 9 | **Wire:** timestamp masking hid the format. | Timestamps keep their shape (digits become 9), so a missing zone or a different precision is a difference. |
| 10 | **Wire:** the judge didn't compare the response content type. | The judge now records it (`respType`). |
| 11 | **Wire:** the judge didn't check the keyId and the Date header. | The keyId is now part of the signature record. Date must parse as an HTTP date in GMT. |
| 12 | **Wire:** the multiset comparison ignored delivery order. | The judge now records each sender's POSTs to each receiver in order. |
| 13 | The golden files changed during the review. | That was the stopped recording. I recorded them again after these fixes. |

**PR review** (Copilot, on the pull request): 4 findings, all accepted.

| # | Finding | Fix |
|---|---|---|
| 1 | The judge stopped the instances as soon as a scenario's checks passed, before queued deliveries went out. `inbox__move_activity`'s recording lacked the Undo and the new Follow. A faster worker would have changed the wire. | Before it stops them, the judge now waits (`settle` in `run.go`). No instance may have an untried delivery or an unprocessed incoming activity, and no fake may have an Accept in flight. Failed work that waits for a retry counts as done. |
| 2 | The judge found the CSRF token with a regexp over the original's exact markup, but admin HTML may differ in the port. | The judge now finds the `<input>` by its `name` attribute, with an HTML tokenizer (`golang.org/x/net/html`). |
| 3 | The proxy didn't close hijacked tunnel connections on `Close`, so they lived on after their scenario. | The proxy tracks tunnels, closes them, and waits for them to finish. |
| 4 | The judge numbered ids by first appearance anywhere, admin HTML included. A change to an admin page could thus give the wire new numbers. | Only the URL and `Location` of the judge's own exchanges count. Bodies count only on the wire between peers. |

**Batch 3** (the scenarios added for coverage: federation, lookup, command line, and the
inventory): 2 blocking, 7 should-fix, 5 nits. All accepted.

| # | Finding | Fix |
|---|---|---|
| 1 | The lookup page answers 200 whether the lookup worked or not, so "200 and a fetched it" passed when parsing failed. | A successful object lookup must show the object fetch, then the author fetch. The original makes the second only once the object parsed (`app/ap_object.py:332-335`). |
| 2 | The judge matched requests by prefix, and `https://b.test/` is a prefix of the WebFinger URL. | Exact URLs everywhere (`sawExchange`, `sawInOrder`). |
| 3 | `remote_interaction` couldn't see its own `BUG(port)`: the fixed URL contains the buggy one. | Exact `Refresh` header for both remote scenarios. |
| 4 | `self_destruct`: the follower branch of the actor Delete never ran. | a and b follow each other. b's Follow of a must end up deleted. |
| 5 | The judge compared a JSON column byte for byte, and didn't check `votersCount`. | `json()` comparison. `votersCount` counts voters, and the poll's Update must reach the voter. |
| 6, 7 | `announce__third_party_note` and `move__to_other_account` asserted less than their comments said. | Announce linked to the fetched Note and shown in the stream. The scenario checks the Move's Undo, the MOVE notification and the profile Update. |
| 8–11 | An unordered check, unlabelled guard checks, a restart that dropped scenario config, and a bookmark not proven unknown first. | Fixed. A restart now adds to the config. |
| 12 | Missing from the inventory: `import-mastodon-following-accounts`, a like of an unknown object, and the poll Update. Also the reply form, which fetches its target, and hide/show boosts, which fetch an unknown actor. | Four scenarios added. The poll Update joined the vote scenarios. |
| 13, 14 | Inventory rows that claimed too much. Pinning listed as outgoing. | I fixed the rows after the other fixes, and moved pinning to the served endpoints. |

**Batch 4** (inbox edge cases, served endpoints, harness changes, and a timing audit of every
scenario): 2 blocking, 8 should-fix, 5 nits. All accepted except where noted.

| # | Finding | Fix |
|---|---|---|
| 1 | The settle barrier read a, then b, then the fakes. It could miss a delivery that landed between the reads. | The pair counts as quiet only after two polls in a row find nothing pending. The two polls must also find the same state: newest queue rows, done counts, Accepts started. |
| 2 | Nothing waited for the profile Update after a restart, so its delivery depended on timing. | The scenario waits for it, and for b to process it. |
| 3, 4 | Any failed incoming activity counted as done. `waitProcessed` required no error, which a successful retry doesn't clear. | `Scenario.IncomingErrors` states the failures that a scenario expects. Settle fails on any other. `waitProcessed` waits for processing only. |
| 5 | `self_destruct` zero-row check (same as batch 3, #4). | Fixed there. |
| 6 | A follower who signs with the wrong key still gets followers-only and direct objects. The original takes the keyId's owner as the signer even when the signature is invalid (`app/httpsig.py:256-273`). | Encoded as `BUG(port)` in `ap__object_acl`. It's a security bug. The owner decided that the port keeps it until parity (ADR 0009). |
| 7 | The judge never compared the served `publicKeyPem` with the key on disk. | Compared with `data/key.pem`. |
| 8, 9 | Unknown Undo/Accept: storage unchecked. Blocked actor's Undo: only the counter checked. | The scenarios now check storage, the undone Like, the notification, and a dropped non-Undo. |
| 10 | One follower and one followee can't show ordering. | Two of each, newest first. |
| 11 | The original never compares the Digest header with the body. Only the signed digest matters. | `inbox__digest_header_ignored`: the original accepts a wrong header on a correctly signed request. Marked `BUG(port)` (owner review), to fix after parity. |
| 12 | Unexercised branches. | Someone else's actor payload, and WebFinger by actor id, added. The `save_to_inbox` blocked-server check is unreachable from outside, because the signature check answers 403 first. The token bypass of hidden collections belongs to 2c. |
| 13–15 | Read's author unchecked. Restart config (as batch 3). `waitReady` ignored cancellation. | Fixed. |

**Copilot, on the stack:**

| # | Finding | Fix |
|---|---|---|
| 1 | Proxy: a stalled request in a tunnel could block `Close`. Tunnels could register after it. The proxy didn't record Host, and gave HEAD responses a body. A comment claimed arrival order. | All fixed. The comment now says completion order, which ids no longer depend on (judge bug 6). |
| 2 | The judge compared only the modulus of the served key with `data/key.pem`. | The judge compares the whole public key. |
| 3 | A relative `-venv` broke, since the CSS compiler runs in another directory. | The judge makes the path absolute. |
| 4 | Poll scenarios checked how many answers the instance counted, not which. | The counted answers must be exactly the ones voted for. |
| 5 | Id numbering depended on the order in which a serializer writes JSON members, which the port may change. | The judge puts bodies in canonical form (sorted members) before it numbers ids. I recorded every golden again. |
| 6 | The judge masks LD signatures and doesn't verify them. | Deferred to part 3, which judges the LD-signed traffic (forwarded activities). The owner left the call to us. Between two instances of the original, an LD signature can only be wrong if the port computes it wrong. Part 1 has no port yet. |

**Owner review** (on the first pull request, which this stack replaces):

| # | Finding | Fix |
|---|---|---|
| 1 | Too large to review. | Split into a stack of pull requests, one per layer or scenario group. |
| 2 | The judge should run on every pull request. | `.github/workflows/judge.yml` runs `go vet` and the judge. A pull request is ready for review only once it passes (ADR 0013). |
| 3 | Coverage: every ActivityPub interaction, lookups of each object type, and error cases. | The coverage inventory, and 67 scenarios beyond the ported suite (section 1). |
| 4 | No scenario may assume that work completes in a fixed time. | Audit below. |
| 5 | Part 2 should be three parts. | 2a, 2b, 2c (top of this report). |
| 6 | Proxy fields, `from` and the plain http path had no documentation. JSON-LD code sat in `tls.go`. `Listen` could register a server after `Close`. | Documented. `jsonld.go`. `Listen` refuses once the proxy closes. |
| 7 | Helpers were spread over scenario files and `run.go`. | Every helper lives in `steps.go`, grouped by kind: waits, assertions, admin actions, fake-actor steps. Scenario files hold only scenarios. |
| 8 | `T.run` hand-rolled a goroutine join. | `sync.WaitGroup.Go`. |
| 9 | Assertion failures printed both values whole. | `equal` prints a `go-cmp` diff. The wire comparison stays a multiset diff of normalized lines, since their order has no meaning. |
| 10 | The judge ignored errors throughout. | The judge handles every error. Errors that only a judge bug can cause panic (`must`). The proxy collects errors from its goroutines, including a handler's panic, and the scenario fails. The one quiet case is a sender that hangs up. Stopping an instance reports a process that exited by itself. |
| 11 | The poll helper bundled following, posting and checking, with fixed roles. | `postQuestion`, `expectQuestion` and `expectDelivered` take the instances as arguments. The vote and lookup scenarios use `postQuestion` too. Voting itself is `poll__vote` and `poll__vote_any_of`. |
| 12 | Other helpers also combined several actions, or fixed which instance plays which role. | Every helper is now one action or one check, and takes the instances as arguments. Their scenarios spell out `remoteNote` and `followedNote`. `pendingFollow` became `pendingFollower`. `pollCounted` became `expectVotesCounted` and `expectPollUpdated`. `lookupObject` became `expectLookedUp`. `remoteRedirect` takes both instances. |
| 13 | The judge must judge received edits. | `inbox__update_note` also checks `updated_at`. `inbox__update_unknown_note` and `inbox__update_unsupported_type` cover the two branches of the Update handler that no scenario reached. |
| 14 | The ignored Digest header is a bug. | Marked `BUG(port)` in `inbox__digest_header_ignored`, to fix after parity. The port also keeps the wrong-key ACL bug (Copilot finding 6) until parity, by the owner's decision. |
| 15 | Field-by-field checks of a document stop at the first difference. | `ap__actor_document` and `ap__collections` compare the fields they check in one `go-cmp` diff. |
| 16 | The judge must delete scenario directories at the end and never share them. | Each scenario's directory has its name, and the judge empties it before the scenario starts. The judge deletes it once the scenario passes, and keeps a failed one's for debugging. Two scenarios with the same name stop the run. Only a restart uses an instance's directory again, within its scenario. |

The timing audit went through every wait in the judge:

- **Waits on time.** None remain. Timeouts are ceilings for a stuck run, all set by one
  `-timeout` (default 2 min. CI uses 5). This includes instance start-up and HTTP requests,
  which used fixed 30 s and 60 s bounds.
- **Checks that something did not happen.** Three waited for a row count to reach zero when
  it already was zero. So they passed before the event they had to observe:
  `follow__manually_rejected` (twice) and `pendingFollow`. They now wait until the instance
  processed the activity, then check once.
- **Reads after a wait on a related row.** `inbox_incoming_follow_request` read the outbox
  and the delivery queue once the follower row appeared. The original writes them in the same
  transaction. But the scenario now waits for processing of the activity, which doesn't
  depend on that.
- **Stopping the pair.** Before it stops the pair, the judge waits until the instances sent
  every delivery and tried every incoming activity (Copilot finding 1). A delivery that fails
  and waits for a retry now keeps the pair from settling, rather than staying off the
  recording.
- **Proxy tunnels** bound their route when they opened. So after a restart, a connection in
  use again reached the stopped process. The proxy now looks up routes per request.


## Not covered yet

- Nothing. Parts 2a, 2b, 2c and 3 below cover what part 1 left out, and no row of the
  coverage inventory waits for a later part.

## Part 2a: the on-disk swap

`scenarios_swap.go` judges the swap test. An instance stops, and the peer's implementation
starts on its `data/` with no migration step (`t.swap`).

- On the Python pair, a swap is a restart. So these scenarios pin what a swap must keep.
- With the port on one side of a Go–Python or Python–Go pair, they are the swap test in
  each direction.

| Scenario | Checks after the swap |
|---|---|
| `swap__served_state` | The instance serves WebFinger, the actor (key included), collections, a post, its activity and its attachment, all unchanged. The schema is still the original's last migration |
| `swap__federates` | The instance delivers new posts, signed with the kept key. It counts a like of an old post |
| `swap__peer` | The swapped receiver still follows its peer and takes its deliveries |
| `swap__custom_emoji` | The instance serves an emoji in `data/custom_emoji` |

The schema check reads `alembic_version`, which is what `alembic current` reports
(`a209f0333f5a`). So it holds for any implementation that ran on the data.

**Clean run:** 4/4 pass, recording the goldens, and 4/4 again against them.

**Mutations,** run on the swap scenarios:

| # | Mutation | Caught by |
|---|---|---|
| 11 | The web server replaces `data/key.pem` whenever it starts | `swap__served_state`, `swap__federates`, `swap__peer` |
| 12 | Uploads go to a temporary directory, and the instance loses them on stop | `swap__served_state` |
| 13 | The instance doesn't load `data/custom_emoji` | `swap__custom_emoji` |
| 14 | A new migration moves the schema past the original's last one | `swap__served_state`, `swap__peer` |

4 of 4 caught.

## Part 2b: the public read surface

`scenarios_public.go` judges what the instance serves to anonymous visitors: browsers, feed
readers, and servers that fetch without a signature. It also judges what the instance must
never serve them.

- A third proxy listener, `visitor`, carries an anonymous client. Its exchanges are on the
  wire like the rest.
- The judge compares its response bodies as `asJSON` keeps them. JSON stays as it is. Feeds
  become XML trees (attribute order and whitespace ignored, RFC 822 dates masked). HTML
  becomes its microformats: each `h-entry`, `h-card` and `h-feed` with its own `u-url`. This
  is because templates may differ.

| Scenario | Judges |
|---|---|
| `public__visibility` | One post of each kind: public, unlisted, followers-only, direct, article, poll, reply, pinned, and pinned then deleted. Every surface that lists or serves posts: home page, outbox, featured, followers, following, tag pages, NodeInfo, WebFinger, the JSON, RSS and Atom feeds, `robots.txt`. Each post as ActivityPub, as its activity and as a page. No response holds the id or text of a followers-only, direct or deleted post |
| `public__not_found` | Unknown objects, articles, tags, attachments, thumbnails and emoji. WebFinger without a resource |
| `public__thumbnail` | An image's thumbnail, as WebP and as the original |
| `public__admin_requires_login` | Every admin page and action sends an anonymous visitor to the login page and changes nothing |
| `public__media_proxy` | Media proxy URLs that the judge signs itself: valid, resized, forged and expired |
| `public__collection_pages` | Followers past 20 page, with a working next link |
| `public__index`, `public__collections_html` | The rest of `tests/test_public.py`: the index as HTML and under each accepted ActivityPub type, and followers and following pages, shown and hidden |

**Found in the original,** marked `BUG(port)`. The port keeps them until parity, as it keeps
every bug of the original.

- The original serves an attachment to anyone who has its URL, whatever its post's
  visibility, and after its author deletes the post (`app/main.py:1533-1551`). The URL holds the
  content's hash, so nobody can guess it. But the delete of a post doesn't take its
  attachments down.
- A forged or expired media proxy URL fails with 500, not a 4xx: the original doesn't handle
  the signature error (`app/main.py:1378-1388`).
- A thumbnail request without an `Accept` header fails with 500 (`app/main.py:1569`).
- The goldens also pin this: the outbox never lists polls (`app/main.py:604`).

**Judge fixes on the way:**

- The proxy now opens a connection per request to an instance. After a 500, uvicorn closes
  the connection, and a connection in use again failed the next request with a 502 from the
  proxy. This showed in `public__media_proxy` under load.
- The judge masks multipart boundaries.
- The original's media proxy ignores `HTTPS_PROXY`. So proxied media come from b's own
  address, which debug mode allows.

**Clean run:** 8/8 pass, recording the goldens, and again against them.
`public__media_proxy` and `public__admin_requires_login` also passed six runs at once. The
full suite, now 105 scenarios, passed twice in a row (297 s and 282 s, four scenarios at a
time).

**Mutations,** run on the public scenarios:

| # | Mutation | Caught by |
|---|---|---|
| 15 | The outbox lists followers-only posts | `public__visibility` |
| 16 | The feeds list deleted posts | `public__visibility` |
| 17 | The home page lists unlisted posts | `public__visibility` |
| 18 | The instance serves an admin page without a session | `public__admin_requires_login` |
| 19 | The media proxy accepts expired URLs | `public__media_proxy` |
| 20 | Tag pages list followers-only posts | `public__visibility` |

6 of 6 caught.

## Part 2c: client protocols — IndieAuth

`scenarios_indieauth.go` judges the authorization server (`app/indieauth.py`) as a
third-party app uses it.

- The app is the `visitor`, so its exchanges are on the wire.
- The judge masks tokens, client secrets and their timestamps.
- The admin's consent is the one step that a browser takes. The judge's logged-in client
  takes it.

| Scenario | Judges |
|---|---|
| `indieauth__discovery` | The authorization server metadata |
| `indieauth__authorization_code` | Registration (RFC 7591), consent with state and issuer, code redemption with PKCE, and introspection. A token that unlocks the outbox and a followers-only post. Refresh and revocation |
| `indieauth__rejected_codes` | A code that a client reuses, or that is unknown, older than two minutes, or grants no scope. A code redeemed for another redirect URI or by another client. Each gets `invalid_grant` |
| `indieauth__token_expiry` | An access token older than an hour is inactive and unlocks nothing |
| `indieauth__introspection_auth` | Introspection answers only the token's own client, and only with its secret |
| `indieauth__client_url` | An unregistered app named by its URL: the consent page fetches its `h-app` |
| `indieauth__hidden_collections` | A token sees the followers and following that an instance hides |

**Found in the original,** marked `BUG(port)`:

- **Security:** the original never checks the PKCE verifier against the challenge.
  `app/indieauth.py:191-227` takes it and ignores it, so anyone can redeem an intercepted
  code without it. Kept until parity per ADR 0013. Worth fixing first after parity.
- A refresh token used twice raises `ValueError`: a 500, not `invalid_grant`
  (`app/indieauth.py:318-319`). An unknown grant type does the same.
- The goldens also pin this, without a marker, since it's a TODO and not a bug: the original
  never checks a token's scopes. Any token unlocks every private object (`app/main.py:719`).

**Clean run:** 7/7 pass, recording the goldens, and again against them.

**Mutations,** run on the IndieAuth scenarios:

| # | Mutation | Caught by |
|---|---|---|
| 21 | Access tokens never expire | `indieauth__token_expiry` |
| 22 | Revoked access tokens stay valid | `indieauth__authorization_code` |
| 23 | A client can redeem a code more than once | `indieauth__rejected_codes` |
| 24 | A client can redeem a code for any redirect URI | `indieauth__rejected_codes` |

4 of 4 caught.

## Part 2c: client protocols — Micropub

`scenarios_micropub.go` judges the Micropub endpoint (`app/micropub.py`) as a client app uses
it. The app has a token from IndieAuth (`grantToken`). `indieauth__authorization_code` judges
that flow step by step, so here it is only setup.

| Scenario | Judges |
|---|---|
| `micropub__create` | A post from a form, from JSON, and with the token in the form: 201, served at its `Location`, delivered to followers |
| `micropub__query` | `q=config`, `q=source` (the Markdown source), and the source of an unknown post |
| `micropub__delete` | A deleted post is gone, and its deletion reaches followers |
| `micropub__update` | See below |
| `micropub__unauthorized` | Create, delete and update each need their scope. The endpoint refuses a request with no token or an unknown one |
| `micropub__invalid` | An `h-card`, an unsupported action, an unknown post |

**Found in the original,** marked `BUG(port)`: an update answers 200 and changes nothing
(`app/micropub.py:104-112`).

**Judge fix on the way:** the masker missed ids inside URL-encoded query values
(`q=source&url=…`). Those goldens thus differed from run to run. The full suite (118
scenarios) still passes against its goldens.

**Clean run:** 6/6 pass, recording the goldens, and again against them.

**Mutations,** run on the Micropub scenarios:

| # | Mutation | Caught by |
|---|---|---|
| 25 | Micropub creates posts without the `create` scope | `micropub__unauthorized` |
| 26 | A post deleted through Micropub isn't deleted for followers | `micropub__delete` |
| 27 | Micropub reads JSON content as a form | `micropub__create` |
| 28 | Micropub returns a post's HTML as its source | `micropub__query` |

4 of 4 caught.

## Part 2c: client protocols — Webmention

`scenarios_webmention.go` judges Webmention both ways (`app/webmentions.py`,
`app/utils/webmentions.py`).

- Incoming: the visitor sends mentions about pages that a fake serves.
- Outgoing: mentions reach a new endpoint on the fakes, `/webmention`, which records each
  mention's source and target.

| Scenario | Judges |
|---|---|
| `webmention__incoming` | A plain mention, a reply, a like and a repost: each fetched and verified, counted by kind, shown on the post's page |
| `webmention__update` | A source that stops linking withdraws its mention and its like. A new link restores both |
| `webmention__article` | An article mentioned by its page's URL |
| `webmention__invalid` | Source equal to target, an unknown target, a source that doesn't link, URLs that aren't URLs. Each gets 400, and the instance stores nothing |
| `webmention__gone_source` | See below |
| `webmention__outgoing` | A public post sends one mention to the page it links to, found from the page's `<link rel="webmention">`. A followers-only post sends none |

**Found in the original,** marked `BUG(port)`: a mention whose source is gone (404 or 410)
fails with a 500, not a withdrawal. The handler reads the fetch's result, which the 404 left
unset (`app/webmentions.py:97-104`).

**Clean run:** 6/6 pass, recording the goldens, and again against them.

**Mutations,** run on the Webmention scenarios:

| # | Mutation | Caught by |
|---|---|---|
| 29 | The instance accepts a mention whether or not its source links to the target | `webmention__update`, `webmention__invalid` |
| 30 | Followers-only posts send webmentions | `webmention__outgoing` |
| 31 | A withdrawn mention stays | `webmention__update` |
| 32 | The instance counts a like as a plain mention | `webmention__incoming`, `webmention__update` |

4 of 4 caught.

## Part 2c: client protocols — C2S posting

`scenarios_c2s.go` judges ActivityPub client-to-server posting (`POST /outbox`,
`app/main.py:635-675`) as a client app uses it, with an IndieAuth token.

| Scenario | Judges |
|---|---|
| `c2s__create` | A Create addressed publicly, unlisted, to followers, and to one actor. Each gets 201 with the object and its URL. The judge checks the visibility that each audience implies, and who receives each Create |
| `c2s__reply` | A reply with a content warning, marked sensitive, delivered as such |
| `c2s__unauthorized` | No token, or an unknown one: 401, nothing posted |
| `c2s__unsupported` | See below |
| `c2s__read_inbox` | The inbox read with a token, as a collection and its first page. Without a token, 401. Added with part 3c: the coverage inventory listed `GET /inbox`, which this part left out |

**Found in the original,** marked `BUG(port)`:

- The original posts a followers-only Create as direct (`app/main.py:651-656`).
- It ignores the client's addressing past the visibility that the addressing implies.
  Recipients come from the content's @-mentions, so a Create addressed to one actor reaches
  no one.
- Anything but a Create raises `ValueError("TODO")`, and a Create by another actor fails an
  `assert`. Both give a 500, not a 4xx (`app/main.py:646-669`).

**Clean run:** 4/4 pass, recording the goldens, and again against them. The full suite, now
128 scenarios, passes against its goldens.

**Mutations,** run on the C2S scenarios:

| # | Mutation | Caught by |
|---|---|---|
| 33 | The outbox accepts posts without an access token | `c2s__unauthorized` |
| 34 | The instance posts an unlisted Create as public | `c2s__create` |
| 35 | A posted Create loses its content warning | `c2s__reply` |

3 of 3 caught.

## Part 3a: recording the Mastodon replay set

Per [ADR 0014](../docs/adr/0014-record-the-mastodon-replay-set-from-a-real-server.md),
`cmd/record-mastodon` records what a real Mastodon sends. It runs Mastodon
(`ghcr.io/mastodon/mastodon:v4.7.6`, the latest release at the time) in Docker next to a
Python instance, both behind the judge proxy. The ADR expected the recording to need the
owner's machine. This session's environment can start a Docker daemon, so I recorded it here.

How it runs (`mastodon.go`, `mastodon_record.go`):

- Mastodon, Postgres and Redis run on the host's network. The proxy routes `m.test` to
  Mastodon's web server. Mastodon sends through its own listener (`http_proxy`) and trusts
  the judge CA (`SSL_CERT_FILE`). Mastodon needs no patch to use the proxy.
- Users and API tokens come from `mastodon_setup.rb`, since Mastodon refuses `.test` email
  domains (no mail servers).
- The script then drives Mastodon through its REST API, except for the account move. Mastodon
  offers moves and aliases only on its settings pages (`config/routes/settings.rb`), not in
  its REST API, and doesn't implement ActivityPub's client-to-server protocol. So the script
  uses `rails runner` to call the same `MoveService` that the settings page calls.
- After each step, the script waits until:
  1. Mastodon delivered something,
  2. the network was quiet for five seconds (Mastodon delivers from background jobs),
  3. a processed every delivery.

The script, in order:

1. A follow, and Mastodon's Accept of a's follow back.
2. Posts: public, unlisted, followers-only, direct, with a mention and hashtag, behind a
   content warning, with an image, and a poll.
3. On a's note: a like and its undo, a boost and its undo, and a reply.
4. A vote in a's poll.
5. An edit, a pin and an unpin, and a delete.
6. A profile update, an unfollow, and a block and its undo.
7. The Accept of a's new follow.
8. A move to another account, and that account's Accept of the follow that a sends it.
9. Account deletion.

a accepted all 30 deliveries (202).

The replay set (`internal/judge/testdata/mastodon.json`) keeps two things per step:

- Each activity that Mastodon delivered, with its content type and its HTTP signature's key
  id, algorithm and header list.
- Each document that a fetched from Mastodon meanwhile: actors, WebFinger, outboxes,
  statuses.

The set uses named placeholders (`{{a_note}}`, `{{a_follow}}`, …) instead of the ids of a's
objects. A replay fills them in with its own ids. The recorder fails if it finds an id of a's
without a name. The set holds no private key, token or personal data: only Mastodon's public
keys, which a replay replaces.

**Found on the way:**

- **Recent Mastodon can't fetch a microblog.pub actor that it hasn't seen before.**
  - When Mastodon fetches an actor or a key, it requires that the response's URL equal the
    one it asked for (`app/helpers/json_ld_helper.rb`, `fetch_resource_without_id_validation`).
  - Its HTTP client changes `https://a.test` to `https://a.test/` before it sends. For
    microblog.pub's id, a bare origin, the two URLs thus never match.
  - Mastodon's own `Request` class shows this against a local server, with or without a proxy.
  - The check first appears in v4.4.27 (built 2026-10-29). It is not in v4.4.26, v4.3.8 or
    v4.2.x. It is in the current 4.5, 4.6 and 4.7 releases.
  - It only affects first contact. A server that already knows the account finds it in
    search, and verifies its deliveries with the stored key. That is why the owner's
    instance still federates with the servers it knows.
  - Here, a fresh Mastodon could neither resolve a nor accept its signed Follow
    (`401 Unable to fetch key JSON`).
  - The recorder mounts an initializer that normalizes the id first (`mastodon_patch.rb`). It
    changes only what Mastodon accepts, never what it sends.
  - The port keeps the original's ids, so the port has the same problem. Worth a check against
    a live server that has never seen an instance, and a decision after parity.
  - [post-parity/mastodon-bare-origin.md](post-parity/mastodon-bare-origin.md) keeps the
    investigation, the Mastodon change that caused it, and the options.
- Mastodon signs with `rsa-sha256` over the headers `host date content-type digest
  (request-target)`, with a key id of the form `<actor>#rsa-<hash>` and not `#main-key`.
  It doesn't sign with `hs2019`, as ADR 0014 assumed.
  - The replay set keeps all three, and the replay signs every delivery in exactly that form.
  - Only the key, and so the signature's value, differ, since the set keeps no private key
    (ADR 0014).
  - Other servers send `hs2019`. A fake actor judges it separately (see "Part 3: `hs2019`
    signatures").
- Mastodon LD-signs every public activity that it delivers (Create, Announce, Update, Delete).
  A forwarded activity carries such a signature. Part 3c replays these activities as a third
  party forwards them, so the recording needs no third server.

## Part 3b: LD signatures in the judge

`ldsig.go` signs and verifies LD signatures (`RsaSignature2017`) as Mastodon and the original
(`app/ldsig.py`) make them. It normalizes with URDNA2015 from `github.com/piprate/json-gold`
([ADR 0015](../docs/adr/0015-use-json-gold-for-ld-signatures.md)). Contexts come from the
captured copies, never from the web. The captured set gains
`https://purl.archive.org/socialweb/webfinger`, which Mastodon 4.7's actor documents name.

It agrees with both canonicalizations that matter:

- **Mastodon's:** every LD signature in the replay set that the recording didn't edit verifies
  with the recorded actor's key (11 of 11).
  - The other three (the boost, its undo and the reply) carry a placeholder instead of an id
    of a's. So their payload no longer matches what Mastodon signed. The replay signs every
    payload again anyway.
  - I checked this with a throwaway program, since the project reserves `go test` for the
    port. The replay's
    scenarios (3c) check the signer against PyLD on every run.
- **PyLD's:** the original's own LD signatures verify on the wire (below).

The wire records now carry a verdict on each LD-signed body, `ldSignature`, like the one on
HTTP signatures. The verdict is `RsaSignature2017 <creator> ok`, `bad`, or `unverified` when the
creator's host isn't an instance. The signature value itself stays masked. This settles
reviewer finding 6 (section 4), deferred to part 3. I recorded every golden with an LD-signed
delivery again. The only change is the added verdict, `ok` in all of them.

**Mutation:**

| # | Mutation | Caught by |
|---|---|---|
| 36 | The original computes LD signatures over the two hashes in the wrong order | every scenario with an LD-signed delivery (verdict `bad`) |

## Part 3c: replaying the Mastodon set

`replay.go` puts the recorded Mastodon server back on the fake network for a scenario. It does
three things:

- It serves the documents that Mastodon served, step by step. So it never serves an actor in a
  later version than the step reached.
- It takes deliveries to its inboxes.
- It delivers what Mastodon delivered, with the scenario's ids filled in for the placeholders.

The replay signs everything again with its own key. The actors it serves carry that key
instead of Mastodon's.

- HTTP signatures have the recorded key id (`<actor>#rsa-<hash>`), algorithm and header list,
  in Mastodon's order. The replay sends the extra headers that Mastodon signed (its
  `collection-synchronization`) as recorded.
- LD signatures cover the delivered payload, with the recorded creator and creation time. The
  signer of part 3b makes them, and the Python instances verify them with PyLD.

| Scenario | Replays |
|---|---|
| `mastodon__follow` | Mastodon's Follow and a's accept. a's Follow and Mastodon's Accept |
| `mastodon__posts` | Posts that are public, unlisted, followers-only, direct, with mention and hashtag, behind a content warning, with an image, and a poll. a stores each with its visibility, warning and attachment, and notifies mentions |
| `mastodon__interactions` | Like, boost and their undos, and a reply, of a's note |
| `mastodon__vote` | A vote in a's poll |
| `mastodon__edit_pin_delete` | An edit, a pin and an unpin, and a delete. a processes and drops the pin and unpin. `Add` and `Remove` carry no id, so the replay waits for them by count |
| `mastodon__profile` | A profile update |
| `mastodon__unfollow_block` | Unfollow, block (Mastodon pairs it with a Reject of a's follow) and unblock |
| `mastodon__move` | A move: a follows the new account, which accepts |
| `mastodon__delete_account` | Account deletion |
| `mastodon__forwarded_reply` | A reply to a's note. a forwards it to its follower b with Mastodon's LD signature, and b checks the signature |
| `mastodon__forwarded_by_third_party` | A post that a fake forwards. a trusts it for its LD signature |
| `mastodon__forwarded_bad_signature` | The same, altered after signing. a fetches the post from Mastodon instead, and keeps Mastodon's text |

**Found in the original:** nothing new. The undone like leaves `likes_count` at -1, as
`like__and_undo` already pins. The replay pins it for Mastodon's Like too (`BUG(port)`).

**Clean run:** the 12 replay scenarios and `c2s__read_inbox` pass, recording the goldens and
twice more against them. The full suite, now 141 scenarios, passes against its goldens.

**Mutations,** run on the replay scenarios:

| # | Mutation | Caught by |
|---|---|---|
| 37 | The instance trusts a forwarded activity without a check of its LD signature | `mastodon__forwarded_bad_signature` |
| 39 | A Move unfollows the old account without a follow of the new one | `mastodon__move` |
| 40 | The instance doesn't forward replies to local posts to its followers | `mastodon__forwarded_reply` |

3 of 3 caught. Mutation 38 belongs to the `hs2019` scenario, which a fake actor plays (next
part).

## The 00b gate

With part 3, the judge covers everything that the plan at the top of this report listed:

- every ActivityPub interaction of the coverage inventory,
- the on-disk swap,
- the public read surface,
- the client protocols,
- a real Mastodon's traffic, replayed with fresh signatures.

One row of the coverage inventory waits for the next part: `hs2019` signatures, which a fake
actor judges. The judge runs 141 scenarios against the Python original. Each of mutations
1-37, 39 and 40 fails at least one. The gate closes on the owner's review of the last part.
