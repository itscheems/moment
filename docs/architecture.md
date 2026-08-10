# Architecture baseline

Moment v0 is one Go module, one React workspace, and one deployable `moment`
binary. The production Web bundle is generated before Go compilation and
embedded by `internal/webassets`; Node is not required at runtime.

Future code follows these inward dependencies:

```text
HTTP / CLI adapters -> application services -> domain rules
                              |
                              +-> storage and media ports
```

Transport packages map syntax and DTOs. They do not own business decisions, and
Gin, SQL, filesystem, and React concepts do not enter the domain package.

Runtime state defaults to a data directory outside the repository. Generated
bundles, binaries, local data, secrets, logs, and worktrees are ignored by Git.
