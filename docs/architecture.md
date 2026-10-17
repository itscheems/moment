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

## Identity and content

A real account is the authentication principal. Password hashes, API token digests and sessions refer to its immutable ID. Every authenticated request resolves current account status and role; missing accounts and database failures never produce a synthetic administrator.

Content stores its author's account ID. That relationship controls editing and private visibility. The `admin` role separately controls public moderation, account administration and site configuration; multiple administrators are supported while preserving at least one active administrator. The selected site display account is a third, independent relationship.

Web features and API clients are organized by function: authentication, accounts, publishing, posts, comments, media, site settings and backups. Browser state that may contain private content is scoped to the instance and account. Media originals deduplicate globally, with per-account reuse grants.

The database uses one canonical baseline. Portable backups use format v2 with real account references and no passwords, API tokens or browser sessions. Development data from the preceding identity model is converted offline into a separate directory before use; the original data remains available for recovery.
