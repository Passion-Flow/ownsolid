// Package plugintool owns unactivated PG14-equivalent MySQL source candidates.
package plugintool

import "embed"

//go:embed *.sql catalog.json
var Files embed.FS
