//go:build repository_integration

package runtimeownerclosure_test

import (
	"encoding/json"
	"errors"
	"io/fs"
	"net/url"
	"os"
	"path"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	agent "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/components"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/distribution"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/repositoryfixture"
	pgassets "github.com/Passion-Flow/ownsolid/services/control/migrations"
	closure "github.com/Passion-Flow/ownsolid/services/control/migrations/mysql/candidates/runtime-owner-closure"
	wire "github.com/Passion-Flow/ownsolid/api/gateway/v1"
)

func stringsFrom(t *testing.T, f repositoryfixture.Fixture, pg, my string, args ...any) []string {
	t.Helper()
	rows, e := f.Store.Query(t.Context(), f.Statement(pg, my, args...))
	if e != nil {
		t.Fatal(e)
	}
	defer rows.Close()
	var result []string
	for rows.Next() {
		var v string
		if e = rows.Scan(&v); e != nil {
			t.Fatal(e)
		}
		result = append(result, v)
	}
	if e = rows.Err(); e != nil {
		t.Fatal(e)
	}
	sort.Strings(result)
	return result
}
func equalNames(t *testing.T, actual []string, expected map[string]bool) {
	t.Helper()
	want := make([]string, 0, len(expected))
	for v := range expected {
		want = append(want, v)
	}
	sort.Strings(want)
	if !reflect.DeepEqual(actual, want) {
		t.Fatal("actual exact owner inventory differs", actual, want)
	}
}
func schemaOwners(t *testing.T, f repositoryfixture.Fixture) {
	t.Helper()
	names, e := fs.Glob(pgassets.Files, "postgresql/*.up.sql")
	if e != nil {
		t.Fatal(e)
	}
	var pg strings.Builder
	baseline := map[string]bool{}
	expected := map[string]bool{}
	create := regexp.MustCompile(`CREATE TABLE ([a-z][a-z0-9_]*)\s*\(`)
	for _, name := range names {
		raw, e := fs.ReadFile(pgassets.Files, name)
		if e != nil {
			t.Fatal(e)
		}
		pg.Write(raw)
		pg.WriteByte('\n')
		version, e := strconv.Atoi(strings.SplitN(path.Base(name), "_", 2)[0])
		if e != nil {
			t.Fatal("invalid actual migration name", e)
		}
		for _, v := range create.FindAllStringSubmatch(string(raw), -1) {
			expected[v[1]] = true
			// M20 closes the shared M01-M19 schema through migration20. Later
			// PostgreSQL-only variants cannot invent MySQL tables or grants.
			if version <= 20 {
				baseline[v[1]] = true
			}
		}
	}
	if len(baseline) != 85 {
		t.Fatal("RCL_EXACT_VERSION_OWNED85_REQUIRED")
	}
	if f.Store.Dialect() == transaction.MySQL {
		expected = baseline
		expected["model_publications"] = true
	}
	expected["schema_migrations"] = true
	equalNames(t, stringsFrom(t, f, "SELECT table_name FROM information_schema.tables WHERE table_schema=current_schema() AND table_type='BASE TABLE'", "SELECT table_name FROM information_schema.tables WHERE table_schema=DATABASE() AND table_type='BASE TABLE'"), expected)
	sqlNames, e := fs.Glob(closure.Files, "*.sql")
	if e != nil {
		t.Fatal(e)
	}
	column := regexp.MustCompile(`(?mi)^\s*([a-z][a-z0-9_]*)\s+(?:uuid|char|varchar|bytea|varbinary|jsonb?|timestamptz|datetime|boolean|bigint|integer)\b`)
	named := regexp.MustCompile(`CONSTRAINT ([a-z][a-z0-9_]*) (?:FOREIGN KEY|CHECK)`)
	for _, name := range sqlNames {
		raw, e := fs.ReadFile(closure.Files, name)
		if e != nil {
			t.Fatal(e)
		}
		sql := string(raw)
		match := create.FindStringSubmatch(sql)
		if match == nil {
			continue
		}
		table := match[1]
		columns := map[string]bool{}
		for _, v := range column.FindAllStringSubmatch(sql, -1) {
			columns[v[1]] = true
		}
		if len(columns) == 0 {
			t.Fatal("declared owner source has no parsed columns", table)
		}
		equalNames(t, stringsFrom(t, f, "SELECT column_name FROM information_schema.columns WHERE table_schema=current_schema() AND table_name=$1", "SELECT column_name FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name=?", table), columns)
		if f.Store.Dialect() != transaction.MySQL {
			continue
		}
		constraints := stringsFrom(t, f, "", "SELECT constraint_name FROM information_schema.table_constraints WHERE constraint_schema=DATABASE() AND table_name=?", table)
		for _, v := range named.FindAllStringSubmatch(sql, -1) {
			if !contains(constraints, v[1]) {
				t.Fatal("actual named owner constraint missing", table, v[1])
			}
		}

		indexes := stringsFrom(t, f, "", "SELECT DISTINCT index_name FROM information_schema.statistics WHERE table_schema=DATABASE() AND table_name=?", table)
		if !contains(indexes, "PRIMARY") {
			t.Fatal("actual owner primary key missing", table)
		}
		for _, v := range regexp.MustCompile(`(?m)^\s*KEY ([a-z][a-z0-9_]*)\(`).FindAllStringSubmatch(sql, -1) {
			if !contains(indexes, v[1]) {
				t.Fatal("actual owner practical index missing", table, v[1])
			}
		}
		var checks int
		if e := f.Store.QueryRow(t.Context(), f.Statement("", "SELECT COUNT(*) FROM information_schema.table_constraints WHERE constraint_schema=DATABASE() AND table_name=? AND constraint_type='CHECK'", table)).Scan(&checks); e != nil || checks != strings.Count(sql, "CHECK(") {
			t.Fatal("actual exact owner CHECK count differs", table, checks, e)
		}
		// Exact measured compiler metadata is a separate RCL08 prerequisite. Here
		// verify every scoped FK's ordered column/target mapping against real SQL.
		foreign := regexp.MustCompile(`CONSTRAINT ([a-z][a-z0-9_]*) FOREIGN KEY\(([^)]+)\) REFERENCES ([a-z][a-z0-9_]*)\(([^)]+)\)`)
		combined := sql
		if table == "gateway_requests" {
			extra, e := fs.ReadFile(closure.Files, "000200_gateway_active_recovery.sql")
			if e != nil {
				t.Fatal(e)
			}
			combined += string(extra)
		}
		for _, v := range foreign.FindAllStringSubmatch(combined, -1) {
			rows, e := f.Store.Query(t.Context(), f.Statement("", "SELECT column_name,referenced_table_name,referenced_column_name FROM information_schema.key_column_usage WHERE constraint_schema=DATABASE() AND table_name=? AND constraint_name=? ORDER BY ordinal_position", table, v[1]))
			if e != nil {
				t.Fatal(e)
			}
			var left, right []string
			for rows.Next() {
				var a, b, c string
				if e = rows.Scan(&a, &b, &c); e != nil {
					t.Fatal(e)
				}
				if b != v[3] {
					t.Fatal("actual FK target changed")
				}
				left = append(left, a)
				right = append(right, c)
			}
			e = rows.Err()
			rows.Close()
			if e != nil || strings.Join(left, ",") != v[2] || strings.Join(right, ",") != v[4] {
				t.Fatal("actual FK mapping differs", v[1], e)
			}
		}
	}
	if f.Store.Dialect() == transaction.MySQL {
		actual := stringsFrom(t, f, "", "SELECT CONCAT(trigger_name,'|',action_timing,'|',event_manipulation,'|',action_order) FROM information_schema.triggers WHERE trigger_schema=DATABASE() AND event_object_table='model_alias_cutover_intents'")
		equalNames(t, actual, map[string]bool{"alias_intent_immutable_update|BEFORE|UPDATE|1": true, "alias_intent_immutable_delete|BEFORE|DELETE|1": true})
	}

}
func contains(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}

