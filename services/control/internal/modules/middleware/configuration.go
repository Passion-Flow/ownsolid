// Package middleware owns scoped PostgreSQL maintenance and external MySQL health.
package middleware

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"syscall"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

const PlatformOwned = "platform_owned"
const CustomerExternal = "customer_external"
const maximumPrivateInputBytes = 1 << 20

var profileKey = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)
var scopeIdentity = regexp.MustCompile(`^[a-f0-9]{32}$`)

type Policy struct {
	RequestSeconds       int `json:"request_seconds"`
	ProbeSeconds         int `json:"probe_seconds"`
	OperationSeconds     int `json:"operation_seconds"`
	LeaseSeconds         int `json:"lease_seconds"`
	RenewSeconds         int `json:"renew_seconds"`
	PollMilliseconds     int `json:"poll_milliseconds"`
	MaximumInstances     int `json:"maximum_instances"`
	PageSize             int `json:"page_size"`
	MaximumEvidenceBytes int `json:"maximum_evidence_bytes"`
}

func DefaultPolicy() Policy {
	return Policy{RequestSeconds: 30, ProbeSeconds: 10, OperationSeconds: 7200, LeaseSeconds: 120, RenewSeconds: 30, PollMilliseconds: 1000, MaximumInstances: 100, PageSize: 50, MaximumEvidenceBytes: 1 << 20}
}

func (p Policy) Validate() error {
	if p.RequestSeconds < 1 || p.RequestSeconds > 300 || p.ProbeSeconds < 1 || p.ProbeSeconds > 120 || p.OperationSeconds < 30 || p.OperationSeconds > 86400 || p.LeaseSeconds < 10 || p.LeaseSeconds > 3600 || p.RenewSeconds < 1 || p.RenewSeconds*3 > p.LeaseSeconds || p.PollMilliseconds < 100 || p.PollMilliseconds > 60000 || p.MaximumInstances < 1 || p.MaximumInstances > 10000 || p.PageSize < 1 || p.PageSize > 200 || p.MaximumEvidenceBytes < 4096 || p.MaximumEvidenceBytes > 16<<20 {
		return fail("MIDDLEWARE_CONFIGURATION_INVALID")
	}
	return nil
}

// Profile contains only protected operator configuration. Browser requests can
// select Key; they cannot replace any connection, tool, scope or file reference.
type Profile struct {
	Key                       string          `json:"key"`
	Kind                      string          `json:"kind"`
	Engine                    string          `json:"engine,omitempty"`
	MySQLConfigurationFile    string          `json:"mysql_configuration_file,omitempty"`
	MySQLEnvironmentFile      string          `json:"mysql_environment_file,omitempty"`
	ExpectedMySQLServerUUID   string          `json:"expected_mysql_server_uuid,omitempty"`
	Host                      string          `json:"host,omitempty"`
	Port                      uint16          `json:"port,omitempty"`
	Database                  string          `json:"database,omitempty"`
	User                      string          `json:"user,omitempty"`
	PasswordFile              string          `json:"password_file,omitempty"`
	CAFile                    string          `json:"ca_file,omitempty"`
	ExpectedMajor             int             `json:"expected_major,omitempty"`
	OperatorConfigurationFile string          `json:"operator_configuration_file,omitempty"`
	BackupRequestFile         string          `json:"backup_request_file,omitempty"`
	ExpectedScopeID           string          `json:"expected_scope_id,omitempty"`
	ExpectedTenantID          string          `json:"expected_tenant_id,omitempty"`
	ArtifactRoot              string          `json:"artifact_root,omitempty"`
	RestoreTargets            []RestoreTarget `json:"restore_targets,omitempty"`
}

type RestoreTarget struct {
	Key                       string `json:"key"`
	OperatorConfigurationFile string `json:"operator_configuration_file"`
	ExpectedScopeID           string `json:"expected_scope_id"`
}

type Configuration struct {
	FormatVersion int       `json:"format_version"`
	Policy        Policy    `json:"policy"`
	Profiles      []Profile `json:"profiles"`
}

func pathValid(p string) bool {
	return filepath.IsAbs(p) && filepath.Clean(p) == p && !strings.ContainsAny(p, "\x00\r\n")
}
func fail(code string) error { return errors.New(code) }
func hash(raw []byte) string { h := sha256.Sum256(raw); return hex.EncodeToString(h[:]) }

