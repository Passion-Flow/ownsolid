// Package resources contains offline release assets.
package resources

import "embed"

//go:embed security/*
var Files embed.FS
