// Package resources contains offline mail templates and release assets.
package resources

import "embed"

//go:embed locales/mail/*/*.json security/*
var Files embed.FS