func (p Profile) Validate() error {
	if p.Engine == "mysql" {
		if !profileKey.MatchString(p.Key) || p.Kind != CustomerExternal || !pathValid(p.MySQLConfigurationFile) || p.MySQLEnvironmentFile != "" && (!pathValid(p.MySQLEnvironmentFile) || p.MySQLEnvironmentFile == p.MySQLConfigurationFile) || !validID(p.ExpectedMySQLServerUUID) {
			return fail("MIDDLEWARE_PROFILE_INVALID")
		}
		if p.Host != "" || p.Port != 0 || p.Database != "" || p.User != "" || p.PasswordFile != "" || p.CAFile != "" || p.ExpectedMajor != 0 || p.OperatorConfigurationFile != "" || p.BackupRequestFile != "" || p.ExpectedScopeID != "" || p.ExpectedTenantID != "" || p.ArtifactRoot != "" || len(p.RestoreTargets) != 0 {
			return fail("MIDDLEWARE_EXTERNAL_MUTATION_REFUSED")
		}
		return nil
	}
	if p.Engine != "" && p.Engine != "postgresql" || p.MySQLConfigurationFile != "" || p.MySQLEnvironmentFile != "" || p.ExpectedMySQLServerUUID != "" {
		return fail("MIDDLEWARE_PROFILE_INVALID")
	}
	host := p.Host
	if !profileKey.MatchString(p.Key) || p.Kind != PlatformOwned && p.Kind != CustomerExternal || host == "" || len(host) > 253 || strings.ContainsAny(host, " /\\\x00\r\n[],:;") && net.ParseIP(host) == nil || p.Port == 0 || !profileKey.MatchString(p.Database) || !profileKey.MatchString(p.User) || !pathValid(p.PasswordFile) || !pathValid(p.CAFile) || p.PasswordFile == p.CAFile || p.ExpectedMajor < 14 || p.ExpectedMajor > 18 {
		return fail("MIDDLEWARE_PROFILE_INVALID")
	}
	if p.Kind == CustomerExternal {
		if p.OperatorConfigurationFile != "" || p.BackupRequestFile != "" || p.ExpectedScopeID != "" || p.ExpectedTenantID != "" || p.ArtifactRoot != "" || len(p.RestoreTargets) > 0 {
			return fail("MIDDLEWARE_EXTERNAL_MUTATION_REFUSED")
		}
		return nil
	}
	if !pathValid(p.OperatorConfigurationFile) || !pathValid(p.BackupRequestFile) || !scopeIdentity.MatchString(p.ExpectedScopeID) || !validID(p.ExpectedTenantID) || !pathValid(p.ArtifactRoot) {
		return fail("MIDDLEWARE_PLATFORM_SCOPE_REQUIRED")
	}
	seen := map[string]bool{}
	for _, t := range p.RestoreTargets {
		if !profileKey.MatchString(t.Key) || seen[t.Key] || !pathValid(t.OperatorConfigurationFile) || t.OperatorConfigurationFile == p.OperatorConfigurationFile || !scopeIdentity.MatchString(t.ExpectedScopeID) || t.ExpectedScopeID == p.ExpectedScopeID {
			return fail("MIDDLEWARE_ISOLATED_TARGET_INVALID")
		}
		seen[t.Key] = true
	}
	return nil
}

func (c Configuration) Validate() error {
	if c.FormatVersion != 1 || c.Policy.Validate() != nil || len(c.Profiles) == 0 || len(c.Profiles) > c.Policy.MaximumInstances {
		return fail("MIDDLEWARE_CONFIGURATION_INVALID")
	}
	seen := map[string]bool{}
	sources := map[string]bool{}
	for _, p := range c.Profiles {
		if seen[p.Key] {
			return fail("MIDDLEWARE_PROFILE_DUPLICATE")
		}
		seen[p.Key] = true
		if e := p.Validate(); e != nil {
			return e
		}
		if p.Kind == PlatformOwned {
			if sources[p.ExpectedScopeID] {
				return fail("MIDDLEWARE_PLATFORM_SCOPE_DUPLICATE")
			}
			sources[p.ExpectedScopeID] = true
		}
	}
	targets := map[string]bool{}
	for _, p := range c.Profiles {
		for _, t := range p.RestoreTargets {
			if sources[t.ExpectedScopeID] || targets[t.ExpectedScopeID] {
				return fail("MIDDLEWARE_ISOLATED_TARGET_INVALID")
			}
			targets[t.ExpectedScopeID] = true
		}
	}
	return nil
}

// LoadFiles is the serving composition entrance for explicit protected references.
// It uses the same descriptor/path authority checks as endpoint secret inputs.
func LoadFiles(configuration, environment string) (Configuration, error) {
	raw, e := readPrivate(configuration)
	if e != nil {
		return Configuration{}, e
	}
	var overrides []byte
	if environment != "" {
		overrides, e = readPrivate(environment)
		if e != nil {
			return Configuration{}, e
		}
	}
	return Load(raw, overrides)
}

