package install

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Paths as the containers see them. compose.yaml mounts the two directories.
const (
	containerConfigDir   = "/run/projectone"
	containerOperatorDir = "/run/projectone-operator"
	imagePortalAssets    = "/opt/projectone/portal"
	imageAdminAssets     = "/opt/projectone/admin"
	imageBlocklist       = "/opt/projectone/security/password-blocklist.txt"
	containerListen      = "0.0.0.0:8443"
	databasePort         = "5432"
	databaseName         = "ownsolid"
	migratorRole         = "ownsolid_migrator"
	runtimeRole          = "ownsolid_runtime"
	credentialFile       = "admin-credential"
)

// Layout is every path the installer owns under install_root.
type Layout struct {
	Root, ComposeFile, ComposeEnv, Config, Operator, Secrets, Postgres string
}

func (c Config) Layout() Layout {
	r := c.InstallRoot
	return Layout{
		Root: r, ComposeFile: filepath.Join(r, "compose.yaml"), ComposeEnv: filepath.Join(r, "compose.env"),
		Config: filepath.Join(r, "config"), Operator: filepath.Join(r, "operator"),
		Secrets: filepath.Join(r, "secrets"), Postgres: filepath.Join(r, "data", "postgres"),
	}
}

// CredentialPath is where the generated administrator password is written.
func (c Config) CredentialPath() string { return filepath.Join(c.Layout().Operator, credentialFile) }

// Materialize creates or refreshes the files under install_root. Generated secrets
// are kept across runs; configuration derived from install.yaml is rewritten.
func Materialize(c Config) error {
	l := c.Layout()
	for _, d := range []struct {
		path    string
		runtime bool
	}{{l.Root, false}, {filepath.Dir(l.Postgres), false}, {l.Postgres, false}, {l.Secrets, false}, {l.Config, true}, {l.Operator, true}} {
		if e := privateDirectory(d.path, c, d.runtime); e != nil {
			return e
		}
	}
	postgresPassword, e := secretValue(filepath.Join(l.Secrets, "postgres-password"))
	if e != nil {
		return e
	}
	// The PostgreSQL entrypoint drops to its own user, then reads the password file and
	// enters the data directory again.
	if os.Geteuid() == 0 {
		for _, path := range []string{filepath.Join(l.Secrets, "postgres-password"), l.Postgres} {
			if e = os.Chown(path, c.Runtime.PostgresUID, -1); e != nil {
				return fmt.Errorf("INSTALL_OWNER_FAILED: %s", path)
			}
		}
	}
	runtimePassword, e := secretValue(filepath.Join(l.Secrets, "runtime-password"))
	if e != nil {
		return e
	}
	if e = c.materializeRuntimeSecrets(l); e != nil {
		return e
	}
	if e = c.materializeTLS(l); e != nil {
		return e
	}
	copies := []struct{ source, target string }{{c.License.TrustFile, "license-trust.json"}}
	if c.SMTP.CAFile != "" {
		copies = append(copies, struct{ source, target string }{c.SMTP.CAFile, "smtp-ca.pem"})
	}
	if c.SMTP.PasswordFile != "" {
		copies = append(copies, struct{ source, target string }{c.SMTP.PasswordFile, "smtp-password"})
	}
	for _, item := range copies {
		raw, e := os.ReadFile(item.source)
		if e != nil {
			return fmt.Errorf("INSTALL_INPUT_UNREADABLE: %s", item.source)
		}
		if e = writeFile(filepath.Join(l.Config, item.target), raw, c, true); e != nil {
			return e
		}
	}
	postgresAddress, _ := c.Network.stateAddresses()
	dsn := func(role, password string) []byte {
		u := url.URL{Scheme: "postgres", User: url.UserPassword(role, password), Host: net.JoinHostPort(postgresAddress.String(), databasePort), Path: "/" + databaseName, RawQuery: "sslmode=disable"}
		return []byte(u.String() + "\n")
	}
	if e = writeFile(filepath.Join(l.Config, "runtime-dsn"), dsn(runtimeRole, runtimePassword), c, true); e != nil {
		return e
	}
	if e = writeFile(filepath.Join(l.Operator, "operator-dsn"), dsn(migratorRole, postgresPassword), c, true); e != nil {
		return e
	}
	runtimeYAML, e := c.renderControl(containerConfigDir + "/runtime-dsn")
	if e != nil {
		return e
	}
	if e = writeFile(filepath.Join(l.Config, "control.yaml"), runtimeYAML, c, true); e != nil {
		return e
	}
	operatorYAML, e := c.renderControl(containerOperatorDir + "/operator-dsn")
	if e != nil {
		return e
	}
	if e = writeFile(filepath.Join(l.Operator, "operator.yaml"), operatorYAML, c, true); e != nil {
		return e
	}
	bootstrap, e := json.Marshal(map[string]any{"format_version": 1, "administrator_email": c.Administrator.Email, "display_name": c.Administrator.DisplayName, "installation_name": c.Administrator.InstallationName, "workspace_name": c.Administrator.WorkspaceName})
	if e != nil {
		return errors.New("INSTALL_BOOTSTRAP_RENDER_FAILED")
	}
	if e = writeFile(filepath.Join(l.Operator, "bootstrap.json"), bootstrap, c, true); e != nil {
		return e
	}
	composeFile, e := os.ReadFile(c.ComposeFile)
	if e != nil {
		return fmt.Errorf("INSTALL_INPUT_UNREADABLE: %s", c.ComposeFile)
	}
	if e = writeFile(l.ComposeFile, composeFile, c, false); e != nil {
		return e
	}
	return writeFile(l.ComposeEnv, []byte(c.renderComposeEnv()), c, false)
}

