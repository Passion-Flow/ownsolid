// Package migrations owns the immutable PostgreSQL migration assets.
package migrations

import "embed"

//go:embed postgresql/*.sql account/*.sql postgresql/mvp-postgresql-cpu-v1/*.sql
var Files embed.FS
