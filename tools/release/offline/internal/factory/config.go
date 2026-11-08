package factory

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"unicode/utf8"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

type Config struct {
	current               bool
	MountRoot             string `json:"mount_root"`
	ArtifactRoot          string `json:"artifact_root"`
	ManifestFile          string `json:"manifest_file"`
	TrustFile             string `json:"trust_file"`
	PrivateKeyFile        string `json:"private_key_file"`
	OutputFile            string `json:"output_file"`
	MaximumManifestBytes  int64  `json:"maximum_manifest_bytes"`
	MaximumAssets         int    `json:"maximum_assets"`
	MaximumAssetBytes     int64  `json:"maximum_asset_bytes"`
	MaximumTotalBytes     int64  `json:"maximum_total_bytes"`
	MaximumMetadataBytes  int64  `json:"maximum_metadata_bytes"`
	MaximumArchiveEntries int    `json:"maximum_archive_entries"`
	TimeoutSeconds        int    `json:"timeout_seconds"`
	CompressionLevel      int    `json:"compression_level"`
	AllowTestFixture      bool   `json:"allow_test_fixture"`
	RequireRKE2           bool   `json:"require_rke2,omitempty"`
	RequireRKE2Components bool   `json:"require_rke2_components,omitempty"`
	MinimumFreeBytes      int64  `json:"minimum_free_bytes,omitempty"`
}

func LoadConfig(raw, environment []byte) (Config, error) {
	var c Config
	if !utf8.Valid(raw) || wire.StrictJSON(wire.FactoryDefaults, &c) != nil || wire.StrictJSON(raw, &c) != nil {
		return c, errors.New("RELEASE_CONFIGURATION_INVALID")
	}
	v := reflect.ValueOf(&c).Elem()
	typ := v.Type()
	keys := map[string]int{}
	fields := map[string]bool{}
	for i := 0; i < v.NumField(); i++ {
		if !typ.Field(i).IsExported() {
			continue
		}
		name := strings.Split(typ.Field(i).Tag.Get("json"), ",")[0]
		keys["PROJECT_ONE_OFFLINE_FACTORY_"+strings.ToUpper(name)] = i
		fields[name] = true
	}
	var supplied map[string]json.RawMessage
	if json.Unmarshal(raw, &supplied) != nil || supplied == nil {
		return c, errors.New("RELEASE_CONFIGURATION_INVALID")
	}
	for name, value := range supplied {
		if !fields[name] || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return c, errors.New("RELEASE_CONFIGURATION_INVALID")
		}
	}
	seen := map[string]bool{}
	for _, line := range bytes.Split(environment, []byte("\n")) {
		s := strings.TrimSpace(string(line))
		if s == "" || strings.HasPrefix(s, "#") {
			continue
		}
		key, value, ok := strings.Cut(s, "=")
		index, known := keys[key]
		if !ok || !known || seen[key] || strings.ContainsAny(value, "\x00\r\n$`\"'") {
			return c, errors.New("RELEASE_ENVIRONMENT_INVALID")
		}
		seen[key] = true
		field := v.Field(index)
		switch field.Kind() {
		case reflect.String:
			field.SetString(value)
		case reflect.Bool:
			if value != "true" && value != "false" {
				return c, errors.New("RELEASE_ENVIRONMENT_INVALID")
			}
			field.SetBool(value == "true")
		default:
			n, e := strconv.ParseInt(value, 10, 64)
			if e != nil {
				return c, errors.New("RELEASE_ENVIRONMENT_INVALID")
			}
			field.SetInt(n)
		}
	}
	if c.MaximumManifestBytes < 1024 || c.MaximumManifestBytes > 4194304 || c.MaximumAssets < 1 || c.MaximumAssets > 4096 || c.MaximumAssetBytes < 1 || c.MaximumAssetBytes > 1<<40 || c.MaximumTotalBytes < c.MaximumAssetBytes || c.MaximumTotalBytes > 1<<42 || c.MaximumMetadataBytes < 1024 || c.MaximumMetadataBytes > 16777216 || c.MaximumArchiveEntries < 16 || c.MaximumArchiveEntries > 131072 || c.TimeoutSeconds < 1 || c.TimeoutSeconds > 86400 || c.CompressionLevel < 1 || c.CompressionLevel > 9 || c.MinimumFreeBytes < 0 || c.MinimumFreeBytes > 1<<50 {
		return c, errors.New("RELEASE_CONFIGURATION_BOUNDS_INVALID")
	}
	return c, nil
}

func Encode(v any) []byte { raw, _ := json.Marshal(v); return append(raw, '\n') }
