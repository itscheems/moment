# Media pipeline

Moment treats uploaded bytes and browser-delivery artifacts as different
assets. The original is portable authority; previews and posters are
replaceable cache entries produced by a versioned recipe.

## Supported product envelope

| Input                                 | Original               | Default browser asset                                   |
| ------------------------------------- | ---------------------- | ------------------------------------------------------- |
| JPEG, PNG, WebP, GIF                  | retained byte-for-byte | JPEG or PNG, longest edge 1600 px                       |
| HEIC                                  | retained byte-for-byte | bounded JPEG decoded with libheif, with color-aware fallbacks |
| MP4 or QuickTime MOV, up to 5 minutes | retained byte-for-byte | H.264/AAC MP4 plus JPEG poster                          |

- An image original may be at most 30 MiB; a video original may be at most
  300 MiB.
- Every display artifact and poster is at most 10 MiB.
- GIF animation remains available only through explicit original access; the
  normal timeline uses a bounded first-frame preview.
- Video output is `yuv420p`, strips source metadata, uses fast-start layout, and
  is re-probed before publication.

The server identifies media from its signature and processor inspection. A
filename extension or client-provided MIME type is never authoritative.

## Image color profiles

RGB image samples keep their embedded ICC profile through decoding, resizing,
and preview encoding. A Display P3 HEIC or JPEG must not lose that profile:
otherwise the browser interprets the same RGB values as sRGB and changes the
colors. JPEG APP2, PNG iCCP, and WebP ICCP profiles follow the pixels into the
derived JPEG or transparent PNG. Browsers perform the final display conversion.
This retains the source gamut without applying saturation or brightness filters.

Only color metadata is copied. EXIF orientation is applied to the pixels;
camera, location, and other EXIF/XMP metadata are omitted. Profiles are bounded
to 1 MiB, validated structurally, and count toward the preview byte ceiling.
Non-RGB profiles are not attached to the RGB output of a decoder. Untagged
images retain the existing sRGB browser interpretation.

HEIC decoding prefers libheif. On macOS, the system `sips` decoder is the
fallback and preserves the complete image, its profile, and orientation.
The FFmpeg fallback requires FFprobe to identify the complete primary image
and checks the decoded dimensions before publication. Some FFmpeg versions
decode only the first tile of an iPhone image while returning success; those
partial results are rejected rather than published as a complete photograph.

The color-profile recipe changes the preview URL and cache directory. Existing
originals remain unchanged; their current previews rebuild on demand or through
`media rebuild-previews`.

## Ingestion and ownership

```text
bounded read
  -> signature inspection / ffprobe
  -> prepare preview and poster in private temporary storage
  -> acquire short exclusive media commit lease
  -> recheck content-hash deduplication
  -> atomically publish cache, immutable original, and database row
```

The immutable original pool deduplicates globally by content hash. A separate account/media grant records reuse permission. Both a first upload and a deduplicated upload grant the authenticated account access to reuse that object. Posts, comments, article bodies, drafts and profile pictures check the actual account's grants. A public reference permits reading the object and does not permit another account to attach it without uploading it themselves. Temporary video posters are scoped by account and upload ID. Unused uploads and private references use private caching.

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

## Apple HLG / Dolby Vision color management

An Apple HLG / Dolby Vision QuickTime MOV sample was inspected with
`ffprobe` and QuickTime metadata. The sample has these properties:

```text
codec=hevc, profile=Main 10, codec_tag=hvc1
stored=1920x1080, display=1080x1920, rotation=-90
pix_fmt=yuv420p10le, bits_per_raw_sample=10, chroma_location=left
color_range=tv, color_space=bt2020nc
color_transfer=arib-std-b67, color_primaries=bt2020
Dolby Vision profile=8, compatibility_id=4, RPU present
```

Compatibility ID 4 is Apple's Dolby Vision 8.4 / HLG-compatible recording
form. Transfer, primaries, matrix, range and bit depth jointly describe the
pixels; they cannot be repaired by relabeling the same samples as BT.709.
Apple also requires Dolby Vision metadata to be regenerated or removed after
pixel editing. Moment therefore performs a real HDR-to-SDR render and does not
copy the source RPU into the edited display rendition.

