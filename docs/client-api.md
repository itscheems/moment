# Client capabilities and data portability

Moment owns content validation, rendered HTML, edit timestamps and data portability.
Clients consume `/api/v1`; cached limits are an offline fallback, not an independent
product contract. Existing authentication and CSRF requirements apply unchanged.

## Discover content limits

`GET /api/v1/capabilities` is public and returns the normal `data`/`meta` envelope:

```json
{
  "data": {
    "version": 1,
    "text_length_unit": "unicode_code_points",
    "limits": {
      "max_image_bytes": 31457280,
      "max_video_bytes": 314572800,
      "max_post_media_bytes": 52428800,
      "max_comment_media_bytes": 41943040,
      "max_post_media": 9,
      "max_comment_media": 4,
      "max_video_duration_seconds": 300,
      "max_image_dimension": 16384,
      "max_image_pixels": 80000000,
      "preview_max_edge": 1600,
      "max_post_code_points": 20000,
      "max_comment_code_points": 2000,
      "max_alt_code_points": 1000,
      "default_page_size": 20,
      "max_page_size": 100,
      "max_cursor_bytes": 512,
      "default_comment_page_size": 20,
      "default_reply_page_size": 10,
      "inline_reply_preview_count": 1,
      "accepted_image_types": ["image/jpeg", "image/png", "image/webp", "image/gif", "image/heic"],
      "accepted_video_types": ["video/mp4", "video/quicktime"],
      "max_post_link_cards": 1
    }
  }
}
```

Values come from the owning domain constants and the media store's normalized
configuration. Embedders that lower ingestion limits publish those effective
limits. Accepted MIME types describe format policy; processor availability is
still reported by upload errors. Profile limits remain available separately in
`GET /account/session`. A file's individual limit does not override the smaller
aggregate post/comment budget. Comment page sizes count roots, not flattened rows.

CLI: `moment site capabilities --data-dir ./moment-data --json`, or
`moment --url http://127.0.0.1:8080 site capabilities --json`.

## Accounts and administration

Passwords, API tokens and browser sessions belong to individual accounts. Every authenticated request resolves that account's current status and role. Roles are `admin` or `member`; status is `pending`, `active` or `suspended`. Multiple administrators are supported, and role changes, suspension and deletion must leave at least one active administrator.

- `POST /login/session` exchanges a password with `username`, or an account API `token`, for a browser session. `GET /account/session` returns the real account and CSRF token.
- `GET /account/security`, `PUT /account/password`, and `GET|POST|DELETE /account/tokens[/id]` manage only the caller's credentials. Password changes atomically revoke all of that account's sessions and API tokens.
- `/account/profile`, `/account/avatar`, and `/account/cover` edit the caller's personal profile. `/site` edits site configuration. `PUT /site/profile` selects the independent display account with `{user_id}`.
- `/accounts` and `PUT /accounts/:id/role` are administrative resources. Selection as the site display account does not grant privileges or transfer content.
- Web settings use `/settings/account`, `/settings/site`, `/settings/members`, and `/settings/backups`. Publishing uses `/compose` and editing a post uses `/m/:id/edit`.

Normal logout ends one session. API-token sign-ins retain their source credential: revoking that token also ends its derived browser sessions, while independently issued tokens remain valid. The session-provenance migration signs out existing browser sessions once because their source credentials cannot be established retrospectively; signing keys, API tokens, passwords and content are retained. Older binaries reject the upgraded schema, so back up the database before upgrading and restore that backup before a downgrade.

Sensitive browser drafts and reader caches are separated by instance and account. A changed or unresolved account cannot inherit another account's private editing state.

## Media transfers and cleanup

Media GET/HEAD routes and `POST /media` use a 30-second idle I/O budget,
renewed before each read/write, with a two-hour maximum transfer window. Upload
processing has a separate five-minute context budget. Normal JSON routes retain
the server's existing timeouts. Reverse proxies and clients must also allow the
necessary transfer duration. A stalled upload returns HTTP 408 with
`MEDIA_UPLOAD_TIMEOUT`; malformed multipart input still returns `INVALID_ARGUMENT`.

`DELETE /media/:id` removes one **unreferenced** media object and requires an active administrator, including with `?dry_run=true`. Cookie callers also need CSRF. Active accounts upload through `POST /media` and receive a reuse grant for their own account. Deduplication shares the immutable object while keeping grants separate. Reading publicly referenced media does not grant permission to reuse it; unused uploads are private to accounts that hold a grant.

The result is:

```json
{"media_id":"01ARZ3NDEKTSV4RRFFQ69G5FAV","dry_run":false,"deleted":true,"cleanup_failures":0}
```

