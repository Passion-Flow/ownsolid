package middleware

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/mysqlstore"
)

// Parser-only synthetic inputs; no listener, database or ambient credential.
func mysqlCaptureFixture(t *testing.T) (Profile, mysqlstore.Configuration) {
	t.Helper()
	root := t.TempDir()
	if e := os.Chmod(root, 0700); e != nil {
		t.Fatal(e)
	}
	key, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	now := time.Now().UTC()
	cert := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, e := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if e != nil {
		t.Fatal(e)
	}
	c := mysqlstore.Configuration{Host: "127.0.0.1", Port: 3306, Database: "synthetic_test", User: "Readonly", PasswordFile: filepath.Join(root, "password"), CAFile: filepath.Join(root, "ca.pem"), ServerName: "mysql.example.invalid", ExpectedVersion: "8.4.8", Policy: mysqlstore.DefaultPolicy()}
	p := Profile{Key: "mysql_readonly", Kind: CustomerExternal, Engine: "mysql", MySQLConfigurationFile: filepath.Join(root, "mysql.json"), MySQLEnvironmentFile: filepath.Join(root, "mysql.env"), ExpectedMySQLServerUUID: "11111111-1111-1111-1111-111111111111"}
	raw, e := json.Marshal(c)
	if e != nil {
		t.Fatal(e)
	}
	for path, content := range map[string][]byte{c.PasswordFile: []byte("synthetic-parser-only"), c.CAFile: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), p.MySQLConfigurationFile: raw, p.MySQLEnvironmentFile: []byte("PROJECT_ONE_MYSQL_MAXIMUM_IDLE_CONNECTIONS=0\n")} {
		if e = os.WriteFile(path, content, 0600); e != nil {
			t.Fatal(e)
		}
	}
	return p, c
}

func TestMySQLProfileOwnsOnlyExternalHealthInputs(t *testing.T) {
	p, _ := mysqlCaptureFixture(t)
	c := Configuration{FormatVersion: 1, Policy: DefaultPolicy(), Profiles: []Profile{p}}
	raw, _ := json.Marshal(c)
	if out, e := Load(raw, nil); e != nil || out.Profiles[0].Engine != "mysql" {
		t.Fatal("MySQL profile rejected", e)
	}
	for _, field := range []string{`"host":""`, `"port":0`, `"password_file":""`, `"operator_configuration_file":""`, `"restore_targets":[]`, `"unknown":false`} {
		bad := strings.Replace(string(raw), `"engine":"mysql"`, `"engine":"mysql",`+field, 1)
		if _, e := Load([]byte(bad), nil); e == nil {
			t.Fatal("mixed/unknown authority accepted", field)
		}
	}
	for _, mutate := range []func(*Profile){func(p *Profile) { p.Kind = platformOwned }, func(p *Profile) { p.Engine = "mariadb" }, func(p *Profile) { p.ExpectedMySQLServerUUID = "" }, func(p *Profile) { p.MySQLEnvironmentFile = p.MySQLConfigurationFile }, func(p *Profile) { p.ExpectedMajor = 18 }} {
		bad := p
		mutate(&bad)
		if bad.Validate() == nil {
			t.Fatal("invalid profile accepted")
		}
	}
	pg := externalFixture()
	pg.Profiles[0].MySQLConfigurationFile = p.MySQLConfigurationFile
	if pg.Validate() == nil {
		t.Fatal("MySQL reference mixed into PostgreSQL")
	}
	for _, pair := range []struct{ old, replacement string }{{`"engine":"mysql"`, `"engine":""`}, {`"engine":"mysql"`, `"engine":false`}, {`"mysql_environment_file":` + string(mustMySQLJSON(t, p.MySQLEnvironmentFile)), `"mysql_environment_file":""`}} {
		if _, e := Load([]byte(strings.Replace(string(raw), pair.old, pair.replacement, 1)), nil); e == nil {
			t.Fatal("empty/false engine or environment reference accepted")
		}
	}
}

func mustMySQLJSON(t *testing.T, v any) []byte {
	t.Helper()
	raw, e := json.Marshal(v)
	if e != nil {
		t.Fatal(e)
	}
	return raw
}

