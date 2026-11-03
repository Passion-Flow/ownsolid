//go:build cgo && odbc_manager && dameng_monitoring_integration

package monitoring

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	agent "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/components"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/deployments"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/distribution"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/licensing"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/models"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/dameng"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/monitoringmetrics"
	gateway "github.com/Passion-Flow/ownsolid/api/gateway/v1"
	licensewire "github.com/Passion-Flow/ownsolid/pkg/license/v1"
)

// This test requires the real admitted driver and an externally provisioned,
// empty cumulative native schema. It owns synthetic rows only, never DDL, vendor
// assets or a server. Retain that schema for independent readback/owner cleanup.
// The common M30 service patch must be integrated; unpatched PG fallthrough fails.
func TestDamengMonitoringNative(t *testing.T) {
	runtimeDirectory := nativeMonitoringRuntimeDir(t)
	path := os.Getenv("PROJECT_ONE_DAMENG_MONITORING_CONFIGURATION")
	schema := os.Getenv("PROJECT_ONE_DAMENG_MONITORING_DISPOSABLE_SCHEMA")
	if path == "" || !strings.HasPrefix(schema, "P1_MONITOR_TEST_") {
		t.Fatal("explicit protected native configuration and disposable P1_MONITOR_TEST_ schema required")
	}
	c, e := dameng.LoadConnectionConfiguration(path, "")
	if e != nil || c.Schema != schema {
		t.Fatal("native schema/configuration admission refused")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()
	kernel, _, e := dameng.Open(ctx, c)
	if kernel != nil {
		t.Cleanup(func() {
			closing, done := context.WithTimeout(context.Background(), time.Duration(c.CleanupSeconds)*time.Second)
			defer done()
			state, e := kernel.Close(closing)
			if e != nil || state.State != "closed" || state.PhysicalOwners != 0 || state.CleanupJobs != 0 {
				t.Error("native cleanup incomplete; retain owner recovery evidence")
			}
		})
	}
	if e != nil {
		t.Fatal("native owner admission", dameng.SafeError(e))
	}
	store, e := transaction.FromDameng(kernel)
	if e != nil {
		t.Fatal(e)
	}
	f := &nativeMonitoringFixture{t: t, ctx: ctx, store: store, policy: Defaults()}
	for _, table := range []string{"TENANTS", "USERS", "WORKSPACES", "MANAGED_NODES", "GATEWAY_REQUESTS", "AUDIT_EVENTS"} {
		if f.count(`SELECT COUNT(*) FROM "`+table+`"`) != 0 {
			t.Fatal("nonempty schema refused", table)
		}
	}
	var complete bool
	f.must("unbootstrapped singleton", store.QueryRow(ctx, transaction.SQL(transaction.Dameng, `SELECT "BOOTSTRAP_COMPLETE" FROM "INSTALLATION_STATE" WHERE "ID"=1`)).Scan(&complete))
	if complete {
		t.Fatal("bootstrapped schema refused")
	}
	blocklist := filepath.Join(runtimeDirectory, "synthetic-password-blocklist")
	f.must("password input", os.WriteFile(blocklist, []byte("passwordpasswordpassword\n"), 0600))
	passwords, e := security.NewPasswords(blocklist)
	f.must("password owner", e)
	key := make([]byte, 32)
	_, e = rand.Read(key)
	f.must("synthetic HMAC key", e)
	f.i = &identity.Service{Storage: store, Passwords: passwords, HMACKey: key}
	f.i.Config.Server.AdminOrigin = "https://admin.example.invalid"
	f.i.Config.Server.PortalOrigin = "https://portal.example.invalid"
	password := "Synthetic native credential " + uuid.NewString()
	f.must("bootstrap", f.i.Bootstrap(ctx, "monitoring@example.invalid", "Synthetic monitoring operator", password, "Synthetic monitoring tenant", "Synthetic monitoring Workspace"))
	f.admin = f.login(password, "admin")
	f.portal = f.login(password, "portal")
	f.must("installed scope", store.QueryRow(ctx, transaction.SQL(transaction.Dameng, `SELECT "TENANT_ID","DEFAULT_WORKSPACE_ID" FROM "INSTALLATION_STATE" WHERE "ID"=1`)).Scan(&f.tenant, &f.workspace))
	f.w = &workspaces.Service{Identity: f.i}
	f.n = &nodes.Service{Identity: f.i, Workspaces: f.w}
	f.c = &components.Service{Nodes: f.n}
	f.n.Components = f.c
	m := &models.Service{Identity: f.i, Workspaces: f.w}
	d := &deployments.Service{Nodes: f.n, Models: m}
	f.n.Inference = d
	f.d = &distribution.Service{Identity: f.i, Workspaces: f.w, Nodes: f.n, Deployments: d}
	licenseKey, trust, e := licensewire.GenerateKey("explicit-native-monitoring-synthetic-license")
	f.must("synthetic license trust", e)
	f.l, e = licensing.New(f.i, trust, licensewire.DefaultPolicy(), func() time.Time { return time.Now().UTC() })
	f.must("license owner", e)
	f.i.BusinessAdmission = f.l
	f.recorder, e = monitoringmetrics.New(f.policy.HTTP, nil)
	f.must("recorder owner", e)
	f.rebuild()
	if !t.Run("native_clob_null_and_exact_wire_limit", func(t *testing.T) {
		defer f.subtest(t)()
		var absent []byte
		f.must("native NULL CLOB", store.QueryRow(ctx, transaction.SQL(transaction.Dameng, `SELECT CAST(NULL AS CLOB) FROM DUAL`)).Scan(&absent))
		if absent != nil {
			t.Fatal("native SQL NULL became empty bytes")
		}
		// Escaped ASCII is a legal UTF8 wire representation. Whitespace pads the
		// valid JSON to the real wire ceiling; no JSON rewriting/truncation is
		// allowed in the native CLOB/text conversion or driver.
		raw := []byte(`{"request_id":"` + uuid.NewString() + `","status":"succee\u0064ed","error_code":null,"usage":{"prompt_tokens":7,"completion_tokens":2,"total_tokens":9},"elapsed_milliseconds":19,"output_started":true}`)
		raw = append(raw, []byte(strings.Repeat(" ", gateway.MaxOutcomeBytes-len(raw)))...)
		value, e := gateway.DecodeOutcome(raw)
		f.must("maximum wire input", e)
		digest, e := value.Digest()
		f.must("maximum input digest", e)
		var readback []byte
		f.must("native maximum CLOB text", store.QueryRow(ctx, transaction.SQL(transaction.Dameng, `SELECT CAST(? AS CLOB) FROM DUAL`, string(raw))).Scan(&readback))
		if string(readback) != string(raw) {
			t.Fatal("native maximum CLOB changed bytes")
		}
		decoded, e := gateway.DecodeOutcome(readback)
		f.must("maximum CLOB decoded", e)
		after, e := decoded.Digest()
		f.must("maximum CLOB digest", e)
		if digest != after {
			t.Fatal("native CLOB changed canonical digest")
		}
		if _, e = gateway.DecodeOutcome(append(readback, ' ')); e == nil {
			t.Fatal("wire ceiling changed")
		}
		f.largeCLOBWindow(raw, value.RequestID, digest)
	}) {
		return
	}

	if !t.Run("actual_empty_snapshot_missing_license_and_diagnostic_audit", func(t *testing.T) {
		defer f.subtest(t)()
		before := f.count(`SELECT COUNT(*) FROM "AUDIT_EVENTS" WHERE "ACTION"='monitoring.snapshot'`)
		started := time.Now().UTC()
		out := f.get("")
		if out.License.State != licensing.Missing || out.License.NewBusinessAllowed || out.ObservedAt.IsZero() || !monitoringNativeTime(out.ObservedAt) || out.Gateway.Sampled != 0 || len(out.Nodes) != 0 || out.Process.Epoch == "" || f.count(`SELECT COUNT(*) FROM "AUDIT_EVENTS" WHERE "ACTION"='monitoring.snapshot'`) != before+1 {
			t.Fatal("actual empty diagnostics or audit invalid")
		}
		if out.ObservedAt.Before(started.Add(-time.Second)) || out.ObservedAt.After(time.Now().UTC().Add(time.Second)) {
			t.Fatal("native clock/session decoding is not the actual server UTC interval")
		}
		f.denied(f.portal, f.workspace, "")
	}) {
		return
	}
	state, e := f.l.StatusAdmin(ctx, f.admin)
	f.must("license deployment", e)
	now := time.Now().UTC().Truncate(time.Second)
	claims := licensewire.Claims{FormatVersion: 1, LicenseID: uuid.NewString(), CustomerReference: "Explicit native monitoring fixture", DeploymentID: state.DeploymentID, IssuedAt: now.Add(-time.Minute).Format(time.RFC3339), NotBefore: now.Add(-time.Minute).Format(time.RFC3339), ExpiresAt: now.Add(time.Hour).Format(time.RFC3339), GraceUntil: now.Add(2 * time.Hour).Format(time.RFC3339), Scope: licensewire.WholePlatformScope}
	raw, e := licensewire.Issue(claims, licenseKey)
	f.must("synthetic license artifact", e)
	_, e = f.l.Import(ctx, f.admin, licensing.ImportInput{Artifact: string(raw), Reason: "Isolated native monitoring fixture"}, uuid.NewString())
	f.must("actual license import", e)
	var firstNode, secondNode string
	if !t.Run("tied_microsecond_keysets_stale_removed_nodes", func(t *testing.T) {
		defer f.subtest(t)()
		firstNode = f.component()
		secondNode = f.component()
		tie := time.Now().UTC().Truncate(time.Microsecond)
		f.exec(`UPDATE "MANAGED_NODES" SET "CREATED_AT"=? WHERE "ID" IN (?,?)`, tie, firstNode, secondNode)
		f.policy.NodePageSize = 1
		f.rebuild()
		first := f.get("")
		if len(first.Nodes) != 1 || first.NextNodeCursor == nil || first.Nodes[0].Metrics == nil || first.Nodes[0].Stale {
			t.Fatal("first node page invalid")
		}
		at, id, e := decodeCursor(*first.NextNodeCursor)
		f.must("canonical cursor", e)
		if !at.Equal(tie) || id != first.Nodes[0].NodeID {
			t.Fatal("native keyset rounded")
		}
		second := f.get(*first.NextNodeCursor)
		ids := []string{firstNode, secondNode}
		sort.Sort(sort.Reverse(sort.StringSlice(ids)))
		if len(second.Nodes) != 1 || second.NextNodeCursor != nil || first.Nodes[0].NodeID != ids[0] || second.Nodes[0].NodeID != ids[1] {
			t.Fatal("tied keyset repeated/skipped/changed order")
		}
		f.policy.NodePageSize = 25
		f.rebuild()
		f.exec(`UPDATE "MANAGED_NODES" SET "LAST_HEARTBEAT_AT"=? WHERE "ID"=?`, tie.Add(-time.Hour), firstNode)
		for _, node := range f.get("").Nodes {
			if node.NodeID == firstNode && !node.Stale {
				t.Fatal("offline observation revived")
			}
		}
		current, e := f.n.Get(ctx, f.admin, f.workspace, secondNode)
		f.must("node before remove", e)
		_, e = f.n.Update(ctx, f.admin, secondNode, current.Name, "removed", current.Version, "Native fixture remove", uuid.NewString())
		f.must("actual node removal", e)
		for _, node := range f.get("").Nodes {
			if node.NodeID == secondNode && (node.Status != "removed" || !node.Stale) {
				t.Fatal("removed node revived")
			}
		}
	}) {
		return
	}
	if !t.Run("actual_gateway_outcomes_digest_usage_held_and_sample", func(t *testing.T) {
		defer f.subtest(t)()
		f.access()
		for _, ms := range []uint64{3, 19, 7} {
			f.report(ms, "succeeded")
		}
		f.report(11, "interrupted")
		out := f.get("").Gateway
		if out.Reported != 4 || out.UsageKnown != 3 || out.UsageUnknown != 1 || out.TotalTokens != 27 || out.HeldInSample != 1 || out.P95Milliseconds == nil || *out.P95Milliseconds != 19 || out.ErrorRate == nil || *out.ErrorRate != 0.25 {
			t.Fatal("real native outcome/usage sample invalid")
		}
		// Exercise actual persisted admission boundaries, not a Go clock guess or
		// an expected SQL string. All four requests belong to this isolated scope.
		var oldest, newest time.Time
		f.must("native admission bounds", f.store.QueryRow(ctx, transaction.SQL(transaction.Dameng, `SELECT MIN("ADMITTED_AT"),MAX("ADMITTED_AT") FROM "GATEWAY_REQUESTS" WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=?`, f.tenant, f.workspace)).Scan(&oldest, &newest))
		oldest, newest = oldest.UTC(), newest.UTC()
		if !monitoringNativeTime(oldest) || !monitoringNativeTime(newest) || newest.Before(oldest) {
			t.Fatal("native admission boundary precision invalid")
		}
		var newestTies int
		f.must("native boundary ties", f.store.QueryRow(ctx, transaction.SQL(transaction.Dameng, `SELECT COUNT(*) FROM "GATEWAY_REQUESTS" WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=? AND "ADMITTED_AT"=?`, f.tenant, f.workspace, newest)).Scan(&newestTies))
		if newestTies < 1 || newestTies > 4 {
			t.Fatal("native boundary tie count invalid")
		}
		if v := f.window(oldest, newest.Add(time.Microsecond)); v.Sampled != 4 || v.Truncated {
			t.Fatal("inclusive native admission start lost rows")
		}
		if v := f.window(oldest.Add(-time.Microsecond), oldest); v.Sampled != 0 || v.Truncated || v.OldestAdmission != nil || v.NewestAdmission != nil || v.P50Milliseconds != nil || v.ErrorRate != nil {
			t.Fatal("exclusive native admission end included boundary rows or invented measurements")
		}
		if v := f.window(newest, newest.Add(time.Microsecond)); v.Sampled != newestTies || v.Truncated {
			t.Fatal("native newest tied admissions lost at inclusive start")
		}
		if oldest.Before(newest) {
			if v := f.window(oldest, newest); v.Sampled != 4-newestTies || v.Truncated {
				t.Fatal("native exclusive end included newest tied admissions")
			}
		}
		f.policy.MaximumGatewayRows = 2
		f.rebuild()
		out = f.get("").Gateway
		if out.Sampled != 2 || !out.Truncated || out.OldestAdmission == nil || out.NewestAdmission == nil {
			t.Fatal("native finite window coverage invalid")
		}
		f.policy.MaximumGatewayRows = 10000
		f.rebuild()
		out = f.get("").Gateway
		if out.Sampled != 4 || out.Truncated || out.Maximum != 10000 {
			t.Fatal("native10001 lookahead policy query changed coverage")
		}
		f.policy.MaximumGatewayRows = 1000
		f.rebuild()
	}) {
		return
	}
	if !t.Run("atomic_audit_rollback_unknown_commit_and_cancelled_delivery", func(t *testing.T) {
		defer f.subtest(t)()
		before := f.count(`SELECT COUNT(*) FROM "AUDIT_EVENTS" WHERE "ACTION"='monitoring.snapshot'`)
		var clock time.Time
		f.must("trusted clock before", store.QueryRow(ctx, transaction.SQL(transaction.Dameng, `SELECT "OBSERVED_AT" FROM "PLATFORM_LICENSE_STATE" WHERE "ID"=1`)).Scan(&clock))
		fault := &nativeMonitoringFaultStore{Store: store}
		f.i.Storage = fault
		fault.armed.Store(true)
		f.denied(f.admin, f.workspace, "")
		var after time.Time
		f.must("trusted clock after rollback", store.QueryRow(ctx, transaction.SQL(transaction.Dameng, `SELECT "OBSERVED_AT" FROM "PLATFORM_LICENSE_STATE" WHERE "ID"=1`)).Scan(&after))
		if f.count(`SELECT COUNT(*) FROM "AUDIT_EVENTS" WHERE "ACTION"='monitoring.snapshot'`) != before || !after.Equal(clock) {
			t.Fatal("failed transaction published audit/clock")
		}
		fault.unknown = true
		fault.armed.Store(true)
		out, e := f.s.Get(ctx, f.admin, f.workspace, "", uuid.NewString())
		if e == nil || e.Error() != string(transaction.CommitUnknown) || !reflect.DeepEqual(out, Snapshot{}) || f.count(`SELECT COUNT(*) FROM "AUDIT_EVENTS" WHERE "ACTION"='monitoring.snapshot'`) != before+1 {
			t.Fatal("actual committed ACK loss exposed projection or replayed", e)
		}
		request, stop := context.WithCancel(ctx)
		fault.unknown = false
		fault.cancel = stop
		fault.armed.Store(true)
		out, e = f.s.Get(request, f.admin, f.workspace, "", uuid.NewString())
		if e == nil || !reflect.DeepEqual(out, Snapshot{}) || f.count(`SELECT COUNT(*) FROM "AUDIT_EVENTS" WHERE "ACTION"='monitoring.snapshot'`) != before+2 {
			t.Fatal("cancelled known-commit delivery exposed projection")
		}
		f.i.Storage = store
	}) {
		return
	}
	if !t.Run("current_source_session_epoch_foreign_and_disabled_workspace", func(t *testing.T) {
		defer f.subtest(t)()
		f.denied(f.portal, f.workspace, "")
		foreignTenant, foreignWorkspace := uuid.NewString(), uuid.NewString()
		f.exec(`INSERT INTO "TENANTS"("ID","NAME","STATUS") VALUES(?,'Foreign native fixture','active')`, foreignTenant)
		f.exec(`INSERT INTO "WORKSPACES"("ID","TENANT_ID","NAME","STATUS") VALUES(?,?,'Foreign native fixture','active')`, foreignWorkspace, foreignTenant)
		f.denied(f.admin, foreignWorkspace, "")
		f.denied(f.admin, f.workspace, "invalid-cursor")
		f.exec(`UPDATE "WORKSPACES" SET "STATUS"='disabled' WHERE "ID"=?`, f.workspace)
		f.denied(f.admin, f.workspace, "")
		f.exec(`UPDATE "WORKSPACES" SET "STATUS"='active' WHERE "ID"=?`, f.workspace)
		f.exec(`DELETE FROM "SESSIONS" WHERE "ID"=?`, f.admin.SessionID)
		f.denied(f.admin, f.workspace, "")
		f.admin = f.login(password, "admin")
		f.exec(`UPDATE "USERS" SET "AUTH_EPOCH"="AUTH_EPOCH"+1 WHERE "ID"=?`, f.admin.Me.User.ID)
		f.denied(f.admin, f.workspace, "")
	}) {
		return
	}
}

// This independent native-fixture prerequisite touches only its new private
// runtime directory. It can run before any vendor server/configuration is ready.
func TestDamengMonitoringRuntimeStorage(t *testing.T) {
	directory := nativeMonitoringRuntimeDir(t)
	p := filepath.Join(directory, "storage-probe")
	value := []byte("synthetic-native-runtime-storage-" + uuid.NewString())
	f, e := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		t.Fatal("MONITORING_NATIVE_RUNTIME_WRITE_FAILED")
	}
	n, writeError := f.Write(value)
	syncError, closeError := f.Sync(), f.Close()
	if writeError != nil || syncError != nil || closeError != nil || n != len(value) {
		t.Fatal("MONITORING_NATIVE_RUNTIME_SYNC_FAILED")
	}
	got, e := os.ReadFile(p)
	if e != nil || !bytes.Equal(got, value) {
		t.Fatal("MONITORING_NATIVE_RUNTIME_READBACK_FAILED")
	}
}

