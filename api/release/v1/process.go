package release

import _ "embed"

//go:embed process-profile.json
var ProcessProfile []byte

type ProcessSettings struct {
	ReservePercent   int `json:"reserve_percent,omitempty"`
	StopSeconds      int `json:"stop_seconds"`
	ObserveSeconds   int `json:"observe_seconds"`
	PollMilliseconds int `json:"poll_milliseconds"`
}
type ProcessPolicy struct {
	FormatVersion int             `json:"format_version"`
	Defaults      ProcessSettings `json:"defaults"`
	Minimum       ProcessSettings `json:"minimum"`
	Maximum       ProcessSettings `json:"maximum"`
}
