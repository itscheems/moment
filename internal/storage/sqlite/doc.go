// Package sqlite owns Moment's authoritative SQLite schema, migrations, and
// connection lifecycle. Feature repositories build on its transaction
// boundary; transport and domain packages do not depend on database details.
package sqlite
