package mysqlstore

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"database/sql"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	driver "github.com/go-sql-driver/mysql"
)

func syntheticConfiguration(t *testing.T) Configuration {
	t.Helper()
	root := t.TempDir()
	if os.Chmod(root, 0700) != nil {
		t.Fatal("private fixture")
	}
	password := filepath.Join(root, "password")
	if os.WriteFile(password, []byte("synthetic private password only"), 0600) != nil {
		t.Fatal("private fixture")
	}
	key, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	certificate := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Synthetic MySQL test CA"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	raw, e := x509.CreateCertificate(rand.Reader, certificate, certificate, &key.PublicKey, key)
	if e != nil {
		t.Fatal(e)
	}
	ca := filepath.Join(root, "ca.pem")
	if os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: raw}), 0600) != nil {
		t.Fatal("private fixture")
	}
	return Configuration{Host: "127.0.0.1", Port: 13306, Database: "synthetic_test", User: "synthetic_operator", PasswordFile: password, CAFile: ca, ServerName: "localhost", ExpectedVersion: "8.4.12", Policy: DefaultPolicy()}
}
func encoded(t *testing.T, value any) []byte {
	t.Helper()
	raw, e := json.Marshal(value)
	if e != nil {
		t.Fatal(e)
	}
	return raw
}

func TestMySQLStrictConfigurationDefaultsAndExplicitEnvironment(t *testing.T) {
	c := syntheticConfiguration(t)
	document := map[string]any{}
	if json.Unmarshal(encoded(t, c), &document) != nil {
		t.Fatal("fixture")
	}
	delete(document, "policy")
	parsed, e := Decode(encoded(t, document), []byte("# explicit input\nPROJECT_ONE_MYSQL_PORT=23306\nPROJECT_ONE_MYSQL_MAXIMUM_OPEN_CONNECTIONS=2\nPROJECT_ONE_MYSQL_MAXIMUM_IDLE_CONNECTIONS=0\nPROJECT_ONE_MYSQL_LOCK_SECONDS=1\n"))
	if e != nil || parsed.Port != 23306 || parsed.Policy.MaximumOpenConnections != 2 || parsed.Policy.MaximumIdleConnections != 0 || parsed.Policy.LockSeconds != 1 || parsed.Policy.MaximumPacketBytes != DefaultPolicy().MaximumPacketBytes {
		t.Fatal("owned defaults/explicit overrides/zero idle", e)
	}
	roundtrip, e := Decode(encoded(t, parsed), nil)
	if e != nil || roundtrip.Policy.MaximumIdleConnections != 0 {
		t.Fatal("explicit zero idle lost in operator snapshot", e)
	}
	for index, environment := range []string{"MYSQL_PASSWORD=working-secret", "PROJECT_ONE_MYSQL_PORT=false", "PROJECT_ONE_MYSQL_PORT=03306", "PROJECT_ONE_MYSQL_PORT=3306\nPROJECT_ONE_MYSQL_PORT=3307", "PROJECT_ONE_MYSQL_HOST=$HOME", "PROJECT_ONE_MYSQL_HOST=''", "PROJECT_ONE_MYSQL_PORT=0", "PROJECT_ONE_MYSQL_MAXIMUM_IDLE_CONNECTIONS=-1", "PROJECT_ONE_MYSQL_MULTI_STATEMENTS=true"} {
		t.Run(fmt.Sprintf("invalid-environment-%d", index), func(t *testing.T) {
			if _, e := Decode(encoded(t, c), []byte(environment)); e == nil {
				t.Fatal("unsafe environment admitted")
			}
		})
	}
}

func TestMySQLRejectsUnknownNullAliasTypesMissingAndUnsafeTargets(t *testing.T) {
	c := syntheticConfiguration(t)
	valid := encoded(t, c)
	for name, raw := range map[string][]byte{"unknown": append(append([]byte{}, valid[:len(valid)-1]...), []byte(`,"unsafe":true}`)...), "duplicate": bytes.Replace(valid, []byte(`"host":`), []byte(`"host":"localhost","host":`), 1), "alias": bytes.Replace(valid, []byte(`"host":`), []byte(`"Host":`), 1), "null": bytes.Replace(valid, []byte(`"port":13306`), []byte(`"port":null`), 1), "boolean": bytes.Replace(valid, []byte(`"port":13306`), []byte(`"port":true`), 1), "missing": []byte(`{"host":"localhost"}`)} {
		t.Run(name, func(t *testing.T) {
			if _, e := Decode(raw, nil); e == nil {
				t.Fatal("invalid typed JSON admitted")
			}
		})
	}
	for name, change := range map[string]func(*Configuration){"URI": func(c *Configuration) { c.Host = "mysql://user:password@localhost" }, "bind-address": func(c *Configuration) { c.Host = "0.0.0.0" }, "IPv6-bind": func(c *Configuration) { c.Host = "::" }, "database-override": func(c *Configuration) { c.Database = "test; DROP DATABASE other" }, "credential-URI": func(c *Configuration) { c.User = "user:password" }, "legacy-version": func(c *Configuration) { c.ExpectedVersion = "8.0.46" }, "relative-secret": func(c *Configuration) { c.PasswordFile = "./password" }, "shared-secret-CA": func(c *Configuration) { c.CAFile = c.PasswordFile }, "maximum-open": func(c *Configuration) { c.Policy.MaximumOpenConnections = 33 }, "idle-over-open": func(c *Configuration) { c.Policy.MaximumIdleConnections = 10 }, "packet-limit": func(c *Configuration) { c.Policy.MaximumPacketBytes = 67108865 }, "timeout": func(c *Configuration) { c.Policy.TransactionSeconds = 0 }} {
		t.Run(name, func(t *testing.T) {
			copy := c
			change(&copy)
			if copy.Validate() == nil {
				t.Fatal("unsafe connection config admitted")
			}
		})
	}
}

