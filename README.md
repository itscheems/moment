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

Posts can carry a saved place snapshot selected from search, resolved from an
explicit browser/device location request, or entered as a manual label. Comments
do not carry locations. Provider credentials remain in the server process and
are never sent to the browser:

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
belongs only to the local development launcher.

No open-source license is granted by this repository at this time.
