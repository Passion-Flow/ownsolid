// Package damengbackup owns protected, one-shot Dameng logical export/import.
// A verified database image is not a matched platform backup or serving grant.
package damengbackup

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"regexp"
	"strings"
)

const FormatVersion = 1
const ConfigurationEnvironment = "PROJECT_ONE_DAMENG_RECOVERY_CONFIGURATION_FILE"
const maximumDocumentBytes = 1 << 20
const maximumPasswordBytes = 4096
const maximumArtifactBytes = 1 << 30

var identifier = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,62}$`)
var digest = regexp.MustCompile(`^[a-f0-9]{64}$`)
var safePath = regexp.MustCompile(`^/[A-Za-z0-9_./ -]+$`)

func fail(code string) error { return errors.New("DAMENG_RECOVERY_" + code) }
func canonical(path string) bool {
	return filepath.IsAbs(path) && filepath.Clean(path) == path && path != "/" && safePath.MatchString(path)
}
func related(a, b string) bool {
	return a == b || strings.HasPrefix(a, b+"/") || strings.HasPrefix(b, a+"/")
}
func principal(value string) bool {
	return identifier.MatchString(value) && value != "SYS" && value != "SYSDBA" && value != "SYSSSO" && value != "SYSAUDITOR"
}

type Policy struct {
	OperationSeconds        int    `json:"operation_seconds"`
	ProbeSeconds            int    `json:"probe_seconds"`
	TerminationGraceSeconds int    `json:"termination_grace_seconds"`
	ObservationMilliseconds int    `json:"observation_milliseconds"`
	MaximumArchiveBytes     int64  `json:"maximum_archive_bytes"`
	MaximumOutputBytes      int    `json:"maximum_output_bytes"`
	MinimumFreeBytes        uint64 `json:"minimum_free_bytes"`
}

func (p Policy) Validate() error {
	if p.OperationSeconds < 1 || p.OperationSeconds > 86400 || p.ProbeSeconds < 1 || p.ProbeSeconds > 120 || p.TerminationGraceSeconds < 1 || p.TerminationGraceSeconds > 30 || p.ObservationMilliseconds < 10 || p.ObservationMilliseconds > 1000 || p.MaximumArchiveBytes < 1<<20 || p.MaximumArchiveBytes > 1<<50 || p.MaximumOutputBytes < 4096 || p.MaximumOutputBytes > maximumDocumentBytes || p.MinimumFreeBytes == 0 {
		return fail("POLICY_INVALID")
	}
	return nil
}

// Connection is deliberately limited to the existing same-host Linux profile.
// Credentials are read from PasswordFile and never accepted as argv/environment.
type Connection struct {
	Transport      string `json:"transport"`
	SocketFile     string `json:"socket_file"`
	SocketOwnerUID int    `json:"socket_owner_uid"`
	User           string `json:"user"`
	Schema         string `json:"schema"`
	PasswordFile   string `json:"password_file"`
}

func (c Connection) Validate() error {
	if c.Transport != "linux_unixsocket" || !canonical(c.SocketFile) || len(c.SocketFile) > 107 || strings.Contains(c.SocketFile, " ") || c.SocketOwnerUID < 1 || c.SocketOwnerUID > 2147483647 || !principal(c.User) || c.Schema != c.User || !canonical(c.PasswordFile) || c.SocketFile == c.PasswordFile {
		return fail("CONNECTION_INVALID")
	}
	return nil
}

type Configuration struct {
	FormatVersion   int        `json:"format_version"`
	InventoryFile   string     `json:"inventory_file"`
	InventorySHA256 string     `json:"inventory_sha256"`
	Connection      Connection `json:"connection"`
	Policy          Policy     `json:"policy"`
}

func (Configuration) String() string   { return "DAMENG_RECOVERY_CONFIGURATION_REDACTED" }
func (Configuration) GoString() string { return "DAMENG_RECOVERY_CONFIGURATION_REDACTED" }
func (c Configuration) Validate() error {
	if c.FormatVersion != FormatVersion || !canonical(c.InventoryFile) || !digest.MatchString(c.InventorySHA256) || c.Connection.Validate() != nil || c.Policy.Validate() != nil || c.InventoryFile == c.Connection.PasswordFile {
		return fail("CONFIGURATION_INVALID")
	}
	return nil
}

// strictJSON rejects duplicate/unknown members, nulls and trailing values. Null
// must not silently become a Go zero value in any protected intent or policy.
func strictJSON(raw []byte, target any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	var visit func() error
	visit = func() error {
		t, e := d.Token()
		if e != nil || t == nil {
			return fail("DOCUMENT_INVALID")
		}
		if delimiter, ok := t.(json.Delim); ok {
			switch delimiter {
			case '{':
				seen := map[string]bool{}
				for d.More() {
					k, e := d.Token()
					key, ok := k.(string)
					if e != nil || !ok || seen[key] {
						return fail("DOCUMENT_INVALID")
					}
					seen[key] = true
					if e = visit(); e != nil {
						return e
					}
				}
			case '[':
				for d.More() {
					if e = visit(); e != nil {
						return e
					}
				}
			default:
				return fail("DOCUMENT_INVALID")
			}
			if _, e = d.Token(); e != nil {
				return fail("DOCUMENT_INVALID")
			}
		}
		return nil
	}
	if e := visit(); e != nil {
		return e
	}
	if _, e := d.Token(); e != io.EOF {
		return fail("DOCUMENT_INVALID")
	}
	d = json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(target) != nil {
		return fail("DOCUMENT_INVALID")
	}
	return nil
}

// LoadConfiguration reads only explicit protected files. The optional env-file
// selects a whole validated replacement, never loose policy or ambient secrets.
func LoadConfiguration(path, environmentFile string) (Configuration, error) {
	load := func(path string) (Configuration, error) {
		var c Configuration
		raw, e := readPrivate(path, maximumDocumentBytes)
		if e != nil || strictJSON(raw, &c) != nil || c.Validate() != nil {
			return Configuration{}, fail("CONFIGURATION_INVALID")
		}
		return c, nil
	}
	c, e := load(path)
	if e != nil || environmentFile == "" {
		return c, e
	}
	raw, e := readPrivate(environmentFile, maximumDocumentBytes)
	if e != nil {
		return Configuration{}, fail("ENVIRONMENT_INVALID")
	}
	replacement := ""
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok || key != ConfigurationEnvironment || replacement != "" || !canonical(value) {
			return Configuration{}, fail("ENVIRONMENT_INVALID")
		}
		replacement = value
	}
	if replacement == "" {
		return Configuration{}, fail("ENVIRONMENT_INVALID")
	}
	return load(replacement)
}
