package dameng

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// A binding contains identities and digests, never password or connection text.
type connectionBinding struct {
	configuration  ConnectionConfiguration
	manifest       NativeManifest
	manifestDigest string
	passwordDigest string
	socket         os.FileInfo
}

func (*connectionBinding) String() string   { return "DAMENG_CONNECTION_BINDING_REDACTED" }
func (*connectionBinding) GoString() string { return "DAMENG_CONNECTION_BINDING_REDACTED" }

func loadNativeManifest(name string) (NativeManifest, string, error) {
	var m NativeManifest
	raw, e := readODBCPrivateInput(name, MaximumConfigurationBytes)
	if e != nil || wire.StrictJSON(raw, &m) != nil || m.Validate() != nil {
		return m, "", fail("DAMENG_NATIVE_MANIFEST_INVALID")
	}
	fieldsRequired := 9
	if m.FormatVersion == 2 {
		fieldsRequired++
	}
	if requiredObject(raw, fieldsRequired) != nil {
		return m, "", fail("DAMENG_NATIVE_MANIFEST_INVALID")
	}
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(raw, &fields)
	var files []json.RawMessage
	_ = json.Unmarshal(fields["files"], &files)
	for _, f := range files {
		if requiredObject(f, 3) != nil {
			return m, "", fail("DAMENG_NATIVE_MANIFEST_INVALID")
		}
	}
	return m, wire.Digest(raw), nil
}

func socketIdentity(c ConnectionConfiguration) (os.FileInfo, error) {
	bad := fail("DAMENG_LOCAL_SOCKET_INVALID")
	parent := filepath.Dir(c.SocketFile)
	resolved, e := filepath.EvalSymlinks(parent)
	if e != nil || resolved != parent || c.SocketOwnerUID != os.Geteuid() {
		return nil, bad
	}
	d, e := os.Lstat(parent)
	if e != nil || !odbcPrivateOwned(d, true) {
		return nil, bad
	}
	i, e := os.Lstat(c.SocketFile)
	if e != nil || i.Mode()&os.ModeType != os.ModeSocket || i.Mode().Perm()&0077 != 0 || i.Mode().Perm()&0200 == 0 {
		return nil, bad
	}
	st, ok := i.Sys().(*syscall.Stat_t)
	if !ok || int(st.Uid) != c.SocketOwnerUID || st.Nlink != 1 {
		return nil, bad
	}
	return i, nil
}

// This is the only process-environment boundary. It verifies declared launch
// inputs; it does not mutate shared environment or read deployment policy here.
func nativeLaunchEnvironment(c ConnectionConfiguration, m NativeManifest) map[string]string {
	return map[string]string{"DM_SVC_PATH": c.NativeConfigurationDirectory, "ODBCSYSINI": c.NativeConfigurationDirectory, "ODBCINI": filepath.Join(c.NativeConfigurationDirectory, "odbc.ini"), "ODBCINSTINI": "odbcinst.ini", "LD_LIBRARY_PATH": m.LibraryDirectory}
}

// LaunchEnvironment provides only nonsecret native-loader references to the
// explicit checker launcher. Open independently verifies actual runtime inputs.
// Preparing these values never establishes native connection admission.
func LaunchEnvironment(c ConnectionConfiguration) (map[string]string, error) {
	settings, e := LaunchSettings(c)
	if e != nil {
		return nil, e
	}
	if settings.WorkingDirectory != "" {
		return nil, fail("DAMENG_NATIVE_LAUNCH_SETTINGS_REQUIRED")
	}
	return settings.Environment, nil
}

// NativeLaunchSettings contains nonsecret child-process inputs. It never changes
// the current process directory or admits native execution by itself.
type NativeLaunchSettings struct {
	FormatVersion    int               `json:"format_version"`
	Environment      map[string]string `json:"environment"`
	WorkingDirectory string            `json:"working_directory"`
}

func (NativeLaunchSettings) String() string   { return "DAMENG_NATIVE_LAUNCH_SETTINGS_REDACTED" }
func (NativeLaunchSettings) GoString() string { return "DAMENG_NATIVE_LAUNCH_SETTINGS_REDACTED" }

func LaunchSettings(c ConnectionConfiguration) (NativeLaunchSettings, error) {
	if e := c.Validate(); e != nil {
		return NativeLaunchSettings{}, e
	}
	m, _, e := loadNativeManifest(c.NativeManifestFile)
	if e != nil {
		return NativeLaunchSettings{}, e
	}
	settings := NativeLaunchSettings{FormatVersion: 1, Environment: nativeLaunchEnvironment(c, m)}
	if m.FormatVersion == 2 {
		settings.WorkingDirectory = m.LibraryDirectory
	}
	return settings, nil
}

