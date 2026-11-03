// Package bundle exposes read-only verification and bounded preparation to the operator.
package bundle

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"unicode/utf8"

	"github.com/Passion-Flow/ownsolid/tools/release/offline/internal/factory"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

type Verification struct {
	MountRoot             string `json:"mount_root"`
	TrustFile             string `json:"trust_file"`
	MaximumManifestBytes  int64  `json:"maximum_manifest_bytes"`
	MaximumAssets         int    `json:"maximum_assets"`
	MaximumAssetBytes     int64  `json:"maximum_asset_bytes"`
	MaximumTotalBytes     int64  `json:"maximum_total_bytes"`
	MaximumMetadataBytes  int64  `json:"maximum_metadata_bytes"`
	MaximumArchiveEntries int    `json:"maximum_archive_entries"`
	AllowTestFixture      bool   `json:"allow_test_fixture"`
	MinimumFreeBytes      int64  `json:"minimum_free_bytes,omitempty"`
}
type Result = factory.Result
type ImageSource = factory.ImageSource
type ImageInventory = factory.ImageInventory
type ImportReceipt = factory.ImportReceipt

// Preserve seeded defaults while rejecting null, aliases and unknown fields at
// every nested public verification input, including consumers outside the CLI.
type verificationJSON Verification

func (p *Verification) UnmarshalJSON(raw []byte) error {
	v := verificationJSON(*p)
	if wire.StrictJSON(raw, &v) != nil {
		return errors.New("RELEASE_CONFIGURATION_INVALID")
	}
	if _, e := factory.LoadConfig(raw, nil); e != nil {
		return e
	}
	*p = Verification(v)
	return nil
}

func OpenImageSource(ctx context.Context, p Verification, filename string, asset wire.Asset, maximumExpandedBytes int64) (*ImageSource, error) {
	c, e := p.config()
	if e != nil {
		return nil, e
	}
	return factory.OpenImageSource(ctx, c, filename, asset, maximumExpandedBytes)
}

func Defaults() Verification {
	var p verificationJSON
	_ = json.Unmarshal(wire.FactoryDefaults, &p)
	return Verification(p)
}
func (p Verification) config() (factory.Config, error) {
	return factory.LoadConfig(factory.Encode(p), nil)
}
func (p Verification) Validate() error { _, e := p.config(); return e }

// Only public verification fields may enter through this explicit component
// env file. Parsing, booleans, precedence and ranges remain factory-owned.
func LoadVerification(raw, environment []byte) (Verification, error) {
	p := Defaults()
	if !utf8.Valid(environment) || wire.StrictJSON(raw, &p) != nil {
		return Verification{}, errors.New("RELEASE_CONFIGURATION_INVALID")
	}
	allowed := map[string]bool{}
	typ := reflect.TypeOf(p)
	for i := 0; i < typ.NumField(); i++ {
		name := strings.Split(typ.Field(i).Tag.Get("json"), ",")[0]
		allowed["PROJECT_ONE_OFFLINE_FACTORY_"+strings.ToUpper(name)] = true
	}
	for _, line := range bytes.Split(environment, []byte("\n")) {
		value := strings.TrimSpace(string(line))
		if value == "" || strings.HasPrefix(value, "#") {
			continue
		}
		key, _, ok := strings.Cut(value, "=")
		if !ok || !allowed[key] {
			return Verification{}, errors.New("RELEASE_ENVIRONMENT_INVALID")
		}
	}
	c, e := factory.LoadConfig(factory.Encode(p), environment)
	if e != nil {
		return Verification{}, e
	}
	// This conversion reads only the internally generated typed Config, after
	// public-field admission above. Untrusted JSON always uses the strict decoder.
	var v verificationJSON
	if json.Unmarshal(factory.Encode(c), &v) != nil {
		return Verification{}, errors.New("RELEASE_CONFIGURATION_INVALID")
	}
	out := Verification(v)
	return out, out.Validate()
}

func Inspect(ctx context.Context, p Verification, source, archiveSHA, manifestSHA string) (Result, error) {
	c, e := p.config()
	if e != nil {
		return Result{}, e
	}
	return factory.InspectExpected(ctx, c, source, archiveSHA, manifestSHA)
}
func Unpack(ctx context.Context, p Verification, source, destination, archiveSHA, manifestSHA string) (Result, error) {
	c, e := p.config()
	if e != nil {
		return Result{}, e
	}
	return factory.Unpack(ctx, c, source, destination, archiveSHA, manifestSHA)
}
func VerifyPrepared(ctx context.Context, p Verification, directory, archiveSHA, manifestSHA string) (Result, error) {
	c, e := p.config()
	if e != nil {
		return Result{}, e
	}
	return factory.VerifyPrepared(ctx, c, directory, archiveSHA, manifestSHA)
}
func ReadProtectedConfiguration(name string) ([]byte, error) { return factory.ReadConfigFile(name) }
func ErrorCode(e error) string                               { return factory.ErrorCode(e) }
