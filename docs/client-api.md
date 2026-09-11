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
