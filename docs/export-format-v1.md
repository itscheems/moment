# Moment export format v1

Moment export v1 is a streaming ZIP portability protocol. It is not a copy of
the live SQLite schema.

The archive contains these controlled paths in stable production order:

```text
README.txt
data/site.json
data/users.jsonl
data/identities.jsonl
data/entries.jsonl
data/comments.jsonl
data/media.jsonl
media/<lowercase-sha256>.<jpg|png|webp|gif|heic|mp4> (zero or more, SHA order)
manifest.json
checksums.sha256
```

## Two layouts, one version

`data/users.jsonl` and `data/identities.jsonl` were added after v1 shipped.
The format version was **not** raised, and must not be: `format_version` is
compared with strict equality, and a database migration only runs forwards, so
the only way back from an upgrade is the backup taken just before it. Raising
the version would make exactly that backup unreadable by exactly the binary
that needs to read it.

The rule that already governs added record fields therefore governs added
members. A writer always emits all nine fixed members. A reader accepts both
layouts:

- **with accounts** — the seven leading members above, then media, then the
  trailer.
- **without accounts** — `README.txt`, `data/site.json`,
  `data/entries.jsonl`, `data/comments.jsonl`, `data/media.jsonl`, then media,
  then the trailer.

The two accounts members are present together or not at all; one without the
other is neither layout and is rejected. `manifest.json` gains
`counts.users` and `counts.identities`, both omitted when zero; a manifest that
declares accounts an archive does not carry is rejected.

An archive without the accounts members still describes exactly one account,
in `data/site.json`: `owner_id`, `username`, `display_name`, `bio`,
`avatar_media_id` and `cover_media_id` are every field an account needs except
its timestamps, and those are taken from the archive's own `created_at`. The
importer reconstructs that account as `role="owner"`, `status="active"`, with
`username_set_at` stamped — the handle was chosen by a person, before any
rename prompt existed, so it must not trigger one on restore. An archive older
still, from before `owner_id` existed at all, names no owner; the restored
generation keeps the account its own migrations created.

`data/site.json` keeps its copy of those five profile fields even though they
now live on the account, because older readers have no other place to find
them. An archive that carries `data/users.jsonl` must state the same values in
both places: a disagreement is rejected rather than resolved, since picking
either answer would silently rename the owner on restore.

Sessions are excluded from the archive entirely. A backup that carried them
would revive dead sign-ins on restore, and a restore is when that is least
wanted; restoring therefore ends every session, which is the intended outcome
rather than a limitation. `data/identities.jsonl` carries no secret: a provider
name and the opaque account id that provider issued cannot be replayed as a
sign-in.

All path separators are `/`; member names are controlled printable ASCII and
must remain normalized relative names. JSON and JSONL are UTF-8 with LF line
endings. Times use canonical UTC RFC 3339 Nano rendering over millisecond
authority; values with sub-millisecond precision are rejected. Entry media records preserve array
order and nullable `alt_text`: `null` is missing, `""` is decorative, and a
non-empty string is provided. Derived search text, FTS, previews, caches,
runtime state, credentials, sessions, local drafts, and GC state are excluded.
An entry quote whose source was deleted is preserved as
`quote_unavailable=true` without a `quoted_entry_id`; this tombstone keeps the
quote wrapper portable without exposing or inventing deleted target content.
An entry that is not public carries `visibility="only_me"`; the key is omitted
for public entries, so an archive of a site with no hidden posts is
byte-identical to one written before the field existed and remains importable
by those versions. A reader treats an absent key as public and rejects any
value it does not know rather than importing it as public.
HEIC, MP4, and QuickTime MOV members are immutable originals. Their JPEG/H.264
display derivatives and video poster images remain replaceable cache artifacts and are
therefore not exported.

`manifest.json` has `format="moment-export"` and `format_version=1`. Its
`source.instance_id` is the stable 32-character lowercase hexadecimal ID read
from the exporting instance's `instance.json`; it is not generated per export
and no host path is included.

`limits.expanded_bytes` is the sum of the uncompressed bytes of every member
except `checksums.sha256`. Because that sum includes `manifest.json`, the
canonical value is the fixed point:

```text
E = bytes(all non-manifest, non-checksums members)
    + bytes(canonical manifest whose expanded_bytes is E)
```

The exporter iterates this equation to equality with a defensive finite bound.
This definition remains exact when the decimal value crosses a digit boundary.

`checksums.sha256` excludes itself and contains one line for every other member,
sorted by path:

```text
<64 lowercase SHA-256 hex>\t<uncompressed decimal bytes>\t<normalized path>\n
```

