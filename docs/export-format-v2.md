# Moment export format v2

Moment exports a streaming ZIP portability protocol rather than a SQLite copy. A full backup uses `format: "moment-export"`, `format_version: 2`, and the HTTP header `X-Archive-Format: moment-export-v2`. Version 1 and incomplete historical layouts are unsupported. Development instances using the preceding database model must first be converted offline into a separate directory, with the original retained.

## Archive layout

Every full backup contains all fixed members, including empty JSONL files when there are no corresponding records. Members appear in this order:

```text
README.txt
data/site.json
data/users.jsonl
data/identities.jsonl
data/entries.jsonl
data/entry_articles.jsonl
data/article_drafts.jsonl
data/comments.jsonl
data/media.jsonl
media/<lowercase-sha256>.<extension>
manifest.json
checksums.sha256
```

Media members follow in SHA order. The checksum file lists every other ZIP member with its SHA-256, uncompressed byte count and path, separated by tabs. Export validates the prepared archive before publication. Import checks member order, canonical paths, duplicate names, checksums, sizes, compression ratios, records, references and media bytes before making data active.

`manifest.json` records the format, creation time, source application and instance, entity counts and expanded-byte limit. Zero counts for identities, articles and drafts may be omitted. File membership is still mandatory, and counts must agree with the records actually present.

## Accounts and site settings

`data/users.jsonl` contains immutable account IDs, usernames, personal profile fields, `admin` or `member` role, account status and timestamps. Multiple administrators are valid; a full site must retain at least one active administrator. Profile pictures refer to media records.

`data/identities.jsonl` preserves external provider bindings and their profile metadata. Password hashes, API token digests and plaintext tokens, OAuth secrets, signing keys and browser sessions are excluded. Restored accounts require new local credentials or an available external sign-in method.

`data/site.json` names the display account with `profile_user_id`, separately from its role. It also contains site configuration and the selected account's projected username, display name, biography and pictures. That projection must agree with the corresponding user record. Changing the display account grants no privileges and transfers no content. Site pins refer to public, published entries.

## Posts, articles and drafts

`data/entries.jsonl` preserves IDs, `author_user_id`, publication and edit timestamps, visibility, publication state, original/repost state, locations, link snapshots, media order and descriptions, and quoted entry IDs. Private and unpublished content remains associated with its real author.

An article entry has `kind: "article"` and a null Markdown body. Its structured document and cover are stored in `data/entry_articles.jsonl`. Ordinary posts carry Markdown instead. Derived HTML and search indexes are rebuilt with the current implementation.

`data/article_drafts.jsonl` preserves the real author, title, structured body, cover, publication relationship and revision timestamps. Published working copies, withdrawn documents and unpublished drafts remain distinct. The full graph is validated, including inline media and references to published entries.

## Comments and media

`data/comments.jsonl` preserves comment IDs, post and parent relationships, the exact reply target, account ID when known, stored author name, body, timestamps, locations, media and pins. There is no identity-kind discriminator. Historical comments without a resolvable account retain their stored signature rather than receiving a fabricated account. The public “author” badge is derived from equality with the current post author's account ID.

`data/media.jsonl` describes immutable originals. Each record also contains sorted account reuse grants:

```json
{"grants":[{"user_id":"11111111111111111111111111111111","created_at":"2026-10-18T00:00:00Z"}]}
```

The original pool deduplicates globally. Reading a public reference does not grant reuse permission. New and deduplicated uploads establish a grant for the uploader. Import validates grant account references and restores them after the accounts. Merge unions grants after resolving media ID collisions.

Unused uploads with an account grant remain portable, including their `unreferenced_since` retention timestamp. Originals without either a content/profile reference or an account grant are outside the portable graph. Offline conversion preserves historical unassigned originals in the converted directory and original snapshot without inventing an uploader.

## Inspection and commit

The full download is `GET /api/v1/backups/export`. Import resources are under `/api/v1/backups/import`; API access requires an active administrator and cookie mutations require CSRF.

Inspection rebuilds an isolated current-schema generation and reports counts, media size, replacement impact, warnings and peak disk requirements. Its staging credential is bound to the initiating real account and credential. A later commit rechecks their current validity, including after waiting for maintenance access. Another account or credential cannot claim or discard the staged operation.

A commit creates and verifies a backup of the current generation, switches `CURRENT`, checks readiness and rolls back on a failed switch. An ambiguous result is resolved by reading durable status rather than replaying a consumed token. Credentials are neither imported from the portable archive nor transferred between accounts.

Direct maintenance uses filesystem authority and requires a stopped server. Staged operations additionally select a real active administrator with `--as`:

```sh
moment --as writer backup inspect ./moment-export.zip --data-dir ./moment-data --json
moment --as writer backup import ./moment-export.zip --data-dir ./moment-data --dry-run --json
moment --as writer backup import ./moment-export.zip --data-dir ./moment-data --yes --json
```

API mode authenticates through that account's configured token and rejects `--as`.

## Merge and personal export

Merge supports incoming-only records, explicit conflict selections and preservation of current-only content. It does not require the archives to select the same display account. User IDs and handles remain unique, at least one active administrator remains, and selected references must resolve. Per-account credential and session records stay outside the portable merge graph.

`GET /api/v1/account/export` produces a separate `moment-account-export-v1` personal archive. It contains only the selected account's profile, bindings, authored content and reachable media. It does not import as a full site backup and cannot select a different account.