func nativeMonitoringRuntimeDir(t *testing.T) string {
	t.Helper()
	// Go1.27.2 testing.TempDir selects GOTMPDIR before os.TempDir/TMPDIR.
	// The compiler directory must never be inherited by this runtime child.
	if os.Getenv("GOTMPDIR") != "" {
		t.Fatal("MONITORING_NATIVE_COMPILER_TMPDIR_INHERITED")
	}
	root := os.Getenv("TMPDIR")
	info, e := os.Lstat(root)
	real, resolveError := filepath.EvalSymlinks(root)
	if root == "" || !filepath.IsAbs(root) || filepath.Clean(root) != root || e != nil || resolveError != nil || real != root || !info.IsDir() || info.Mode().Perm() != 0700 {
		t.Fatal("MONITORING_NATIVE_RUNTIME_TMPDIR_REQUIRED")
	}
	directory := t.TempDir()
	relative, e := filepath.Rel(root, directory)
	if e != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		t.Fatal("MONITORING_NATIVE_RUNTIME_TMPDIR_MISMATCH")
	}
	observation, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	command := exec.CommandContext(observation, "/usr/bin/findmnt", "--json", "--target", directory, "--output", "TARGET,SOURCE,FSTYPE,OPTIONS")
	command.Env = []string{"LC_ALL=C"}
	raw, e := command.Output()
	var state struct {
		Filesystems []struct {
			Target  string `json:"target"`
			Source  string `json:"source"`
			FSType  string `json:"fstype"`
			Options string `json:"options"`
		} `json:"filesystems"`
	}
	if e != nil || len(raw) > 16384 || json.Unmarshal(raw, &state) != nil || len(state.Filesystems) != 1 {
		t.Fatal("MONITORING_NATIVE_RUNTIME_FILESYSTEM_UNKNOWN")
	}
	fs := state.Filesystems[0]
	if fs.Target == "" || fs.Source == "" || fs.FSType == "" || fs.FSType == "tmpfs" || fs.FSType == "ramfs" || !strings.Contains(","+fs.Options+",", ",rw,") {
		t.Fatal("MONITORING_NATIVE_RUNTIME_FILESYSTEM_REFUSED")
	}
	// This records actual mapping; the fixture owner must still establish the
	// durable backing/source identity. A filesystem type alone is not proof.
	compact, _ := json.Marshal(state)
	t.Logf("native_runtime_tmpdir=%s native_runtime_mount=%s", directory, compact)
	return directory
}

