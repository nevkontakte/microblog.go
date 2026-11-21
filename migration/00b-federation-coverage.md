# 00b — Federation coverage inventory

Every ActivityPub interaction the original supports, and the judge scenario that covers it. Built
from the code, not the test suite:

- the inbox dispatch in `save_to_inbox` (`app/boxes.py:2282-2541`) and its handlers,
- the senders (`send_*` in `app/boxes.py`, `app/outgoing_activities.py:58-97`),
- the admin actions (`app/admin.py:870-1260`) and lookup (`app/lookup.py`, `app/admin.py:91-158`),
- the public ActivityPub endpoints (`app/main.py`),
- the CLI tasks that federate (`tasks.py`: `move-to`, `self-destruct`,
  `import-mastodon-following-accounts`).

Status: **covered** (by the scenarios named), **2c** (client protocols, part 2c), **3**
(Mastodon replay set, part 3), or **n/a** (not reachable, with the reason).

## Outgoing: what an instance sends

| Interaction | Trigger | Scenario | Status |
|---|---|---|---|
| Follow an instance / a fake | admin `follow` | `follow`, `send_follow_request`, `inbox_accept_follow_request` | covered |
| Follow, manually approved: accepted / rejected | admin `follow` | `follow__manually_approved`, `follow__manually_rejected` | covered |
| Follow a peer that blocked me | admin `follow` | `follow__peer_blocked_me` | covered |
| Undo Follow, Like, Announce | admin `undo` | `unfollow`, `like__and_undo`, `announce__and_undo` | covered |
| Block, Undo Block | admin `block`, `unblock` | `block__and_unblock` | covered |
| Like a remote note. Like a note that a doesn't have (a fetches it first) | admin `like` | `like__and_undo`, `like__unknown_object` | covered |
| Announce a remote note, and a note by a third actor | admin `announce` | `announce__and_undo`, `announce__third_party_note` | covered |
| Create: four visibilities, reply, mention, attachment, CW, polls, article, emoji, hashtag | admin `new` | `post__*`, `send_create_activity__*`, `emoji_*`, `tags_*` | covered |
| Direct message to a peer that blocked me | admin `new` | `post__direct_to_peer_blocked_me` | covered |
| Delete a note, with replies recounted | admin `delete` | `delete__note`, `send_delete__reverts_side_effects` | covered |
| Vote: oneOf, anyOf. The poll's author sends an Update back to the voters | admin `vote` | `poll__vote`, `poll__vote_any_of` | covered |
| Accept / Reject an incoming follow | admin `accept_incoming_follow`, `reject_incoming_follow` | `follow__manually_*` | covered |
| Bookmark an unknown object (fetches it) | admin `bookmark` | `bookmark__unknown_object` | covered |
| Reply form for an object a doesn't have (fetched and stored) | admin `GET /admin/new?in_reply_to=` | `reply__unknown_object` | covered |
| Update of the local actor after a profile change | outgoing worker start (`app/outgoing_activities.py:38-97`) | `actor_update__profile_change` | covered |
| Move to another account | `inv move-to` | `move__to_other_account` | covered |
| Delete of the local actor | `inv self-destruct` | `self_destruct` | covered |
| Follow every account of a Mastodon export | `inv import-mastodon-following-accounts` | `import_following` | covered |
| Remote follow / remote interaction (webfinger subscribe template) | public `POST /remote_follow`, `/remote_interaction` | `remote_follow`, `remote_interaction` | covered |
| `send_update` of an object | none: `app/boxes.py:825` has no caller | — | n/a |
| Webmention delivery (an `outgoing_activity` with a target) | admin `new` with links | `webmention__outgoing` | covered |
| C2S `POST /outbox`, `GET /inbox` | IndieAuth token | `c2s__*`, `c2s__read_inbox` | covered |
| Hide / show an actor's boosts (fetches an unknown actor) | admin `hide_announces`, `show_announces` | `announces__hide_and_show` | covered |
| Force delete, pin / unpin | admin actions | `pin__featured` (served below) | n/a: local state, the original sends nothing |

## Incoming: what an instance accepts (`save_to_inbox`)

