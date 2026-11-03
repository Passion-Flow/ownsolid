package nodes_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/httpfixture"
	lifecycle "github.com/Passion-Flow/ownsolid/api/model-lifecycle/v1"
)

// Exact catalog/weights/node observations are synthetic contract metadata only.
// These tests exercise actual node DB authorization, not a model deployment or GPU.
func derivedNodeCommand(t *testing.T, a identity.Auth) wire.InferenceCommand {
	t.Helper()
	id, model := uuid.NewString(), uuid.NewString()
	c := lifecycle.DerivedCatalog{SchemaVersion: 1, Kind: wire.DerivedModelKind, Scope: lifecycle.Scope{TenantID: a.Me.Workspace.TenantID, WorkspaceID: a.Me.Workspace.ID, PreparationID: id}, ModelID: model, BaseModelID: id, BaseCatalogSHA256: wire.ModelCatalogDigest(), AttemptID: id, ArtifactID: model, ReportSHA256: strings.Repeat("a", 64), RecipeSHA256: lifecycle.RecipeDigest(), BaseRepository: lifecycle.ApprovedBaseRepository, BaseRevision: lifecycle.ApprovedBaseRevision, License: lifecycle.ApprovedBaseLicense, Format: "safetensors", Architecture: lifecycle.ApprovedBaseArchitecture, ModelType: lifecycle.ApprovedBaseModelType, Precision: "BF16", MaximumContextTokens: 2048, Files: lifecycle.ApprovedBaseFiles()}
	for i, asset := range c.Files {
		if asset.Path == "model.safetensors" {
			c.Files[i].SizeBytes = 128
			c.Files[i].SHA256 = strings.Repeat("b", 64)
		}
	}
	sha, e := c.Digest()
	if e != nil {
		t.Fatal(e)
	}
	return wire.InferenceCommand{Action: "deploy", DeploymentID: id, ModelID: model, CatalogDigest: sha, GPUUUID: "GPU-" + id, Revision: 1, Derived: &wire.DerivedInferenceIdentity{SchemaVersion: 1, Catalog: c, ServingRecipeSHA256: wire.DerivedServingRecipeDigest()}}
}
func enrollDerivedNode(t *testing.T, s *nodes.Service, a identity.Auth, profile string) wire.Enrolled {
	t.Helper()
	join, e := s.MintForProfile(context.Background(), a, a.Me.Workspace.ID, "Synthetic derived "+profile, profile, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	version := wire.ModelLifecycleVersion
	if profile == wire.DockerAIProfile {
		version = wire.AIVersion
	}
	n, e := s.Enroll(context.Background(), wire.Enrollment{JoinToken: join.Token, Handshake: wire.Handshake{ProtocolVersion: version, ExecutionProfile: profile, Capabilities: wire.Capabilities(profile), Facts: facts()}}, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	return n
}
func queueDerivedMetadata(t *testing.T, s *nodes.Service, a identity.Auth, node string, c wire.InferenceCommand, key string) (nodes.Task, error) {
	t.Helper()
	// No-op owning callback is explicitly confined to this metadata authorization test.
	// Real deployment ownership/preflight/publication is verified in the deployment module.
	return s.CreateInferenceTaskBound(context.Background(), a, node, wire.DerivedInferenceOperation, key, "Synthetic exact typed node authority test", uuid.NewString(), []byte(strings.Repeat("f", 32)), func(pgx.Tx, nodes.Node) (wire.InferenceCommand, error) { return c, nil }, func(pgx.Tx, nodes.Node, nodes.Task, wire.InferenceCommand) error { return nil })
}
func TestDerivedNodeCurrentEpochCommandImmutabilityLeaseAndGeneratedSchema(t *testing.T) {
	s, a := setup(t)
	ctx := context.Background()
	node := enrollDerivedNode(t, s, a, wire.DockerModelLifecycleProfile)
	c := derivedNodeCommand(t, a)
	key := uuid.NewString()
	task, e := queueDerivedMetadata(t, s, a, node.NodeID, c, key)
	if e != nil {
		t.Fatal(e)
	}
	again, e := queueDerivedMetadata(t, s, a, node.NodeID, c, key)
	if e != nil || again.ID != task.ID {
		t.Fatal("typed metadata replay", e)
	}
	var epoch int64
	if e = s.DB.QueryRow(ctx, "SELECT inference_requested_epoch FROM node_tasks WHERE id=$1", task.ID).Scan(&epoch); e != nil || epoch != a.Epoch {
		t.Fatal("original current Admin epoch not captured", e)
	}
	if _, e = s.DB.Exec(ctx, "UPDATE node_tasks SET inference_requested_epoch=inference_requested_epoch+1 WHERE id=$1", task.ID); e == nil {
		t.Fatal("delegation changed")
	}
	if _, e = s.DB.Exec(ctx, "UPDATE node_tasks SET payload='{}' WHERE id=$1", task.ID); e == nil {
		t.Fatal("derived command changed")
	}
	claim, e := s.Claim(ctx, node.NodeID, node.Credential, uuid.NewString())
	if e != nil || claim.Lease == nil || claim.Lease.Inference == nil || claim.Lease.Inference.Derived == nil {
		t.Fatal("exact derived lease", e)
	}
	encoded, _ := json.Marshal(claim.Lease)
	var value any
	json.Unmarshal(encoded, &value)
	schemas := httpfixture.Schemas(t, "../../../../../api/openapi/v1/nodes/openapi.json")
	if e = schemas["Lease"].Validate(value); e != nil {
		t.Fatal("generated derived lease schema", e)
	}
	if _, e = s.Renew(ctx, node.NodeID, node.Credential, task.ID, claim.Lease.LeaseToken); e != nil {
		t.Fatal("current derived renew", e)
	}
	if _, e = s.DB.Exec(ctx, "UPDATE users SET auth_epoch=auth_epoch+1 WHERE id=$1", a.Me.User.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Renew(ctx, node.NodeID, node.Credential, task.ID, claim.Lease.LeaseToken); e == nil {
		t.Fatal("stale creator epoch renewed")
	}
}
func TestDerivedNodeRejectsOldProfileForeignScopeAndChangedEpochAfterActualIO(t *testing.T) {
	s, a := setup(t)
	ctx := context.Background()
	old := enrollDerivedNode(t, s, a, wire.DockerAIProfile)
	c := derivedNodeCommand(t, a)
	if _, e := queueDerivedMetadata(t, s, a, old.NodeID, c, uuid.NewString()); e == nil {
		t.Fatal("old profile acquired derived operation")
	}
	node := enrollDerivedNode(t, s, a, wire.DockerModelLifecycleProfile)
	foreign := c
	copyIdentity := *c.Derived
	foreign.Derived = &copyIdentity
	foreign.Derived.Catalog.Scope.WorkspaceID = uuid.NewString()
	foreign.CatalogDigest, _ = foreign.Derived.Catalog.Digest()
	if _, e := queueDerivedMetadata(t, s, a, node.NodeID, foreign, uuid.NewString()); e == nil {
		t.Fatal("foreign catalog scope created task")
	}
	task, e := queueDerivedMetadata(t, s, a, node.NodeID, c, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	claim, e := s.Claim(ctx, node.NodeID, node.Credential, uuid.NewString())
	if e != nil || claim.Lease == nil {
		t.Fatal(e)
	}
	file := filepath.Join(t.TempDir(), "synthetic-input")
	if e = os.WriteFile(file, []byte("actual private IO"), 0600); e != nil {
		t.Fatal(e)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- s.WithLease(ctx, node.NodeID, node.Credential, task.ID, claim.Lease.LeaseToken, func(tx pgx.Tx, n nodes.Node, task nodes.Task) error {
			close(entered)
			<-release
			if _, e := os.ReadFile(file); e != nil {
				return e
			}
			_, e := tx.Exec(ctx, "INSERT INTO audit_events(id,occurred_at,tenant_id,audience,action,outcome,target_type,target_id,reason,request_id) VALUES($1,clock_timestamp(),$2,'system','synthetic.derived_io','success','node_task',$3,'Explicit current-authority rollback fixture',$4)", uuid.NewString(), n.TenantID, task.ID, uuid.NewString())
			return e
		})
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("callback did not reach IO boundary")
	}
	if _, e = s.DB.Exec(ctx, "UPDATE users SET auth_epoch=auth_epoch+1 WHERE id=$1", a.Me.User.ID); e != nil {
		close(release)
		t.Fatal(e)
	}
	close(release)
	select {
	case e := <-done:
		expected(t, e, "ACCESS_DENIED")
	case <-time.After(2 * time.Second):
		t.Fatal("authority recheck deadlocked after IO")
	}
	var count int
	if e = s.DB.QueryRow(ctx, "SELECT count(*) FROM audit_events WHERE action='synthetic.derived_io'").Scan(&count); e != nil || count != 0 {
		t.Fatal("revoked IO callback committed", e)
	}
}
