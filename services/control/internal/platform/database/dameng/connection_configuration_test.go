package dameng

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func connectorConfigurationFixture() ConnectionConfiguration {
	return ConnectionConfiguration{FormatVersion: 1, Transport: LocalSocketTransport, NativeManifestFile: "/private/connector/native.json", NativeConfigurationDirectory: "/private/connector/native-config", SocketFile: "/private/socket/dm.sock", SocketOwnerUID: 501, User: "P1_TEST", Schema: "P1_TEST", PasswordFile: "/private/connector/password", OperationSeconds: 30, TransactionSeconds: 30, CleanupSeconds: 10, Policy: ConnectionPolicy{5, 10, 10, 65536, 4096, 128, 16, 65536, 8192, 2, 2, 0, 0, 0}}
}
func TestConnectorConfigurationClosedRequiredAndEnvironment(t *testing.T) {
	root := t.TempDir()
	if e := os.Chmod(root, 0700); e != nil {
		t.Fatal(e)
	}
	// Some host temporary roots use a symlink; the input contract needs its actual path.
	root, e := filepath.EvalSymlinks(root)
	if e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(root, "configuration.json")
	environment := filepath.Join(root, "environment")
	c := connectorConfigurationFixture()
	raw, e := json.Marshal(c)
	if e != nil {
		t.Fatal(e)
	}
	write := func(value []byte) {
		t.Helper()
		if e := os.WriteFile(path, value, 0600); e != nil {
			t.Fatal(e)
		}
	}
	write(raw)
	if got, e := LoadConnectionConfiguration(path, ""); e != nil || got != c {
		t.Fatal("valid configuration rejected", e)
	}
	invalid := []string{
		strings.Replace(string(raw), `"format_version":1`, `"format_version":1,"format_version":1`, 1),
		strings.Replace(string(raw), `"format_version":1,`, "", 1),
		strings.Replace(string(raw), `"operation_seconds":30`, `"operation_seconds":false`, 1),
		strings.Replace(string(raw), `"operation_seconds":30`, `"operation_seconds":null`, 1),
		strings.Replace(string(raw), `"maximum_rows":128`, `"maximum_rows":null`, 1),
		strings.Replace(string(raw), `"maximum_rows":128`, `"other":128`, 1),
		strings.Replace(string(raw), `"transport":"linux_unixsocket"`, `"transport":"tls"`, 1),
		strings.TrimSuffix(string(raw), "}") + `,"arbitrary":true}`,
	}
	for i, value := range invalid {
		write([]byte(value))
		if _, e := LoadConnectionConfiguration(path, ""); e == nil {
			t.Fatalf("invalid input %d accepted", i)
		}
	}
	write(raw)
	if e := os.WriteFile(environment, []byte(connectorConfigurationEnvironment+"="+path+"\n"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := LoadConnectionConfiguration(path, environment); e != nil {
		t.Fatal(e)
	}
	for _, value := range []string{"UNKNOWN=" + path, connectorConfigurationEnvironment + "=false", connectorConfigurationEnvironment + "=" + path + "\n" + connectorConfigurationEnvironment + "=" + path, "# no setting"} {
		if e := os.WriteFile(environment, []byte(value), 0600); e != nil {
			t.Fatal(e)
		}
		if _, e := LoadConnectionConfiguration(path, environment); e == nil {
			t.Fatal("invalid environment accepted")
		}
	}
	if e := os.Chmod(path, 0644); e != nil {
		t.Fatal(e)
	}
	if _, e := LoadConnectionConfiguration(path, ""); e == nil {
		t.Fatal("public configuration accepted")
	}
}
func TestConnectorPolicyAndRedaction(t *testing.T) {
	base := connectorConfigurationFixture()
	for _, change := range []func(*ConnectionConfiguration){func(c *ConnectionConfiguration) { c.User = "SYSDBA" }, func(c *ConnectionConfiguration) { c.Schema = "user;PWD=x" }, func(c *ConnectionConfiguration) { c.Transport = "tcp" }, func(c *ConnectionConfiguration) { c.Policy.MaximumColumns = 5 }, func(c *ConnectionConfiguration) { c.Policy.MaximumCellBytes = 16 }, func(c *ConnectionConfiguration) { c.Policy.MaximumResultBytes = 1024 }, func(c *ConnectionConfiguration) { c.Policy.MaximumSQLBytes = 20 }, func(c *ConnectionConfiguration) { c.Policy.MaximumPhysicalConnections = 1 }, func(c *ConnectionConfiguration) { c.OperationSeconds = 0 }, func(c *ConnectionConfiguration) { c.SocketOwnerUID = 0 }, func(c *ConnectionConfiguration) { c.PasswordFile = "/private/../password" }} {
		c := base
		change(&c)
		if c.Validate() == nil {
			t.Fatal("invalid constructor accepted")
		}
	}
	for _, value := range []any{base, NativeManifest{}, &connectionBinding{configuration: base}} {
		for _, format := range []string{"%v", "%+v", "%#v"} {
			text := fmt.Sprintf(format, value)
			if strings.Contains(text, "private/") || !strings.Contains(text, "REDACTED") {
				t.Fatal("configuration leaked")
			}
		}
	}
	if odbcQuoted("a};UID=other;{") != "{a}};UID=other;{}" {
		t.Fatal("ODBC braced attribute not escaped")
	}
}
func TestConnectorNativeManifestRequiredIdentity(t *testing.T) {
	m := NativeManifest{FormatVersion: 1, Architecture: "arm64", LibraryDirectory: "/private/native", DriverFile: "libdodbc.so", LicenseFile: "LICENSE", DriverVersion: "observed-driver", DBMSName: "observed-dbms", DBMSVersion: "observed-version", Files: []NativeFile{{"libdodbc.so", 1, strings.Repeat("a", 64)}, {"LICENSE", 1, strings.Repeat("b", 64)}}}
	if e := m.Validate(); e != nil {
		t.Fatal(e)
	}
	cpp := m
	cpp.Files = append(append([]NativeFile(nil), m.Files...), NativeFile{"libstdc++.so.6", 1, strings.Repeat("c", 64)})
	if err := cpp.Validate(); err != nil {
		t.Fatal("canonical C++ support library filename refused", err)
	}
	for _, change := range []func(*NativeManifest){func(m *NativeManifest) { m.Architecture = "darwin-arm64" }, func(m *NativeManifest) { m.DriverFile = "../lib.so" }, func(m *NativeManifest) { m.DBMSVersion = "" }, func(m *NativeManifest) { m.Files = append(m.Files, m.Files[0]) }, func(m *NativeManifest) { m.LicenseFile = "absent" }, func(m *NativeManifest) {
		m.Files = []NativeFile{{"libdodbc.so", 1, "<unobserved>"}, {"LICENSE", 1, strings.Repeat("b", 64)}}
	}, func(m *NativeManifest) {
		m.Files = append(append([]NativeFile(nil), m.Files...), NativeFile{"libc.so.6", 1, strings.Repeat("c", 64)})
	}} {
		c := m
		change(&c)
		if c.Validate() == nil {
			t.Fatal("invalid manifest accepted")
		}
	}
}

func TestConnectorLaunchMappingAndNativeConfigurationRefusal(t *testing.T) {
	root := t.TempDir()
	if e := os.Chmod(root, 0700); e != nil {
		t.Fatal(e)
	}
	root, e := filepath.EvalSymlinks(root)
	if e != nil {
		t.Fatal(e)
	}
	c := connectorConfigurationFixture()
	c.NativeConfigurationDirectory = root
	c.NativeManifestFile = filepath.Join(root, "native-manifest.json")
	m := NativeManifest{FormatVersion: 1, Architecture: "arm64", LibraryDirectory: "/private/native", DriverFile: "libdodbc.so", LicenseFile: "LICENSE", DriverVersion: "observed-driver", DBMSName: "observed-dbms", DBMSVersion: "observed-version", Files: []NativeFile{{"libdodbc.so", 1, strings.Repeat("a", 64)}, {"LICENSE", 1, strings.Repeat("b", 64)}}}
	raw, e := json.Marshal(m)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(c.NativeManifestFile, raw, 0600); e != nil {
		t.Fatal(e)
	}
	values, e := LaunchEnvironment(c)
	if e != nil || len(values) != 5 {
		t.Fatal("launch mapping rejected", e)
	}
	for key, value := range values {
		t.Setenv(key, value)
	}
	t.Setenv("LD_PRELOAD", "")
	t.Setenv("LD_AUDIT", "")
	for _, name := range []string{"dm_svc.conf", "odbc.ini", "odbcinst.ini"} {
		if e = os.WriteFile(filepath.Join(root, name), []byte("# intentionally no shared service mapping\n"), 0600); e != nil {
			t.Fatal(e)
		}
	}
	if e = nativeEnvironment(c, m); e != nil {
		t.Fatal(e)
	}
	t.Setenv("LD_PRELOAD", "/arbitrary/library")
	if nativeEnvironment(c, m) == nil {
		t.Fatal("native injection accepted")
	}
	t.Setenv("LD_PRELOAD", "")
	t.Setenv("ODBCSYSINI", "/etc")
	if nativeEnvironment(c, m) == nil {
		t.Fatal("shared configuration accepted")
	}
	t.Setenv("ODBCSYSINI", values["ODBCSYSINI"])
	if e = os.WriteFile(filepath.Join(root, "dm_svc.conf"), []byte("service=(network)\n"), 0600); e != nil {
		t.Fatal(e)
	}
	if nativeEnvironment(c, m) == nil {
		t.Fatal("service mapping accepted")
	}
}
