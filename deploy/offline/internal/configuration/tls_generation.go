package configuration

import (
	"bytes"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// TLSGenerationRequest owns explicit protected references, never certificate or key bytes.
type TLSGenerationRequest struct {
	FormatVersion   int    `json:"format_version"`
	CertificateFile string `json:"certificate_file"`
	PrivateKeyFile  string `json:"private_key_file"`
	TrustBundleFile string `json:"trust_bundle_file"`
	Reason          string `json:"reason"`
	Target          string `json:"target,omitempty"`
}

func (r TLSGenerationRequest) Validate() error {
	bad := errors.New("OFFLINE_OPERATOR_TLS_GENERATION_CONFIGURATION_INVALID")
	if r.FormatVersion != 1 && r.FormatVersion != 2 || r.FormatVersion == 1 && r.Target != "" || r.FormatVersion == 2 && r.Target != "browser" && r.Target != "machine" || !utf8.ValidString(r.Reason) || strings.TrimSpace(r.Reason) == "" || utf8.RuneCountInString(r.Reason) > 500 || strings.ContainsFunc(r.Reason, unicode.IsControl) {
		return bad
	}
	seen := map[string]bool{}
	for _, p := range []string{r.CertificateFile, r.PrivateKeyFile, r.TrustBundleFile} {
		if !filepath.IsAbs(p) || filepath.Clean(p) != p || len(p) > 1024 || strings.ContainsAny(p, "\x00\r\n") || !utf8.ValidString(p) || seen[p] {
			return bad
		}
		seen[p] = true
	}
	return nil
}
func LoadTLSGenerationRequest(raw, environment []byte) (TLSGenerationRequest, error) {
	r := TLSGenerationRequest{}
	var members map[string]json.RawMessage
	if !utf8.Valid(raw) || wire.StrictJSON(raw, &members) != nil || wire.StrictJSON(raw, &r) != nil {
		return r, errors.New("OFFLINE_OPERATOR_TLS_GENERATION_CONFIGURATION_INVALID")
	}
	allowed := map[string]bool{"format_version": true, "certificate_file": true, "private_key_file": true, "trust_bundle_file": true, "reason": true}
	if r.FormatVersion == 2 {
		allowed["target"] = true
		if len(members) != len(allowed) || r.Validate() != nil {
			return r, errors.New("OFFLINE_OPERATOR_TLS_GENERATION_CONFIGURATION_INVALID")
		}
	}
	semanticNames := map[string]bool{}
	for name := range members {
		canonical := name
		if r.FormatVersion != 2 {
			// Retain the old Go JSON decoder's single case-folded spelling of
			// a V1 field, while refusing two spellings of the same authority.
			canonical = ""
			for field := range allowed {
				if strings.EqualFold(field, name) {
					canonical = field
					break
				}
			}
		}
		if !allowed[canonical] || semanticNames[canonical] {
			return r, errors.New("OFFLINE_OPERATOR_TLS_GENERATION_CONFIGURATION_INVALID")
		}
		semanticNames[canonical] = true
	}
	// Historical V1 may fill omitted/empty fields through its explicit four-key
	// overlay. Only V2 requires a complete independently valid base snapshot.
	if r.FormatVersion == 2 && !utf8.Valid(environment) {
		return r, errors.New("OFFLINE_OPERATOR_TLS_GENERATION_ENVIRONMENT_INVALID")
	}
	seen := map[string]bool{}
	for _, line := range bytes.Split(environment, []byte("\n")) {
		s := strings.TrimSpace(string(line))
		if s == "" || strings.HasPrefix(s, "#") {
			continue
		}
		key, value, ok := strings.Cut(s, "=")
		if !ok || seen[key] || strings.ContainsAny(value, "\x00\r\n$`\"'") {
			return r, errors.New("OFFLINE_OPERATOR_TLS_GENERATION_ENVIRONMENT_INVALID")
		}
		seen[key] = true
		switch key {
		case "PROJECT_ONE_OFFLINE_TLS_CERTIFICATE_FILE":
			r.CertificateFile = value
		case "PROJECT_ONE_OFFLINE_TLS_PRIVATE_KEY_FILE":
			r.PrivateKeyFile = value
		case "PROJECT_ONE_OFFLINE_TLS_TRUST_BUNDLE_FILE":
			r.TrustBundleFile = value
		case "PROJECT_ONE_OFFLINE_TLS_REASON":
			r.Reason = value
		case "PROJECT_ONE_OFFLINE_TLS_TARGET":
			if r.FormatVersion != 2 || value != "browser" && value != "machine" {
				return r, errors.New("OFFLINE_OPERATOR_TLS_GENERATION_ENVIRONMENT_INVALID")
			}
			r.Target = value
		default:
			return r, errors.New("OFFLINE_OPERATOR_TLS_GENERATION_ENVIRONMENT_INVALID")
		}
	}
	return r, r.Validate()
}