// Load is an explicit typed boundary, not an ambient environment reader. The
// protected JSON owns endpoint profiles; explicit policy env-file keys override
// only their matching integer fields. Null/duplicate/unknown/false values fail.
func Load(raw, environment []byte) (Configuration, error) {
	c := Configuration{Policy: DefaultPolicy()}
	if len(raw) == 0 || len(raw) > maximumPrivateInputBytes || wire.StrictJSON(raw, &c) != nil {
		return Configuration{}, fail("MIDDLEWARE_CONFIGURATION_INVALID")
	}
	var members struct {
		Profiles []map[string]json.RawMessage `json:"profiles"`
	}
	if json.Unmarshal(raw, &members) != nil {
		return Configuration{}, fail("MIDDLEWARE_CONFIGURATION_INVALID")
	}
	for i, p := range c.Profiles {
		if _, present := members.Profiles[i]["engine"]; present && p.Engine == "" {
			return Configuration{}, fail("MIDDLEWARE_PROFILE_INVALID")
		}
		if p.Engine == "mysql" {
			if _, present := members.Profiles[i]["mysql_environment_file"]; present && p.MySQLEnvironmentFile == "" {
				return Configuration{}, fail("MIDDLEWARE_PROFILE_INVALID")
			}
			for _, key := range []string{"host", "port", "database", "user", "password_file", "ca_file", "expected_major"} {
				if _, present := members.Profiles[i][key]; present {
					return Configuration{}, fail("MIDDLEWARE_PROFILE_INVALID")
				}
			}
		} else {
			for _, key := range []string{"mysql_configuration_file", "mysql_environment_file", "expected_mysql_server_uuid"} {
				if _, present := members.Profiles[i][key]; present {
					return Configuration{}, fail("MIDDLEWARE_PROFILE_INVALID")
				}
			}
		}
		if p.Kind != CustomerExternal {
			continue
		}
		for _, key := range []string{"operator_configuration_file", "backup_request_file", "expected_scope_id", "expected_tenant_id", "artifact_root", "restore_targets"} {
			if _, present := members.Profiles[i][key]; present {
				return Configuration{}, fail("MIDDLEWARE_EXTERNAL_MUTATION_REFUSED")
			}
		}
	}
	v := reflect.ValueOf(&c.Policy).Elem()
	typ := v.Type()
	fields := map[string]reflect.Value{}
	for i := 0; i < v.NumField(); i++ {
		fields["PROJECT_ONE_MIDDLEWARE_"+strings.ToUpper(typ.Field(i).Tag.Get("json"))] = v.Field(i)
	}
	seen := map[string]bool{}
	for _, line := range bytes.Split(environment, []byte("\n")) {
		s := strings.TrimSpace(string(line))
		if s == "" || strings.HasPrefix(s, "#") {
			continue
		}
		key, value, ok := strings.Cut(s, "=")
		field, known := fields[key]
		if !ok || !known || seen[key] || strings.ContainsAny(value, "\x00\r\n$`\"'") {
			return Configuration{}, fail("MIDDLEWARE_ENVIRONMENT_INVALID")
		}
		seen[key] = true
		n, e := strconv.ParseInt(value, 10, 32)
		if e != nil {
			return Configuration{}, fail("MIDDLEWARE_ENVIRONMENT_INVALID")
		}
		field.SetInt(n)
	}
	if e := c.Validate(); e != nil {
		return Configuration{}, e
	}
	return c, nil
}

func readPrivate(path string) ([]byte, error) {
	if !pathValid(path) {
		return nil, fail("MIDDLEWARE_PRIVATE_INPUT_INVALID")
	}
	parent := filepath.Dir(path)
	resolved, e := filepath.EvalSymlinks(parent)
	if e != nil || resolved != parent {
		return nil, fail("MIDDLEWARE_PRIVATE_INPUT_INVALID")
	}
	r, e := os.OpenRoot(parent)
	if e != nil {
		return nil, fail("MIDDLEWARE_PRIVATE_INPUT_INVALID")
	}
	defer r.Close()
	owned := func(i os.FileInfo, single bool) bool {
		st, ok := i.Sys().(*syscall.Stat_t)
		return ok && st.Uid == uint32(os.Geteuid()) && (!single || st.Nlink == 1)
	}
	d, e := r.Stat(".")
	if e != nil || d.Mode() != os.ModeDir|0700 || !owned(d, false) {
		return nil, fail("MIDDLEWARE_PRIVATE_INPUT_INVALID")
	}
	f, e := r.OpenFile(filepath.Base(path), os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if e != nil {
		return nil, fail("MIDDLEWARE_PRIVATE_INPUT_INVALID")
	}
	defer f.Close()
	b, e := f.Stat()
	if e != nil || b.Mode() != 0600 || !owned(b, true) || b.Size() < 1 || b.Size() > maximumPrivateInputBytes {
		return nil, fail("MIDDLEWARE_PRIVATE_INPUT_INVALID")
	}
	raw, e := io.ReadAll(io.LimitReader(f, maximumPrivateInputBytes+1))
	a, ae := f.Stat()
	p, pe := r.Lstat(filepath.Base(path))
	if e != nil || ae != nil || pe != nil || !os.SameFile(b, a) || !os.SameFile(b, p) || a.Mode() != b.Mode() || p.Mode() != b.Mode() || !owned(a, true) || !owned(p, true) || int64(len(raw)) != b.Size() || a.Size() != b.Size() || p.Size() != b.Size() || !b.ModTime().Equal(a.ModTime()) || !b.ModTime().Equal(p.ModTime()) {
		return nil, fail("MIDDLEWARE_PRIVATE_INPUT_CHANGED")
	}
	return raw, nil
}
