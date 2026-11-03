//go:build repository_integration && middleware_endpoint_integration

package middleware

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

type endpointTestInput struct {
	FormatVersion           int        `json:"format_version"`
	ConfigurationFile       string     `json:"configuration_file"`
	ExpectedVersionNumber   int        `json:"expected_version_number"`
	ExpectedTableCount      int64      `json:"expected_table_count"`
	ExpectedClusterName     string     `json:"expected_cluster_name"`
	ExpectedServerStartedAt *time.Time `json:"expected_server_started_at,omitempty"`
}

func endpointInputs(t *testing.T) (endpointTestInput, Configuration, *CapturedInputs, *CapturedInputs) {
	t.Helper()
	raw, e := readPrivate(os.Getenv("PROJECT_ONE_MIDDLEWARE_ENDPOINT_CONFIGURATION"))
	if e != nil {
		t.Fatal("explicit private endpoint test input required")
	}
	var input endpointTestInput
	if wire.StrictJSON(raw, &input) != nil || input.FormatVersion != 1 || input.ExpectedVersionNumber < 140000 || input.ExpectedVersionNumber >= 190000 || input.ExpectedTableCount < 1 || len(input.ExpectedClusterName) > 128 || input.ExpectedClusterName == "" && input.ExpectedServerStartedAt == nil || input.ExpectedServerStartedAt != nil && input.ExpectedServerStartedAt.IsZero() {
		t.Fatal("invalid explicit endpoint test contract")
	}
	raw, e = readPrivate(input.ConfigurationFile)
	if e != nil {
		t.Fatal("private endpoint configuration required")
	}
	c, e := Load(raw, nil)
	if e != nil || len(c.Profiles) != 1 {
		t.Fatal("exact single endpoint required")
	}
	p := c.Profiles[0]
	ip := net.ParseIP(p.Host)
	if p.Kind != CustomerExternal || ip == nil || !ip.IsLoopback() || !strings.HasSuffix(p.Database, "_test") || p.ExpectedMajor != input.ExpectedVersionNumber/10000 {
		t.Fatal("reviewed isolated loopback external TLS fixture required")
	}
	original, e := Capture(p)
	if e != nil {
		t.Fatal("actual protected endpoint capture failed")
	}
	root := t.TempDir()
	if os.Chmod(root, 0700) != nil {
		t.Fatal("private test directory required")
	}
	password, e := readPrivate(p.PasswordFile)
	if e != nil {
		t.Fatal("actual protected password required")
	}
	p.PasswordFile, p.CAFile = filepath.Join(root, "password"), filepath.Join(root, "ca.pem")
	if os.WriteFile(p.PasswordFile, password, 0600) != nil || os.WriteFile(p.CAFile, original.ca, 0600) != nil {
		t.Fatal("private test input copies failed")
	}
	copy, e := Capture(p)
	if e != nil {
		t.Fatal("copied protected input capture failed")
	}
	t.Cleanup(func() {
		if !original.Matches() {
			t.Error("original endpoint inputs changed")
		}
	})
	return input, c, original, copy
}

func endpointContext(t *testing.T, p Policy) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(p.ProbeSeconds*4+10)*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func positiveEndpoint(t *testing.T, ctx context.Context, input endpointTestInput, c *CapturedInputs, policy Policy) Observation {
	t.Helper()
	out, e := Probe(ctx, c, policy)
	if e != nil {
		t.Fatal("actual verified TLS positive prerequisite failed")
	}
	if out.VersionNumber != input.ExpectedVersionNumber || out.TableCount != input.ExpectedTableCount || out.Database != c.profile.Database || out.User != c.profile.User || out.TLSVersion == "" || out.TLSCipher == "" || out.ServerAddress == "" || out.ServerPort == 0 || out.ObservedAt.IsZero() {
		t.Fatal("actual endpoint observation mismatch")
	}
	pc, e := c.connection(policy.ProbeSeconds)
	if e != nil {
		t.Fatal("actual bound connector required")
	}
	conn, e := pgx.ConnectConfig(ctx, pc)
	if e != nil {
		t.Fatal("independent actual cluster read failed")
	}
	defer conn.Close(ctx)
	var cluster string
	var started time.Time
	if conn.QueryRow(ctx, `SELECT current_setting('cluster_name'),pg_postmaster_start_time()`).Scan(&cluster, &started) != nil || cluster != input.ExpectedClusterName || input.ExpectedServerStartedAt != nil && !started.Equal(*input.ExpectedServerStartedAt) {
		t.Fatal("foreign fixture cluster refused")
	}
	return out
}

func endpointRefused(t *testing.T, ctx context.Context, c *CapturedInputs, p Policy, expected string) {
	t.Helper()
	out, e := Probe(ctx, c, p)
	if e == nil || !reflect.DeepEqual(out, Observation{}) || expected != "" && e.Error() != expected {
		t.Fatal("failed endpoint exposed observation or wrong safe failure")
	}
}

