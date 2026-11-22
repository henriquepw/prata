// Package schema exposes Prata's canonical SQLite schema files.
package schema

import "embed"

// Files contains the SQL used by the client and server databases. The server migrations live in migration/,
// each file named <yyyymmddhhmmss>_<name>.sql so that sorting by name applies them oldest first.
//
//go:embed *.sql migration/*.sql
var Files embed.FS
