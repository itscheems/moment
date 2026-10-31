# Account interactions

Likes and notification history are application services shared by content
writers. Each like belongs to an authenticated active account and one post or
comment. Repeating a request to set the same state does not add another like or
notification. Removing a like preserves the earlier notification as history;
liking the content again creates a new activity.

Comments address the selected reply target and its retained participants.
Optional participants can be excluded. The direct target cannot be excluded,
and explicitly mentioned accounts are resolved independently. Each recipient
receives one notification for an action, with all applicable reasons preserved.
The selected participants are stored with the comment so later replies do not
silently restore excluded people.

Mentions are extracted from Markdown prose and structured article text. Code,
links, email addresses, titles and media descriptions are not implicit mentions.
A handle keeps its resolved account binding across account renames. Unresolved
text is not rebound on unrelated edits. Removing a mention and adding it again
is a new action. Draft saves and archive reconstruction do not publish events.

Content mutations and their notifications commit in the same SQLite
transaction. A notification storage failure rolls back the content mutation,
including its retry record. Reading history does not mark it as viewed.

Each notification has a stable identifier and a separate commit sequence.
Pagination cursors are bound to the account, view and active data generation.
The All view and the Mentions view have separate monotonic viewing positions;
viewing mentions does not consume unviewed likes. Machine consumption maintains
its own progress.

Notifications store source references rather than body snapshots. Detail reads
return the latest authorized post, article or comment. Deleted, withdrawn or
inaccessible sources retain a minimal historical event without exposing a body
or preview, and do not contribute to the visible new-notification count.

Interaction tables are introduced by a database migration. Older executables
refuse to open a newer database. Switching back to an older executable is not a
data rollback; a rollback build must retain support for the migrated schema.

HTTP exposes idempotent `PUT` and `DELETE /api/v1/posts/{id}/like` and `/comments/{id}/like`, plus `GET` on the same paths. `GET /api/v1/likes?kind=post&ids=id,id` batches up to 100 visible subjects; unreadable subjects are omitted. `GET` on the content's `/reply-context` returns participants and the required direct target. Comment creation accepts `exclude_reply_user_ids`; explicit body mentions are independent of exclusions.

The authenticated inbox is `/api/v1/account/notifications`. List queries accept `scope=all|mentions`, `limit=1..100`, optional `reason=like|reply|mention|conversation`, and either an older `cursor` or incremental `after`. The page returns `items`, `next_cursor`, `through_cursor`, `new_count` and `mentions_new_count`. Cursors belong to the account, generation, scope and reason filter. Use the returned incremental `next_cursor` until it is empty, then retain `through_cursor` as the checkpoint. A notification's `/{id}` read returns current full `post` or `comment`; unavailable sources have no excerpt, body or protected reply context. Reading never changes human progress.

`GET /state` returns counts and an account checkpoint. `POST /seen` accepts `scope` and the `through_cursor` returned by that same view. All acknowledges everything through its boundary; Mentions acknowledges only conversation and mention events. Events arriving after that boundary remain new.

`GET /stream` uses SSE with an initial `sync`, subsequent coalesced `sync` events, and 15-second heartbeats. Events carry only progress hints, never bodies. A shared observer samples durable state once per active account each second, including direct CLI writes from other processes. Clients reconnect, read state and fetch incremental history; the stream itself never acknowledges viewing. Browser cookies or bearer headers authenticate streams; query parameters are rejected. Revocation, suspension, maintenance, bundle replacement and server shutdown close live streams. Stream write deadlines are isolated from ordinary HTTP request timeouts.

CLI supports `post|comment like|unlike|like-status|reply-context <id>` and `notification list|get|state|seen`. Direct operations select a real account with `--as`; API mode uses the token holder. List accepts `--scope`, `--reason`, `--cursor`, `--after`, and `--limit`. Seen requires the returned boundary in `--through`. Comment create/reply accepts repeatable `--exclude-reply-user`. Every command emits one JSON document with `--json`; machine consumption does not clear unread counts.