| Interaction | Scenario | Status |
|---|---|---|
| Unsigned request, signature over a wrong digest, Digest header not matching the body (accepted), signature by the wrong key | `inbox_requires_httpsig`, `inbox__bad_digest`, `inbox__digest_header_ignored`, `inbox__wrong_key` | covered |
| Actor sent as the payload (python-federation). Someone else's actor (ignored) | `inbox__actor_payload` | covered |
| Unknown actor that is gone, unknown actor deleting itself | `inbox__incoming_delete_for_unknown_actor`, `inbox__incoming_self_delete_for_unknown_actor` | covered |
| Server on `blocked_servers` (403 at the signature check. The second check in `save_to_inbox` is unreachable from outside) | `inbox__blocked_server` | covered |
| Transient object without an id (`Add`, `Remove`) | `inbox__transient_object` | covered |
| Blocked actor: dropped, except its Undo | `inbox__actor_is_blocked`, `inbox__blocked_actor_undo` | covered |
| Duplicate activity | `inbox__duplicate_activity` | covered |
| Create from a follower, already deleted, from a stranger mentioning me | `inbox__create_*`, `send_create_activity__no_followers_and_with_mention` | covered |
| Create whose object has another actor | `inbox__create_actor_mismatch` | covered |
| Reply to my note, poll vote | `post__reply`, `poll__vote` | covered |
| Read (Honk: fetch the object) | `inbox__read_activity` | covered |
| Update of an actor, of a note, of a note by someone else, of an unknown note, of an unsupported type | `inbox__update_actor`, `inbox__update_note`, `inbox__update_note_wrong_actor`, `inbox__update_unknown_note`, `inbox__update_unsupported_type` | covered |
| Move | `inbox__move_activity`, `move__to_other_account` | covered |
| Delete of a note, of a known actor | `delete__note`, `inbox__incoming_delete_for_known_actor` | covered |
| Follow: automatic, manual | `inbox_incoming_follow_request*` | covered |
| Undo of Follow, Like, Announce, Block. Undo of an unknown activity | `unfollow`, `like__and_undo`, `announce__and_undo`, `block__and_unblock`, `inbox__undo_unknown` | covered |
| Accept / Reject of my Follow. Accept of an unknown activity | `follow__*`, `inbox__accept_unknown` | covered |
| EmojiReact (unsupported: kept if it targets my object, dropped otherwise) | `inbox__emoji_react` | covered |
| Like, Announce of my note. Announce of a note I don't have (I fetch it) | `like__and_undo`, `announce__and_undo`, `announce__third_party_note` | covered |
| View (PeerTube, dropped) | `inbox__view` | covered |
| Block | `inbox__block_activity`, `block__and_unblock` | covered |
| Forwarded activity with an LD signature: valid, and failing (the receiver fetches it from its origin instead). A reply to a local post, which the instance forwards to its followers | `mastodon__forwarded_by_third_party`, `mastodon__forwarded_bad_signature`, `mastodon__forwarded_reply` | covered |
| `hs2019` signatures | `inbox__hs2019_signature` | covered |
| Everything a real Mastodon (v4.7.6) delivers. Relationships: follow, accept, unfollow, block, unblock. Posts: each visibility, mention, content warning, attachment, poll, edit, pin, unpin, delete. Interactions: like, boost, their undos, reply, vote. Account: profile update, move, deletion | `mastodon__*` | covered |

## Lookup (`/admin/lookup`)

| Query | Scenario | Status |
|---|---|---|
| `@user@host` | `lookup__remote_actor` | covered |
| Actor URL | `lookup__actor_url` | covered |
| Note, Article, Question URL | `lookup__note`, `lookup__article`, `lookup__question` | covered |
| Object served wrapped in a Create | `lookup__object_wrapped_in_create` | covered |
| HTML page with an `application/activity+json` alternate link | `lookup__html_alternate` | covered |
| Known actor / known object (redirects to the admin page) | `lookup__known_actor`, `lookup__known_object` | covered |
| Unknown handle (webfinger 404), missing object (404), gone object (410) | `lookup__unknown_handle`, `lookup__missing_object`, `lookup__gone_object` | covered |
| Followers-only object fetched by a non-follower | `lookup__private_object` | covered |
| Unreachable host | `lookup__unreachable_host` | covered |

## Served ActivityPub endpoints

| Endpoint | Scenario | Status |
|---|---|---|
| Actor document, incl. `alsoKnownAs`, `movedTo` | every scenario (on the wire), `ap__actor_document`, `move__to_other_account` | covered |
| WebFinger by account and by actor id, unknown resource | every scenario, `ap__webfinger` | covered |
| `outbox` empty, with items | `outbox__no_activities`, `ap__collections` | covered |
| `followers`, `following` (newest first), `hides_followers` / `hides_following` | `ap__collections`, `ap__hidden_collections`, `indieauth__hidden_collections` (the token bypass) | covered |
| `featured`, after pinning and unpinning | `pin__featured` | covered |
| Object ACL: followers-only and direct, signed, unsigned, and signed by the wrong key (served: `BUG(port)`). Deleted object | `ap__object_acl`, `ap__deleted_object` | covered |
| `/o/<id>/activity` | `ap__object_activity` | covered |
| `/t/<tag>`, `/e/<name>` | `tags__*`, `emoji_ap_endpoint` | covered |
| NodeInfo | every scenario (readiness probe) | covered |