### Verified root cause and rejected patches

The historical VideoToolbox path scaled 10-bit HLG frames and wrote BT.709
properties without first rendering their HDR luminance into SDR. That changed
labels, not the intended pixel values. Later AVFoundation work corrected that
backend error, but the editor still replaced its color-managed JPEG poster
with a hidden/local `<video>` frame after upload. The cover therefore moved
back into the browser's HDR video decode/compositor path. A screen capture can
force a different compositor pass, which matches the reported “correct only
while taking a screenshot” behavior. Timeline video also relied on the native
video poster surface, so static poster and decoded video did not have an
explicit UI boundary.

One final failure survived even after the stored display file was correct.
Chrome's native media cache reused an older rendition at the stable `/preview`
URL without revalidating it. In the same browser process,
`chrome://media-internals` reported BT.2020/HLG for that player while `ffprobe`
of the served MP4 and its raw H.264 SPS both reported BT.709. A fresh browser
process rendered correctly. The display contract therefore includes the URL
cache key, not only file bytes and HTTP validators: every preview and poster
URL carries the exact current recipe as `?v=<recipe>`. A recipe change produces
a new native-media cache key; the immutable original URL remains stable. An
automated cross-language test prevents the Go and Web recipe constants from
drifting.

Moment has one display contract now. It contains no CSS `filter`, canvas color
rewrite, per-page brightness value, or saturation compensation:

```text
immutable Dolby Vision 8.4 / HLG original
  |-> representative 0.35 s AVFoundation SDR segment
  |     -> early full-range JPEG poster (upload feedback)
  |
  `-> complete AVFoundation HDR-to-SDR render
        -> 8-bit limited-range BT.709 intermediate
        -> bounded H.264/AAC display MP4
        -> durable full-range JPEG poster from that MP4
```

Darwin uses `avconvert PresetHighestQuality` for the AVFoundation render, then
FFmpeg performs only the bounded browser encode. Non-Darwin deployments use
FFmpeg's explicit linear-light `zscale -> tonemap -> zscale` path and must ship
the required filters; failure is preferable to silently retagging HDR. A
wide-gamut SDR input also receives an explicit primaries/matrix conversion,
without the HDR tone-map stage. `AVAssetImageGenerator.forceSDR` is not used as
a shortcut to an unprofiled JPEG: it changes dynamic range policy but does not
by itself define Moment's BT.709/full-range JPEG encoding contract.

### Artifact contracts

| Artifact       | Pixel and metadata contract                                                                                                                      | Publication and UI contract                                                                         |
| -------------- | ------------------------------------------------------------------------------------------------------------------------------------------------ | --------------------------------------------------------------------------------------------------- |
| Original       | Exact uploaded `hvc1` Main 10 bytes, BT.2020/HLG, limited range, Dolby Vision 8.4 RPU retained                                                   | Separate original-quality URL; never used as an editor or timeline cover                            |
| Early poster   | Color-managed SDR frame, JPEG `yuvj444p`, full range; BT.709 limited video samples are explicitly converted to JPEG's full-range matrix          | Generated from a short segment and reported independently while the full video continues processing |
| Display MP4    | H.264 High/`avc1`, 8-bit `yuv420p`, limited range; SPS/container primaries, transfer and matrix all BT.709; no Dolby Vision metadata; fast-start | The only source used by normal Web playback                                                         |
| Durable poster | Representative frame decoded from the display MP4, JPEG `yuvj444p`, full range                                                                   | Replaces the early poster only after the complete display asset succeeds                            |

The editor always renders a poster as `<img>` and never seeks a local or final
video merely to paint a cover. Timeline/detail keeps an explicit poster image
above the video until playback has actually started, and restores it after the
video ends. During playback and a mid-video pause, the visible frame comes from
the SDR display MP4. The lightbox offers the immutable original only through
the explicit original-quality action; it does not auto-play the local HLG blob
during processing.

Web delivery uses `/preview?v=<recipe>` and `/poster?v=<recipe>` for every
derived surface. Do not remove that query: `Cache-Control: must-revalidate` and
a recipe-bearing ETag are insufficient to force every native media cache path
to discard a previously decoded stream. `/api/v1/media/<id>` deliberately has
no recipe query because it addresses the byte-identical original.

Inspect a source and all derived contracts before changing the recipe:

```sh
ffprobe -v error -select_streams v:0 \
  -show_entries \
