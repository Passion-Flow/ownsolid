package configuration

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/netip"
	"path/filepath"
	"strconv"
	"strings"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// Operation inputs never change the original installation configuration digest.
// BackupSource admits an existing complete backup, not permission to create one.
type CumulativeUpgradeRequest struct {
	MVPConfigurationFiles   string               `json:"mvp_configuration_files,omitempty"`
	FormatVersion           int                  `json:"format_version"`
	PreparedRoot            string               `json:"prepared_root"`
	ArchiveSHA256           string               `json:"expected_archive_sha256"`
	ManifestSHA256          string               `json:"expected_manifest_sha256"`
	BackupSource            RestoreSourceRequest `json:"backup_source"`
	PageSize                int                  `json:"page_size"`
	OperationSeconds        int                  `json:"operation_seconds"`
	ReadinessDialAddress    string               `json:"readiness_dial_address"`
	ReadinessCAName         string               `json:"readiness_ca_name"`
	ReadinessTimeoutSeconds int                  `json:"readiness_timeout_seconds"`
}

func CumulativeUpgradeDefaults() CumulativeUpgradeRequest {
	return CumulativeUpgradeRequest{FormatVersion: 1, BackupSource: RestoreSourceDefaults(), PageSize: 50, OperationSeconds: 5, ReadinessTimeoutSeconds: 5}
}
func (r CumulativeUpgradeRequest) Validate() error {
	bad := errors.New("OFFLINE_OPERATOR_CUMULATIVE_UPGRADE_CONFIGURATION_INVALID")
	addr, e := netip.ParseAddrPort(r.ReadinessDialAddress)
	if r.MVPConfigurationFiles != "" && (!filepath.IsAbs(r.MVPConfigurationFiles) || filepath.Clean(r.MVPConfigurationFiles) != r.MVPConfigurationFiles || len(r.MVPConfigurationFiles) > 1024 || strings.ContainsAny(r.MVPConfigurationFiles, "\x00\r\n")) {
		return bad
	}
	if r.FormatVersion != 1 || r.PreparedRoot == "" || !filepath.IsAbs(r.PreparedRoot) || filepath.Clean(r.PreparedRoot) != r.PreparedRoot || len(r.PreparedRoot) > 1024 || strings.ContainsAny(r.PreparedRoot, "\x00\r\n") || !wire.ValidSHA256(r.ArchiveSHA256) || !wire.ValidSHA256(r.ManifestSHA256) || r.BackupSource.Validate() != nil || r.PageSize < 1 || r.PageSize > 100 || r.OperationSeconds < 1 || r.OperationSeconds > 30 || r.ReadinessTimeoutSeconds < 1 || r.ReadinessTimeoutSeconds > 60 || e != nil || !addr.Addr().IsLoopback() || addr.Addr().Zone() != "" || addr.Port() == 0 || addr.String() != r.ReadinessDialAddress || r.ReadinessCAName != "" && (!wire.SafePath(r.ReadinessCAName) || strings.ContainsAny(r.ReadinessCAName, "/\\")) {
		return bad
	}
	return nil
}
func LoadCumulativeUpgradeRequest(raw, environment []byte) (CumulativeUpgradeRequest, error) {
	r := CumulativeUpgradeDefaults()
	bad := errors.New("OFFLINE_OPERATOR_CUMULATIVE_UPGRADE_CONFIGURATION_INVALID")
	var fields map[string]json.RawMessage
	if wire.StrictJSON(raw, &fields) != nil || wire.StrictJSON(raw, &r) != nil {
		return r, bad
	}
	allowed := map[string]bool{}
	for _, name := range []string{"format_version", "prepared_root", "expected_archive_sha256", "expected_manifest_sha256", "backup_source", "page_size", "operation_seconds", "readiness_dial_address", "readiness_ca_name", "readiness_timeout_seconds", "mvp_configuration_files"} {
		allowed[name] = true
	}
	for name := range fields {
		if !allowed[name] {
			return r, bad
		}
	}
	if value, ok := fields["mvp_configuration_files"]; ok {
		var name string
		if json.Unmarshal(value, &name) != nil || name == "" {
			return r, bad
		}
	}
	if nested, ok := fields["backup_source"]; ok {
		var members map[string]json.RawMessage
		if wire.StrictJSON(nested, &members) != nil {
			return r, bad
		}
		for name := range members {
			switch name {
			case "format_version", "backup_root", "manifest_sha256", "maximum_bytes", "maximum_entries":
			default:
				return r, bad
			}
		}
	}
	seen := map[string]bool{}
	for _, line := range bytes.Split(environment, []byte("\n")) {
		s := strings.TrimSpace(string(line))
		if s == "" || strings.HasPrefix(s, "#") {
			continue
		}
		key, value, ok := strings.Cut(s, "=")
		if !ok || seen[key] || strings.ContainsAny(value, "\x00\r\n$`\"'") {
			return r, bad
		}
		seen[key] = true
		switch key {
		case "PROJECT_ONE_OFFLINE_CUMULATIVE_UPGRADE_MVP_CONFIGURATION_FILES":
			if value == "" {
				return r, bad
			}
			r.MVPConfigurationFiles = value
		case "PROJECT_ONE_OFFLINE_CUMULATIVE_UPGRADE_PREPARED_ROOT":
			r.PreparedRoot = value
		case "PROJECT_ONE_OFFLINE_CUMULATIVE_UPGRADE_ARCHIVE_SHA256":
			r.ArchiveSHA256 = value
		case "PROJECT_ONE_OFFLINE_CUMULATIVE_UPGRADE_MANIFEST_SHA256":
			r.ManifestSHA256 = value
		case "PROJECT_ONE_OFFLINE_CUMULATIVE_UPGRADE_BACKUP_ROOT":
			r.BackupSource.BackupRoot = value
		case "PROJECT_ONE_OFFLINE_CUMULATIVE_UPGRADE_BACKUP_MANIFEST_SHA256":
			r.BackupSource.ManifestSHA256 = value
		case "PROJECT_ONE_OFFLINE_CUMULATIVE_UPGRADE_READINESS_DIAL_ADDRESS":
			r.ReadinessDialAddress = value
		case "PROJECT_ONE_OFFLINE_CUMULATIVE_UPGRADE_READINESS_CA_NAME":
			r.ReadinessCAName = value
		case "PROJECT_ONE_OFFLINE_CUMULATIVE_UPGRADE_PAGE_SIZE", "PROJECT_ONE_OFFLINE_CUMULATIVE_UPGRADE_OPERATION_SECONDS", "PROJECT_ONE_OFFLINE_CUMULATIVE_UPGRADE_READINESS_TIMEOUT_SECONDS", "PROJECT_ONE_OFFLINE_CUMULATIVE_UPGRADE_BACKUP_MAXIMUM_BYTES", "PROJECT_ONE_OFFLINE_CUMULATIVE_UPGRADE_BACKUP_MAXIMUM_ENTRIES":
			n, e := strconv.ParseInt(value, 10, 64)
			if e != nil || strconv.FormatInt(n, 10) != value {
				return r, bad
			}
			switch key {
			case "PROJECT_ONE_OFFLINE_CUMULATIVE_UPGRADE_PAGE_SIZE":
				if n < 1 || n > 100 {
					return r, bad
				}
				r.PageSize = int(n)
			case "PROJECT_ONE_OFFLINE_CUMULATIVE_UPGRADE_OPERATION_SECONDS":
				if n < 1 || n > 30 {
					return r, bad
				}
				r.OperationSeconds = int(n)
			case "PROJECT_ONE_OFFLINE_CUMULATIVE_UPGRADE_READINESS_TIMEOUT_SECONDS":
				if n < 1 || n > 60 {
					return r, bad
				}
				r.ReadinessTimeoutSeconds = int(n)
			case "PROJECT_ONE_OFFLINE_CUMULATIVE_UPGRADE_BACKUP_MAXIMUM_BYTES":
				r.BackupSource.MaximumBytes = n
			case "PROJECT_ONE_OFFLINE_CUMULATIVE_UPGRADE_BACKUP_MAXIMUM_ENTRIES":
				if n < 4 || n > 1000000 {
					return r, bad
				}
				r.BackupSource.MaximumEntries = int(n)
			}
		default:
			return r, bad
		}
	}
	return r, r.Validate()
}
