// Package configuration owns strict protected operator inputs; no ambient settings.
package configuration

import (
	"bytes"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"

	"github.com/Passion-Flow/ownsolid/tools/release/offline/pkg/bundle"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

type Config struct {
	// BoundScopeID is a trusted in-process fence, never JSON/env configuration.
	// Omitting it from JSON preserves original installation fingerprints exactly.
	BoundScopeID           string              `json:"-"`
	Verification           bundle.Verification `json:"verification"`
	BundleFile             string              `json:"bundle_file"`
	ExpectedArchiveSHA256  string              `json:"expected_archive_sha256"`
	ExpectedManifestSHA256 string              `json:"expected_manifest_sha256"`
	DestinationRoot        string              `json:"destination_root"`
	TimeoutSeconds         int                 `json:"timeout_seconds"`
	ScopeRoot              string              `json:"scope_root,omitempty"`
	MaximumJournalBytes    int64               `json:"maximum_journal_bytes"`
	Engine                 *Engine             `json:"engine,omitempty"`
	Compose                *Compose            `json:"compose,omitempty"`
}

type Compose struct {
	Process         *wire.ProcessSettings `json:"process,omitempty"`
	DockerBinary    string                `json:"docker_binary"`
	PluginDirectory string                `json:"plugin_directory"`
	CommandSeconds  int                   `json:"command_seconds"`
	OutputBytes     int64                 `json:"output_bytes"`
	ModelFile       string                `json:"model_file,omitempty"`
	InputsFile      string                `json:"inputs_file,omitempty"`
}

func composePolicy() wire.ComposePolicy {
	var p wire.ComposePolicy
	_ = wire.StrictJSON(wire.ComposeProfile, &p)
	return p
}
func (c Compose) Validate() error {
	if c.Process != nil {
		var p wire.ProcessPolicy
		if wire.StrictJSON(wire.ProcessProfile, &p) != nil || p.FormatVersion != 1 {
			return errors.New("OFFLINE_OPERATOR_PROCESS_POLICY_INVALID")
		}
		v := c.Process
		if v.ReservePercent < p.Minimum.ReservePercent || v.ReservePercent > p.Maximum.ReservePercent || v.StopSeconds < p.Minimum.StopSeconds || v.StopSeconds > p.Maximum.StopSeconds || v.ObserveSeconds < p.Minimum.ObserveSeconds || v.ObserveSeconds > p.Maximum.ObserveSeconds || v.PollMilliseconds < p.Minimum.PollMilliseconds || v.PollMilliseconds > p.Maximum.PollMilliseconds || v.StopSeconds >= c.CommandSeconds || v.PollMilliseconds > v.ObserveSeconds*1000 {
			return errors.New("OFFLINE_OPERATOR_PROCESS_CONFIGURATION_INVALID")
		}
	}
	p := composePolicy()
	if c.ModelFile != "" && (!filepath.IsAbs(c.ModelFile) || filepath.Clean(c.ModelFile) != c.ModelFile || strings.ContainsAny(c.ModelFile, "\x00\r\n")) {
		return errors.New("OFFLINE_OPERATOR_COMPOSE_MODEL_REFERENCE_INVALID")
	}
	if c.InputsFile != "" && (!filepath.IsAbs(c.InputsFile) || filepath.Clean(c.InputsFile) != c.InputsFile || strings.ContainsAny(c.InputsFile, "\x00\r\n")) {
		return errors.New("OFFLINE_OPERATOR_INPUTS_REFERENCE_INVALID")
	}
	if p.FormatVersion != 1 || c.DockerBinary == "" || c.PluginDirectory == "" || !filepath.IsAbs(c.DockerBinary) || filepath.Clean(c.DockerBinary) != c.DockerBinary || !filepath.IsAbs(c.PluginDirectory) || filepath.Clean(c.PluginDirectory) != c.PluginDirectory || strings.ContainsAny(c.DockerBinary+c.PluginDirectory, "\x00\r\n") || c.CommandSeconds < p.CommandMinimum || c.CommandSeconds > p.CommandMaximum || c.OutputBytes < p.OutputMinimum || c.OutputBytes > p.OutputMaximum {
		return errors.New("OFFLINE_OPERATOR_COMPOSE_CONFIGURATION_INVALID")
	}
	return nil
}

type Engine struct {
	SocketFile                string                 `json:"socket_file"`
	Profile                   string                 `json:"profile"`
	DialSeconds               int                    `json:"dial_seconds"`
	RequestSeconds            int                    `json:"request_seconds"`
	LoadSeconds               int                    `json:"load_seconds"`
	ResponseBytes             int64                  `json:"response_bytes"`
	MaximumExpandedImageBytes int64                  `json:"maximum_expanded_image_bytes"`
	Utility                   *wire.UtilityResources `json:"utility,omitempty"`
}
type operatorDefaults struct {
	TimeoutSeconds      int    `json:"timeout_seconds"`
	MaximumJournalBytes int64  `json:"maximum_journal_bytes"`
	EngineDefaults      Engine `json:"engine_defaults"`
}

func defaults() operatorDefaults {
	var d operatorDefaults
	_ = wire.StrictJSON(wire.OperatorDefaults, &d)
	return d
}

func Defaults() Config {
	d := defaults()
	return Config{Verification: bundle.Defaults(), TimeoutSeconds: d.TimeoutSeconds, MaximumJournalBytes: d.MaximumJournalBytes}
}
func Load(raw, environment []byte) (Config, error) {
	c := Defaults()
	var members map[string]json.RawMessage
	if wire.StrictJSON(raw, &members) != nil {
		return c, errors.New("OFFLINE_OPERATOR_CONFIGURATION_INVALID")
	}
	_, engineSet := members["engine"]
	_, composeSet := members["compose"]
	utilitySet := false
	processSet := false
	if rawCompose, ok := members["compose"]; ok {
		var nested map[string]json.RawMessage
		if wire.StrictJSON(rawCompose, &nested) == nil {
			_, processSet = nested["process"]
		}
	}
	if rawEngine, ok := members["engine"]; ok {
		var nested map[string]json.RawMessage
		if wire.StrictJSON(rawEngine, &nested) == nil {
			_, utilitySet = nested["utility"]
		}
	}
	for _, line := range bytes.Split(environment, []byte("\n")) {
		if strings.HasPrefix(strings.TrimSpace(string(line)), "PROJECT_ONE_OFFLINE_OPERATOR_COMPOSE_PROCESS_") {
			processSet = true
		}
		if strings.HasPrefix(strings.TrimSpace(string(line)), "PROJECT_ONE_OFFLINE_OPERATOR_ENGINE_") {
			engineSet = true
		}
		if strings.HasPrefix(strings.TrimSpace(string(line)), "PROJECT_ONE_OFFLINE_OPERATOR_ENGINE_UTILITY_") {
			utilitySet = true
		}
		if strings.HasPrefix(strings.TrimSpace(string(line)), "PROJECT_ONE_OFFLINE_OPERATOR_COMPOSE_") {
			composeSet = true
		}
	}
	if engineSet {
		d := defaults().EngineDefaults
		if utilitySet {
			var policy wire.UtilityPolicy
			_ = wire.StrictJSON(wire.VolumeUtilityProfile, &policy)
			d.Utility = &policy.Defaults
		}
		c.Engine = &d
	}
	if composeSet {
		p := composePolicy()
		c.Compose = &Compose{CommandSeconds: p.CommandDefault, OutputBytes: p.OutputDefault}
		if processSet {
			var process wire.ProcessPolicy
			_ = wire.StrictJSON(wire.ProcessProfile, &process)
			c.Compose.Process = &process.Defaults
		}
	}
	if wire.StrictJSON(raw, &c) != nil {
		return c, errors.New("OFFLINE_OPERATOR_CONFIGURATION_INVALID")
	}
	fields := map[string]reflect.Value{}
	var collect func(reflect.Value, string)
	collect = func(v reflect.Value, prefix string) {
		typ := v.Type()
		for i := 0; i < v.NumField(); i++ {
			field := v.Field(i)
			name := strings.Split(typ.Field(i).Tag.Get("json"), ",")[0]
			if name == "-" {
				continue
			}
			if field.Kind() == reflect.Pointer {
				if field.IsNil() {
					continue
				}
				field = field.Elem()
			}
			if field.Kind() == reflect.Struct {
				nested := prefix
				if name == "engine" || name == "compose" || name == "utility" || name == "process" {
					nested += strings.ToUpper(name) + "_"
				}
				collect(field, nested)
				continue
			}
			fields["PROJECT_ONE_OFFLINE_OPERATOR_"+prefix+strings.ToUpper(name)] = field
		}
	}
	collect(reflect.ValueOf(&c).Elem(), "")
	seen := map[string]bool{}
	for _, line := range bytes.Split(environment, []byte("\n")) {
		s := strings.TrimSpace(string(line))
		if s == "" || strings.HasPrefix(s, "#") {
			continue
		}
		key, value, ok := strings.Cut(s, "=")
		field, known := fields[key]
		if !ok || !known || seen[key] || strings.ContainsAny(value, "\x00\r\n$`\"'") {
			return c, errors.New("OFFLINE_OPERATOR_ENVIRONMENT_INVALID")
		}
		seen[key] = true
		switch field.Kind() {
		case reflect.String:
			field.SetString(value)
		case reflect.Bool:
			if value != "true" && value != "false" {
				return c, errors.New("OFFLINE_OPERATOR_ENVIRONMENT_INVALID")
			}
			field.SetBool(value == "true")
		default:
			n, e := strconv.ParseInt(value, 10, 64)
			if e != nil {
				return c, errors.New("OFFLINE_OPERATOR_ENVIRONMENT_INVALID")
			}
			field.SetInt(n)
		}
	}
	if c.TimeoutSeconds < 1 || c.TimeoutSeconds > 86400 || c.MaximumJournalBytes < MinimumJournalBytes || c.MaximumJournalBytes > MaximumJournalBytes || c.Verification.Validate() != nil {
		return c, errors.New("OFFLINE_OPERATOR_CONFIGURATION_BOUNDS_INVALID")
	}
	if !wire.ValidSHA256(c.ExpectedArchiveSHA256) || !wire.ValidSHA256(c.ExpectedManifestSHA256) {
		return c, errors.New("OFFLINE_OPERATOR_INDEPENDENT_EXPECTED_DIGESTS_REQUIRED")
	}
	if c.Engine != nil {
		if e := c.Engine.Validate(); e != nil {
			return c, e
		}
	}
	if c.Compose != nil {
		if e := c.Compose.Validate(); e != nil {
			return c, e
		}
		if c.Engine == nil {
			return c, errors.New("OFFLINE_OPERATOR_ENGINE_CONFIGURATION_REQUIRED")
		}
	}
	return c, nil
}
func (e Engine) Validate() error {
	if e.Utility != nil {
		var p wire.UtilityPolicy
		if wire.StrictJSON(wire.VolumeUtilityProfile, &p) != nil || p.FormatVersion != 1 || e.Utility.MemoryBytes < p.Minimum.MemoryBytes || e.Utility.MemoryBytes > p.Maximum.MemoryBytes || e.Utility.MilliCPU < p.Minimum.MilliCPU || e.Utility.MilliCPU > p.Maximum.MilliCPU || e.Utility.PIDs < p.Minimum.PIDs || e.Utility.PIDs > p.Maximum.PIDs {
			return errors.New("OFFLINE_OPERATOR_UTILITY_CONFIGURATION_INVALID")
		}
	}
	var profile struct {
		Native string `json:"native_profile"`
		Local  string `json:"local_profile"`
	}
	_ = wire.UniqueJSON(wire.EngineProfile, &profile)
	if e.SocketFile == "" || !strings.HasPrefix(e.SocketFile, "/") || strings.ContainsAny(e.SocketFile, "\x00\r\n") || (e.Profile != profile.Native && e.Profile != profile.Local) || e.DialSeconds < 1 || e.DialSeconds > 30 || e.RequestSeconds < 2 || e.RequestSeconds > 120 || e.LoadSeconds < 5 || e.LoadSeconds > 7200 || e.DialSeconds > e.RequestSeconds || e.RequestSeconds > e.LoadSeconds || e.ResponseBytes < 4096 || e.ResponseBytes > 16777216 || e.MaximumExpandedImageBytes < 1048576 || e.MaximumExpandedImageBytes > 1099511627776 {
		return errors.New("OFFLINE_OPERATOR_ENGINE_CONFIGURATION_INVALID")
	}
	return nil
}
func (c Config) Fingerprint() string { raw, _ := json.Marshal(c); return wire.Digest(raw) }
