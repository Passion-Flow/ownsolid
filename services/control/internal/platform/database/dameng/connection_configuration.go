package dameng

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"regexp"
	"strings"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

const LocalSocketTransport = "linux_unixsocket"

// The observed vendor profile uses ./ and ../release. It is admitted only with
// the independently checked private working-directory topology, never by a
// generic search-path override or a patched vendor library.
const RelativeReleaseLoaderProfile = "dm_relative_release_v1"

const nativeInstallerAlias = "libodbcinst.so"
const nativeInstallerSONAME = "libodbcinst.so.2"

// Idle connections are disabled until a native session-reset contract exists.
const MaximumIdleConnections = 0
const connectorConfigurationEnvironment = "PROJECT_ONE_DAMENG_CONNECTOR_CONFIGURATION_FILE"

type ConnectionPolicy struct {
	LoginSeconds               int `json:"login_seconds"`
	ConnectionSeconds          int `json:"connection_seconds"`
	QuerySeconds               int `json:"query_seconds"`
	MaximumParameterBytes      int `json:"maximum_parameter_bytes"`
	MaximumCellBytes           int `json:"maximum_cell_bytes"`
	MaximumRows                int `json:"maximum_rows"`
	MaximumColumns             int `json:"maximum_columns"`
	MaximumResultBytes         int `json:"maximum_result_bytes"`
	MaximumSQLBytes            int `json:"maximum_sql_bytes"`
	MaximumOpenConnections     int `json:"maximum_open_connections"`
	MaximumPhysicalConnections int `json:"maximum_physical_connections"`
	MaximumLongValueBytes      int `json:"maximum_long_value_bytes,omitempty"`
	TransferChunkBytes         int `json:"transfer_chunk_bytes,omitempty"`
	MaximumRetainedBytes       int `json:"maximum_retained_bytes,omitempty"`
}

func (p ConnectionPolicy) native() ODBCConfig {
	version := 2
	if p.MaximumLongValueBytes != 0 || p.TransferChunkBytes != 0 || p.MaximumRetainedBytes != 0 {
		version = 3
	}
	return ODBCConfig{FormatVersion: version, LoginSeconds: p.LoginSeconds, ConnectionSeconds: p.ConnectionSeconds, QuerySeconds: p.QuerySeconds, MaximumParameterBytes: p.MaximumParameterBytes, MaximumCellBytes: p.MaximumCellBytes, MaximumRows: p.MaximumRows, MaximumColumns: p.MaximumColumns, MaximumResultBytes: p.MaximumResultBytes, MaximumSQLBytes: p.MaximumSQLBytes, MaximumOpenConnections: p.MaximumOpenConnections, MaximumPhysicalConnections: p.MaximumPhysicalConnections, MaximumLongValueBytes: p.MaximumLongValueBytes, TransferChunkBytes: p.TransferChunkBytes, MaximumRetainedBytes: p.MaximumRetainedBytes}
}

type ConnectionConfiguration struct {
	FormatVersion                int              `json:"format_version"`
	Transport                    string           `json:"transport"`
	NativeManifestFile           string           `json:"native_manifest_file"`
	NativeConfigurationDirectory string           `json:"native_configuration_directory"`
	SocketFile                   string           `json:"socket_file"`
	SocketOwnerUID               int              `json:"socket_owner_uid"`
	User                         string           `json:"user"`
	Schema                       string           `json:"schema"`
	PasswordFile                 string           `json:"password_file"`
	OperationSeconds             int              `json:"operation_seconds"`
	TransactionSeconds           int              `json:"transaction_seconds"`
	CleanupSeconds               int              `json:"cleanup_seconds"`
	Policy                       ConnectionPolicy `json:"policy"`
}

func (ConnectionConfiguration) String() string   { return "DAMENG_CONNECTION_CONFIGURATION_REDACTED" }
func (ConnectionConfiguration) GoString() string { return "DAMENG_CONNECTION_CONFIGURATION_REDACTED" }