func (c Config) materializeRuntimeSecrets(l Layout) error {
	hmacPath := filepath.Join(l.Config, "auth-hmac")
	if !exists(hmacPath) {
		key, e := randomBytes(48)
		if e != nil {
			return e
		}
		if e = writeFile(hmacPath, key, c, true); e != nil {
			return e
		}
	}
	keyringPath := filepath.Join(l.Config, "mail-keyring.json")
	if exists(keyringPath) {
		return nil
	}
	key, e := randomBytes(32)
	if e != nil {
		return e
	}
	keyring, e := json.Marshal(map[string]any{"active": "k1", "keys": map[string]string{"k1": base64.StdEncoding.EncodeToString(key)}})
	if e != nil {
		return errors.New("INSTALL_SECRET_RENDER_FAILED")
	}
	return writeFile(keyringPath, keyring, c, true)
}

// renderControl produces the control plane configuration for one database identity.
func (c Config) renderControl(dsnFile string) ([]byte, error) {
	document := map[string]any{}
	for key, value := range c.ControlSettings {
		document[key] = value
	}
	document["topology"] = map[string]any{"size": c.Topology.Size}
	document["server"] = map[string]any{
		"listen_address": containerListen, "portal_origin": c.Origins.Portal, "admin_origin": c.Origins.Admin,
		"tls_certificate_file": containerConfigDir + "/certificate.pem", "tls_private_key_file": containerConfigDir + "/private-key.pem",
		"portal_assets": imagePortalAssets, "admin_assets": imageAdminAssets,
	}
	document["database"] = map[string]any{"dsn_file": dsnFile}
	document["security"] = map[string]any{
		"auth_hmac_key_file": containerConfigDir + "/auth-hmac", "mail_keyring_file": containerConfigDir + "/mail-keyring.json",
		"password_blocklist_file": imageBlocklist,
	}
	document["licensing"] = map[string]any{"trust_file": containerConfigDir + "/license-trust.json"}
	smtp := map[string]any{"host": c.SMTP.Host, "port": c.SMTP.Port, "from_address": c.SMTP.FromAddress, "tls_mode": c.SMTP.TLSMode}
	if c.SMTP.CAFile != "" {
		smtp["ca_file"] = containerConfigDir + "/smtp-ca.pem"
	}
	if c.SMTP.Username != "" {
		smtp["username"] = c.SMTP.Username
		smtp["password_file"] = containerConfigDir + "/smtp-password"
	}
	document["smtp"] = smtp
	raw, e := yaml.Marshal(document)
	if e != nil {
		return nil, errors.New("INSTALL_CONTROL_RENDER_FAILED")
	}
	return raw, nil
}