func nativeEnvironment(c ConnectionConfiguration, m NativeManifest) error {
	want := nativeLaunchEnvironment(c, m)
	for k, v := range want {
		if os.Getenv(k) != v {
			return fail("DAMENG_NATIVE_ENVIRONMENT_INVALID")
		}
	}
	for _, k := range []string{"LD_PRELOAD", "LD_AUDIT"} {
		if os.Getenv(k) != "" {
			return fail("DAMENG_NATIVE_ENVIRONMENT_INVALID")
		}
	}
	for _, name := range []string{"dm_svc.conf", "odbc.ini", "odbcinst.ini"} {
		raw, e := readODBCPrivateInput(filepath.Join(c.NativeConfigurationDirectory, name), 65536)
		if e != nil {
			return fail("DAMENG_NATIVE_CONFIGURATION_INVALID")
		}
		for _, line := range strings.Split(string(raw), "\n") {
			line = strings.TrimSpace(line)
			if line != "" && !strings.HasPrefix(line, "#") && !strings.HasPrefix(line, ";") {
				return fail("DAMENG_NATIVE_CONFIGURATION_INVALID")
			}
		}
	}
	return nil
}

// VerifyNativeInventoryForValidation checks the complete static native inventory
// before metadata discovery. All three unobserved SQLGetInfo identity fields must
// be empty. It creates no native handle, Store, connection or readiness authority;
// normal manifest loading and Open still require the complete observed identity.
func VerifyNativeInventoryForValidation(ctx context.Context, m NativeManifest) error {
	if m.validateStaticInventory() != nil || m.DriverVersion != "" || m.DBMSName != "" || m.DBMSVersion != "" {
		return fail("DAMENG_NATIVE_MANIFEST_INVALID")
	}
	return verifyNativeArtifacts(ctx, m)
}

func verifyNativeFiles(ctx context.Context, m NativeManifest) error {
	if m.Validate() != nil {
		return fail("DAMENG_NATIVE_ARTIFACT_INVALID")
	}
	return verifyNativeArtifacts(ctx, m)
}

