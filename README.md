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

No open-source license is granted by this repository at this time.