`MEDIA_IN_USE` (409) protects references from posts, comments, profile pictures and
link cards; `MEDIA_NOT_FOUND` (404) means the record is absent, including a repeat
delete. A successful authority deletion can carry a cleanup warning if the
original file could not be removed. Deletion is serialized with ingestion,
reference writes and prune; it does not bypass the shared reference resolver.
Abandoned uploads that a client cannot clean remain eligible for `media prune`.

CLI: `moment media delete <id> --dry-run --json`, then `--yes` to delete.
Both direct and API modes return the same result and retain confirmation rules.

## Request source

Send `X-Moment-Surface: ios` on native requests, including login and public reads.
Recognized `cli`, `api`, and `ios` declarations take precedence over the cookie
heuristic because native clients can hold session cookies too. An undeclared
cookie request defaults to `web`; undeclared bearer requests default to `api`.
Neither the declaration nor a cookie proves which software made a request.
Authentication and authorization continue to be enforced independently.

## Rendered content and edit time

Posts and comments linkify explicit HTTP(S) URLs after common CJK punctuation.
Clients should render `content_html` rather than reconstruct these rules. Code
spans/blocks and explicit Markdown link labels keep their existing semantics.
A card hides a bare URL only when the URL occupies the final line alone; a URL
inside a sentence intentionally remains visible.

Post and search DTOs add nullable `edited_at` (RFC 3339 UTC strings with millisecond
precision). Actual changes
to Markdown, ordered media/alt text, location, map card, or link cards update this field.
No-op updates and repost/unrepost activity do not. `updated_at` remains the general
mutation timestamp. Null means no recorded edit: historical rows are not backfilled
from ambiguous update times. Archives, import and merge preserve `edited_at` as an
nullable field in formats v2 and v3.

## Post visibility

Every post carries `visibility`, either `"everyone"` (the default) or
`"only_me"`. `POST /posts` accepts an optional `visibility`, and
`PATCH /posts/:id` accepts it alone or alongside other fields; changing
it advances `updated_at` but never `edited_at`. Unknown values are rejected with
`INVALID_ARGUMENT`.

The server checks publication state and authorship on every read. Public posts are readable by everyone. Only their author may read private posts or unpublished articles; an administrator receives no override. Hidden content is absent from other accounts' timelines, search and archive counts, and quoted targets become unavailable. Media is public only through a public content or active-profile reference; private responses use private caching.

Any active account may publish a post or article. Authors edit, withdraw and delete their own content. Administrators may delete another account's public post and manage site pins, but cannot rewrite another author's text or inspect their drafts. Comment author badges compare the comment account ID with the current post author ID. Public account and comment projections do not expose administrative roles.

CLI content writes require an explicit local actor, for example `moment --as writer post create --data-dir ./moment-data --content 'Hello'`. API mode uses the verified account credential and rejects `--as`. Direct reads may select an actor with `--as`; without one they use public visibility. `MOMENT_TOKEN` or `--token-file` selects the API account on reads as well as writes.

## Quoting comments

`POST /posts` accepts either `quoted_post_id` or `quoted_comment_id`, never both. Both are immutable create-only canonical ULIDs. A quote may be published with an empty body. CLI direct and API mode expose `post create --quote-comment <comment-id>`; `--quote` continues to quote a post.

An available comment target uses the existing `quoted_post` projection with `id` set to the comment ID and `comment_post_id` set to its containing post ID. Link to `/m/<comment_post_id>/comments/<id>`. Registered targets carry their own `author`; historical guest targets instead carry `comment_author_name`. Comment quotes contain a Moment body, media and optional public location, with no article or map card. Every read checks the containing post's visibility. Deleted or unreadable targets become the usual `unavailable: true` tombstone without target identity, body, media or containing-post metadata.

## Timeline paging

`GET /api/v1/posts` reads one page at a time, newest first. `author_user_id` filters by an immutable account ID without widening visibility; the CLI exposes the same filter as `post list --author-id`. The page's `meta`
carries two opaque cursors: `next_cursor` continues *older* from the page's
last item and `prev_cursor` continues *newer* from its first. Either goes back
as `cursor=`; a cursor knows its own direction. Each is `null` once the read
that produced the page proved that direction exhausted, and a page proves only
the direction it read — a newer page's `next_cursor` is set whenever the page
has items. Cursors issued before this contract carry no direction and continue
older.

`anchor=YYYY-MM-DD` names a site-calendar day to read back from: the items are
what appeared on or before it and `prev_cursor` continues into what appeared
after it. The anchor is a position on the timeline's own axis — the instant a
row appeared, which for a repost is the repost — not a publication window, so
`anchor` cannot be combined with `from`, `to` or `cursor` (`INVALID_ARGUMENT`).

`from` and `to` are unchanged: a half-open window on publication in the site
timezone, and a cursor minted under a window is valid only with that window.

