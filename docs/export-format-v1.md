# Moment export format v1

Moment export v1 is a streaming ZIP portability protocol. It is not a copy of
the live SQLite schema.

The archive contains these controlled paths in stable production order:

```text
README.txt
data/site.json
data/entries.jsonl
data/comments.jsonl
data/media.jsonl
media/<lowercase-sha256>.<jpg|png|webp|gif> (zero or more, SHA order)
manifest.json
checksums.sha256
```

All path separators are `/`; member names are controlled printable ASCII and
must remain normalized relative names. JSON and JSONL are UTF-8 with LF line
endings. Times use canonical UTC RFC 3339 Nano rendering over millisecond
authority; values with sub-millisecond precision are rejected. Entry media records preserve array
order and nullable `alt_text`: `null` is missing, `""` is decorative, and a
non-empty string is provided. Derived search text, FTS, previews, caches,
runtime state, credentials, sessions, local drafts, and GC state are excluded.

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
Manifest counts must equal the three JSONL record counts. Entry and media
records use their specified strict order; comments use entry, root group,
root-first, then reply creation/ID order. Entry usages, comments, replies, and
the site avatar must resolve within the exported record graph, and
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