type nativeMonitoringFixture struct {
	t                 *testing.T
	ctx               context.Context
	store             transaction.Store
	i                 *identity.Service
	w                 *workspaces.Service
	n                 *nodes.Service
	c                 *components.Service
	d                 *distribution.Service
	l                 *licensing.Service
	recorder          *monitoringmetrics.Recorder
	s                 *Service
	policy            Config
	admin, portal     identity.Auth
	tenant, workspace string
	gateway           distribution.Provisioned
	alias             distribution.Alias
	key               distribution.MintedKey
}

func (f *nativeMonitoringFixture) subtest(t *testing.T) func() {
	previous := f.t
	f.t = t
	return func() { f.t = previous }
}
func (f *nativeMonitoringFixture) must(label string, e error) {
	f.t.Helper()
	if e != nil {
		f.t.Fatal(label, e)
	}
}
func (f *nativeMonitoringFixture) exec(query string, args ...any) {
	f.t.Helper()
	_, e := f.store.Exec(f.ctx, transaction.SQL(transaction.Dameng, query, args...))
	f.must("native fixture SQL", e)
}
func (f *nativeMonitoringFixture) count(query string) int {
	f.t.Helper()
	var n int
	f.must("native count", f.store.QueryRow(f.ctx, transaction.SQL(transaction.Dameng, query)).Scan(&n))
	return n
}
func (f *nativeMonitoringFixture) login(password, audience string) identity.Auth {
	f.t.Helper()
	_, token, _, e := f.i.Login(f.ctx, "monitoring@example.invalid", password, audience, "", uuid.NewString())
	f.must("native login", e)
	a, e := f.i.Authenticate(f.ctx, token, audience)
	f.must("native authentication", e)
	return a
}
func (f *nativeMonitoringFixture) rebuild() {
	f.t.Helper()
	var e error
	f.s, e = New(f.i, f.w, f.l, f.c, f.d, f.recorder, f.policy)
	f.must("monitoring assembly", e)
}
func (f *nativeMonitoringFixture) get(cursor string) Snapshot {
	f.t.Helper()
	out, e := f.s.Get(f.ctx, f.admin, f.workspace, cursor, uuid.NewString())
	f.must("native monitoring read", e)
	return out
}