func TestActualRuntimeOwnerClosure(t *testing.T) {
	t.Run("RCL01_schema_owners", func(t *testing.T) { schemaOwners(t, rclFixture(t)) })
	t.Run("RCL02_component_runtime", func(t *testing.T) {
		g := pairedComponentSetup(t)
		g.installed(t)
		before := g.count(t, "SELECT COUNT(*) FROM audit_events", "SELECT COUNT(*) FROM audit_events")
		drop := g.auditFailure(t, "component.configure_requested")
		c := agent.ComponentConfig{LogLevel: "debug", MaxScrapeRequests: 3}
		input := components.Request{Action: "configure", Config: &c, Reason: "RCL actual nondefault configuration"}
		key := uuid.NewString()
		result, e := g.s.RequestDetailed(g.ctx, g.admin, g.node.NodeID, input, key, uuid.NewString())
		if e == nil || !reflect.DeepEqual(result, nodes.Task{}) || g.count(t, "SELECT COUNT(*) FROM audit_events", "SELECT COUNT(*) FROM audit_events") != before {
			t.Fatal("atomic audit rollback", e)
		}
		drop()
		task, e := g.s.RequestDetailed(g.ctx, g.admin, g.node.NodeID, input, key, uuid.NewString())
		if e != nil {
			t.Fatal(e)
		}
		g.complete(t, task, maintenanceObservation("1.12.0", &c), nil)
		state, e := g.s.Get(g.ctx, g.portal, g.f.Workspace, g.node.NodeID)
		if e != nil || state.AppliedConfig == nil || *state.AppliedConfig != c || state.ReceivedAt == nil || state.ReceivedAt.Location() != time.UTC {
			t.Fatal("actual current component", e)
		}
		replay, e := g.s.RequestDetailed(g.ctx, g.admin, g.node.NodeID, input, key, uuid.NewString())
		if e != nil || replay.ID != task.ID {
			t.Fatal("exact intent replay", e)
		}
		foreignWorkspace, foreignTask := g.foreignTask(t)
		refusal(t, g.f, "UPDATE managed_components SET workspace_id=$1 WHERE node_id=$2", "UPDATE managed_components SET workspace_id=? WHERE node_id=?", foreignWorkspace, g.node.NodeID)
		foreignTenant, foreignScope := uuid.NewString(), uuid.NewString()
		g.exec(t, "INSERT INTO tenants(id,name,status) VALUES($1,'RCL actual foreign installation','active')", "INSERT INTO tenants(id,name,status) VALUES(?,'RCL actual foreign installation','active')", foreignTenant)
		g.exec(t, "INSERT INTO workspaces(id,tenant_id,name,status) VALUES($1,$2,'RCL foreign tenant Workspace','active')", "INSERT INTO workspaces(id,tenant_id,name,status) VALUES(?,?,'RCL foreign tenant Workspace','active')", foreignScope, foreignTenant)
		refusal(t, g.f, "UPDATE managed_components SET tenant_id=$1,workspace_id=$2 WHERE node_id=$3", "UPDATE managed_components SET tenant_id=?,workspace_id=? WHERE node_id=?", foreignTenant, foreignScope, g.node.NodeID)
		denied, e := g.s.Get(g.ctx, g.portal, foreignScope, g.node.NodeID)
		if e == nil || !reflect.DeepEqual(denied, components.State{}) {
			t.Fatal("actual foreign tenant projected")
		}

		refusal(t, g.f, "UPDATE managed_components SET last_task_id=$1 WHERE node_id=$2", "UPDATE managed_components SET last_task_id=? WHERE node_id=?", foreignTask.ID, g.node.NodeID)
		g.complete(t, g.request(t, "remove"), agent.ComponentObservation{Component: agent.NodeExporter, Version: agent.NodeExporterVersion, State: "not_installed", ObservedAt: time.Now().UTC()}, nil)
		state, e = g.s.Get(g.ctx, g.admin, "", g.node.NodeID)
		if e != nil || state.Observation == nil || state.Observation.State != "not_installed" {
			t.Fatal("actual remove result", e)
		}
		g.exec(t, "UPDATE users SET auth_epoch=auth_epoch+1 WHERE id=$1", "UPDATE users SET auth_epoch=auth_epoch+1 WHERE id=?", g.f.User)
		state, e = g.s.Get(g.ctx, g.portal, g.f.Workspace, g.node.NodeID)
		if e == nil || !reflect.DeepEqual(state, components.State{}) {
			t.Fatal("stale actor projected")
		}
	})
	t.Run("RCL03_latest_logs", func(t *testing.T) {
		g := pairedComponentSetup(t)
		g.installed(t)
		c := agent.ComponentConfig{LogLevel: "warn", MaxScrapeRequests: 2}
		logs := pairedComponentLogs()
		g.complete(t, g.request(t, "logs"), maintenanceObservation("1.12.0", &c), &logs)
		logs.Entries[0].Message = "RCL actual latest replacement"
		task := g.request(t, "logs")
		claim := g.claim(t, task)
		o := maintenanceObservation("1.12.0", &c)
		result := agent.Result{LeaseToken: claim.Lease.LeaseToken, Outcome: "success", Component: &o, Logs: &logs}
		drop := g.auditFailure(t, "node.task_succeeded")
		if ack, e := g.s.Nodes.Complete(g.ctx, g.node.NodeID, g.node.Credential, task.ID, result, uuid.NewString()); e == nil || ack != "" {
			t.Fatal("failed log audit projected")
		}
		drop()
		if _, e := g.s.Nodes.Complete(g.ctx, g.node.NodeID, g.node.Credential, task.ID, result, uuid.NewString()); e != nil {
			t.Fatal(e)
		}
		got, e := g.s.Logs(g.ctx, g.admin, g.node.NodeID)
		if e != nil || got.Snapshot == nil || got.TaskID == nil || *got.TaskID != task.ID || got.Snapshot.Entries[0].Message != logs.Entries[0].Message || g.count(t, "SELECT COUNT(*) FROM component_log_snapshots", "SELECT COUNT(*) FROM component_log_snapshots") != 1 {
			t.Fatal("actual latest snapshot", e)
		}
		refusal(t, g.f, "UPDATE component_log_snapshots SET snapshot='[]'::jsonb WHERE node_id=$1", "UPDATE component_log_snapshots SET snapshot=CAST('[]' AS JSON) WHERE node_id=?", g.node.NodeID)
		oversized, _ := json.Marshal(map[string]string{"message": strings.Repeat("x", 17000)})
		refusal(t, g.f, "UPDATE component_log_snapshots SET snapshot=$1::jsonb WHERE node_id=$2", "UPDATE component_log_snapshots SET snapshot=CAST(? AS JSON) WHERE node_id=?", oversized, g.node.NodeID)
		_, foreign := g.foreignTask(t)
		refusal(t, g.f, "UPDATE component_log_snapshots SET task_id=$1 WHERE node_id=$2", "UPDATE component_log_snapshots SET task_id=? WHERE node_id=?", foreign.ID, g.node.NodeID)
		if _, e = g.s.Logs(g.ctx, g.portal, g.node.NodeID); e == nil {
			t.Fatal("Portal read private journal")
		}
		s := g.wrapped(&pairedComponentOutcomeStore{Store: g.f.Store, at: 1})
		uncertain, e := s.Logs(g.ctx, g.admin, g.node.NodeID)
		if e == nil || !reflect.DeepEqual(uncertain, components.LogSnapshot{}) {
			t.Fatal("unknown read projected")
		}
	})
	t.Run("RCL04_key_authority", func(t *testing.T) {
		g := pairedDistributionSetup(t).access(t)
		key := uuid.NewString()
		input := g.keyInput()
		input.Label = "RCL exact private key"
		minted, e := g.s.MintKey(g.ctx, g.portal, input, key, uuid.NewString())
		if e != nil || minted.Secret == nil {
			t.Fatal(e)
		}
		again, e := g.s.MintKey(g.ctx, g.portal, input, key, uuid.NewString())
		if e != nil || again.Key.ID != minted.Key.ID || again.Secret != nil {
			t.Fatal("secret/idempotence replay", e)
		}
		page, e := g.s.KeyPage(g.ctx, g.portal, g.f.Workspace, url.Values{})
		if e != nil || len(page.Items) != 2 {
			t.Fatal("private key page", e)
		}
		if _, e = g.s.AuthorizeModels(g.ctx, g.gateway.Gateway.ID, *g.gateway.Secret, *minted.Secret); e != nil {
			t.Fatal(e)
		}
		for _, field := range []string{"secret_hash", "fingerprint"} {
			refusal(t, g.f, "UPDATE api_keys SET "+field+"=$1 WHERE id=$2", "UPDATE api_keys SET "+field+"=? WHERE id=?", []byte{1}, minted.Key.ID)
		}
		refusal(t, g.f, "UPDATE api_keys SET expires_at=created_at WHERE id=$1", "UPDATE api_keys SET expires_at=created_at WHERE id=?", minted.Key.ID)
		refusal(t, g.f, "UPDATE api_keys SET revoked_at=clock_timestamp(),revoke_reason=NULL WHERE id=$1", "UPDATE api_keys SET revoked_at=UTC_TIMESTAMP(6),revoke_reason=NULL WHERE id=?", minted.Key.ID)
		if _, e = g.s.RevokeKey(g.ctx, g.portal, g.f.Workspace, minted.Key.ID, "RCL actual revoke", uuid.NewString()); e != nil {
			t.Fatal(e)
		}
		if _, e = g.s.AuthorizeModels(g.ctx, g.gateway.Gateway.ID, *g.gateway.Secret, *minted.Secret); e == nil {
			t.Fatal("revoked key authorized")
		}
		g.exec(t, "UPDATE api_keys SET created_at=clock_timestamp()-interval '2 minutes',expires_at=clock_timestamp()-interval '1 minute' WHERE id=$1", "UPDATE api_keys SET created_at=TIMESTAMPADD(MINUTE,-2,UTC_TIMESTAMP(6)),expires_at=TIMESTAMPADD(MINUTE,-1,UTC_TIMESTAMP(6)) WHERE id=?", g.key.Key.ID)
		if _, e = g.s.AuthorizeModels(g.ctx, g.gateway.Gateway.ID, *g.gateway.Secret, *g.key.Secret); e == nil {
			t.Fatal("expired key authorized")
		}
		viewerID := uuid.NewString()
		email := viewerID + "@example.invalid"
		g.exec(t, "INSERT INTO users(id,email_canonical,email_display,display_name,locale,status) VALUES($1,$2,$3,'RCL private other viewer','en','active')", "INSERT INTO users(id,email_canonical,email_display,display_name,locale,status) VALUES(?,?,?,'RCL private other viewer','en','active')", viewerID, email, email)
		g.exec(t, "INSERT INTO workspace_memberships(user_id,tenant_id,workspace_id,role) VALUES($1,$2,$3,'viewer')", "INSERT INTO workspace_memberships(user_id,tenant_id,workspace_id,role) VALUES(?,?,?,'viewer')", viewerID, g.f.Tenant, g.f.Workspace)
		viewer := g.sessionAs(t, viewerID, "portal")
		private, e := g.s.KeyPage(g.ctx, viewer, g.f.Workspace, url.Values{})
		if e != nil || len(private.Items) != 0 {
			t.Fatal("other creator private keys exposed", e)
		}
		detail, e := g.s.Key(g.ctx, viewer, g.f.Workspace, minted.Key.ID)
		if e == nil || !reflect.DeepEqual(detail, distribution.APIKey{}) {
			t.Fatal("other creator private key detail exposed")
		}
		g.exec(t, "DELETE FROM workspace_memberships WHERE user_id=$1 AND workspace_id=$2", "DELETE FROM workspace_memberships WHERE user_id=? AND workspace_id=?", g.f.User, g.f.Workspace)
		if keys, e := g.s.Keys(g.ctx, g.portal, g.f.Workspace); e == nil || len(keys) != 0 {
			t.Fatal("removed member keys projected")
		}
	})
	t.Run("RCL05_gateway_capacity", func(t *testing.T) {
		g := pairedDistributionSetup(t).access(t)
		input := g.admission()
		if _, e := g.s.Admit(g.ctx, g.gateway.Gateway.ID, *g.gateway.Secret, input, uuid.NewString()); e != nil {
			t.Fatal(e)
		}
		g.held(t, input.RequestID, true)
		refusal(t, g.f, "UPDATE gateway_requests SET capacity_held=false WHERE id=$1", "UPDATE gateway_requests SET capacity_held=false WHERE id=?", input.RequestID)
		refusal(t, g.f, "UPDATE gateway_requests SET deadline_at=admitted_at WHERE id=$1", "UPDATE gateway_requests SET deadline_at=admitted_at WHERE id=?", input.RequestID)
		refusal(t, g.f, "UPDATE gateway_requests SET alias_revision=999 WHERE id=$1", "UPDATE gateway_requests SET alias_revision=999 WHERE id=?", input.RequestID)
		refusal(t, g.f, "UPDATE gateway_requests SET state='reported',resolved_at=clock_timestamp(),reported_at=clock_timestamp(),outcome='{}'::jsonb,outcome_sha256=repeat('a',64) WHERE id=$1", "UPDATE gateway_requests SET state='reported',resolved_at=UTC_TIMESTAMP(6),reported_at=UTC_TIMESTAMP(6),outcome=CAST('{}' AS JSON),outcome_sha256=REPEAT('a',64) WHERE id=?", input.RequestID)
		code := "gateway_interrupted"
		uncertain := wire.Outcome{RequestID: input.RequestID, Status: "incomplete", ErrorCode: &code, ElapsedMilliseconds: 1, OutputStarted: true}
		if _, e := g.s.Report(g.ctx, g.gateway.Gateway.ID, *g.gateway.Secret, uncertain, uuid.NewString()); e != nil {
			t.Fatal(e)
		}
		g.held(t, input.RequestID, true)
		other := g.admission()
		if _, e := g.s.Admit(g.ctx, g.gateway.Gateway.ID, *g.gateway.Secret, other, uuid.NewString()); e != nil {
			t.Fatal(e)
		}
		success := pairedDistributionSuccess(other.RequestID)
		if _, e := g.s.Report(g.ctx, g.gateway.Gateway.ID, *g.gateway.Secret, success, uuid.NewString()); e != nil {
			t.Fatal(e)
		}
		g.held(t, other.RequestID, false)
		record, e := g.s.Request(g.ctx, g.portal, g.f.Workspace, other.RequestID)
		if e != nil || record.Outcome == nil || record.Outcome.Usage == nil || record.Outcome.Usage.TotalTokens != 9 {
			t.Fatal("exact outcome history", e)
		}
	})
	t.Run("RCL06_recovery_cycle", func(t *testing.T) {
		g := pairedDistributionSetup(t).access(t)
		input := g.admission()
		other := g.admission()
		for _, in := range []wire.AdmissionRequest{input, other} {
			if _, e := g.s.Admit(g.ctx, g.gateway.Gateway.ID, *g.gateway.Secret, in, uuid.NewString()); e != nil {
				t.Fatal(e)
			}
		}
		if _, e := g.s.RevokeGateway(g.ctx, g.admin, g.f.Workspace, g.gateway.Gateway.ID, "RCL ordered recovery", uuid.NewString()); e != nil {
			t.Fatal(e)
		}
		key := uuid.NewString()
		challenge, e := g.s.CreateRecoveryChallenge(g.ctx, g.admin, g.f.Workspace, input.RequestID, "RCL exact recovery", key, uuid.NewString())
		if e != nil {
			t.Fatal(e)
		}
		again, e := g.s.CreateRecoveryChallenge(g.ctx, g.admin, g.f.Workspace, input.RequestID, "RCL exact recovery", key, uuid.NewString())
		if e != nil || again.ID != challenge.ID {
			t.Fatal("exact challenge replay", e)
		}
		refusal(t, g.f, "UPDATE gateway_requests SET active_recovery_id=$1 WHERE id=$2", "UPDATE gateway_requests SET active_recovery_id=? WHERE id=?", challenge.ID, other.RequestID)
		refusal(t, g.f, "UPDATE gateway_recoveries SET fence_accepted_at=clock_timestamp() WHERE id=$1", "UPDATE gateway_recoveries SET fence_accepted_at=UTC_TIMESTAMP(6) WHERE id=?", challenge.ID)
		refusal(t, g.f, "UPDATE gateway_recoveries SET confirmed_at=clock_timestamp(),confirmed_deployment_revision=0 WHERE id=$1", "UPDATE gateway_recoveries SET confirmed_at=UTC_TIMESTAMP(6),confirmed_deployment_revision=0 WHERE id=?", challenge.ID)
		refusal(t, g.f, "UPDATE gateway_recoveries SET fingerprint=$1 WHERE id=$2", "UPDATE gateway_recoveries SET fingerprint=? WHERE id=?", []byte{1}, challenge.ID)
		before := g.stopEnvelope(t, 1)
		if _, e = g.s.AcceptRecoveryFence(g.ctx, g.admin, g.f.Workspace, input.RequestID, challenge.ID, g.fence(challenge.ID), "RCL explicit synthetic physical fence", uuid.NewString()); e != nil {
			t.Fatal(e)
		}
		if _, e = g.s.ConfirmRecovery(g.ctx, g.admin, g.f.Workspace, input.RequestID, challenge.ID, before, "Reject old stop", uuid.NewString()); e == nil {
			t.Fatal("old stop released capacity")
		}
		g.held(t, input.RequestID, true)
		after := g.stopEnvelope(t, before)
		if confirmed, e := g.s.ConfirmRecovery(g.ctx, g.admin, g.f.Workspace, input.RequestID, challenge.ID, after, "RCL new positive stop", uuid.NewString()); e != nil || confirmed.ConfirmedAt == nil {
			t.Fatal(e)
		}
		g.held(t, input.RequestID, false)
	})
	t.Run("RCL07_immutable_cutover", func(t *testing.T) {
		g := pairedDistributionSetup(t).access(t)
		key := uuid.NewString()
		in := distribution.CutoverRequest{Action: "pause", ExpectedRevision: 1, Reason: "RCL immutable target"}
		result, e := g.s.Cutover(g.ctx, g.admin, g.f.Workspace, g.alias.ID, in, key, uuid.NewString())
		if e != nil || result.AppliedRevision != 2 {
			t.Fatal(e)
		}
		replay, e := g.s.Cutover(g.ctx, g.admin, g.f.Workspace, g.alias.ID, in, key, uuid.NewString())
		if e != nil || !replay.Replayed || replay.AppliedRevision != 2 {
			t.Fatal("historical exact replay", e)
		}
		refusal(t, g.f, "UPDATE model_alias_cutover_intents SET result_revision=1 WHERE alias_id=$1", "UPDATE model_alias_cutover_intents SET result_revision=1 WHERE alias_id=?", g.alias.ID)
		refusal(t, g.f, "DELETE FROM model_alias_cutover_intents WHERE alias_id=$1", "DELETE FROM model_alias_cutover_intents WHERE alias_id=?", g.alias.ID)
		refusal(t, g.f, "INSERT INTO model_alias_cutover_intents(tenant_id,workspace_id,actor_user_id,idempotency_key,alias_id,result_revision,fingerprint) VALUES($1,$2,$3,$4,$5,999,$6)", "INSERT INTO model_alias_cutover_intents(tenant_id,workspace_id,actor_user_id,idempotency_key,alias_id,result_revision,fingerprint) VALUES(?,?,?,?,?,999,?)", g.f.Tenant, g.f.Workspace, g.f.User, uuid.NewString(), g.alias.ID, make([]byte, 32))
		in.Reason = "Changed exact intent"
		if v, e := g.s.Cutover(g.ctx, g.admin, g.f.Workspace, g.alias.ID, in, key, uuid.NewString()); e == nil || !reflect.DeepEqual(v, distribution.CutoverResult{}) {
			t.Fatal("changed intent projected")
		}
	})
}

