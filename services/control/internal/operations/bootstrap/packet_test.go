package bootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.yaml.in/yaml/v3"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/databaseaccess"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
)

func TestPreparedBootstrapPacketOfflinePrivateDistinctAndStrict(t *testing.T) {
	input, output := paths(t)
	if e := PreparePacket(input, output); e != nil {
		t.Fatal(e)
	}
	var p Packet
	raw, e := readPrivate(output, &p)
	if e != nil || p.validate() != nil {
		t.Fatal("private packet readback", e)
	}
	other := filepath.Join(filepath.Dir(output), "other-packet.json")
	if e := PreparePacket(input, other); e != nil {
		t.Fatal(e)
	}
	var q Packet
	if _, e := readPrivate(other, &q); e != nil || q.Password == p.Password {
		t.Fatal("non-distinct generated credential")
	}
	if e := PreparePacket(input, output); e == nil {
		t.Fatal("packet overwritten")
	}
	retained, _ := os.ReadFile(output)
	if !bytes.Equal(raw, retained) {
		t.Fatal("existing packet changed")
	}
	for name, bad := range map[string][]byte{
		"unknown":  bytes.Replace(raw, []byte(`"format_version":1`), []byte(`"format_version":1,"extra":true`), 1),
		"purpose":  bytes.Replace(raw, []byte(`"initial-administrator"`), []byte(`"other"`), 1),
		"null":     bytes.Replace(raw, []byte(`"password":"`+p.Password+`"`), []byte(`"password":null`), 1),
		"hash":     bytes.Replace(raw, []byte(p.RequestSHA256), []byte(strings.Repeat("0", 64)), 1),
		"encoding": bytes.Replace(raw, []byte(p.Password), []byte(p.Password+"="), 1),
		"missing":  []byte(`{"format_version":1}`),
	} {
		t.Run(name, func(t *testing.T) {
			file := filepath.Join(filepath.Dir(output), "invalid-packet.json")
			if os.WriteFile(file, bad, 0600) != nil {
				t.Fatal("owned packet fixture")
			}
			var p Packet
			if _, e := readPrivate(file, &p); e == nil && p.validate() == nil {
				t.Fatal("invalid packet accepted")
			}
		})
	}
}

type packetFixture struct {
	service                 *identity.Service
	runtime                 databaseaccess.RuntimeInput
	lease                   *pgxpool.Conn
	role, root, runtimeFile string
}

