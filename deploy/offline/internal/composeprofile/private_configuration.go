package composeprofile

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

const PrivateConfigurationMaximumRequestBytes = 65536
const privateConfigurationInvalid = "OFFLINE_COMPOSE_PRIVATE_CONFIGURATION_INVALID"

var privateIdentity = regexp.MustCompile(`^[a-f0-9]{32}$`)

type PrivateConfigurationFile struct {
	Name   string `json:"name"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
}

// Immutable file identities and operation identity are request data, never env
// overrides. Paths, numeric ownership and budgets use one explicit typed owner.
type PrivateConfiguration struct {
	FormatVersion     int                        `json:"format_version"`
	ScopeID           string                     `json:"scope_id"`
	OperationID       string                     `json:"operation_id"`
	VolumeName        string                     `json:"volume_name"`
	SourceRoot        string                     `json:"source_root"`
	TargetRoot        string                     `json:"target_root"`
	SourceUID         int                        `json:"source_uid"`
	SourceGID         int                        `json:"source_gid"`
	UID               int                        `json:"uid"`
	GID               int                        `json:"gid"`
	MaximumFileBytes  int64                      `json:"maximum_file_bytes"`
	MaximumTotalBytes int64                      `json:"maximum_total_bytes"`
	TimeoutSeconds    int                        `json:"timeout_seconds"`
	Files             []PrivateConfigurationFile `json:"files"`
}

func privateAbsolute(name string) bool {
	return filepath.IsAbs(name) && filepath.Clean(name) == name && name != "/" && !strings.ContainsFunc(name, unicode.IsControl) && !strings.Contains(name, "\\")
}

func (p PrivateConfiguration) Validate() error {
	bad := errors.New(privateConfigurationInvalid)
	if p.FormatVersion != 1 || !privateIdentity.MatchString(p.ScopeID) || !privateIdentity.MatchString(p.OperationID) || p.VolumeName != "projectone-"+p.ScopeID+"-control-config" || !privateAbsolute(p.SourceRoot) || !privateAbsolute(p.TargetRoot) || p.SourceRoot == p.TargetRoot || strings.HasPrefix(p.SourceRoot, p.TargetRoot+"/") || strings.HasPrefix(p.TargetRoot, p.SourceRoot+"/") {
		return bad
	}
	if p.UID < 1 || p.GID < 1 || p.UID > math.MaxInt32 || p.GID > math.MaxInt32 || p.SourceUID < 0 || p.SourceGID < 0 || p.SourceUID > math.MaxInt32 || p.SourceGID > math.MaxInt32 || p.MaximumFileBytes < 1 || p.MaximumFileBytes > 4<<20 || p.MaximumTotalBytes < p.MaximumFileBytes || p.MaximumTotalBytes > 32<<20 || p.TimeoutSeconds < 1 || p.TimeoutSeconds > 300 || len(p.Files) < 1 || len(p.Files) > 256 {
		return bad
	}
	var total int64
	previous := ""
	for _, f := range p.Files {
		if !machineBasename.MatchString(f.Name) || f.Name <= previous || f.Bytes < 1 || f.Bytes > p.MaximumFileBytes || !wire.ValidSHA256(f.SHA256) {
			return bad
		}
		previous = f.Name
		total += f.Bytes
	}
	if total > p.MaximumTotalBytes {
		return bad
	}
	return nil
}

func LoadPrivateConfiguration(raw, overlay []byte) (PrivateConfiguration, error) {
	bad := errors.New(privateConfigurationInvalid)
	keys := []string{"format_version", "scope_id", "operation_id", "volume_name", "source_root", "target_root", "source_uid", "source_gid", "uid", "gid", "maximum_file_bytes", "maximum_total_bytes", "timeout_seconds", "files"}
	if len(raw) < 1 || len(raw) > PrivateConfigurationMaximumRequestBytes || !utf8.Valid(raw) {
		return PrivateConfiguration{}, bad
	}
	members, ok := machineMembers(raw, keys...)
	if !ok {
		return PrivateConfiguration{}, bad
	}
	for _, v := range members {
		if bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
			return PrivateConfiguration{}, bad
		}
	}
	var p PrivateConfiguration
	if wire.StrictJSON(raw, &p) != nil || p.Validate() != nil {
		return PrivateConfiguration{}, bad
	}
	var entries struct {
		Files []json.RawMessage `json:"files"`
	}
	if json.Unmarshal(raw, &entries) != nil {
		return PrivateConfiguration{}, bad
	}
	for _, f := range entries.Files {
		if _, ok := machineMembers(f, "name", "bytes", "sha256"); !ok {
			return PrivateConfiguration{}, bad
		}
	}
	if len(overlay) > maximumEnvironmentBytes || !utf8.Valid(overlay) || bytes.IndexByte(overlay, 0) >= 0 {
		return PrivateConfiguration{}, bad
	}
	seen := map[string]bool{}
	for _, line := range strings.Split(string(overlay), "\n") {
		line = strings.TrimSuffix(line, "\r")
		if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok || seen[key] || value == "" || strings.TrimSpace(value) != value || strings.ContainsAny(line, "\r\x00") {
			return PrivateConfiguration{}, bad
		}
		seen[key] = true
		if strings.HasPrefix(value, `"`) {
			var decoded string
			if wire.StrictJSON([]byte(value), &decoded) != nil || decoded == "" {
				return PrivateConfiguration{}, bad
			}
			value = decoded
		}
		if key == "PROJECT_ONE_PRIVATE_SOURCE_ROOT" {
			p.SourceRoot = value
			continue
		}
		if key == "PROJECT_ONE_PRIVATE_TARGET_ROOT" {
			p.TargetRoot = value
			continue
		}
		n, e := strconv.ParseInt(value, 10, 64)
		if e != nil || n < 0 || strconv.FormatInt(n, 10) != value {
			return PrivateConfiguration{}, bad
		}
		switch key {
		case "PROJECT_ONE_PRIVATE_SOURCE_UID":
			if n > math.MaxInt32 {
				return PrivateConfiguration{}, bad
			}
			p.SourceUID = int(n)
		case "PROJECT_ONE_PRIVATE_SOURCE_GID":
			if n > math.MaxInt32 {
				return PrivateConfiguration{}, bad
			}
			p.SourceGID = int(n)
		case "PROJECT_ONE_PRIVATE_UID":
			if n > math.MaxInt32 {
				return PrivateConfiguration{}, bad
			}
			p.UID = int(n)
		case "PROJECT_ONE_PRIVATE_GID":
			if n > math.MaxInt32 {
				return PrivateConfiguration{}, bad
			}
			p.GID = int(n)
		case "PROJECT_ONE_PRIVATE_MAXIMUM_FILE_BYTES":
			p.MaximumFileBytes = n
		case "PROJECT_ONE_PRIVATE_MAXIMUM_TOTAL_BYTES":
			p.MaximumTotalBytes = n
		case "PROJECT_ONE_PRIVATE_TIMEOUT_SECONDS":
			if n > 300 {
				return PrivateConfiguration{}, bad
			}
			p.TimeoutSeconds = int(n)
		default:
			return PrivateConfiguration{}, bad
		}
	}
	if p.Validate() != nil {
		return PrivateConfiguration{}, bad
	}
	return p, nil
}

func (p PrivateConfiguration) Fingerprint() string {
	raw, _ := json.Marshal(p)
	return wire.Digest(raw)
}
