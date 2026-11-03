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

// BackupRequest is an operation boundary separate from immutable installation
// configuration. No ambient environment variables or secrets are consumed.
type BackupRequest struct {
	FormatVersion    int    `json:"format_version"`
	DestinationRoot  string `json:"destination_root"`
	Reason           string `json:"reason"`
	MaximumBytes     int64  `json:"maximum_bytes"`
	MaximumEntries   int    `json:"maximum_entries"`
	StreamSeconds    int    `json:"stream_seconds"`
	ObserveSeconds   int    `json:"observe_seconds"`
	PollMilliseconds int    `json:"poll_milliseconds"`
}

func BackupDefaults() BackupRequest {
	return BackupRequest{FormatVersion: 1, MaximumBytes: 64 << 30, MaximumEntries: 100000, StreamSeconds: 1800, ObserveSeconds: 1800, PollMilliseconds: 250}
}

func (r BackupRequest) Validate() error {
	if r.FormatVersion != 1 || r.DestinationRoot == "" || !filepath.IsAbs(r.DestinationRoot) || filepath.Clean(r.DestinationRoot) != r.DestinationRoot || len(r.DestinationRoot) > 1024 || strings.ContainsAny(r.DestinationRoot, "\x00\r\n") || !utf8.ValidString(r.Reason) || strings.TrimSpace(r.Reason) == "" || utf8.RuneCountInString(r.Reason) > 500 || strings.ContainsFunc(r.Reason, unicode.IsControl) || r.MaximumBytes < 1<<20 || r.MaximumBytes > 1<<40 || r.MaximumEntries < 4 || r.MaximumEntries > 1000000 || r.StreamSeconds < 5 || r.StreamSeconds > 7200 || r.ObserveSeconds < 5 || r.ObserveSeconds > 7200 || r.PollMilliseconds < 50 || r.PollMilliseconds > 5000 || r.PollMilliseconds > r.ObserveSeconds*1000 {
		return errors.New("OFFLINE_OPERATOR_BACKUP_CONFIGURATION_INVALID")
	}
	return nil
}

func LoadBackupRequest(raw, environment []byte) (BackupRequest, error) {
	r := BackupDefaults()
	if wire.StrictJSON(raw, &r) != nil {
		return r, errors.New("OFFLINE_OPERATOR_BACKUP_CONFIGURATION_INVALID")
	}
	seen := map[string]bool{}
	for _, line := range bytes.Split(environment, []byte("\n")) {
		s := strings.TrimSpace(string(line))
		if s == "" || strings.HasPrefix(s, "#") {
			continue
		}
		key, value, ok := strings.Cut(s, "=")
		if !ok || seen[key] || strings.ContainsAny(value, "\x00\r\n$`\"'") {
			return r, errors.New("OFFLINE_OPERATOR_BACKUP_ENVIRONMENT_INVALID")
		}
		seen[key] = true
		if key == "PROJECT_ONE_OFFLINE_BACKUP_DESTINATION_ROOT" {
			r.DestinationRoot = value
			continue
		}
		if key == "PROJECT_ONE_OFFLINE_BACKUP_REASON" {
			r.Reason = value
			continue
		}
		n, e := strconv.ParseInt(value, 10, 64)
		if e != nil {
			return r, errors.New("OFFLINE_OPERATOR_BACKUP_ENVIRONMENT_INVALID")
		}
		switch key {
		case "PROJECT_ONE_OFFLINE_BACKUP_MAXIMUM_BYTES":
			r.MaximumBytes = n
		case "PROJECT_ONE_OFFLINE_BACKUP_MAXIMUM_ENTRIES":
			if n < 4 || n > 1000000 {
				return r, errors.New("OFFLINE_OPERATOR_BACKUP_ENVIRONMENT_INVALID")
			}
			r.MaximumEntries = int(n)
		case "PROJECT_ONE_OFFLINE_BACKUP_STREAM_SECONDS":
			if n < 5 || n > 7200 {
				return r, errors.New("OFFLINE_OPERATOR_BACKUP_ENVIRONMENT_INVALID")
			}
			r.StreamSeconds = int(n)
		case "PROJECT_ONE_OFFLINE_BACKUP_OBSERVE_SECONDS":
			if n < 5 || n > 7200 {
				return r, errors.New("OFFLINE_OPERATOR_BACKUP_ENVIRONMENT_INVALID")
			}
			r.ObserveSeconds = int(n)
		case "PROJECT_ONE_OFFLINE_BACKUP_POLL_MILLISECONDS":
			if n < 50 || n > 5000 {
				return r, errors.New("OFFLINE_OPERATOR_BACKUP_ENVIRONMENT_INVALID")
			}
			r.PollMilliseconds = int(n)
		default:
			return r, errors.New("OFFLINE_OPERATOR_BACKUP_ENVIRONMENT_INVALID")
		}
	}
	return r, r.Validate()
}