// ComposeVariables are the values compose.yaml consumes.
func (c Config) ComposeVariables() map[string]string {
	l := c.Layout()
	postgresAddress, dynamicRange := c.Network.stateAddresses()
	return map[string]string{
		"OWNSOLID_STATE_SUBNET": c.Network.StateSubnet, "OWNSOLID_STATE_DYNAMIC_RANGE": dynamicRange.String(),
		"OWNSOLID_POSTGRES_ADDRESS": postgresAddress.String(),
		"OWNSOLID_PROJECT":          c.Project, "OWNSOLID_CONTROL_IMAGE": c.Images.Control, "OWNSOLID_POSTGRES_IMAGE": c.Images.Postgres,
		"OWNSOLID_RUNTIME_UID": strconv.Itoa(c.Runtime.UID), "OWNSOLID_RUNTIME_GID": strconv.Itoa(c.Runtime.GID),
		"OWNSOLID_BIND_ADDRESS": c.Network.BindAddress, "OWNSOLID_HTTPS_PORT": strconv.Itoa(c.Network.HTTPSPort),
		"OWNSOLID_CONFIG_DIR": l.Config, "OWNSOLID_OPERATOR_DIR": l.Operator, "OWNSOLID_POSTGRES_DATA": l.Postgres,
		"OWNSOLID_POSTGRES_PASSWORD_FILE": filepath.Join(l.Secrets, "postgres-password"),
		"OWNSOLID_CONTROL_MEMORY":         c.Resources.ControlMemory, "OWNSOLID_CONTROL_CPUS": c.Resources.ControlCPUs,
		"OWNSOLID_POSTGRES_MEMORY": c.Resources.PostgresMemory, "OWNSOLID_POSTGRES_CPUS": c.Resources.PostgresCPUs,
		"OWNSOLID_POSTGRES_MAX_CONNECTIONS": strconv.Itoa(c.Resources.PostgresMaxConnections),
	}
}

func (c Config) renderComposeEnv() string {
	variables := c.ComposeVariables()
	keys := make([]string, 0, len(variables))
	for key := range variables {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString("# Written by ownsolid-install from install.yaml. Edit install.yaml, not this file.\n")
	for _, key := range keys {
		b.WriteString(key + "=" + variables[key] + "\n")
	}
	return b.String()
}

// privateDirectory creates a 0700 directory, refusing a symlink or a file in its place.
func privateDirectory(path string, c Config, runtime bool) error {
	st, e := os.Lstat(path)
	if errors.Is(e, os.ErrNotExist) {
		if e = os.MkdirAll(path, 0o700); e != nil {
			return fmt.Errorf("INSTALL_DIRECTORY_FAILED: %s", path)
		}
	} else if e != nil || !st.IsDir() {
		return fmt.Errorf("INSTALL_DIRECTORY_INVALID: %s must be a directory, not a symlink or file", path)
	}
	if e = os.Chmod(path, 0o700); e != nil {
		return fmt.Errorf("INSTALL_DIRECTORY_FAILED: %s", path)
	}
	return own(path, c, runtime)
}

func own(path string, c Config, runtime bool) error {
	if os.Geteuid() != 0 || !runtime {
		return nil
	}
	if e := os.Chown(path, c.Runtime.UID, c.Runtime.GID); e != nil {
		return fmt.Errorf("INSTALL_OWNER_FAILED: %s", path)
	}
	return nil
}

// writeFile atomically publishes a 0600 file.
func writeFile(path string, data []byte, c Config, runtime bool) error {
	fail := fmt.Errorf("INSTALL_WRITE_FAILED: %s", path)
	if st, e := os.Lstat(path); e == nil && !st.Mode().IsRegular() {
		return fmt.Errorf("INSTALL_FILE_INVALID: %s must be a regular file", path)
	}
	f, e := os.CreateTemp(filepath.Dir(path), ".install-*")
	if e != nil {
		return fail
	}
	temporary := f.Name()
	defer os.Remove(temporary)
	_, writeError := f.Write(data)
	syncError := f.Sync()
	closeError := f.Close()
	if writeError != nil || syncError != nil || closeError != nil || os.Chmod(temporary, 0o600) != nil {
		return fail
	}
	if e = own(temporary, c, runtime); e != nil {
		return e
	}
	if os.Rename(temporary, path) != nil {
		return fail
	}
	return nil
}

// secretValue returns the secret stored at path, generating it on first use. The
// value suits a URL and meets the control plane's runtime credential format.
func secretValue(path string) (string, error) {
	raw, e := os.ReadFile(path)
	if e == nil {
		value := strings.TrimSpace(string(raw))
		if value == "" {
			return "", fmt.Errorf("INSTALL_SECRET_INVALID: %s is empty", path)
		}
		return value, nil
	}
	if !errors.Is(e, os.ErrNotExist) {
		return "", fmt.Errorf("INSTALL_SECRET_UNREADABLE: %s", path)
	}
	key, e := randomBytes(32)
	if e != nil {
		return "", e
	}
	value := base64.RawURLEncoding.EncodeToString(key)
	if e = writeFile(path, []byte(value+"\n"), Config{}, false); e != nil {
		return "", e
	}
	return value, nil
}

func randomBytes(n int) ([]byte, error) {
	b := make([]byte, n)
	if _, e := rand.Read(b); e != nil {
		return nil, errors.New("INSTALL_ENTROPY_UNAVAILABLE")
	}
	return b, nil
}

func exists(path string) bool {
	_, e := os.Lstat(path)
	return e == nil
}