// This is the direct owning helper's window contract, not an API snapshot or a
// second pool. The actual store supplies one transaction with the same current
// actor/install/active-Workspace/License fences; only known commit returns it.
func (f *nativeMonitoringFixture) window(from, until time.Time) distribution.MonitoringWindow {
	f.t.Helper()
	var out distribution.MonitoringWindow
	e := f.store.Within(f.ctx, false, func(ctx context.Context, q transaction.Executor) error {
		if e := f.i.RecheckOrderedWithin(ctx, q, f.admin); e != nil {
			return e
		}
		var tenant string
		if e := q.QueryRow(ctx, installationStatement(q)).Scan(&tenant); e != nil {
			return e
		}
		w, e := f.w.RecheckScopeWithin(ctx, q, f.admin, f.workspace)
		if e != nil {
			return e
		}
		if tenant != w.TenantID || w.Status != "active" {
			return errors.New("ACCESS_DENIED")
		}
		if _, e = f.l.MonitoringWithin(ctx, q, f.admin); e != nil {
			return e
		}
		out, e = f.d.MonitoringWithin(ctx, q, f.admin, f.workspace, from, until, f.policy.MaximumGatewayRows)
		return e
	})
	f.must("native direct window", e)
	return out
}

// This bounded native transport fixture has the same twelve result types as a
// full gateway page and one lookahead row. It is not 10001 business admissions
// or a capacity benchmark: the real Admit/Report/window cases remain separate.
// Keeping CLOB type is essential: 10001*4096 alone exceeds the ordinary16MiB
// result budget. No configured byte/LOB/retained-memory guard is relaxed here.
func (f *nativeMonitoringFixture) largeCLOBWindow(raw []byte, request, digest string) {
	f.t.Helper()
	const rowsRequired = 10001
	if len(raw) != gateway.MaxOutcomeBytes {
		f.t.Fatal("exact maximum legal outcome required")
	}
	admitted := time.Now().UTC().Truncate(time.Microsecond)
	deadline, resolved := admitted.Add(time.Second), admitted.Add(19*time.Millisecond)
	rows, e := f.store.Query(f.ctx, transaction.SQL(transaction.Dameng, `SELECT CAST(? AS CHAR(36)),CAST('reported' AS VARCHAR(16)),CAST(0 AS BIT),CAST(? AS TIMESTAMP(6)),CAST(? AS TIMESTAMP(6)),CAST(? AS TIMESTAMP(6)),CAST(? AS TIMESTAMP(6)),CAST(? AS CLOB),CAST(? AS VARCHAR(64)),CAST(16 AS INTEGER),CAST(65536 AS INTEGER),CAST(300 AS INTEGER) FROM DUAL CONNECT BY LEVEL<=10001`, request, admitted, deadline, resolved, resolved, string(raw), digest))
	f.must("native10001 maximum CLOB result-shape query", e)
	defer rows.Close()
	count := 0
	for rows.Next() {
		var id, state string
		var held bool
		var admission, due time.Time
		var resolution, report *time.Time
		var outcome []byte
		var sha *string
		var maxOutput, maxContext, seconds int
		f.must("native maximum CLOB row scan", rows.Scan(&id, &state, &held, &admission, &due, &resolution, &report, &outcome, &sha, &maxOutput, &maxContext, &seconds))
		if id != request || state != "reported" || held || !admission.Equal(admitted) || !due.Equal(deadline) || resolution == nil || report == nil || !resolution.Equal(resolved) || !report.Equal(resolved) || !bytes.Equal(outcome, raw) || sha == nil || *sha != digest || maxOutput != 16 || maxContext != 65536 || seconds != 300 {
			f.t.Fatal("native maximum result changed type, nullable fields, bytes, digest or binding")
		}
		count++
		if count > rowsRequired {
			f.t.Fatal("native result exceeded fixed fixture row bound")
		}
	}
	f.must("native maximum CLOB rows terminal", rows.Err())
	if count != rowsRequired {
		f.t.Fatal("native maximum CLOB result lost or truncated rows")
	}
}
func (f *nativeMonitoringFixture) denied(a identity.Auth, workspace, cursor string) {
	f.t.Helper()
	out, e := f.s.Get(f.ctx, a, workspace, cursor, uuid.NewString())
	if e == nil || !reflect.DeepEqual(out, Snapshot{}) {
		f.t.Fatal("failed read exposed projection", e)
	}
}
func (f *nativeMonitoringFixture) enroll(profile string) (agent.Enrolled, agent.Handshake) {
	f.t.Helper()
	join, e := f.n.MintForProfile(f.ctx, f.admin, f.workspace, "Native monitoring "+uuid.NewString(), profile, uuid.NewString())
	f.must("native join", e)
	version := agent.InferenceVersion
	if profile == agent.SystemdComponentsProfile {
		version = agent.ManagementVersion
	}
	h := agent.Handshake{ProtocolVersion: version, ExecutionProfile: profile, Capabilities: agent.Capabilities(profile), Facts: agent.Facts{OSID: "ubuntu", OSVersion: "24.04", Architecture: "amd64", Hostname: "synthetic-native-monitoring-node", LogicalCPUs: 4, MemoryTotalBytes: 16 << 30, DiskPath: "/", DiskTotalBytes: 100 << 30, DiskAvailableBytes: 50 << 30, ObservedAt: time.Now().UTC(), AgentVersion: agent.AgentVersion}}
	n, e := f.n.Enroll(f.ctx, agent.Enrollment{JoinToken: join.Token, Handshake: h}, uuid.NewString())
	f.must("native enrollment", e)
	return n, h
}
func (f *nativeMonitoringFixture) component() string {
	f.t.Helper()
	n, h := f.enroll(agent.SystemdComponentsProfile)
	h.Component = &agent.ComponentObservation{Component: agent.NodeExporter, Version: agent.NodeExporterVersion, Managed: true, State: "running", ObservedAt: time.Now().UTC(), Metrics: &agent.ComponentMetrics{CPUSecondsTotal: 100, CPUIdleSecondsTotal: 40, MemoryTotalBytes: 4096, MemoryAvailableBytes: 2048, FilesystemTotalBytes: 8192, FilesystemAvailableBytes: 4096}}
	_, e := f.n.Heartbeat(f.ctx, n.NodeID, n.Credential, h)
	f.must("actual stored component observation", e)
	return n.NodeID
}
func (f *nativeMonitoringFixture) access() {
	f.t.Helper()
	n, _ := f.enroll(agent.DockerInferenceProfile)
	model, deployment, gpu := uuid.NewString(), uuid.NewString(), "GPU-"+uuid.NewString()
	manifest, _ := json.Marshal(agent.ApprovedModel())
	f.exec(`INSERT INTO "MODEL_VERSIONS"("ID","TENANT_ID","WORKSPACE_ID","NAME","MODEL_KEY","CATALOG_DIGEST","MANIFEST","STATE","CREATED_BY") VALUES(?,?,?,'Synthetic native monitoring model','qwen3-0.6b',?,?,'ready',?)`, model, f.tenant, f.workspace, agent.ModelCatalogDigest(), string(manifest), f.admin.Me.User.ID)
	o := agent.DeploymentObservation{DeploymentID: deployment, ModelID: model, CatalogDigest: agent.ModelCatalogDigest(), GPUUUID: gpu, Revision: 1, Managed: true, State: "running", Ready: true, ContainerID: strings.Repeat("a", 64), ImageID: agent.ApprovedInferenceRecipe().ImageConfigDigest, Verification: &agent.GenerationVerification{Model: agent.InferenceModelName, PromptSHA256: agent.InferencePromptDigest(), ResponseSHA256: strings.Repeat("c", 64), InputTokens: 1, OutputTokens: 1, ObservedAt: time.Now().UTC()}}
	raw, _ := json.Marshal(o)
	// Only physical node/model facts are synthetic; business persistence,
	// current authority, signed license, admission/outcome and digest are real.
	f.exec(`INSERT INTO "DEPLOYMENTS"("ID","TENANT_ID","WORKSPACE_ID","NODE_ID","MODEL_ID","GPU_UUID","DESIRED_STATE","OBSERVED_STATE","REVISION","OBSERVATION","OBSERVED_AT","CREATED_BY","CATALOG_DIGEST") VALUES(?,?,?,?,?,?,'running','running',1,?,SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)),?,?)`, deployment, f.tenant, f.workspace, n.NodeID, model, gpu, string(raw), f.admin.Me.User.ID, agent.ModelCatalogDigest())
	f.exec(`INSERT INTO "GPU_LEASES"("ID","TENANT_ID","WORKSPACE_ID","NODE_ID","GPU_UUID","DEPLOYMENT_ID","REVISION","STATE") VALUES(?,?,?,?,?,?,1,'held')`, deployment, f.tenant, f.workspace, n.NodeID, gpu, deployment)
	var e error
	f.gateway, e = f.d.Provision(f.ctx, f.admin, distribution.ProvisionRequest{WorkspaceID: f.workspace, NodeID: n.NodeID, Name: "Native monitoring gateway", ClientOrigin: "https://gateway.example.invalid", Reason: "Native monitoring fixture"}, uuid.NewString(), uuid.NewString())
	f.must("native gateway provision", e)
	f.alias, e = f.d.Publish(f.ctx, f.admin, distribution.PublishRequest{WorkspaceID: f.workspace, GatewayID: f.gateway.Gateway.ID, Name: "native-monitor-model", DeploymentID: deployment, DeploymentRevision: 1, Reason: "Native monitoring fixture"}, uuid.NewString(), uuid.NewString())
	f.must("native alias publication", e)
	f.key, e = f.d.MintKey(f.ctx, f.portal, distribution.KeyRequest{WorkspaceID: f.workspace, AliasID: f.alias.ID, Label: "Native monitoring key", TTLSeconds: 300, Reason: "Native monitoring fixture"}, uuid.NewString(), uuid.NewString())
	f.must("native key", e)
}
func (f *nativeMonitoringFixture) report(ms uint64, status string) {
	f.t.Helper()
	id := uuid.NewString()
	_, e := f.d.Admit(f.ctx, f.gateway.Gateway.ID, *f.gateway.Secret, gateway.AdmissionRequest{RequestID: id, APIKey: *f.key.Secret, Model: f.alias.Name, MaximumOutputTokens: 16}, uuid.NewString())
	f.must("native request admission", e)
	o := gateway.Outcome{RequestID: id, Status: status, ElapsedMilliseconds: ms}
	if status == "succeeded" {
		o.OutputStarted = true
		o.Usage = &gateway.Usage{PromptTokens: 7, CompletionTokens: 2, TotalTokens: 9}
	} else {
		code := "gateway_interrupted"
		o.ErrorCode = &code
	}
	_, e = f.d.Report(f.ctx, f.gateway.Gateway.ID, *f.gateway.Secret, o, uuid.NewString())
	f.must("native outcome persistence", e)
}