func verifyNativeArtifacts(ctx context.Context, m NativeManifest) error {
	bad := fail("DAMENG_NATIVE_ARTIFACT_INVALID")
	if runtime.GOOS != "linux" || runtime.GOARCH != m.Architecture {
		return bad
	}
	resolved, e := filepath.EvalSymlinks(m.LibraryDirectory)
	if e != nil || resolved != m.LibraryDirectory {
		return bad
	}
	i, e := os.Lstat(m.LibraryDirectory)
	if e != nil || !odbcPrivateOwned(i, true) {
		return bad
	}
	if e := verifyNativeLayout(m, i); e != nil {
		return e
	}
	r, e := os.OpenRoot(m.LibraryDirectory)
	if e != nil {
		return bad
	}
	defer r.Close()
	d, e := r.Open(".")
	if e != nil {
		return bad
	}
	di, e := d.Stat()
	if e != nil || !os.SameFile(i, di) {
		d.Close()
		return bad
	}
	expectedEntries := len(m.Files)
	if m.FormatVersion == 2 {
		expectedEntries++ // The closed, empty dependencies lookup directory.
	}
	names, e := d.Readdirnames(expectedEntries + 1)
	d.Close()
	if e != nil && e != io.EOF || len(names) != expectedEntries {
		return bad
	}
	want := map[string]bool{}
	for _, v := range m.Files {
		if v.Name != m.LicenseFile {
			want[v.Name] = true
		}
	}
	for _, n := range names {
		if !want[n] && n != m.LicenseFile && !(m.FormatVersion == 2 && n == "dependencies") {
			return bad
		}
	}
	for _, v := range m.Files {
		if e = ctx.Err(); e != nil {
			return e
		}
		before, e := r.Lstat(v.Name)
		if e != nil || !odbcPrivateOwned(before, false) || before.Size() != v.Bytes {
			return bad
		}
		f, e := r.OpenFile(v.Name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
		if e != nil {
			return bad
		}
		opened, e := f.Stat()
		if e != nil || !odbcPrivateSameFile(before, opened) {
			f.Close()
			return bad
		}
		if v.Name != m.LicenseFile {
			if inspectNativeLibraryProfile(f, v.Name, m.Architecture, want, m.LoaderProfile) != nil {
				f.Close()
				return bad
			}
		}
		h := sha256.New()
		buffer := make([]byte, 65536)
		var n int64
		for {
			if e = ctx.Err(); e != nil {
				break
			}
			var count int
			count, e = f.Read(buffer)
			n += int64(count)
			if n > v.Bytes {
				e = bad
				break
			}
			_, _ = h.Write(buffer[:count])
			if e != nil {
				break
			}
		}
		after, se := f.Stat()
		ce := f.Close()
		named, ne := r.Lstat(v.Name)
		if e != io.EOF || se != nil || ce != nil || ne != nil || n != v.Bytes || !odbcPrivateSameFile(before, after) || !odbcPrivateSameFile(before, named) || hex.EncodeToString(h.Sum(nil)) != v.SHA256 {
			return bad
		}
	}
	current, e := os.Lstat(m.LibraryDirectory)
	if e != nil || !os.SameFile(i, current) || !odbcPrivateOwned(current, true) {
		return bad
	}
	return nil
}
func bindConnectionInputs(ctx context.Context, c ConnectionConfiguration) (*connectionBinding, error) {
	if c.Validate() != nil || runtime.GOOS != "linux" {
		return nil, fail("DAMENG_CONNECTOR_LINUX_REQUIRED")
	}
	m, digest, e := loadNativeManifest(c.NativeManifestFile)
	if e != nil {
		return nil, e
	}
	if e = verifyNativeFiles(ctx, m); e != nil {
		return nil, e
	}
	if e = nativeEnvironment(c, m); e != nil {
		return nil, e
	}
	socket, e := socketIdentity(c)
	if e != nil {
		return nil, e
	}
	password, e := readODBCPrivateInput(c.PasswordFile, 65536)
	if e != nil {
		return nil, fail("DAMENG_PASSWORD_INPUT_INVALID")
	}
	defer clear(password)
	if !validODBCText(string(password), 65536) || strings.ContainsAny(string(password), "\r\n") {
		return nil, fail("DAMENG_PASSWORD_INPUT_INVALID")
	}
	return &connectionBinding{c, m, digest, wire.Digest(password), socket}, nil
}
func (b *connectionBinding) verify(ctx context.Context) error {
	m, d, e := loadNativeManifest(b.configuration.NativeManifestFile)
	if e != nil || d != b.manifestDigest {
		return fail("DAMENG_NATIVE_INPUT_CHANGED")
	}
	if e = verifyNativeFiles(ctx, m); e != nil {
		return e
	}
	if e = nativeEnvironment(b.configuration, m); e != nil {
		return e
	}
	i, e := socketIdentity(b.configuration)
	if e != nil || !os.SameFile(b.socket, i) {
		return fail("DAMENG_LOCAL_SOCKET_CHANGED")
	}
	p, e := readODBCPrivateInput(b.configuration.PasswordFile, 65536)
	if e != nil {
		return fail("DAMENG_PASSWORD_INPUT_INVALID")
	}
	defer clear(p)
	if wire.Digest(p) != b.passwordDigest {
		return fail("DAMENG_NATIVE_INPUT_CHANGED")
	}
	return nil
}
func odbcQuoted(value string) string { return "{" + strings.ReplaceAll(value, "}", "}}") + "}" }
func (b *connectionBinding) connectionString(ctx context.Context) ([]byte, error) {
	if e := b.verify(ctx); e != nil {
		return nil, e
	}
	p, e := readODBCPrivateInput(b.configuration.PasswordFile, 65536)
	if e != nil {
		return nil, fail("DAMENG_PASSWORD_INPUT_INVALID")
	}
	defer clear(p)
	if wire.Digest(p) != b.passwordDigest {
		return nil, fail("DAMENG_NATIVE_INPUT_CHANGED")
	}
	c := b.configuration
	// No DSN, service name, address list, port or caller-supplied attributes.
	text := "DRIVER=" + odbcQuoted(filepath.Join(b.manifest.LibraryDirectory, b.manifest.DriverFile)) + ";PROTOCOL_TYPE=UNIXSOCKET;SERVER=" + odbcQuoted(c.SocketFile) + ";UID=" + odbcQuoted(c.User) + ";PWD=" + odbcQuoted(string(p)) + ";SCHEMA=" + odbcQuoted(c.Schema) + ";CHARACTER_CODE=PG_UTF8;COMPATIBLE_MODE=DM;RW_SEPARATE=FALSE;FAST_INSERT=FALSE;QUOTE_REPLACE=FALSE;"
	return []byte(text), nil
}
