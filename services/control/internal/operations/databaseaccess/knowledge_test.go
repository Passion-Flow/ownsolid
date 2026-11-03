package databaseaccess

import (
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/components"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	"strings"
	"testing"
	"time"
)

func TestM12RuntimeUpgradePreservesRoleAndResumesGrants(t *testing.T) {
	for _, interrupted := range []bool{false, true} {
		t.Run(map[bool]string{false: "complete", true: "resume-after-schema"}[interrupted], func(t *testing.T) {
			f := newActualFixture(t, "sso")
			ctx := t.Context()
			if status, e := Prepare(ctx, f.service.DB, f.lease, f.input); e != nil || status != Verified {
				t.Fatal(status, e)
			}
			password := identityfixture.Bootstrap(t, f.service)
			var oid uint32
			var verifier string
			if e := f.lease.QueryRow(ctx, "SELECT oid,rolpassword FROM pg_authid WHERE rolname=$1", f.role).Scan(&oid, &verifier); e != nil {
				t.Fatal(e)
			}
			profile := "knowledge"
			f.input.configuration.Server.ApplicationProfile = &profile
			f.input.configuration.Models.ArtifactRoot = t.TempDir()
			f.input.configuration.Training = config.TrainingConfiguration{ArtifactRoot: t.TempDir(), ImageReference: "projectone/training@sha256:" + strings.Repeat("a", 64), ImageID: "sha256:" + strings.Repeat("b", 64), WorkerSourceSHA256: strings.Repeat("c", 64)}
			f.input.configuration.Datasets = config.DatasetConfiguration{ArtifactRoot: t.TempDir(), PythonExecutable: "/usr/local/bin/python3.12", ProcessorFile: "/opt/projectone/data-processing/process.py"}
			f.input.configuration.ModelPreparation = config.ModelPreparationConfiguration{ArtifactRoot: t.TempDir(), ImageReference: "projectone/model-lifecycle@sha256:" + strings.Repeat("d", 64), ImageID: "sha256:" + strings.Repeat("e", 64), WorkerSourceSHA256: strings.Repeat("f", 64)}
			if interrupted {
				if e := database.Migrate(ctx, f.lease, "knowledge"); e != nil {
					t.Fatal(e)
				}
				if _, e := Check(ctx, f.service.DB, f.lease, f.input); e == nil {
					t.Fatal("missing grants accepted")
				}
			}
			for i := 0; i < 2; i++ {
				if status, e := UpgradeKnowledge(ctx, f.service.DB, f.lease, f.input); e != nil || status != KnowledgeUpgradeComplete {
					t.Fatal(status, e)
				}
			}
			var after uint32
			var hash string
			if e := f.lease.QueryRow(ctx, "SELECT oid,rolpassword FROM pg_authid WHERE rolname=$1", f.role).Scan(&after, &hash); e != nil || after != oid || hash != verifier {
				t.Fatal("runtime principal changed", e)
			}
			runtime, e := pgxpool.NewWithConfig(ctx, f.input.connection.Copy())
			if e != nil {
				t.Fatal(e)
			}
			defer runtime.Close()
			clone := *f.service
			clone.DB = runtime
			clone.Config.Server.ApplicationProfile = &profile
			a := identityfixture.Login(t, &clone, "admin@example.invalid", password, "portal")
			w := workspaces.Service{DB: runtime, Identity: &clone}
			if _, e = w.CreateNote(ctx, a, a.Me.Workspace.ID, "Upgraded runtime note", "synthetic", uuid.NewString()); e != nil {
				t.Fatal(e)
			}
			admin := identityfixture.Login(t, &clone, "admin@example.invalid", password, "admin")
			n := nodes.Service{DB: runtime, Identity: &clone, Workspaces: &w}
			if _, e = n.Mint(ctx, admin, a.Me.Workspace.ID, "Restricted node", uuid.NewString()); e != nil {
				t.Fatal(e)
			}

			component := &components.Service{Nodes: &n}
			n.Components = component
			token, e := n.MintForProfile(ctx, admin, a.Me.Workspace.ID, "Restricted management", wire.SystemdMaintenanceProfile, uuid.NewString())
			if e != nil {
				t.Fatal(e)
			}
			facts := wire.Facts{OSID: "ubuntu", OSVersion: "24.04", Architecture: "amd64", Hostname: "synthetic-runtime", LogicalCPUs: 1, MemoryTotalBytes: 1024, DiskPath: "/", DiskTotalBytes: 2048, DiskAvailableBytes: 1024, ObservedAt: time.Now().UTC(), AgentVersion: wire.AgentVersion}
			node, e := n.Enroll(ctx, wire.Enrollment{JoinToken: token.Token, Handshake: wire.Handshake{ProtocolVersion: wire.MaintenanceVersion, ExecutionProfile: wire.SystemdMaintenanceProfile, Capabilities: wire.Capabilities(wire.SystemdMaintenanceProfile), Facts: facts}}, uuid.NewString())
			if e != nil {
				t.Fatal(e)
			}
			if _, e = component.Request(ctx, admin, node.NodeID, "start", uuid.NewString(), "Synthetic restricted DML", uuid.NewString()); e != nil {
				t.Fatal("runtime component write", e)
			}
			if _, e = component.Get(ctx, a, a.Me.Workspace.ID, node.NodeID); e != nil {
				t.Fatal("runtime component read", e)
			}

			configuration := wire.DefaultComponentConfig()
			observation := wire.ComponentObservation{Component: wire.NodeExporter, Version: "1.12.1", State: "running", Managed: true, Config: &configuration, ObservedAt: time.Now().UTC(), Metrics: &wire.ComponentMetrics{CPUSecondsTotal: 1, CPUIdleSecondsTotal: 1, MemoryTotalBytes: 1024, MemoryAvailableBytes: 512, FilesystemTotalBytes: 2048, FilesystemAvailableBytes: 1024}}
			claim, e := n.Claim(ctx, node.NodeID, node.Credential, uuid.NewString())
			if e != nil || claim.Lease == nil {
				t.Fatal(e)
			}
			if _, e = n.Complete(ctx, node.NodeID, node.Credential, claim.Lease.TaskID, wire.Result{LeaseToken: claim.Lease.LeaseToken, Outcome: "success", Component: &observation}, uuid.NewString()); e != nil {
				t.Fatal(e)
			}
			job, e := component.Request(ctx, admin, node.NodeID, "logs", uuid.NewString(), "Synthetic restricted logs", uuid.NewString())
			if e != nil {
				t.Fatal(e)
			}
			claim, e = n.Claim(ctx, node.NodeID, node.Credential, uuid.NewString())
			if e != nil || claim.Lease == nil {
				t.Fatal(e)
			}
			now := time.Now().UTC()
			logs := wire.ComponentLogs{Source: "systemd_journal", Available: true, ObservedAt: now, Since: now.Add(-time.Minute), Entries: []wire.ComponentLogEntry{}}
			if _, e = n.Complete(ctx, node.NodeID, node.Credential, job.ID, wire.Result{LeaseToken: claim.Lease.LeaseToken, Outcome: "success", Component: &observation, Logs: &logs}, uuid.NewString()); e != nil {
				t.Fatal(e)
			}
			if v, e := component.Logs(ctx, admin, node.NodeID); e != nil || v.Snapshot == nil {
				t.Fatal(e)
			}
			for _, table := range []string{"knowledge_bases", "knowledge_generations", "knowledge_documents", "knowledge_source_versions", "knowledge_attempts", "knowledge_chunks", "knowledge_mutation_intents"} {
				if _, e = runtime.Exec(ctx, "SELECT * FROM "+table+" LIMIT 0"); e != nil {
					t.Fatal("knowledge runtime read grant", table, e)
				}
				if _, e = runtime.Exec(ctx, "DELETE FROM "+table+" WHERE false"); e != nil {
					t.Fatal("knowledge runtime DML grant", table, e)
				}
			}
			for _, sql := range []string{"CREATE TABLE forbidden_m02(id integer)", "DELETE FROM schema_migrations", "CREATE EXTENSION hstore", "ALTER EXTENSION vector UPDATE", "TRUNCATE knowledge_chunks"} {
				if _, e = runtime.Exec(ctx, sql); e == nil {
					t.Fatal("runtime DDL/ledger mutation allowed")
				}
			}
		})
	}
}