func newPacketFixture(t *testing.T) *packetFixture {
	t.Helper()
	s := identityfixture.New(t)
	input, _ := paths(t)
	root := filepath.Dir(input)
	role := "bp_runtime_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	password, e := databaseaccess.NewCredential()
	if e != nil {
		t.Fatal(e)
	}
	cc := s.DB.Config().ConnConfig
	q := url.Values{"sslmode": {"disable"}}
	for k, v := range cc.RuntimeParams {
		q.Set(k, v)
	}
	uri := url.URL{Scheme: "postgres", User: url.UserPassword(role, password), Host: net.JoinHostPort(cc.Host, strconv.Itoa(int(cc.Port))), Path: "/" + cc.Database, RawQuery: q.Encode()}
	c := s.Config
	c.Server.ListenAddress = "127.0.0.1:19443"
	c.Database.DSNFile = filepath.Join(root, "runtime-dsn")
	c.Database.MaxOpenConnections = 4
	c.Database.MaxIdleConnections = 1
	c.Database.StatementTimeoutSeconds = 5
	c.SMTP.Host = "smtp.example.invalid"
	c.SMTP.Port = 587
	c.SMTP.TLSMode = "starttls_required"
	c.SMTP.ConnectTimeoutSeconds = 5
	c.SMTP.OperationTimeoutSeconds = 20
	raw, e := yaml.Marshal(c)
	if e != nil {
		t.Fatal(e)
	}
	runtimeFile := filepath.Join(root, "runtime.yaml")
	if os.WriteFile(c.Database.DSNFile, []byte(uri.String()), 0600) != nil || os.WriteFile(runtimeFile, raw, 0600) != nil {
		t.Fatal("private runtime fixture")
	}
	runtime, e := databaseaccess.LoadRuntime(runtimeFile)
	if e != nil {
		t.Fatal(e)
	}
	held, e := s.DB.Acquire(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	var locked bool
	if e = held.QueryRow(context.Background(), "SELECT pg_try_advisory_lock($1)", database.InstallationLockID).Scan(&locked); e != nil || !locked {
		held.Release()
		t.Fatal("owned fixture installation lock", e)
	}
	f := &packetFixture{s, runtime, held, role, root, runtimeFile}
	t.Cleanup(func() {
		if f.lease != nil {
			f.lease.Exec(context.Background(), "SELECT pg_advisory_unlock($1)", database.InstallationLockID)
			f.lease.Release()
		}
		s.DB.Exec(context.Background(), "DROP OWNED BY "+pgx.Identifier{role}.Sanitize())
		s.DB.Exec(context.Background(), "DROP ROLE IF EXISTS "+pgx.Identifier{role}.Sanitize())
	})
	status, e := databaseaccess.Prepare(context.Background(), s.DB, held, runtime)
	if e != nil || status != databaseaccess.Verified {
		t.Fatal("actual owned restricted role", e)
	}
	return f
}
func (f *packetFixture) packet(t *testing.T) string {
	t.Helper()
	output := filepath.Join(f.root, "prepared-packet.json")
	if e := PreparePacket(filepath.Join(f.root, "request.json"), output); e != nil {
		t.Fatal(e)
	}
	return output
}
func TestActualPreparedBootstrapPacketTransactionCheckRepeatAndCredential(t *testing.T) {
	f := newPacketFixture(t)
	path := f.packet(t)
	ctx := context.Background()
	before, _ := os.ReadFile(path)
	if status, e := RunPacket(ctx, f.service, f.lease, CheckPacketCommand, path, f.runtime); e != nil || status != NotCommitted {
		t.Fatal("uncommitted check", e)
	}
	for _, command := range []string{ApplyPacketCommand, CheckPacketCommand, ApplyPacketCommand} {
		if status, e := RunPacket(ctx, f.service, f.lease, command, path, f.runtime); e != nil || status != Verified {
			t.Fatal("actual packet bootstrap", command, e)
		}
	}
	if count(t, f.service, "SELECT count(*) FROM users") != 1 || count(t, f.service, "SELECT count(*) FROM tenants") != 1 || count(t, f.service, "SELECT count(*) FROM workspaces") != 1 || count(t, f.service, "SELECT count(*) FROM audit_events WHERE action='installation.bootstrapped'") != 1 || count(t, f.service, "SELECT count(*) FROM sessions") != 0 || count(t, f.service, "SELECT count(*) FROM mail_deliveries") != 0 {
		t.Fatal("duplicate bootstrap or external delivery")
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("secret packet modified")
	}
	var packet Packet
	if _, e := readPrivate(path, &packet); e != nil {
		t.Fatal(e)
	}
	var phc string
	if e := f.service.DB.QueryRow(ctx, "SELECT password_phc FROM password_credentials").Scan(&phc); e != nil {
		t.Fatal(e)
	}
	if ok, e := f.service.Passwords.Verify(ctx, phc, packet.Password); e != nil || !ok {
		t.Fatal("real initial credential does not authenticate")
	}
	conflictingCredential, e := databaseaccess.NewCredential()
	if e != nil {
		t.Fatal(e)
	}
	packet.Password = conflictingCredential
	raw, _ := json.Marshal(packet)
	wrong := filepath.Join(f.root, "conflicting-packet.json")
	os.WriteFile(wrong, raw, 0600)
	if _, e := RunPacket(ctx, f.service, f.lease, ApplyPacketCommand, wrong, f.runtime); e == nil || e.Error() != "BOOTSTRAP_CREDENTIAL_CONFLICT" {
		t.Fatal("conflicting credential accepted or not actually checked", e)
	}
	if count(t, f.service, "SELECT count(*) FROM users") != 1 {
		t.Fatal("conflict changed user state")
	}
}
func TestActualPreparedBootstrapPacketRollbackAndLeaseRuntimeRefusal(t *testing.T) {
	f := newPacketFixture(t)
	path := f.packet(t)
	ctx := context.Background()
	if _, e := RunPacket(ctx, f.service, nil, ApplyPacketCommand, path, f.runtime); e == nil {
		t.Fatal("missing lease")
	}
	if _, e := RunPacket(ctx, f.service, f.lease, ApplyPacketCommand, path, databaseaccess.RuntimeInput{}); e == nil {
		t.Fatal("missing actual runtime context")
	}
	raw, e := os.ReadFile(f.runtimeFile)
	if e != nil {
		t.Fatal(e)
	}
	var runtimeConfig config.Config
	if yaml.Unmarshal(raw, &runtimeConfig) != nil {
		t.Fatal("owned runtime configuration")
	}
	dsn, e := os.ReadFile(runtimeConfig.Database.DSNFile)
	if e != nil {
		t.Fatal("owned runtime DSN")
	}
	endpoint, e := url.Parse(strings.TrimSpace(string(dsn)))
	if e != nil {
		t.Fatal("owned runtime URI")
	}
	endpoint.Host = net.JoinHostPort(endpoint.Hostname(), "1")
	runtimeConfig.Database.DSNFile = filepath.Join(f.root, "wrong-target-dsn")
	raw, _ = yaml.Marshal(runtimeConfig)
	other := filepath.Join(f.root, "wrong-target.yaml")
	if os.WriteFile(runtimeConfig.Database.DSNFile, []byte(endpoint.String()), 0600) != nil || os.WriteFile(other, raw, 0600) != nil {
		t.Fatal("owned wrong-target fixture")
	}
	wrong, e := databaseaccess.LoadRuntime(other)
	if e != nil {
		t.Fatal("explicit wrong target parse", e)
	}
	if _, e := RunPacket(ctx, f.service, f.lease, ApplyPacketCommand, path, wrong); e == nil || e.Error() != "RUNTIME_ACCESS_CONNECTION_CONTEXT_INVALID" {
		t.Fatal("wrong target reached bootstrap", e)
	}
	tenant := uuid.NewString()
	if _, e := f.service.DB.Exec(ctx, "INSERT INTO tenants(id,name,status) VALUES($1,'Synthetic partial installation','active')", tenant); e != nil {
		t.Fatal(e)
	}
	if _, e := RunPacket(ctx, f.service, f.lease, ApplyPacketCommand, path, f.runtime); e == nil || e.Error() != "BOOTSTRAP_INITIAL_INSTALLATION_REQUIRED" {
		t.Fatal("nonempty installation mutated", e)
	}
	if count(t, f.service, "SELECT count(*) FROM users") != 0 || count(t, f.service, "SELECT count(*) FROM tenants") != 1 {
		t.Fatal("refusal changed existing fixture state")
	}
	if _, e := f.service.DB.Exec(ctx, "DELETE FROM tenants WHERE id=$1", tenant); e != nil {
		t.Fatal(e)
	}
	// A held table lock fails the audit write without changing the catalog,
	// which the schema check would refuse.
	blocker, e := f.service.DB.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = blocker.Exec(ctx, "LOCK TABLE audit_events IN SHARE ROW EXCLUSIVE MODE"); e != nil {
		t.Fatal(e)
	}
	if _, e := RunPacket(ctx, f.service, f.lease, ApplyPacketCommand, path, f.runtime); e == nil || e.Error() != "BOOTSTRAP_MUTATION_UNCERTAIN_RECONCILE_REQUIRED" {
		t.Fatal("actual transaction failure", e)
	}
	if e = blocker.Rollback(ctx); e != nil {
		t.Fatal(e)
	}
	if count(t, f.service, "SELECT count(*) FROM users") != 0 || count(t, f.service, "SELECT count(*) FROM tenants") != 0 || count(t, f.service, "SELECT count(*) FROM workspaces") != 0 || count(t, f.service, "SELECT count(*) FROM audit_events") != 0 {
		t.Fatal("failed packet left partial state")
	}
	if status, e := RunPacket(ctx, f.service, f.lease, CheckPacketCommand, path, f.runtime); e != nil || status != NotCommitted {
		t.Fatal("rollback not observable", e)
	}
	if status, e := RunPacket(ctx, f.service, f.lease, ApplyPacketCommand, path, f.runtime); e != nil || status != Verified {
		t.Fatal("explicit proven rollback continuation", e)
	}
}