## Exports

`GET /backups/export` returns a complete `moment-export-v4` backup for an active administrator.
`GET /account/export` returns the authenticated account's
`moment-account-export-v2` personal archive. Pending accounts can export their own
data; unauthenticated or revoked/suspended sessions cannot. Neither route accepts
a body or query parameters, and personal export cannot select another account.

Personal archives contain:

- `data/account.json`: the selected profile, role and status;
- `data/identities.jsonl`: that account's provider bindings and profile metadata,
  without passwords, bearer tokens, OAuth secrets or sessions;
- `data/comments.jsonl`: comments attributed to that account;
- `data/entries.jsonl`: posts and articles authored by the selected account;
- `data/media.jsonl` and `media/<sha256>.<extension>`: only originals reachable from
  the selected profile and content;
- `README.txt`, `manifest.json`, and `checksums.sha256` covering every other member.

Referenced posts, parents, replies and quoted IDs do not pull somebody else's
content into a personal export. This is a portability document, not an importable
site snapshot. The existing backup inspector rejects its distinct format.

Both HTTP downloads are prepared into a private temporary file before returning
200, then delivered with `Content-Length`, `X-Archive-Format`, and
`X-Archive-SHA256`. Preparation is bounded to five minutes and memory remains
bounded while temporary disk use scales with archive size. Success, failure and
request cancellation clean up the temporary file. Preparation failure returns a
JSON error without publishing a partial ZIP. Clients can use the declared length
for progress and still verify checksums after downloading.

Each request captures a fresh snapshot. `Accept-Ranges: none` makes explicit that
resume across separate requests is not supported; a Range request receives a full
200 response, never bytes spliced from different snapshots.

CLI: `moment user export <username> --data-dir ./moment-data --output ./mine.zip --json`
exports a selected account under local filesystem authority. API mode uses
`moment --url http://127.0.0.1:8080 user export --output ./mine.zip --json`, with no
username: it exports the credential holder. Existing destinations require
`--overwrite`, and incomplete or invalid files are not published.

## Structured article reading

Article detail reads carry the validated document in `article.body`. Web readers
render that document using the same Tiptap content extensions as the article
editor. `content_html` remains a derived representation for API consumers.

An article with inline pictures also carries `article.body_media`: a deduplicated
array of `{media_id, mime_type, width, height}`. These dimensions reserve space
before each picture loads. The document owns picture order and descriptions;
this read projection is neither stored document attributes nor the `media`
attachment grid. List and quoted article cards omit it alongside the full body.
The same projection is returned by HTTP and CLI detail reads. Clients accept
its absence from older servers, and must never infer body media from a cover.


## Author article management

An active authenticated account may manage its own articles and drafts. Article
management uses `/api/v1/articles`; retired identity-based routes are absent. An administrator has
no override for another account's draft, private article, edit, withdrawal or
deletion. Actor identity comes from the verified credential, never a request
body author field. Ordinary posts use the same active-account and author rules.

- `GET /articles/drafts` returns body-free summaries with a cached plain-text
  `excerpt`, ordered by last save. Draft details also include the same excerpt.
  `GET /articles/drafts/:id` returns the selected document. Summaries and details
  include `author_user_id`, `source_state` (`draft`, `working_copy`, `withdrawn`)
  and `source_visibility`. Empty document roots serialize `content` as `[]`.
- `POST /articles/drafts` creates a draft, including an untitled empty draft.
  `Idempotency-Key` retries one create without duplicating it. Each author has
  a limit of 50 drafts including working copies; other authors have independent
  limits. Replacing a draft retains the existing optimistic revision contract.
- `GET /articles` returns the current author's original published article cards,
  without bodies or repost projections, with the existing opaque cursor metadata.
  `GET /articles/:id` is an author-only management detail read. Public reading
  continues through `/api/v1/posts/:id`.
- `POST /articles/:id/draft` creates or reuses a working copy; saving that copy
  does not change the public version. `POST /articles/drafts/:id/publish` publishes
  or replaces the canonical article.
- `POST /articles/:id/unpublish` atomically removes public visibility and returns
  the existing working copy or a new draft. Existing unpublished edits take
  precedence over the public body. A quota failure leaves the publication visible.
  Repeating withdrawal returns the same draft. Republish keeps the article ID,
  original publication time, comments and link. Withdrawn content is absent from
  public detail, lists, search, archive, quotes and comment threads; exclusive
  media is also private.
- `DELETE /articles/:id` removes the article, linked draft and comments. Discarding
  a published working copy keeps the public article; discarding a withdrawn
  article's draft also removes the retained article and comments.

Post DTOs carry their actual public `author` identity; private visibility is
scoped to this author rather than an administrator role. Backups and personal
account exports preserve authorship and publication state. Legacy backups
without authorship use their original site account; omitted publication state
means published.

