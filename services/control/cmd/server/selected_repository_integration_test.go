//go:build repository_integration

package main

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.yaml.in/yaml/v3"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/deployments"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	identityhttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/licensing"
	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/recovery"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/mysqlmigrate"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/mail"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/repositoryfixture"
	licensewire "github.com/Passion-Flow/ownsolid/pkg/license/v1"
)

// The database, installation lock, HTTPS, owners, signatures and SQL effects are
// actual. HTML pages and model/GPU observations are explicitly synthetic fixtures.
// The MySQL fixture principal owns isolated migrations; it is not DML-role proof.
type serverCompositionStory struct {
	f      repositoryfixture.Fixture
	ctx    context.Context
	c      config.Config
	d      *selectedDatabase
	owner  *identity.Service
	h      *identityhttp.Handler
	mail   *mail.Outbox
	key    licensewire.PrivateKey
	root   string
	client *http.Client
}

func serverCompositionSetup(t *testing.T, unbootstrapped bool) *serverCompositionStory {
	t.Helper()
	if os.Getenv("PROJECT_ONE_REPOSITORY_ENGINE") == "mysql" {
		catalog, e := mysqlmigrate.DefaultCatalog()
		if e != nil || requireCompleteMySQLSchema(catalog.Steps()) != nil {
			t.Fatal("MYSQL_COMPLETE_SCHEMA_NOT_ACTIVATED")
		}
	}
	var f repositoryfixture.Fixture
	if unbootstrapped {
		f = repositoryfixture.NewUnbootstrapped(t)
	} else {
		f = repositoryfixture.New(t)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	t.Cleanup(cancel)
	root := t.TempDir()
	if e := os.Chmod(root, 0700); e != nil {
		t.Fatal(e)
	}
	x := &serverCompositionStory{f: f, ctx: ctx, root: root}
	x.c.Database.MaxOpenConnections = 4
	x.c.Database.MaxIdleConnections = 1
	x.c.Database.StatementTimeoutSeconds = 5
	x.c.Database.MySQLMigrations = config.DefaultMySQLMigrationPolicy()
	if f.PostgreSQL != nil {
		cc := f.PostgreSQL.Config().ConnConfig
		if len(cc.Fallbacks) != 0 {
			t.Fatal("explicit isolated PostgreSQL profile without transport fallback required")
		}
		u := url.URL{Scheme: "postgres", User: url.UserPassword(cc.User, cc.Password), Host: net.JoinHostPort(cc.Host, strconv.Itoa(int(cc.Port))), Path: "/" + cc.Database}
		q := url.Values{"sslmode": {"disable"}}
		if cc.TLSConfig != nil {
			raw, e := config.Secret(os.Getenv("PROJECT_ONE_REPOSITORY_POSTGRES_DSN_FILE"))
			if e != nil {
				t.Fatal("protected original verify-full PostgreSQL input required")
			}
			original, e := url.Parse(strings.TrimSpace(string(raw)))
			if e != nil || original.Scheme != "postgres" && original.Scheme != "postgresql" || original.Host != u.Host || original.Path != u.Path || original.Query().Get("sslmode") != "verify-full" || original.Query().Get("sslrootcert") == "" || cc.TLSConfig.InsecureSkipVerify || cc.TLSConfig.RootCAs == nil {
				t.Fatal("exact isolated verify-full PostgreSQL profile required; never downgrade TLS")
			}
			q = original.Query()
			u.Scheme = original.Scheme
		}
		for k, v := range cc.RuntimeParams {
			q.Set(k, v)
		}
		u.RawQuery = q.Encode()
		x.c.Database.Engine = config.PostgreSQLDatabaseEngine
		x.c.Database.DSNFile = x.private(t, "postgres-dsn", []byte(u.String()))
	} else {
		x.c.Database.Engine = config.MySQLDatabaseEngine
		x.c.Database.MySQLConfigurationFile = os.Getenv("PROJECT_ONE_REPOSITORY_MYSQL_CONFIGURATION")
	}
	x.c.Server.PortalOrigin = "https://portal.example.invalid"
	x.c.Server.AdminOrigin = "https://admin.example.invalid"
	l, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	x.c.Server.ListenAddress = l.Addr().String()
	if e = l.Close(); e != nil {
		t.Fatal(e)
	}
	x.c.SMTP.Host = "smtp.example.invalid"
	x.c.SMTP.Port = 587
	x.c.SMTP.FromAddress = "platform@example.invalid"
	x.c.SMTP.TLSMode = "starttls_required"
	x.c.SMTP.ConnectTimeoutSeconds = 2
	x.c.SMTP.OperationTimeoutSeconds = 3
	for audience, destination := range map[string]*string{"portal": &x.c.Server.PortalAssets, "admin": &x.c.Server.AdminAssets} {
		*destination = filepath.Join(root, audience)
		if e = os.Mkdir(*destination, 0700); e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(filepath.Join(*destination, "index.html"), []byte("<!doctype html><title>Synthetic "+audience+" routing fixture</title>"), 0600); e != nil {
			t.Fatal(e)
		}
	}
	key, trust, e := licensewire.GenerateKey("synthetic-server-composition")
	if e != nil {
		t.Fatal(e)
	}
	x.key = key
	raw, e := json.Marshal(trust)
	if e != nil {
		t.Fatal(e)
	}
	x.c.Licensing = &config.LicensingConfiguration{TrustFile: x.private(t, "license-trust.json", raw)}
	x.c.Security.PasswordBlocklistFile = x.private(t, "blocklist", []byte("passwordpassword\n"))
	hmac := make([]byte, 32)
	if _, e = rand.Read(hmac); e != nil {
		t.Fatal(e)
	}
	x.c.Security.AuthHMACKeyFile = x.private(t, "auth-key", hmac)
	raw, e = json.Marshal(map[string]any{"active": "synthetic", "keys": map[string]string{"synthetic": base64.StdEncoding.EncodeToString(hmac)}})
	if e != nil {
		t.Fatal(e)
	}
	x.c.Security.MailKeyringFile = x.private(t, "mail-keyring.json", raw)
	x.configureTLS(t)
	x.d, e = openSelectedDatabase(ctx, x.c, "serve")
	if e != nil {
		t.Fatal("actual selected kernel/installation open", e)
	}
	t.Cleanup(func() {
		if e := x.d.Close(); e != nil {
			t.Error("actual selected database cleanup", e)
		}
	})
	x.owner = x.d.identity(x.c)
	x.owner.HMACKey = hmac
	x.owner.Passwords, e = security.NewPasswords(x.c.Security.PasswordBlocklistFile, x.c.Policy().Identity)
	if e != nil {
		t.Fatal(e)
	}
	keys, e := mail.LoadKeyring(x.c.Security.MailKeyringFile)
	if e != nil {
		t.Fatal(e)
	}
	templates, e := mail.LoadTemplates()
	if e != nil {
		t.Fatal(e)
	}
	x.mail = &mail.Outbox{DB: x.d.postgres, Keys: keys, Config: x.c, Templates: templates}
	if x.d.mysql != nil {
		x.mail.Storage = x.d.store
	}
	x.owner.Mail = x.mail
	x.h = identityhttp.New(x.owner)
	if e = x.h.InitializationError(); e != nil {
		t.Fatal("actual selected construction", e)
	}
	return x
}
func (x *serverCompositionStory) private(t *testing.T, name string, raw []byte) string {
	t.Helper()
	path := filepath.Join(x.root, name)
	if e := os.WriteFile(path, raw, 0600); e != nil {
		t.Fatal(e)
	}
	return path
}
func (x *serverCompositionStory) configureTLS(t *testing.T) {
	t.Helper()
	key, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	now := time.Now().UTC()
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Synthetic isolated server trust"}, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour), DNSNames: []string{"portal.example.invalid", "admin.example.invalid"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, IsCA: true, BasicConstraintsValid: true}
	der, e := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if e != nil {
		t.Fatal(e)
	}
	encoded, e := x509.MarshalPKCS8PrivateKey(key)
	if e != nil {
		t.Fatal(e)
	}
	x.c.Server.TLSCertificateFile = x.private(t, "certificate.pem", pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	x.c.Server.TLSPrivateKeyFile = x.private(t, "private-key.pem", pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: encoded}))
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})) {
		t.Fatal("synthetic trust invalid")
	}
	// Advertised DNS authorities must also be the TLS SNI/verification names.
	// Map only their sockets to this private listener; never override Host on an
	// IP URL or bypass the production authority/SNI check.
	targets := make(map[string]bool)
	for _, audience := range []string{"portal", "admin"} {
		origin, e := config.ParseBrowserOrigin(x.origin(t, audience))
		if e != nil || origin.Scheme != "https" {
			t.Fatal("COMPOSITION_VERIFIED_HTTPS_ORIGIN_REQUIRED")
		}
		targets[net.JoinHostPort(origin.Hostname, strconv.Itoa(int(origin.Port)))] = true
	}
	address := x.c.Server.ListenAddress
	dialer := &net.Dialer{}
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12},
		DialContext: func(ctx context.Context, network, target string) (net.Conn, error) {
			if network != "tcp" || !targets[target] {
				return nil, errors.New("COMPOSITION_PRIVATE_DIAL_TARGET_REQUIRED")
			}
			return dialer.DialContext(ctx, network, address)
		},
	}
	x.client = &http.Client{Transport: transport, Timeout: 3 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return errors.New("COMPOSITION_REDIRECT_REFUSED")
		},
	}
	t.Cleanup(transport.CloseIdleConnections)
}

