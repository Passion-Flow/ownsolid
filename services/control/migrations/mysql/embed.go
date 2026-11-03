// Package mysqlmigrations owns immutable MySQL business-schema assets.
// The initial account/Workspace subset does not enable a product MySQL profile.
package mysqlmigrations

import "embed"

//go:embed *.sql reference.json evolution.json
var Files embed.FS