'stream=codec_name,profile,codec_tag_string,pix_fmt,bits_per_raw_sample,chroma_location,color_range,color_space,color_transfer,color_primaries:stream_side_data' \
  -of json input.mov

ffprobe -v error -select_streams v:0 \
  -show_entries \
'stream=codec_name,profile,codec_tag_string,pix_fmt,bits_per_raw_sample,color_range,color_space,color_transfer,color_primaries:stream_side_data' \
  -of json display.mp4

ffprobe -v error -select_streams v:0 \
  -show_entries stream=pix_fmt,color_range -of default=noprint_wrappers=1 \
  poster.jpg
```

File tags are necessary but not sufficient evidence. The repeatable acceptance
sequence uploads the real fixture in Chromium, records when the early poster
becomes visible and when the upload completes, opens the finished post, plays
and pauses the display MP4, and captures the poster and playback surfaces. On
macOS, repeat in Safari when available. Compare the early poster, durable
poster, and a decoded display frame at the same representative timestamp; do
not use the local HDR original as the SDR visual reference. Rebuild stale
derived assets with `moment media rebuild-previews`; originals must remain
byte-identical.

For Chromium, also select the newly created player in
`chrome://media-internals` and verify both the versioned URL and the demuxed
track color space. The accepted display track reports BT.709 primaries,
transfer and matrix with limited range. This catches stale native-media cache
entries and incorrect H.264 SPS metadata that an MP4 container-only probe can
miss.

References:

- [Apple TN3145: HDR video metadata](https://developer.apple.com/documentation/technotes/tn3145-hdr-video-metadata)
- [Apple: Incorporating HDR video with Dolby Vision into your apps](https://developer.apple.com/av-foundation/Incorporating-HDR-video-with-Dolby-Vision-into-your-apps.pdf)
- [Apple: AVAssetImageGenerator forceSDR](https://developer.apple.com/documentation/avfoundation/avassetimagegenerator/dynamicrangepolicy-swift.struct/forcesdr)
- [Apple: HDR playback, editing and export](https://developer.apple.com/news/?id=rwbholxw)
- [FFmpeg filter documentation](https://ffmpeg.org/ffmpeg-filters.html)
- [WebKit HDR playback capabilities](https://webkit.org/blog/11340/new-webkit-features-in-safari-14/)

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
viewing intent. A separate `<img>` owns the static cover; the video element is
not allowed to replace it until its `playing` event. This makes poster timing
and video decoding distinct states instead of relying on platform compositor
behavior for a paused `<video>`.

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
`heif-convert`, then macOS `sips` when available, then dimension-checked FFmpeg
with FFprobe. If no usable decoder exists, ingestion returns
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

- **tus:** introduce resumable upload when mobile weak-network failure is
  observed at the current 300 MiB ceiling, or uploads regularly take long
  enough that restarting is material. It also requires a
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

Set `MOMENT_TEST_HEIF_CONVERT` as well to exercise the preferred libheif path.
Set `MOMENT_TEST_COLOR_IMAGE` to a real tagged JPEG or HEIC to verify its RGB
profile survives ingestion, and `MOMENT_TEST_HEIC_FALLBACK_FIXTURE` to the
portrait HEIC fixture to exercise macOS `sips` and reject partial FFmpeg tiles.
The QuickTime
fixture additionally asserts the exact Dolby Vision 8.4/HLG source metadata,
immutable original bytes, early-poster timing, the display MP4's
H.264/8-bit/limited-range BT.709 bitstream contract, absence of Dolby Vision
metadata, JPEG 4:4:4/full-range encoding, and bounded pixel differences
between early poster, durable poster, and the decoded display frame.