// The instrumentation drops only the acknowledgement after the real native
// commit, or provokes a real native constraint failure before that commit. It
// does not pretend a driver/network fault or an absent native server occurred.
type nativeMonitoringFaultStore struct {
	transaction.Store
	armed   atomic.Bool
	unknown bool
	cancel  context.CancelFunc
}

func (s *nativeMonitoringFaultStore) Within(ctx context.Context, ro bool, fn func(context.Context, transaction.Executor) error) error {
	injected := false
	e := s.Store.Within(ctx, ro, func(ctx context.Context, q transaction.Executor) error {
		if e := fn(ctx, q); e != nil {
			return e
		}
		if s.armed.CompareAndSwap(true, false) {
			injected = true
			if !s.unknown && s.cancel == nil {
				_, e := q.Exec(ctx, transaction.SQL(transaction.Dameng, `INSERT INTO "AUDIT_EVENTS"("ID","AUDIENCE","ACTION","OUTCOME","REQUEST_ID") VALUES('invalid','admin','monitoring.constraint','invalid','invalid')`))
				return e
			}
		}
		return nil
	})
	if e == nil && injected {
		if s.unknown {
			return errors.Join(context.Canceled, &transaction.Error{Kind: transaction.CommitRolledBack}, &transaction.Error{Kind: transaction.CommitUnknown})
		}
		if s.cancel != nil {
			s.cancel()
		}
	}
	return e
}
