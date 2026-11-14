// Package runtimeownerclosure contains inactive, source-reviewed runtime schema closure candidates.
package runtimeownerclosure

import "embed"

// Files retains the exact source statements and their unmeasured catalog.
//
//go:embed *.sql catalog.json
var Files embed.FS