func (x *serverCompositionStory) origin(t *testing.T, audience string) string {
	t.Helper()
	switch audience {
	case "portal":
		return x.c.Server.PortalOrigin
	case "admin":
		return x.c.Server.AdminOrigin
	default:
		t.Fatal("COMPOSITION_AUDIENCE_INVALID")
		return ""
	}
}
func (x *serverCompositionStory) exec(t *testing.T, pg, my string, args ...any) {
	t.Helper()
	if _, e := x.f.Store.Exec(x.ctx, x.f.Statement(pg, my, args...)); e != nil {
		t.Fatal("actual isolated fixture mutation", e)
	}
}
func (x *serverCompositionStory) serve(t *testing.T) func() {
	t.Helper()
	ctx, cancel := context.WithCancel(x.ctx)
	done := make(chan error, 1)
	go func() {
		startup, stop := context.WithTimeout(ctx, config.Seconds(x.c.Policy().HTTP.StartupSeconds))
		defer stop()
		done <- serveSelected(ctx, startup, x.c, x.d, x.owner, x.mail)
	}()
	stopped := false
	stop := func() {
		if stopped {
			return
		}
		stopped = true
		cancel()
		select {
		case e := <-done:
			if e != nil {
				t.Error("actual selected server stop", e)
			}
		case <-time.After(20 * time.Second):
			t.Error("actual selected server did not join")
		}
	}
	t.Cleanup(stop)
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case e := <-done:
			stopped = true
			cancel()
			t.Fatal("actual selected serve refused", e)
		case <-deadline.C:
			t.Fatal("actual selected HTTPS did not listen")
		case <-ticker.C:
			r, e := http.NewRequestWithContext(x.ctx, "GET", x.origin(t, "portal")+"/health/live", nil)
			if e != nil {
				t.Fatal(e)
			}
			if response, e := x.client.Do(r); e == nil {
				_, _ = io.Copy(io.Discard, response.Body)
				response.Body.Close()
				if response.StatusCode == 200 {
					return stop
				}
			}
		}
	}
}
func (x *serverCompositionStory) request(t *testing.T, audience, method, path string, body []byte, cookies []*http.Cookie, csrf string) (int, map[string]any, []*http.Cookie) {
	t.Helper()
	r, e := http.NewRequestWithContext(x.ctx, method, x.origin(t, audience)+path, bytes.NewReader(body))
	if e != nil {
		t.Fatal(e)
	}
	r.Header.Set("Origin", x.origin(t, audience))
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	if csrf != "" {
		r.Header.Set("X-CSRF-Token", csrf)
	}
	for _, cookie := range cookies {
		r.AddCookie(cookie)
	}
	response, e := x.client.Do(r)
	if e != nil {
		t.Fatal("actual verified HTTPS", e)
	}
	defer response.Body.Close()
	raw, e := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if e != nil || len(raw) == 1<<20 {
		t.Fatal("bounded actual HTTPS response", e)
	}
	var out map[string]any
	if strings.HasPrefix(response.Header.Get("Content-Type"), "application/json") || strings.HasPrefix(response.Header.Get("Content-Type"), "application/problem+json") {
		if json.Unmarshal(raw, &out) != nil {
			t.Fatal("invalid actual HTTPS JSON")
		}
	}
	return response.StatusCode, out, response.Cookies()
}
func (x *serverCompositionStory) session(t *testing.T) identity.Auth {
	t.Helper()
	var issued identity.IssuedSession
	if e := x.d.store.Within(x.ctx, false, func(ctx context.Context, q transaction.Executor) error {
		var e error
		issued, e = x.owner.IssueSessionWithin(ctx, q, x.f.User, 1, "admin", "", uuid.NewString(), nil)
		return e
	}); e != nil {
		t.Fatal(e)
	}
	a, e := x.owner.Authenticate(x.ctx, issued.Token, "admin")
	if e != nil {
		t.Fatal(e)
	}
	return a
}
func (x *serverCompositionStory) deployment(t *testing.T) (wire.Enrolled, deployments.Deployment) {
	t.Helper()
	a := x.session(t)
	state, e := x.h.Licensing.Service.StatusAdmin(x.ctx, a)
	if e != nil {
		t.Fatal(e)
	}
	now := time.Now().UTC().Truncate(time.Second)
	claims := licensewire.Claims{FormatVersion: 1, LicenseID: uuid.NewString(), CustomerReference: "Explicit synthetic restore fixture", DeploymentID: state.DeploymentID, IssuedAt: now.Add(-time.Minute).Format(time.RFC3339), NotBefore: now.Add(-time.Minute).Format(time.RFC3339), ExpiresAt: now.Add(time.Hour).Format(time.RFC3339), GraceUntil: now.Add(2 * time.Hour).Format(time.RFC3339), Scope: licensewire.WholePlatformScope}
	raw, e := licensewire.Issue(claims, x.key)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = x.h.Licensing.Service.Import(x.ctx, a, licensing.ImportInput{Artifact: string(raw), Reason: "Actual signed synthetic restore fixture"}, uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	h := wire.Handshake{ProtocolVersion: wire.InferenceVersion, ExecutionProfile: wire.DockerInferenceProfile, Capabilities: wire.Capabilities(wire.DockerInferenceProfile), Facts: wire.Facts{OSID: "ubuntu", OSVersion: "24.04", Architecture: "amd64", Hostname: "synthetic-restore-target", LogicalCPUs: 4, MemoryTotalBytes: 16 << 30, DiskPath: "/", DiskTotalBytes: 100 << 30, DiskAvailableBytes: 50 << 30, ObservedAt: now, AgentVersion: wire.AgentVersion}}
	join, e := x.h.Nodes.Service.MintForProfile(x.ctx, a, x.f.Workspace, "Synthetic restore target", h.Profile(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	n, e := x.h.Nodes.Service.Enroll(x.ctx, wire.Enrollment{JoinToken: join.Token, Handshake: h}, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	gpu := "GPU-" + uuid.NewString()
	h.Inference = &wire.InferenceObservation{GPUs: []wire.NVIDIAGPU{{UUID: gpu, Name: "Synthetic restore GPU", DriverVersion: "580.1", MemoryTotalMiB: 16384, MemoryFreeMiB: 16384, ComputeCapability: 8, MIGMode: "Disabled", ObservedAt: now}}, ObservedAt: now}
	if _, e = x.h.Nodes.Service.Heartbeat(x.ctx, n.NodeID, n.Credential, h); e != nil {
		t.Fatal(e)
	}
	model := uuid.NewString()
	manifest, e := json.Marshal(wire.ApprovedModel())
	if e != nil {
		t.Fatal(e)
	}
	x.exec(t, "INSERT INTO model_versions(id,tenant_id,workspace_id,name,model_key,catalog_digest,manifest,state,created_by) VALUES($1,$2,$3,'Synthetic metadata only',$4,$5,$6,'ready',$7)", "INSERT INTO model_versions(id,tenant_id,workspace_id,name,model_key,catalog_digest,manifest,state,created_by) VALUES(?,?,?,'Synthetic metadata only',?,?,CAST(? AS JSON),'ready',?)", model, x.f.Tenant, x.f.Workspace, wire.ApprovedModel().ModelKey, wire.ModelCatalogDigest(), manifest, x.f.User)
	d, e := x.h.Deployments.Service.Create(x.ctx, a, deployments.CreateRequest{NodeID: n.NodeID, ModelID: model, GPUUUID: gpu, Reason: "Actual intent with synthetic target metadata"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	return n, d
}
func (x *serverCompositionStory) snapshot(t *testing.T, node, deployment string) []any {
	t.Helper()
	var epoch, audits int64
	var status, physical, desired, observed, task string
	var credential bool
	for _, item := range []struct {
		pg, my string
		args   []any
		dest   []any
	}{
		{"SELECT auth_epoch FROM users WHERE id=$1", "SELECT auth_epoch FROM users WHERE id=?", []any{x.f.User}, []any{&epoch}},
		{"SELECT count(*) FROM audit_events", "SELECT count(*) FROM audit_events", nil, []any{&audits}},
		{"SELECT status,credential_hash IS NOT NULL FROM managed_nodes WHERE id=$1", "SELECT status,credential_hash IS NOT NULL FROM managed_nodes WHERE id=?", []any{node}, []any{&status, &credential}},
		{"SELECT g.state,d.desired_state,d.observed_state,t.state FROM deployments d JOIN gpu_leases g ON g.deployment_id=d.id JOIN node_tasks t ON t.id=d.last_task_id WHERE d.id=$1", "SELECT g.state,d.desired_state,d.observed_state,t.state FROM deployments d JOIN gpu_leases g ON g.deployment_id=d.id JOIN node_tasks t ON t.id=d.last_task_id WHERE d.id=?", []any{deployment}, []any{&physical, &desired, &observed, &task}},
	} {
		if e := x.f.Store.QueryRow(x.ctx, x.f.Statement(item.pg, item.my, item.args...)).Scan(item.dest...); e != nil {
			t.Fatal(e)
		}
	}
	return []any{epoch, audits, status, credential, physical, desired, observed, task}
}

// This explicit test-only boundary surrounds a genuine transaction. It models
// acknowledgement classification; it does not establish a real wire COMMIT fault.
type restoreAcknowledgementStore struct {
	transaction.Store
	before bool
}

func (s restoreAcknowledgementStore) Within(ctx context.Context, read bool, fn func(context.Context, transaction.Executor) error) error {
	e := s.Store.Within(ctx, read, func(ctx context.Context, q transaction.Executor) error {
		if e := fn(ctx, q); e != nil {
			return e
		}
		if s.before {
			return &transaction.Error{Kind: transaction.CommitUnknown}
		}
		return nil
	})
	if e != nil {
		return e
	}
	return &transaction.Error{Kind: transaction.CommitUnknown}
}

func TestActualPairedServerComposition(t *testing.T) {
	for _, name := range []string{"current_installation_and_schema_lock", "concurrent_installation_refusal", "changed_ledger_is_not_repaired", "operator_schema_check_without_auth_assets", "actual_shared_owner_construction", "installed_https_readiness_and_dual_entrances", "unbootstrapped_https_boundary_and_restart", "startup_owner_failure_never_listens", "restore_revokes_authorities_preserves_physical_owner", "restore_atomic_rollback", "restore_unknown_before_commit", "restore_unknown_after_commit", "inference_removal_receipt_replay", "inference_removal_foreign_prefence_rejected"} {
		t.Run(name, func(t *testing.T) {
			x := serverCompositionSetup(t, name == "unbootstrapped_https_boundary_and_restart")
			switch name {
			case "current_installation_and_schema_lock":
				if e := x.d.Verify(x.ctx); e != nil {
					t.Fatal(e)
				}
				if e := x.d.Close(); e != nil {
					t.Fatal(e)
				}
				d, e := openSelectedDatabase(x.ctx, x.c, "serve")
				if e != nil {
					t.Fatal("released installation lease unavailable", e)
				}
				if e = d.Close(); e != nil {
					t.Fatal(e)
				}
			case "concurrent_installation_refusal":
				if d, e := openSelectedDatabase(x.ctx, x.c, "serve"); e == nil || e.Error() != "INSTALLATION_PROCESS_ALREADY_RUNNING" || d != nil {
					if d != nil {
						_ = d.Close()
					}
					t.Fatal("concurrent selected owner admitted", e)
				}
			case "changed_ledger_is_not_repaired":
				x.exec(t, "UPDATE schema_migrations SET checksum=repeat('0',64) WHERE version=1", "UPDATE schema_migrations SET source_sha256=REPEAT('0',64) WHERE step=1")
				if e := x.d.Verify(x.ctx); e == nil {
					t.Fatal("changed applied source accepted")
				}
				var actual string
				if e := x.f.Store.QueryRow(x.ctx, x.f.Statement("SELECT checksum FROM schema_migrations WHERE version=1", "SELECT source_sha256 FROM schema_migrations WHERE step=1")).Scan(&actual); e != nil || actual != strings.Repeat("0", 64) {
					t.Fatal("readiness repaired ledger", e)
				}
			case "operator_schema_check_without_auth_assets":
				if e := x.d.Close(); e != nil {
					t.Fatal(e)
				}
				c := x.c
				c.Security.AuthHMACKeyFile = filepath.Join(x.root, "absent-hmac")
				c.Security.MailKeyringFile = filepath.Join(x.root, "absent-mail")
				c.Security.PasswordBlocklistFile = filepath.Join(x.root, "absent-blocklist")
				raw, e := yaml.Marshal(c)
				if e != nil {
					t.Fatal(e)
				}
				file := x.private(t, "operator.yaml", raw)
				args := os.Args
				t.Cleanup(func() { os.Args = args })
				os.Args = []string{"control", "check-schema", "--config", file}
				if e = run(); e != nil {
					t.Fatal("actual selected CLI schema check", e)
				}
			case "actual_shared_owner_construction":
				h := x.h
				w := h.Workspaces.Service
				if h.Nodes.Service.Identity != x.owner || h.Models.Service.Identity != x.owner || h.Distribution.Service.Identity != x.owner || h.Datasets.Service.Identity != x.owner || h.Training.Identity != x.owner || h.ModelPreparations.Identity != x.owner || h.Knowledge.Service.Identity != x.owner || h.Plugins.Service.Identity != x.owner || h.Workflows.Service.Identity != x.owner || h.Preferences.Service.Identity != x.owner || h.Notifications.Service.Identity != x.owner {
					t.Fatal("foreign Identity owner")
				}
				if h.Nodes.Service.Workspaces != w || h.Models.Service.Workspaces != w || h.Training.Workspaces != w || h.ModelPreparations.Workspaces != w || h.Knowledge.Service.Workspaces != w || h.Nodes.Service.Inference != h.Deployments.Service || h.Nodes.ComponentArtifact == nil {
					t.Fatal("unwired borrowed owner")
				}
				if x.d.mysql != nil && (x.owner.DB != nil || x.owner.Storage == nil || x.mail.DB != nil || x.mail.Storage == nil) {
					t.Fatal("MySQL PG fallback or missing mail owner")
				}
			case "installed_https_readiness_and_dual_entrances":
				stop := x.serve(t)
				defer stop()
				for _, audience := range []string{"portal", "admin"} {
					if status, _, _ := x.request(t, audience, "GET", "/", nil, nil, ""); status != 200 {
						t.Fatal("actual static entrance", audience, status)
					}
					if status, _, _ := x.request(t, audience, "GET", "/health/ready", nil, nil, ""); status != 200 {
						t.Fatal("actual selected readiness", audience, status)
					}
				}
			case "unbootstrapped_https_boundary_and_restart":
				stop := x.serve(t)
				if status, problem, _ := x.request(t, "portal", "GET", "/health/ready", nil, nil, ""); status != 503 || problem["code"] != "SERVICE_NOT_READY" {
					t.Fatal("unbootstrapped business ready")
				}
				if status, problem, _ := x.request(t, "portal", "GET", "/api/v1/me", nil, nil, ""); status != 401 || problem["code"] != "AUTH_REQUIRED" {
					t.Fatal("unbootstrapped identity admitted")
				}
				stop()
				if e := x.d.Close(); e != nil {
					t.Fatal(e)
				}
				d, e := openSelectedDatabase(x.ctx, x.c, "bootstrap-admin")
				if e != nil {
					t.Fatal(e)
				}
				owner := d.identity(x.c)
				owner.Passwords = x.owner.Passwords
				password := "Synthetic bootstrap passphrase " + uuid.NewString()
				if e = owner.Bootstrap(x.ctx, "bootstrap@example.invalid", "Synthetic operator", password, "Synthetic bootstrap tenant", "Default"); e != nil {
					_ = d.Close()
					t.Fatal("actual secure offline bootstrap", e)
				}
				if e = d.Close(); e != nil {
					t.Fatal(e)
				}
				x.d, e = openSelectedDatabase(x.ctx, x.c, "serve")
				if e != nil {
					t.Fatal(e)
				}
				x.owner = x.d.identity(x.c)
				x.owner.Passwords = owner.Passwords
				x.owner.HMACKey, e = config.Secret(x.c.Security.AuthHMACKeyFile)
				if e != nil {
					t.Fatal(e)
				}
				x.mail.DB = x.d.postgres
				x.mail.Storage = nil
				if x.d.mysql != nil {
					x.mail.Storage = x.d.store
				}
				x.owner.Mail = x.mail
				stop = x.serve(t)
				defer stop()
				if status, _, _ := x.request(t, "admin", "GET", "/health/ready", nil, nil, ""); status != 200 {
					t.Fatal("restart after actual bootstrap not ready", status)
				}
				for _, audience := range []string{"portal", "admin"} {
					status, body, cookies := x.request(t, audience, "GET", "/api/v1/auth/csrf", nil, nil, "")
					if status != 200 {
						t.Fatal("actual CSRF", status)
					}
					data, ok := body["data"].(map[string]any)
					if !ok {
						t.Fatal("actual CSRF envelope")
					}
					token, ok := data["token"].(string)
					if !ok {
						t.Fatal("actual CSRF token")
					}
					raw, _ := json.Marshal(map[string]string{"email": "bootstrap@example.invalid", "password": password})
					status, _, sessionCookies := x.request(t, audience, "POST", "/api/v1/auth/login", raw, cookies, token)
					if status != 200 {
						t.Fatal("actual HTTPS login after restart", audience, status)
					}
					found := false
					for _, cookie := range sessionCookies {
						if cookie.Name == "__Host-"+audience+"_session" {
							found = true
							if !cookie.Secure || !cookie.HttpOnly || cookie.Domain != "" || cookie.Path != "/" {
								t.Fatal("unsafe actual session cookie")
							}
						}
					}
					if !found {
						t.Fatal("actual session cookie missing")
					}
					cookies = append(cookies, sessionCookies...)
					if status, _, _ := x.request(t, audience, "GET", "/api/v1/me", nil, cookies, ""); status != 200 {
						t.Fatal("actual authenticated HTTPS", status)
					}
					status, body, newContext := x.request(t, audience, "GET", "/api/v1/auth/csrf", nil, cookies, "")
					if status != 200 {
						t.Fatal("actual authenticated CSRF", status)
					}
					token = body["data"].(map[string]any)["token"].(string)
					// The context cookie is stable; replace its previous copy explicitly.
					for _, replacement := range newContext {
						for i, cookie := range cookies {
							if cookie.Name == replacement.Name {
								cookies[i] = replacement
							}
						}
					}
					if status, _, _ := x.request(t, audience, "POST", "/api/v1/auth/logout", nil, cookies, token); status != 204 {
						t.Fatal("actual HTTPS logout", status)
					}
					if status, _, _ := x.request(t, audience, "GET", "/api/v1/me", nil, cookies, ""); status != 401 {
						t.Fatal("revoked HTTPS session survived", status)
					}
				}
			case "startup_owner_failure_never_listens":
				x.exec(t, "DELETE FROM user_preferences WHERE user_id=$1", "DELETE FROM user_preferences WHERE user_id=?", x.f.User)
				if e := serveSelected(x.ctx, x.ctx, x.c, x.d, x.owner, x.mail); e == nil {
					t.Fatal("missing owning preference state accepted")
				}
				conn, e := net.DialTimeout("tcp", x.c.Server.ListenAddress, 100*time.Millisecond)
				if e == nil {
					conn.Close()
					t.Fatal("failed startup exposed listener")
				}
			case "restore_revokes_authorities_preserves_physical_owner", "restore_atomic_rollback", "restore_unknown_before_commit", "restore_unknown_after_commit":
				n, d := x.deployment(t)
				staleActor := x.session(t)
				before := x.snapshot(t, n.NodeID, d.ID)
				owner := x.owner
				if name == "restore_atomic_rollback" {
					if x.f.Store.Dialect() == transaction.PostgreSQL {
						x.exec(t, "CREATE FUNCTION m20_restore_abort() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RAISE EXCEPTION 'synthetic restore failure';END;$$", "")
					}
					x.exec(t, "CREATE TRIGGER m20_restore_abort BEFORE UPDATE ON managed_nodes FOR EACH ROW EXECUTE FUNCTION m20_restore_abort()", "CREATE TRIGGER m20_restore_abort BEFORE UPDATE ON managed_nodes FOR EACH ROW SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='synthetic restore failure'")
				}
				if strings.HasPrefix(name, "restore_unknown_") {
					copy := *x.owner
					copy.DB = nil
					copy.Storage = restoreAcknowledgementStore{Store: x.d.store, before: name == "restore_unknown_before_commit"}
					owner = &copy
				}
				e := recovery.InvalidateRestored(x.ctx, owner, "Actual isolated selected whole restore")
				if name == "restore_revokes_authorities_preserves_physical_owner" {
					if e != nil {
						t.Fatal(e)
					}
				} else if e == nil {
					t.Fatal("failed/unknown restore reported success")
				}
				after := x.snapshot(t, n.NodeID, d.ID)
				if name == "restore_atomic_rollback" || name == "restore_unknown_before_commit" {
					if !reflect.DeepEqual(before, after) {
						t.Fatal("partial restore escaped rollback", before, after)
					}
					if _, e := x.h.Nodes.Service.Get(x.ctx, staleActor, "", n.NodeID); e != nil {
						t.Fatal("rollback lost actual current session", e)
					}
				} else {
					if after[0].(int64) != before[0].(int64)+1 || after[2] != "removed" || after[3] != false || after[4] != "recovery_required" || after[6] != "unknown" || after[7] != "cancelled" {
						t.Fatal("authority/physical ownership fence lost", after)
					}
					if v, e := x.h.Nodes.Service.Get(x.ctx, staleActor, "", n.NodeID); e == nil || !reflect.ValueOf(v).IsZero() {
						t.Fatal("old human authority survived restore", e)
					}
					if v, e := x.h.Nodes.Service.Claim(x.ctx, n.NodeID, n.Credential, uuid.NewString()); e == nil || !reflect.ValueOf(v).IsZero() {
						t.Fatal("old machine authority survived restore", e)
					}
				}
				if strings.HasPrefix(name, "restore_unknown_") && e.Error() != string(transaction.CommitUnknown) {
					t.Fatal("unknown restore classification lost", e)
				}
			case "inference_removal_receipt_replay", "inference_removal_foreign_prefence_rejected":
				n, d := x.deployment(t)
				if e := recovery.FenceInferenceNode(x.ctx, x.owner, n.NodeID, "Actual isolated operator fence"); e != nil {
					t.Fatal(e)
				}
				var fence time.Time
				if e := x.f.Store.QueryRow(x.ctx, x.f.Statement("SELECT inference_fenced_at FROM managed_nodes WHERE id=$1", "SELECT inference_fenced_at FROM managed_nodes WHERE id=?", n.NodeID)).Scan(&fence); e != nil {
					t.Fatal(e)
				}
				r := wire.InferenceRemovalReceipt{SchemaVersion: 1, ReceiptID: uuid.NewString(), NodeID: n.NodeID, DeploymentID: d.ID, ModelID: d.ModelID, CatalogDigest: wire.ModelCatalogDigest(), GPUUUID: d.GPUUUID, LocalRevision: d.Revision, OwnedContainerAbsent: true, RuntimeCacheAbsent: true, CompletedAt: fence.Add(time.Second)}
				before := x.snapshot(t, n.NodeID, d.ID)
				if name == "inference_removal_foreign_prefence_rejected" {
					for _, mutate := range []func(*wire.InferenceRemovalReceipt){func(r *wire.InferenceRemovalReceipt) { r.GPUUUID = "GPU-" + uuid.NewString() }, func(r *wire.InferenceRemovalReceipt) { r.CompletedAt = fence.Add(-time.Second) }, func(r *wire.InferenceRemovalReceipt) { r.LocalRevision = d.Revision + 1 }} {
						bad := r
						mutate(&bad)
						if e := recovery.ReconcileInferenceRemoval(x.ctx, x.owner, bad, "Actual rejected synthetic attestation"); e == nil {
							t.Fatal("foreign/pre-fence receipt released owner")
						}
						if !reflect.DeepEqual(before, x.snapshot(t, n.NodeID, d.ID)) {
							t.Fatal("failed receipt mutated owner")
						}
					}
					return
				}
				if e := recovery.ReconcileInferenceRemoval(x.ctx, x.owner, r, "Actual local-root synthetic attestation"); e != nil {
					t.Fatal(e)
				}
				after := x.snapshot(t, n.NodeID, d.ID)
				if after[4] != "released" || after[5] != "removed" || after[6] != "not_installed" {
					t.Fatal("confirmed operator release missing", after)
				}
				if e := recovery.ReconcileInferenceRemoval(x.ctx, x.owner, r, "Exact retained operator replay"); e != nil || !reflect.DeepEqual(after, x.snapshot(t, n.NodeID, d.ID)) {
					t.Fatal("receipt replay changed history", e)
				}
				r.ReceiptID = uuid.NewString()
				if e := recovery.ReconcileInferenceRemoval(x.ctx, x.owner, r, "Changed receipt must refuse"); e == nil || e.Error() != "IDEMPOTENCY_CONFLICT" {
					t.Fatal("changed receipt replay accepted", e)
				}
			}
		})
	}
}
