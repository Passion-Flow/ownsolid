package configuration

import (
	"bytes"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

type RestoreSourceRequest struct {
	FormatVersion  int    `json:"format_version"`
	BackupRoot     string `json:"backup_root"`
	ManifestSHA256 string `json:"manifest_sha256"`
	MaximumBytes   int64  `json:"maximum_bytes"`
	MaximumEntries int    `json:"maximum_entries"`
}

func RestoreSourceDefaults() RestoreSourceRequest {
	backup := BackupDefaults()
	return RestoreSourceRequest{FormatVersion: 1, MaximumBytes: backup.MaximumBytes, MaximumEntries: backup.MaximumEntries}
}

func (r RestoreSourceRequest) Validate() error {
	if r.FormatVersion != 1 || r.BackupRoot == "" || !utf8.ValidString(r.BackupRoot) || !filepath.IsAbs(r.BackupRoot) || filepath.Clean(r.BackupRoot) != r.BackupRoot || len(r.BackupRoot) > 1024 || strings.ContainsFunc(r.BackupRoot, unicode.IsControl) || !wire.ValidSHA256(r.ManifestSHA256) || r.MaximumBytes < 1<<20 || r.MaximumBytes > 1<<40 || r.MaximumEntries < 4 || r.MaximumEntries > 1000000 {
		return errors.New("OFFLINE_OPERATOR_RESTORE_SOURCE_CONFIGURATION_INVALID")
	}
	return nil
}

func LoadRestoreSourceRequest(raw, environment []byte) (RestoreSourceRequest, error) {
	r := RestoreSourceDefaults()
	if wire.StrictJSON(raw, &r) != nil {
		return r, errors.New("OFFLINE_OPERATOR_RESTORE_SOURCE_CONFIGURATION_INVALID")
	}
	seen := map[string]bool{}
	for _, line := range bytes.Split(environment, []byte("\n")) {
		s := strings.TrimSpace(string(line))
		if s == "" || strings.HasPrefix(s, "#") {
			continue
		}
		key, value, ok := strings.Cut(s, "=")
		if !ok || seen[key] || strings.ContainsAny(value, "\x00\r\n$`\"'") {
			return r, errors.New("OFFLINE_OPERATOR_RESTORE_SOURCE_ENVIRONMENT_INVALID")
		}
		seen[key] = true
		switch key {
		case "PROJECT_ONE_OFFLINE_RESTORE_SOURCE_BACKUP_ROOT":
			r.BackupRoot = value
		case "PROJECT_ONE_OFFLINE_RESTORE_SOURCE_MANIFEST_SHA256":
			r.ManifestSHA256 = value
		case "PROJECT_ONE_OFFLINE_RESTORE_SOURCE_MAXIMUM_BYTES", "PROJECT_ONE_OFFLINE_RESTORE_SOURCE_MAXIMUM_ENTRIES":
			n, e := strconv.ParseInt(value, 10, 64)
			if e != nil || key == "PROJECT_ONE_OFFLINE_RESTORE_SOURCE_MAXIMUM_ENTRIES" && (n < 4 || n > 1000000) {
				return r, errors.New("OFFLINE_OPERATOR_RESTORE_SOURCE_ENVIRONMENT_INVALID")
			}
			if key == "PROJECT_ONE_OFFLINE_RESTORE_SOURCE_MAXIMUM_BYTES" {
				r.MaximumBytes = n
			} else {
				r.MaximumEntries = int(n)
			}
		default:
			return r, errors.New("OFFLINE_OPERATOR_RESTORE_SOURCE_ENVIRONMENT_INVALID")
		}
	}
	return r, r.Validate()
}
