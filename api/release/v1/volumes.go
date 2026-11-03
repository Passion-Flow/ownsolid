package release

import _ "embed"

//go:embed volume-utility-profile.json
var VolumeUtilityProfile []byte

type UtilityResources struct {
	MemoryBytes int64 `json:"memory_bytes"`
	MilliCPU    int64 `json:"milli_cpu"`
	PIDs        int64 `json:"pids"`
}
type UtilityPolicy struct {
	FormatVersion int              `json:"format_version"`
	Defaults      UtilityResources `json:"defaults"`
	Minimum       UtilityResources `json:"minimum"`
	Maximum       UtilityResources `json:"maximum"`
}
