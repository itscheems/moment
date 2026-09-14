# Moment

Moment is a self-hosted personal timeline: capture now, find later, and keep the
data portable under the site owner's control.

## Toolchain

The repository pins Go, Node.js, pnpm, Task, lint, and release tools in
`.mise.toml`. With [mise](https://mise.jdx.dev/) installed:

```sh
mise install
mise exec -- task setup
mise exec -- task ci
```

The stable entry points are:

- `task run` — build Moment, initialize local data when needed, and run the
  server in the foreground until `Ctrl+C`;
- `task fmt` — format Go and Web sources;
- `task lint` — run static checks and Web type checking;
- `task test` — run Go race tests and Web tests;
- `task build` — write the single CGo-free binary to `dist/`;
- `task ci` — run the complete local gate.

Release qualification, artifact verification, native installation, and
container lifecycle commands are documented in [docs/releasing.md](docs/releasing.md).

After building:

```sh
./dist/moment version --json
```

To start a local site:

```sh
mise exec -- task run
```

The first run initializes the ignored `moment-data/` directory. Its one-time
owner token is saved to the ignored `.moment-local/owner.token` file and shown
in an interactive terminal, so the owner Web screens can be tested without
guessing a password. Later runs reuse both files. The server listens on
`127.0.0.1:8080` and stops cleanly with `Ctrl+C`.

An owner token cannot be recovered from an existing instance. If the local
token file is missing, rotate the token while the server is stopped:

```sh
mise exec -- task run -- --rotate-owner-token
```

Override the defaults when needed:

```sh
MOMENT_DATA_DIR=/absolute/path/to/data \
MOMENT_LISTEN=127.0.0.1:18080 \
MOMENT_OWNER_TOKEN_FILE=/absolute/path/to/owner.token \
mise exec -- task run
```

Runtime data and secrets must stay outside source control. Copy `.env.example`
for local conventions; do not commit `.env` files or real instance data.

## Post locations

Posts and owner comments can carry a saved place snapshot selected from search,
resolved from an explicit browser/device location request, or entered as a
manual label. Provider credentials remain in the server process and are never
sent to the browser:

```sh
AMAP_API_KEY=... \
GEOAPIFY_API_KEY=... \
mise exec -- task run
```

For the development launcher, the same values may be placed in the ignored
`.env.local` file. `scripts/dev/serve.sh` parses only the documented location
fields and does not evaluate the file as shell code:

```dotenv
AMAP_API_KEY=...
GEOAPIFY_API_KEY=...
```

Moment prefers AMap for mainland China and Geoapify for searches biased outside
mainland China, with automatic fallback when both are configured. Either key can
be omitted; manual labels remain available when no provider is configured. The
Moment binary itself intentionally does not parse `.env` files; this convenience
belongs only to the local development launcher. The
same workflow is agent-operable through `moment location search`, `moment
location nearby`, `moment post create --location-name`, and `moment post create
--location-json`. Use `moment post update --clear-location` to remove a stored
place.

Client capability discovery, native request attribution, selected media cleanup,
edit timestamps, and account exports are documented in [docs/client-api.md](docs/client-api.md).

## Post visibility

Each post is either visible to everyone (the default) or only to the site
owner. The composer's audience pill chooses between the two, and the server
applies the choice on every route that can return a post: timeline, detail,
search, archive counts, comment threads, share metadata, and the post's own
media. Visitors and signed-in members never receive a hidden post; the owner's
session or token does. The same contract is available to agents through
`moment post create --visibility only_me` and `moment post update <id>
--visibility everyone`, and to other clients through the `visibility` field
documented in [docs/client-api.md](docs/client-api.md).

## Comment moderation

Comment threads stay one level deep. The owner can delete any comment from its
overflow menu and can pin one top-level comment per post; the pinned root and
its direct replies move together while their timestamps remain unchanged.
Deleting that root also clears its pin. The same moderation contract is
available to agents through `moment comment pin`, `moment comment unpin`, and
`moment comment delete` in direct or API mode.

## Media originals and display versions

Moment accepts JPEG, PNG, WebP, GIF, and HEIC image originals up to 30 MiB,
plus MP4 and QuickTime MOV video originals up to 300 MiB. Video is currently
limited to five minutes.
Original bytes are retained for explicit original-quality access. Timeline and
lightbox views use replaceable display artifacts capped at 10 MiB: HEIC is
decoded to a bounded JPEG, while accepted video containers are normalized to
H.264/AAC MP4 with a JPEG
poster and file-backed HTTP byte-range delivery. Processor work is bounded and
runs outside the short exclusive media commit boundary. The container image installs FFmpeg and
libheif tools. Native installations that need HEIC or video ingestion must make
`ffmpeg`, `ffprobe`, and `heif-convert` available on `PATH`; otherwise ingestion
fails clearly with `MEDIA_PROCESSOR_UNAVAILABLE` and does not publish a partial
media record.

The lifecycle, repair commands, quality strategy, and objective thresholds for
tus, adaptive streaming, player frameworks, libvips, and Live Photos are
documented in [docs/media-pipeline.md](docs/media-pipeline.md).

No open-source license is granted by this repository at this time.
