package release

import _ "embed"

//go:embed compose-profile.json
var ComposeProfile []byte

type ComposePolicy struct {
	FormatVersion  int      `json:"format_version"`
	Versions       []string `json:"compose_versions"`
	CommandDefault int      `json:"command_seconds_default"`
	CommandMinimum int      `json:"command_seconds_minimum"`
	CommandMaximum int      `json:"command_seconds_maximum"`
	OutputDefault  int64    `json:"output_bytes_default"`
	OutputMinimum  int64    `json:"output_bytes_minimum"`
	OutputMaximum  int64    `json:"output_bytes_maximum"`
}
