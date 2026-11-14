package bootstrap

import (
	"bytes"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"unicode/utf8"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	strict "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	release "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// DamengPolicy is a standalone local-operator projection of existing component
// policy, not another server/database selection authority. Runtime uses exactly
// config.RuntimePolicy and its validation. No transport, DSN or invented vendor
// selection is supplied to identity by this native operator configuration.
type DamengPolicy struct {
	FormatVersion         int                   `json:"format_version"`
	Runtime               *config.RuntimePolicy `json:"runtime"`
	PasswordBlocklistFile string                `json:"password_blocklist_file"`
	LicenseTrustFile      string                `json:"license_trust_file"`
}

// DamengPolicyTemplate is offline authoring material. The missing required
// blocklist reference deliberately prevents executing it without explicit input.
func DamengPolicyTemplate() DamengPolicy {
	runtime := config.DefaultRuntimePolicy()
	return DamengPolicy{FormatVersion: 1, Runtime: &runtime}
}

func (DamengPolicy) String() string   { return "DAMENG_OPERATOR_POLICY_REDACTED" }
func (DamengPolicy) GoString() string { return "DAMENG_OPERATOR_POLICY_REDACTED" }
func policyPath(p string) bool {
	return p != "" && filepath.IsAbs(p) && filepath.Clean(p) == p && !strings.ContainsAny(p, "\x00\n\r")
}
func (p DamengPolicy) Validate() error {
	if p.FormatVersion != 1 || p.Runtime == nil || p.Runtime.Validate() != nil || !policyPath(p.PasswordBlocklistFile) || p.LicenseTrustFile != "" && !policyPath(p.LicenseTrustFile) {
		return errors.New("DAMENG_OPERATOR_POLICY_INVALID")
	}
	return nil
}
func (p DamengPolicy) Configuration() (config.Config, error) {
	if e := p.Validate(); e != nil {
		return config.Config{}, e
	}
	runtime := *p.Runtime
	c := config.Config{Runtime: &runtime}
	c.Security.PasswordBlocklistFile = p.PasswordBlocklistFile
	if p.LicenseTrustFile != "" {
		c.Licensing = &config.LicensingConfiguration{TrustFile: p.LicenseTrustFile}
	}
	return c, nil
}
func LoadDamengPolicy(path, environment string) (DamengPolicy, error) {
	load := func(path string) (DamengPolicy, error) {
		var p DamengPolicy
		raw, e := readPrivateBytes(path)
		if e != nil || decodeDamengPolicy(raw, &p) != nil {
			return p, errors.New("DAMENG_OPERATOR_POLICY_INVALID")
		}
		return p, p.Validate()
	}
	p, e := load(path)
	if e != nil || environment == "" {
		return p, e
	}
	raw, e := readPrivateBytes(environment)
	if e != nil {
		return DamengPolicy{}, errors.New("DAMENG_OPERATOR_ENVIRONMENT_INVALID")
	}
	seen := false
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok || seen || key != "PROJECT_ONE_DAMENG_OPERATOR_POLICY_FILE" || !policyPath(value) {
			return DamengPolicy{}, errors.New("DAMENG_OPERATOR_ENVIRONMENT_INVALID")
		}
		seen = true
		path = value
	}
	if !seen {
		return DamengPolicy{}, errors.New("DAMENG_OPERATOR_ENVIRONMENT_INVALID")
	}
	return load(path)
}

// RuntimePolicy retains its existing JSON projection, including exported field
// names where no JSON tag exists. The dataset wire decoder requires explicit
// tags and therefore cannot decode this configuration. StrictJSON rejects null,
// duplicates, unknown fields and trailing data; comparing the typed roundtrip
// additionally refuses case aliases and missing fields (including zero values).
// Defaults and value validation remain exclusively with config.RuntimePolicy.
func decodeDamengPolicy(raw []byte, out *DamengPolicy) error {
	bad := errors.New("DAMENG_OPERATOR_POLICY_INVALID")
	if !utf8.Valid(raw) || !strict.ValidJSONUnicode(raw) || release.StrictJSON(raw, out) != nil {
		return bad
	}
	canonical, e := json.Marshal(out)
	if e != nil {
		return bad
	}
	var original, encoded any
	for _, pair := range []struct {
		raw []byte
		out *any
	}{{raw, &original}, {canonical, &encoded}} {
		decoder := json.NewDecoder(bytes.NewReader(pair.raw))
		decoder.UseNumber()
		if decoder.Decode(pair.out) != nil {
			return bad
		}
	}
	if !reflect.DeepEqual(original, encoded) {
		return bad
	}
	return nil
}