// Separate root: the runner must provision an actual203 migrated fresh schema
// and exact temporary DML-only grants before invoking this existing native binary.
func TestActualRuntimeOwnerClosureProtectedRuntime(t *testing.T) {
	if os.Getenv("PROJECT_ONE_RCL_PREINSTALLED_SCHEMA") != "true" {
		t.Fatal("RCL_PROTECTED_MIGRATED_SCOPE_REQUIRED")
	}
	g := pairedDistributionSetup(t).access(t)
	var principal, cipherName, cipher string
	if e := g.f.Store.QueryRow(g.ctx, transaction.SQL(transaction.MySQL, "SELECT CURRENT_USER()")).Scan(&principal); e != nil || !regexp.MustCompile(`^rcl_[a-f0-9]{24}@%$`).MatchString(principal) {
		t.Fatal("RCL_ACTUAL_PROTECTED_PRINCIPAL_REQUIRED", e)
	}
	if e := g.f.Store.QueryRow(g.ctx, transaction.SQL(transaction.MySQL, "SHOW SESSION STATUS LIKE 'Ssl_cipher'")).Scan(&cipherName, &cipher); e != nil || cipherName != "Ssl_cipher" || cipher == "" {
		t.Fatal("RCL_ACTUAL_TLS_RUNTIME_SESSION_REQUIRED", e)
	}
	t.Log("RCL_RUNTIME_PRINCIPAL=" + principal)
	t.Log("RCL_RUNTIME_TLS_CONFIRMED")
	input := g.admission()
	if _, e := g.s.Admit(g.ctx, g.gateway.Gateway.ID, *g.gateway.Secret, input, uuid.NewString()); e != nil {
		t.Fatal("protected actual admission", e)
	}
	if _, e := g.s.Report(g.ctx, g.gateway.Gateway.ID, *g.gateway.Secret, pairedDistributionSuccess(input.RequestID), uuid.NewString()); e != nil {
		t.Fatal("protected actual report", e)
	}
	g.held(t, input.RequestID, false)
	if _, e := g.s.RevokeKey(g.ctx, g.portal, g.f.Workspace, g.key.Key.ID, "Protected actual revoke", uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	c := rclComponentForDistribution(t, g)
	config := agent.ComponentConfig{LogLevel: "debug", MaxScrapeRequests: 3}
	logs := pairedComponentLogs()
	c.complete(t, c.request(t, "logs"), maintenanceObservation("1.12.0", &config), &logs)
	if out, e := c.s.Logs(g.ctx, g.admin, c.node.NodeID); e != nil || out.Snapshot == nil {
		t.Fatal("protected actual logs", e)
	}
	if _, e := g.s.Cutover(g.ctx, g.admin, g.f.Workspace, g.alias.ID, distribution.CutoverRequest{Action: "pause", ExpectedRevision: 1, Reason: "Protected actual immutable cutover"}, uuid.NewString(), uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	for index, sql := range []string{"CREATE TABLE rcl_forbidden(id INT)", "ALTER TABLE api_keys ADD COLUMN rcl_forbidden INT", "GRANT SELECT ON *.* TO '" + strings.TrimSuffix(principal, "@%") + "'@'%'", "UPDATE schema_migrations SET state=state WHERE step=1", "DELETE FROM schema_migrations WHERE step=1"} {
		_, e := g.f.Store.Exec(g.ctx, transaction.SQL(transaction.MySQL, sql))
		var denied *transaction.Error
		if !errors.As(e, &denied) || !(denied.Number == 1044 || denied.Number == 1142 || denied.Number == 1227 || denied.Number == 1410 || index == 2 && denied.Number == 1045) {
			t.Fatal("RCL_ACTUAL_PRIVILEGE_REFUSAL_REQUIRED", e)
		}
		t.Logf("RCL_PRIVILEGE_REFUSAL number=%d", denied.Number)
	}
}

func rclComponentForDistribution(t *testing.T, g pairedDistributionStory) pairedComponentStory {
	t.Helper()
	n := &nodes.Service{Identity: g.owner, Workspaces: g.s.Workspaces}
	c := &components.Service{Nodes: n}
	n.Components = c
	join, e := n.MintForProfile(g.ctx, g.admin, g.f.Workspace, "RCL protected component", agent.SystemdMaintenanceProfile, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	facts := agent.Facts{OSID: "ubuntu", OSVersion: "24.04", Architecture: "amd64", Hostname: "rcl-protected", LogicalCPUs: 1, MemoryTotalBytes: 1024, DiskPath: "/", DiskTotalBytes: 4096, DiskAvailableBytes: 2048, ObservedAt: time.Now().UTC(), AgentVersion: agent.AgentVersion}
	h := agent.Handshake{ProtocolVersion: agent.MaintenanceVersion, ExecutionProfile: agent.SystemdMaintenanceProfile, Capabilities: agent.Capabilities(agent.SystemdMaintenanceProfile), Facts: facts}
	enrolled, e := n.Enroll(g.ctx, agent.Enrollment{JoinToken: join.Token, Handshake: h}, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	o := maintenanceObservation("1.12.0", &agent.ComponentConfig{LogLevel: "debug", MaxScrapeRequests: 3})
	h.Component = &o
	if _, e = n.Heartbeat(g.ctx, enrolled.NodeID, enrolled.Credential, h); e != nil {
		t.Fatal(e)
	}
	if _, e = c.Get(g.ctx, g.admin, "", enrolled.NodeID); e != nil {
		t.Fatal("actual component read", e)
	}
	return pairedComponentStory{f: g.f, ctx: g.ctx, owner: g.owner, s: c, admin: g.admin, portal: g.portal, node: enrolled, handshake: h}
}

// Called only after the separate real203 migration, before logical export.
func TestActualRuntimeOwnerClosureRetainedGraph(t *testing.T) {
	if os.Getenv("PROJECT_ONE_RCL_PREINSTALLED_SCHEMA") != "true" {
		t.Fatal("RCL_ACTUAL_MIGRATED_GRAPH_REQUIRED")
	}
	g := pairedDistributionSetup(t).access(t)
	in := g.admission()
	if _, e := g.s.Admit(g.ctx, g.gateway.Gateway.ID, *g.gateway.Secret, in, uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	if _, e := g.s.RevokeGateway(g.ctx, g.admin, g.f.Workspace, g.gateway.Gateway.ID, "RCL retained recovery graph", uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	challenge, e := g.s.CreateRecoveryChallenge(g.ctx, g.admin, g.f.Workspace, in.RequestID, "RCL retained exact challenge", uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	if _, e = g.s.AcceptRecoveryFence(g.ctx, g.admin, g.f.Workspace, in.RequestID, challenge.ID, g.fence(challenge.ID), "Explicit synthetic physical fence envelope", uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	if _, e = g.s.Cutover(g.ctx, g.admin, g.f.Workspace, g.alias.ID, distribution.CutoverRequest{Action: "pause", ExpectedRevision: 1, Reason: "RCL immutable retained history"}, uuid.NewString(), uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	c := rclComponentForDistribution(t, g)
	config := agent.ComponentConfig{LogLevel: "debug", MaxScrapeRequests: 3}
	logs := pairedComponentLogs()
	c.complete(t, c.request(t, "logs"), maintenanceObservation("1.12.0", &config), &logs)
	g.held(t, in.RequestID, true)
}