func TestMySQLActualPrivateFileModesLinksBoundsAndLoad(t *testing.T) {
	c := syntheticConfiguration(t)
	path := filepath.Join(filepath.Dir(c.PasswordFile), "configuration.json")
	if os.WriteFile(path, encoded(t, c), 0600) != nil {
		t.Fatal("fixture")
	}
	if parsed, e := Load(path, ""); e != nil || parsed.Database != c.Database {
		t.Fatal("private snapshot", e)
	}
	link := filepath.Join(filepath.Dir(path), "linked")
	if os.Symlink(path, link) != nil {
		t.Fatal("fixture")
	}
	if _, e := Load(link, ""); e == nil {
		t.Fatal("symlink accepted")
	}
	if os.Remove(link) != nil || os.Link(path, link) != nil {
		t.Fatal("fixture")
	}
	if _, e := Load(path, ""); e == nil {
		t.Fatal("hard link accepted")
	}
	if os.Remove(link) != nil {
		t.Fatal("fixture")
	}
	if os.Chmod(path, 0644) != nil {
		t.Fatal("fixture")
	}
	if _, e := Load(path, ""); e == nil {
		t.Fatal("public configuration accepted")
	}
	if os.Chmod(path, 0600) != nil {
		t.Fatal("fixture")
	}
	if _, e := privateInput(path, 1); e == nil {
		t.Fatal("input bound omitted")
	}
}

func TestMySQLConnectorUsesPrivateCAExactSANAndFixedSecuritySemantics(t *testing.T) {
	c := syntheticConfiguration(t)
	config, e := connectorConfiguration(c)
	if e != nil || config.TLS == nil || config.TLS.InsecureSkipVerify || config.TLS.ServerName != "localhost" || config.TLS.MinVersion != tls.VersionTLS12 || config.TLS.RootCAs == nil {
		t.Fatal("mandatory verified TLS", e)
	}
	if config.AllowNativePasswords || config.AllowOldPasswords || config.AllowCleartextPasswords || config.AllowAllFiles || config.AllowFallbackToPlaintext || config.MultiStatements || config.InterpolateParams || !config.ParseTime || config.Loc != time.UTC || !config.CheckConnLiveness {
		t.Fatal("MySQL security semantics weakened")
	}
	if config.Collation != "" || config.Params["collation_connection"] != "'utf8mb4_0900_bin'" || config.Params["time_zone"] != "'+00:00'" || config.Params["transaction_isolation"] != "'READ-COMMITTED'" || config.Params["max_execution_time"] != "5000" {
		t.Fatal("session configuration drift")
	}
	clone, e := driver.NewConnector(config)
	if e != nil || clone == nil {
		t.Fatal("actual upstream connector", e)
	}
	if os.WriteFile(c.CAFile, []byte("invalid synthetic CA"), 0600) != nil {
		t.Fatal("fixture")
	}
	if _, e := connectorConfiguration(c); e == nil {
		t.Fatal("invalid CA accepted")
	}
	if os.Chmod(c.PasswordFile, 0644) != nil {
		t.Fatal("fixture")
	}
	if _, e := connectorConfiguration(c); e == nil || strings.Contains(e.Error(), "synthetic private") {
		t.Fatal("secret source refusal/redaction", e)
	}
}

func TestMySQLErrorAndDriverLogsNeverEchoVendorSecrets(t *testing.T) {
	secret := "synthetic-secret-or-duplicate-customer-value"
	for number, code := range map[uint16]string{1062: "MYSQL_UNIQUE_CONFLICT", 1451: "MYSQL_FOREIGN_KEY_CONFLICT", 1452: "MYSQL_FOREIGN_KEY_CONFLICT", 3819: "MYSQL_CHECK_CONFLICT", 1213: "MYSQL_DEADLOCK", 1205: "MYSQL_LOCK_TIMEOUT"} {
		err := SafeError(&driver.MySQLError{Number: number, SQLState: [5]byte{'4', '0', '0', '0', '1'}, Message: secret})
		var safe *Error
		if err.Error() != code || strings.Contains(err.Error(), secret) || !errors.As(err, &safe) || safe.Number != number {
			t.Fatal("typed safe vendor error", err)
		}
	}
	for _, err := range []error{context.Canceled, context.DeadlineExceeded, sql.ErrNoRows} {
		if !errors.Is(SafeError(err), err) {
			t.Fatal("safe sentinel lost")
		}
	}
	if strings.Contains(SafeError(errors.New(secret)).Error(), secret) {
		t.Fatal("raw driver error retained")
	}
	var buffer bytes.Buffer
	original := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buffer, nil)))
	defer slog.SetDefault(original)
	privateDriverLogger{}.Print(secret)
	if strings.Contains(buffer.String(), secret) || !strings.Contains(buffer.String(), "MYSQL_DRIVER_DIAGNOSTIC_REDACTED") {
		t.Fatal("driver log redaction")
	}
}
