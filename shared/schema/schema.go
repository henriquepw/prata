// Package schema exposes Prata's canonical SQLite schema files.
package schema

import "embed"

// Files contains the SQL used by the client and server databases.
//
//go:embed *.sql
var Files embed.FS
