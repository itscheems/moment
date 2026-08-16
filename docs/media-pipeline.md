# Media pipeline

Moment treats uploaded bytes and browser-delivery artifacts as different
assets. The original is portable authority; previews and posters are
replaceable cache entries produced by a versioned recipe.

## Supported product envelope

| Input                | Original               | Default browser asset                                   |
| -------------------- | ---------------------- | ------------------------------------------------------- |
| JPEG, PNG, WebP, GIF | retained byte-for-byte | JPEG or PNG, longest edge 1600 px                       |
| HEIC                 | retained byte-for-byte | bounded JPEG decoded with libheif, with FFmpeg fallback |
| MP4 or QuickTime MOV, up to 5 minutes | retained byte-for-byte | H.264/AAC MP4 plus JPEG poster               |

- An image original may be at most 30 MiB; a video original may be at most
  100 MiB.
- Every display artifact and poster is at most 10 MiB.
- GIF animation remains available only through explicit original access; the
  normal timeline uses a bounded first-frame preview.
- Video output is `yuv420p`, strips source metadata, uses fast-start layout, and
  is re-probed before publication.

The server identifies media from its signature and processor inspection. A
filename extension or client-provided MIME type is never authoritative.

## Ingestion and ownership

```text
bounded read
  -> signature inspection / ffprobe
  -> prepare preview and poster in private temporary storage
  -> acquire short exclusive media commit lease
  -> recheck content-hash deduplication
  -> atomically publish cache, immutable original, and database row
```

External conversion does not run while the exclusive media lease is held.
Image work, including HEIC conversion, uses a two-worker lane. Video probe and
FFmpeg work use a separate one-worker lane. Ordinary image decode additionally
uses a weighted pixel-memory semaphore.

Ingest always prepares fresh derived bytes before the commit boundary. This
avoids trusting a cache hit that a concurrent prune could remove. A failed
processor or commit never publishes an authoritative media row or original;
an orphaned derived cache entry is harmless and can be rebuilt or pruned.

## Video quality strategy

Moment optimizes for a useful display version, not for making every result
artificially approach 10 MiB.

1. Encode H.264 at CRF 23 with AAC 96 kbit/s.
2. If the result exceeds the byte budget, retry at CRF 28.
3. If needed, make one final duration-derived constrained-bitrate attempt.
4. Re-probe the selected result for H.264, `yuv420p`, dimensions, duration,
   byte budget, and fast-start ordering.

The three-attempt ceiling keeps request cost bounded. The five-minute source
limit makes the 10 MiB delivery budget honest; longer video belongs to a
different product and delivery architecture.

## Browser delivery and loading

The HTTP transport opens the safe rooted file and passes the seekable reader
directly to Go's `ServeContent`. The shared media lease remains held until the
response closes. Byte ranges therefore avoid both unnecessary network bytes
and whole-file server buffering.

The Web client keeps 20-item cursor pages separate from media resource loading.
Images use their bounded preview with native lazy loading. Timeline, profile,
search, and quoted videos initially reserve geometry without a `src` or
`poster`; an `IntersectionObserver` attaches both within a 600 px preload
margin and uses native `<video preload="metadata" playsInline>`. Leaving the
margin pauses immediately and releases those attributes after a three-second
retention window, avoiding refetch churn during small reverse scrolls. The
primary video on a post detail page loads eagerly because navigation expresses
viewing intent.

Non-detail posts also use progressive `content-visibility: auto` containment
where supported. This reduces layout and paint work as cursor pages accumulate
without changing the accessible DOM, page size, cursor restoration, or
keyboard order. Virtualization remains deferred until measurements show that
long retained timelines need it.

Progressive H.264 MP4 plus HTTP Range is sufficient for the current short-video
envelope. Opening the lightbox is explicit intent and loads the preview; the
original-quality action loads the immutable original only when requested.

## Runtime dependencies and repair

Container images install FFmpeg, ffprobe, and libheif tools. Native deployments
that accept video must expose `ffmpeg` and `ffprobe` on `PATH`. HEIC prefers
`heif-convert` when available and otherwise uses the same bounded FFmpeg
runtime. If neither decoder exists, ingestion returns
`MEDIA_PROCESSOR_UNAVAILABLE` before authority is published. A present decoder
that rejects corrupt input does not silently fall through to another decoder.

Useful deterministic maintenance commands:

```sh
moment media list --json
moment media verify --json
moment media rebuild-previews --dry-run --json
moment media rebuild-previews --json
moment media prune --dry-run --json
```

Backups contain originals and authoritative metadata, not preview recipes or
posters. After a recipe change, `media rebuild-previews` recreates the current
cache without changing originals.

## Deliberately deferred frameworks

These are thresholds, not roadmap promises:

- **tus:** introduce resumable upload when the source ceiling grows beyond
  roughly 100 MiB, mobile weak-network upload failure is observed, or uploads
  regularly take long enough that restarting is material. It also requires a
  staged-upload lifecycle, quotas, authentication, expiry cleanup, and Web/CLI
  parity; adding only the protocol endpoint would create incomplete authority.
- **HLS/DASH and hls.js/Shaka:** introduce adaptive delivery only when Moment
  supports long video, multiple renditions, or CDN delivery. They do not improve
  the current bounded progressive MP4 path.
- **Video.js or another player shell:** adopt only when native controls cannot
  satisfy an identified accessibility or product interaction. It is not a
  transcoding or resumable-upload solution.
- **libvips:** replace the HEIC-to-JPEG intermediate path when profiling shows
  image conversion CPU or peak memory is material. Keeping it as an external
  process would preserve Moment's CGo-free binary. Until then, adding another
  native runtime is more operational cost than benefit.
- **Live Photos:** model a logical asset containing the still original, paired
  video, Apple asset identifier, and key-frame time. Do not infer pairing from
  filenames or treat the two files as unrelated attachments.

## Verification

The regular Go suite covers lock lifetime, cache publication, bounded worker
lanes, processor absence, cancellation, and repair. Real processor integration
can be run explicitly:

```sh
MOMENT_TEST_FFMPEG="$(command -v ffmpeg)" \
MOMENT_TEST_FFPROBE="$(command -v ffprobe)" \
go test ./internal/media -run 'TestIngest(HEIC|MP4|QuickTime)Real' -count=1
```

Set `MOMENT_TEST_HEIF_CONVERT` as well to exercise the preferred libheif path;
the HEIC integration deliberately also runs with FFmpeg alone.