func unrelatedEndpointCA(t *testing.T) []byte {
	t.Helper()
	key, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		t.Fatal("test CA key generation failed")
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Synthetic unrelated endpoint test CA"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	raw, e := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if e != nil {
		t.Fatal("test CA generation failed")
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: raw})
}

func TestActualMiddlewarePostgreSQLProbe(t *testing.T) {
	for _, leaf := range []string{"health_version_actual_tls", "readonly_privileges_and_actual_denial", "wrong_password_zero_output", "wrong_ca_zero_output", "wrong_server_name_actual_rejection", "version_mismatch_zero_output", "captured_input_drift_refused"} {
		t.Run(leaf, func(t *testing.T) {
			input, c, original, copy := endpointInputs(t)
			ctx := endpointContext(t, c.Policy)
			out := positiveEndpoint(t, ctx, input, original, c.Policy)
			switch leaf {
			case "health_version_actual_tls":
				if !copy.Matches() {
					t.Fatal("copy capture drift")
				}
			case "readonly_privileges_and_actual_denial":
				if out.Superuser || out.CreateRole || out.CreateDatabase || out.CreateSchema || out.CreateAnySchemaObjects || out.WriteAnyTable || !out.ReadAllTables {
					t.Fatal("actual role is not the reviewed readonly fixture role")
				}
				pc, e := copy.connection(c.Policy.ProbeSeconds)
				if e != nil {
					t.Fatal("actual readonly connector required")
				}
				// Test-only: prove role permission, not the client's read-only default.
				pc.RuntimeParams["default_transaction_read_only"] = "off"
				conn, e := pgx.ConnectConfig(ctx, pc)
				if e != nil {
					t.Fatal("actual readonly role connection failed")
				}
				defer conn.Close(ctx)
				tx, e := conn.Begin(ctx)
				if e != nil {
					t.Fatal("bounded actual denial transaction required")
				}
				defer func() {
					cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Duration(c.Policy.ProbeSeconds)*time.Second)
					defer cancel()
					if e := tx.Rollback(cleanup); e != nil && !errors.Is(e, pgx.ErrTxClosed) {
						t.Error("actual denial rollback failed")
					}
				}()
				_, e = tx.Exec(ctx, `CREATE TABLE public.`+pgx.Identifier{"m26_denial_" + strings.ReplaceAll(uuid.NewString(), "-", "")}.Sanitize()+` (id integer)`)
				var denied *pgconn.PgError
				if !errors.As(e, &denied) || denied.Code != "42501" {
					t.Fatal("actual role write permission was not denied")
				}
			case "wrong_password_zero_output":
				if os.WriteFile(copy.profile.PasswordFile, []byte(uuid.NewString()), 0600) != nil {
					t.Fatal("owned password mutation failed")
				}
				changed, e := Capture(copy.profile)
				if e != nil {
					t.Fatal("wrong password capture failed")
				}
				endpointRefused(t, ctx, changed, c.Policy, "MIDDLEWARE_CONNECTION_FAILED")
			case "wrong_ca_zero_output":
				if os.WriteFile(copy.profile.CAFile, unrelatedEndpointCA(t), 0600) != nil {
					t.Fatal("owned CA mutation failed")
				}
				changed, e := Capture(copy.profile)
				if e != nil {
					t.Fatal("unrelated valid CA capture failed")
				}
				endpointRefused(t, ctx, changed, c.Policy, "MIDDLEWARE_CONNECTION_FAILED")
			case "wrong_server_name_actual_rejection":
				pc, e := copy.connection(c.Policy.ProbeSeconds)
				if e != nil {
					t.Fatal("bound TLS connector required")
				}
				pc.TLSConfig.ServerName = "m26-invalid.example.invalid"
				conn, e := pgx.ConnectConfig(ctx, pc)
				if conn != nil {
					_ = conn.Close(ctx)
				}
				var mismatch x509.HostnameError
				if !errors.As(e, &mismatch) {
					t.Fatal("actual certificate server-name mismatch was not rejected")
				}
			case "version_mismatch_zero_output":
				p := copy.profile
				if p.ExpectedMajor == 18 {
					p.ExpectedMajor = 17
				} else {
					p.ExpectedMajor = 18
				}
				changed, e := Capture(p)
				if e != nil {
					t.Fatal("negative major capture failed")
				}
				endpointRefused(t, ctx, changed, c.Policy, "MIDDLEWARE_ENDPOINT_IDENTITY_MISMATCH")
			case "captured_input_drift_refused":
				raw, e := readPrivate(copy.profile.PasswordFile)
				if e != nil {
					t.Fatal("owned copy read failed")
				}
				if os.WriteFile(copy.profile.PasswordFile, append(bytes.Clone(raw), '\n'), 0600) != nil {
					t.Fatal("owned drift mutation failed")
				}
				endpointRefused(t, ctx, copy, c.Policy, "MIDDLEWARE_INPUT_CHANGED")
			}
		})
	}
}
