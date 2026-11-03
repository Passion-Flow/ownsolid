// Package monitoring assembles current operational facts without execution authority.
package monitoring

import (
	"bytes"
	"encoding/json"
	"errors"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/monitoringmetrics"
	"github.com/Passion-Flow/ownsolid/tools/release/offline/pkg/bundle"
	release "github.com/Passion-Flow/ownsolid/api/release/v1"
)

const ConfigEnvironment = "PROJECT_ONE_MONITORING_CONFIG_FILE"

type Config struct {
	FormatVersion       int                      `json:"format_version"`
	OperationSeconds    int                      `json:"operation_seconds"`
	LookbackSeconds     int                      `json:"lookback_seconds"`
	MaximumGatewayRows  int                      `json:"maximum_gateway_rows"`
	NodePageSize        int                      `json:"node_page_size"`
	ConcurrentSnapshots int                      `json:"concurrent_snapshots"`
	HTTP                monitoringmetrics.Policy `json:"http"`
}

func Defaults() Config { return Config{1, 10, 3600, 1000, 25, 2, monitoringmetrics.Defaults()} }
func (c Config) Validate() error {
	if c.FormatVersion != 1 || c.OperationSeconds < 1 || c.OperationSeconds > 120 || c.LookbackSeconds < 1 || c.LookbackSeconds > 86400 || c.MaximumGatewayRows < 1 || c.MaximumGatewayRows > 10000 || c.NodePageSize < 1 || c.NodePageSize > 100 || c.ConcurrentSnapshots < 1 || c.ConcurrentSnapshots > 32 || c.HTTP.Validate() != nil {
		return errors.New("MONITORING_CONFIGURATION_INVALID")
	}
	return nil
}
func Load(path string) (Config, error) {
	raw, e := bundle.ReadProtectedConfiguration(path)
	if e != nil {
		return Config{}, errors.New("MONITORING_CONFIGURATION_REQUIRED")
	}
	c := Defaults()
	if release.StrictJSON(raw, &c) != nil || !validPolicyValues(raw) || c.Validate() != nil {
		return Config{}, errors.New("MONITORING_CONFIGURATION_INVALID")
	}
	return c, nil
}
func LoadEnvironment(values map[string]string) (Config, error) {
	if len(values) != 1 || values[ConfigEnvironment] == "" {
		return Config{}, errors.New("MONITORING_CONFIGURATION_REQUIRED")
	}
	return Load(values[ConfigEnvironment])
}

// JSON null cannot silently preserve a default numeric, boolean or object value.
func validPolicyValues(raw []byte) bool {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		return false
	}
	for key, value := range fields {
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return false
		}
		if key == "http" {
			var nested map[string]json.RawMessage
			if json.Unmarshal(value, &nested) != nil || nested == nil {
				return false
			}
			for _, child := range nested {
				if bytes.Equal(bytes.TrimSpace(child), []byte("null")) {
					return false
				}
			}
		}
	}
	return true
}
