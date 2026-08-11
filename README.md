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

- `task run` — build the Web bundle, then run the local binary;
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

Runtime data and secrets must stay outside source control. Copy `.env.example`
for local conventions; do not commit `.env` files or real instance data.

No open-source license is granted by this repository at this time.