An importer can therefore recompute every digest and byte count, including the
manifest and `expanded_bytes`, without trusting ZIP metadata or Moment runtime
state. Member order is stable but the manifest is deliberately not required to
be first, so export never buffers the complete archive.

The exporter emits stable known-field JSON bytes. Independent v1 readers ignore
unknown object fields for forward compatibility, but reject duplicate keys,
missing required known fields, `null` where a required value or array is
expected, malformed UTF-8, multiple JSON values, and non-LF record endings.
Manifest counts must equal the JSONL record counts they name. Entry and media
records use their specified strict order; comments use entry, root group,
root-first, then reply creation/ID order; accounts use strict ID order and
credentials strict provider then provider-account order. Entry usages,
comments, replies, account pictures, and the site avatar must resolve within
the exported record graph, and every credential must name an account the same
archive carries. At most one
top-level comment per entry may carry the optional `pinned` selection, which is
restored with the thread during import. The
`data/media.jsonl` `archive_path` values match `media/` members one-to-one.
Validation streams member bodies, bounds individual record memory, and uses
only ID maps proportional to the record graph.

The authenticated HTTP download is `GET /api/v1/owner/export`. It accepts no
query parameters or request body and returns `Content-Type: application/zip`,
`X-Archive-Format: moment-export-v1`, and a controlled attachment filename.
File-mode clients prepare, validate, flush, and atomically publish the result.
Direct-mode file destinations must be outside the selected Moment data
directory, including paths that enter it through a symlink or filesystem alias.
Raw stdout clients require the exact format header before their first byte;
after streaming begins, a network or integrity failure is represented by a
truncated ZIP on stdout, a non-zero exit, and diagnostics only on stderr.

## Import inspection

`moment backup inspect <archive.zip>` and authenticated
`POST /api/v1/owner/import/inspect` run the same read-only preflight. The HTTP
endpoint accepts a raw `application/zip` body. Before any member is copied into
a candidate generation, inspection validates normalized controlled paths,
duplicates, regular-file mode, compression method, declared and actual bytes,
compression ratio, member/record quotas, protocol version, checksums, JSONL,
the complete record graph, and every media signature, hash, size, MIME type,
dimension, and animation budget.

Valid records are then written through the current migrations and bulk import
rules into `staging/import/<generation-id>/generation/`; Moment never imports a
SQLite file from the ZIP. Entry Markdown is normalized again, FTS is rebuilt,
foreign keys and SQLite integrity are checked, media references and nullable
alt states are revalidated, and active `CURRENT` data remains untouched.

Accounts are written before the content that names them. A freshly migrated
generation is not accountless — the migrations create an owner there, holding
the single-owner index and, by default, the handle `owner` — so the archive's
owner is written first and takes that row's place before any other account is
inserted. The staged generation is then compared field for field against the
archive it was built from, including its accounts; when the archive carries no
accounts members, the comparison is against the same owner the importer
synthesized, so the check still compares two independently produced answers.

The preview includes archive/expanded/staging bytes, pre-import backup and
rollback reserves, a safety margin of at least 10% or 2 GiB, required
additional bytes, estimated peak footprint, available bytes, replacement
counts, time range, media bytes, and warnings. Insufficient space returns
`IMPORT_DISK_SPACE_INSUFFICIENT` before a ready token exists.

Preview counts are `entries`, `comments`, `media`, `users`, and `identities`,
on both the incoming archive and the replacement impact. Unlike the manifest's
`counts.users` and `counts.identities`, none of the five is ever omitted: a
preview that drops a zero reads as an import that leaves that dimension alone,
and commit leaves nothing alone. The incoming `users` count is what the archive
*describes*, not what its members list — a seven-member archive with no
accounts members previews as `users: 1`, the owner its site record names and
the importer will synthesize, and `identities: 0`. The replacement counts are
read from the live database, so they name the accounts this site has now, all
of which commit deletes. Both are computed from the same rule the rebuilt
generation is later validated against, so the number shown before the decision
and the number enforced after it cannot disagree.

Adding the two account counts changed the preview response shape. The CLI reads
it with an exact field set — every declared name required, no other name
allowed — so a CLI and a server from either side of this change cannot talk to
each other about import preview, in either direction. Matching binaries are
already the expectation for direct mode; in API mode, upgrade both.

Successful inspection persists `archive.zip`, the rebuilt generation, and a
private `staging.json`. Only a SHA-256 token hash is stored. The plaintext token
is returned once, expires after 30 minutes, and is bound to archive hash,
instance ID, owner session epoch, and generation ID. A ready token survives a
restart; claim atomically changes it to `committing`, and ready tokens that are
expired, mismatched, corrupt, already committing, or used cannot replay. This
inspection contract does not switch `CURRENT`; replacement and rollback are a
separate commit operation.