var nativeIdentifier = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,62}$`)

func (c ConnectionConfiguration) Validate() error {
	if (c.FormatVersion != 1 && c.FormatVersion != 2) || c.Policy.native().FormatVersion != c.FormatVersion+1 || c.Transport != LocalSocketTransport || !canonicalPath(c.NativeManifestFile) || !canonicalPath(c.NativeConfigurationDirectory) || !canonicalPath(c.SocketFile) || len(c.SocketFile) > 107 || c.SocketOwnerUID < 1 || c.SocketOwnerUID > 2147483647 || !nativeIdentifier.MatchString(c.User) || !nativeIdentifier.MatchString(c.Schema) || c.User == "SYS" || c.User == "SYSDBA" || c.User == "SYSSSO" || c.User == "SYSAUDITOR" || !canonicalPath(c.PasswordFile) || c.PasswordFile == c.NativeManifestFile || c.OperationSeconds < 1 || c.OperationSeconds > 120 || c.TransactionSeconds < 1 || c.TransactionSeconds > 120 || c.CleanupSeconds < 1 || c.CleanupSeconds > 30 || c.Policy.native().validateBounds() != nil || c.Policy.MaximumColumns < 6 || c.Policy.MaximumCellBytes < 256 || c.Policy.MaximumResultBytes < 2048 || c.Policy.MaximumSQLBytes < 1024 {
		return fail("DAMENG_CONNECTION_CONFIGURATION_INVALID")
	}
	return nil
}
func requiredObject(raw []byte, count int) error {
	var fields map[string]json.RawMessage
	if wire.StrictJSON(raw, &fields) != nil || len(fields) != count {
		return fail("DAMENG_CONNECTION_CONFIGURATION_INVALID")
	}
	for _, v := range fields {
		if bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
			return fail("DAMENG_CONNECTION_CONFIGURATION_INVALID")
		}
	}
	return nil
}

// LoadConnectionConfiguration reads only explicit files. Environment values are
// not read here; a protected --env-file may replace the valid base file reference.
func LoadConnectionConfiguration(path, environmentFile string) (ConnectionConfiguration, error) {
	var c ConnectionConfiguration
	load := func(name string) (ConnectionConfiguration, error) {
		var out ConnectionConfiguration
		raw, e := readODBCPrivateInput(name, MaximumConfigurationBytes)
		var fields map[string]json.RawMessage
		if e != nil || requiredObject(raw, 13) != nil || wire.StrictJSON(raw, &out) != nil || wire.StrictJSON(raw, &fields) != nil || out.Validate() != nil {
			return out, fail("DAMENG_CONNECTION_CONFIGURATION_INVALID")
		}
		policyFields := 11
		if out.FormatVersion == 2 {
			policyFields = 14
		}
		if requiredObject(fields["policy"], policyFields) != nil {
			return out, fail("DAMENG_CONNECTION_CONFIGURATION_INVALID")
		}
		return out, nil
	}
	var e error
	if c, e = load(path); e != nil {
		return c, e
	}
	if environmentFile == "" {
		return c, nil
	}
	raw, e := readODBCPrivateInput(environmentFile, MaximumConfigurationBytes)
	if e != nil {
		return ConnectionConfiguration{}, fail("DAMENG_CONNECTION_ENVIRONMENT_INVALID")
	}
	seen := false
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok || k != connectorConfigurationEnvironment || seen || !canonicalPath(v) {
			return ConnectionConfiguration{}, fail("DAMENG_CONNECTION_ENVIRONMENT_INVALID")
		}
		seen = true
		path = v
	}
	if !seen {
		return ConnectionConfiguration{}, fail("DAMENG_CONNECTION_ENVIRONMENT_INVALID")
	}
	return load(path)
}

type NativeFile struct {
	Name   string `json:"name"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
}
type NativeManifest struct {
	FormatVersion    int          `json:"format_version"`
	Architecture     string       `json:"architecture"`
	LibraryDirectory string       `json:"library_directory"`
	DriverFile       string       `json:"driver_file"`
	LicenseFile      string       `json:"license_file"`
	DriverVersion    string       `json:"driver_version"`
	DBMSName         string       `json:"dbms_name"`
	DBMSVersion      string       `json:"dbms_version"`
	Files            []NativeFile `json:"files"`
	LoaderProfile    string       `json:"loader_profile,omitempty"`
}

func (NativeManifest) String() string   { return "DAMENG_NATIVE_MANIFEST_REDACTED" }
func (NativeManifest) GoString() string { return "DAMENG_NATIVE_MANIFEST_REDACTED" }

var nativeFileName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]{0,127}$`)

func (m NativeManifest) Validate() error {
	if err := m.validateStaticInventory(); err != nil {
		return err
	}
	for _, value := range []string{m.DriverVersion, m.DBMSName, m.DBMSVersion} {
		if !validODBCText(value, 128) || strings.TrimSpace(value) != value || value == "" {
			return fail("DAMENG_NATIVE_MANIFEST_INVALID")
		}
	}
	return nil
}

func (m NativeManifest) validateStaticInventory() error {
	bad := fail("DAMENG_NATIVE_MANIFEST_INVALID")
	if (m.FormatVersion != 1 && m.FormatVersion != 2) || m.FormatVersion == 1 && m.LoaderProfile != "" || m.FormatVersion == 2 && (m.LoaderProfile != RelativeReleaseLoaderProfile || filepath.Base(m.LibraryDirectory) != "bin") || m.Architecture != "arm64" && m.Architecture != "amd64" || !canonicalPath(m.LibraryDirectory) || !nativeFileName.MatchString(m.DriverFile) || !nativeFileName.MatchString(m.LicenseFile) || m.DriverFile == m.LicenseFile || len(m.Files) < 2 || len(m.Files) > 64 {
		return bad
	}
	seen := map[string]bool{}
	inventory := map[string]NativeFile{}
	var total int64
	for _, f := range m.Files {
		if !nativeFileName.MatchString(f.Name) || m.FormatVersion == 2 && f.Name == "dependencies" || nativeBaseLibrary(f.Name) || seen[f.Name] || f.Bytes < 1 || f.Bytes > 1<<30 || !wire.ValidSHA256(f.SHA256) {
			return bad
		}
		seen[f.Name] = true
		inventory[f.Name] = f
		total += f.Bytes
	}
	// DM dynamically requests the standard unixODBC installer name. The only
	// admitted alias is this exact byte-identical regular-file pair in the
	// versioned vendor profile. License bytes never satisfy a native dependency.
	if alias, exists := inventory[nativeInstallerAlias]; exists {
		target, present := inventory[nativeInstallerSONAME]
		if m.FormatVersion != 2 || m.LoaderProfile != RelativeReleaseLoaderProfile || !present || m.LicenseFile == nativeInstallerAlias || m.LicenseFile == nativeInstallerSONAME || alias.Bytes != target.Bytes || alias.SHA256 != target.SHA256 {
			return bad
		}
	}
	if !seen[m.DriverFile] || !seen[m.LicenseFile] || total > 2<<30 {
		return bad
	}
	return nil
}