func TestMySQLCaptureBindsNestedInputsAndRefusesUnsafeFiles(t *testing.T) {
	for _, which := range []string{"configuration", "environment", "password", "ca"} {
		t.Run(which, func(t *testing.T) {
			p, c := mysqlCaptureFixture(t)
			captured, e := Capture(p)
			if e != nil || !captured.Matches() || captured.mysql.Policy.MaximumIdleConnections != 0 {
				t.Fatal("typed capture failed", e)
			}
			if _, e = captured.connection(1); e == nil {
				t.Fatal("MySQL capture routed through PostgreSQL")
			}
			path := map[string]string{"configuration": p.MySQLConfigurationFile, "environment": p.MySQLEnvironmentFile, "password": c.PasswordFile, "ca": c.CAFile}[which]
			before, e := os.ReadFile(path)
			if e != nil {
				t.Fatal(e)
			}
			changed := append(append([]byte(nil), before...), '\n')
			if e = os.WriteFile(path, changed, 0600); e != nil {
				t.Fatal(e)
			}
			if captured.Matches() {
				t.Fatal("nested byte drift accepted")
			}
			if out, e := ProbeMySQL(context.Background(), captured, DefaultPolicy()); e == nil || out != (MySQLObservation{}) {
				t.Fatal("changed input produced observation", e)
			}
			if e = os.WriteFile(path, before, 0600); e != nil {
				t.Fatal(e)
			}
			if !captured.Matches() {
				t.Fatal("exact restored bytes changed fingerprint")
			}
			if e = os.Chmod(path, 0644); e != nil {
				t.Fatal(e)
			}
			if out, e := Capture(p); e == nil || out != nil {
				t.Fatal("public nested input accepted")
			}
			if e = os.Chmod(path, 0600); e != nil {
				t.Fatal(e)
			}
			retained := path + ".retained"
			if e = os.Rename(path, retained); e != nil {
				t.Fatal(e)
			}
			if e = os.Symlink(retained, path); e != nil {
				t.Fatal(e)
			}
			if out, e := Capture(p); e == nil || out != nil {
				t.Fatal("linked nested input accepted")
			}
		})
	}
}

func TestMySQLObservationRequiresActualTypedFacts(t *testing.T) {
	o := MySQLObservation{ObservedAt: time.Now().UTC(), Database: "synthetic_test", User: "Readonly", ServerUUID: "11111111-1111-1111-1111-111111111111", Version: "8.4.8", TLSVersion: "TLSv1.3", TLSCipher: "TLS_AES_256_GCM_SHA384", VisibleTableCount: 0}
	raw, _ := json.Marshal(o)
	var decoded MySQLObservation
	if json.Unmarshal(raw, &decoded) != nil || !decoded.valid() || decoded.ReadOnly || decoded.SuperReadOnly {
		t.Fatal("actual false facts lost")
	}
	var fields map[string]json.RawMessage
	if e := json.Unmarshal(raw, &fields); e != nil {
		t.Fatal(e)
	}
	for name := range fields {
		var bad map[string]json.RawMessage
		_ = json.Unmarshal(raw, &bad)
		delete(bad, name)
		value, _ := json.Marshal(bad)
		decoded = o
		if json.Unmarshal(value, &decoded) == nil || decoded != (MySQLObservation{}) {
			t.Fatal("missing fact accepted", name)
		}
	}
	for _, change := range []struct{ old, new string }{{`"read_only":false`, `"read_only":"false"`}, {`"super_read_only":false`, `"super_read_only":null`}, {`"visible_table_count":0`, `"visible_table_count":-1`}, {`"visible_table_count":0`, `"visible_table_count":9007199254740992`}, {`"version":"8.4.8"`, `"version":"8.4.8-commercial"`}, {`"tls_version":"TLSv1.3"`, `"tls_version":"plaintext"`}, {`"read_only":false`, `"read_only":false,"password":"private"`}} {
		decoded = o
		if json.Unmarshal([]byte(strings.Replace(string(raw), change.old, change.new, 1)), &decoded) == nil || decoded != (MySQLObservation{}) {
			t.Fatal("invalid fact accepted", change.old)
		}
	}
	if out, e := ProbeMySQL(nil, nil, DefaultPolicy()); e == nil || out != (MySQLObservation{}) {
		t.Fatal("invalid probe returned positive result")
	}
}

func TestMySQLAdditionPreservesLegacyPostgreSQLFingerprint(t *testing.T) {
	my, c := mysqlCaptureFixture(t)
	p := externalFixture().Profiles[0]
	p.PasswordFile = c.PasswordFile
	p.CAFile = c.CAFile
	captured, e := Capture(p)
	if e != nil {
		t.Fatal(e)
	}
	// Pre-MySQL JSON ordering and absent optional fields, independently frozen.
	legacy := `{"key":"external_readonly","kind":"customer_external","host":"127.0.0.1","port":5432,"database":"synthetic","user":"readonly","password_file":PASSWORD,"ca_file":CA,"expected_major":18}`
	passwordPath, _ := json.Marshal(c.PasswordFile)
	caPath, _ := json.Marshal(c.CAFile)
	legacy = strings.ReplaceAll(strings.ReplaceAll(legacy, "PASSWORD", string(passwordPath)), "CA", string(caPath))
	profile, _ := json.Marshal(p)
	if !bytes.Equal(profile, []byte(legacy)) {
		t.Fatal("legacy profile bytes changed")
	}
	password, _ := os.ReadFile(c.PasswordFile)
	ca, _ := os.ReadFile(c.CAFile)
	bound, _ := json.Marshal(struct {
		Profile                               json.RawMessage
		Password, CA, Operator, BackupRequest []byte
		Targets                               map[string][]byte
	}{json.RawMessage(legacy), password, ca, nil, nil, map[string][]byte{}})
	if captured.fingerprint != hash(bound) {
		t.Fatal("legacy registration fingerprint changed")
	}
	if _, e = Capture(my); e != nil {
		t.Fatal(e)
	}
}
