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
      "inline_reply_preview_count": 3,
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

## Media transfers and cleanup

Media GET/HEAD routes and `POST /owner/media` use a 30-second idle I/O budget,
renewed before each read/write, with a two-hour maximum transfer window. Upload
processing has a separate five-minute context budget. Normal JSON routes retain
the server's existing timeouts. Reverse proxies and clients must also allow the
necessary transfer duration. A stalled upload returns HTTP 408 with
`MEDIA_UPLOAD_TIMEOUT`; malformed multipart input still returns `INVALID_ARGUMENT`.

`DELETE /owner/media/:id` removes one **unreferenced** media object. It requires the
owner, including when `?dry_run=true` is supplied. Cookie callers also need CSRF.
The pool is shared and has no per-upload owner, so approval to comment/upload does
not grant permission to delete another person's unpublished media.

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
to Markdown, ordered media/alt text, location, or link cards update this field.
No-op updates and repost/unrepost activity do not. `updated_at` remains the general
mutation timestamp. Null means no recorded edit: historical rows are not backfilled
from ambiguous update times. Archives, import and merge preserve `edited_at` as an
optional v1 field. The database adds a forward-only nullable column.

## Post visibility

Every post carries `visibility`, either `"everyone"` (the default) or
`"only_me"`. `POST /owner/posts` accepts an optional `visibility`, and
`PATCH /owner/posts/:id` accepts it alone or alongside other fields; changing
it advances `updated_at` but never `edited_at`. Unknown values are rejected with
`INVALID_ARGUMENT`.

The server decides who sees what, and every route that can return a post
applies the same rule: the site owner reads everything, everybody else --
visitors, crawlers, and signed-in members alike -- reads only `everyone`
posts. A hidden post answers `POST_NOT_FOUND` from `GET /posts/:id` and from
its comment routes, is absent from `GET /posts`, `GET /search` and the archive
counts, embeds as an unavailable tombstone when quoted, contributes no share
metadata, and its media answers `MEDIA_NOT_FOUND` unless every file is also
referenced from a public post, an avatar, or a cover. Clients therefore need
no visibility logic of their own: send the owner credential on reads to see
hidden posts, or omit it to render the site as a visitor does. Because the
answer depends on the credential, JSON responses stay `Cache-Control:
no-store` and owner-only media is served `private`.

CLI: `moment post create --visibility only_me`, `moment post update <id>
--visibility everyone`. Direct mode reads as the owner; `--url` mode presents
`MOMENT_TOKEN` on reads when it is configured and otherwise reads as a visitor.

## Timeline paging

`GET /api/v1/posts` reads one page at a time, newest first. The page's `meta`
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

`GET /owner/export` remains an owner-only, complete `moment-export-v1` backup.
`GET /account/export` returns the authenticated account's
`moment-account-export-v1` personal archive. Pending accounts can export their own
data; unauthenticated or revoked/suspended sessions cannot. Neither route accepts
a body or query parameters, and personal export cannot select another account.

Personal archives contain:

- `data/account.json`: the selected profile, role and status;
- `data/identities.jsonl`: that account's provider bindings and profile metadata,
  without passwords, bearer tokens, OAuth secrets or sessions;
- `data/comments.jsonl`: comments attributed to that account;
- `data/entries.jsonl`: the owner's entries, or an empty file for a member;
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