The equivalent CLI commands are `article drafts`, `article draft`, `article new`,
`article save`, `article list`, `article get`, `article edit`, `article publish`,
`article unpublish` and `article delete`. New draft creation accepts
`--idempotency-key`. Withdrawal and deletion support `--dry-run` and require
confirmation (`--yes` in noninteractive use). Direct mode attributes content
to the original local account; API mode resolves the supplied account credential.
Unattributed recovery authority cannot publish or manage another author's work.


## Published map cards

Ordinary post create/PATCH accepts `map_card`, and post/detail/list/search/available quoted-post DTOs return the same nullable snapshot:

```json
{"places":[{"name":"Ginza","display_name":"Chuo, Tokyo, Japan","latitude":35.6712345,"longitude":139.7654321,"coordinate_system":"wgs84"}],"connect_points":false}
```

A map contains 1–20 ordered places. Each place requires a name, display name and complete coordinates in `wgs84` or `gcj02`. Source coordinates are preserved to seven decimal places; conversion belongs to the renderer. Provider identifiers are excluded. `connect_points` requires at least two places when true. Unknown or duplicated map fields, invalid coordinates, missing coordinates and an empty places list return `INVALID_ARGUMENT`. On PATCH, omission preserves the map, JSON null clears it, and an object replaces it. A map may be the only content of a post; clearing the last substantive content returns `CONTENT_EMPTY`. Map changes update `edited_at` and participate in create idempotency. Existing author and visibility rules apply, including quote tombstones. Historical `location` remains a label-only public projection; adding maps does not reveal its stored coordinates.

Article documents add the `mapCard` block node in schema version 3. Its attributes are `{map_card: <same snapshot>, id?: <node ID>}`. Schemas 1 and 2 remain readable, but cannot contain map nodes. Map nodes are substantive content and their labels enter plain-text summaries and search. Draft map changes remain unpublished until the draft is published. Rendering uses the stored snapshot and performs no provider lookups.

Location search accepts `GET /locations/search?q=...&scope=global|auto`. The default `auto` retains existing regional provider selection and optional coordinate bias. `global` queries all configured Geoapify/AMap providers concurrently without device bias, interleaves worldwide/China results in stable order, and keeps successful results when another provider is unavailable. Lookup still requires an active account. Nearby lookup is unchanged.

CLI: `moment --as writer post create --data-dir ./moment-data --map-json ./map.json --json`; `moment --as writer post update <post-id> --data-dir ./moment-data --clear-map --json`; `moment location search --query Tokyo --scope global --json`. Map JSON accepts a file or `-` for stdin; map/media/location/link JSON cannot share stdin. Post and location commands support direct and API mode.

Full-site archives export format 4 and read formats 2, 3 and 4. Maps survive replacement import and selective merge, including article bodies and drafts. Format 2 cannot carry map cards or schema-3 article bodies; such payloads are refused instead of silently losing content. Format 1 remains unsupported. The separate account-export format also carries the map snapshot.

## Comment conversations

`GET /posts/:post_id/comments/:comment_id` reads a comment without scanning preceding pages. Its data contains `comment`, nullable `reply_target` (the full safe projection of the immediate target), `root_comment_id`, nullable `pinned_comment_id`, and `direct_reply_count` (the total number of replies addressing this comment, independent of the loaded page). The post visibility rule applies to every context read. Missing or cross-post comments return `COMMENT_NOT_FOUND`; an unreadable post returns `POST_NOT_FOUND`.

`GET /posts/:post_id/comments/:comment_id/replies` accepts `scope=thread|direct`, `sort=oldest|newest`, `cursor`, and `limit`. Omitted scope retains the existing whole-floor `thread` behavior and requires a root comment. `direct` accepts any comment and lists only replies explicitly addressing it; a root also includes historical replies with no saved target. Reply pages default to oldest first, default to 10, and cap at 100. Newest-first pages reverse both the creation-time order and the ID tie-breaker. Cursors are bound to the requested sort; changing sort starts a new first page. Direct cursors cannot be used for a different comment or in thread scope. `reply_count` describes the whole floor on roots and the direct-answer total on replies, including answers outside the loaded page. `replies_next_cursor` remains exclusive to root whole-floor pagination.

Root-list reads accept `sort=oldest|newest` (default `oldest`) and default to 20 roots with one inline reply preview per root. Inline and pinned reply previews use the same direction. Pinning remains separate from the chronological stream. The CLI provides `comment get <post-id> <comment-id>` and `comment replies <post-id> <comment-id> --scope direct`, in both direct and API modes. Both collection commands accept `--sort oldest|newest` in direct and API modes. Existing calls without `--scope` retain whole-floor semantics.
