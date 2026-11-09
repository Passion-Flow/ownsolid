//go:build repository_integration

package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/api/agent/modelartifact"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/agentca"
	"github.com/google/uuid"
	"go.yaml.in/yaml/v3"
)

func TestActualModelImportExpiryStartup(t *testing.T) {
	if os.Getenv("PROJECT_ONE_REPOSITORY_ENGINE") != config.PostgreSQLDatabaseEngine {
		t.Skip("model upload expiry requires the isolated PostgreSQL fixture")
	}
	x := serverCompositionSetup(t, false)
	bundle, key := agentca.Material(t)
	x.c.AgentIdentity = &config.AgentIdentityConfiguration{
		CACertificateFile: x.private(t, "agent-ca.pem", bundle),
		CAPrivateKeyFile:  x.private(t, "agent-ca-key.pem", key),
	}
	x.c.Models.ArtifactRoot = filepath.Join(x.root, "models")
	other, disabled, foreign, foreignTenant := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	x.exec(t, "INSERT INTO tenants(id,name,status) VALUES($1,'Foreign fixture','active')", "", foreignTenant)
	x.exec(t, `INSERT INTO workspaces(id,tenant_id,name,status) VALUES
		($1,$4,'Other active workspace','active'),($2,$4,'Disabled workspace','disabled'),($3,$5,'Foreign workspace','active')`,
		"", other, disabled, foreign, x.f.Tenant, foreignTenant)
	seed := func(workspace string) (string, string) {
		t.Helper()
		id := uuid.NewString()
		path := filepath.Join(x.c.Models.ArtifactRoot, "imports", id)
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
		chunk := filepath.Join(path, "chunk-000000000000")
		if err := os.WriteFile(chunk, []byte{0xa5}, 0600); err != nil {
			t.Fatal(err)
		}
		x.exec(t, `INSERT INTO model_imports(id,tenant_id,workspace_id,name,catalog_digest,expected_bytes,uploaded_bytes,
			state,requested_by,requested_epoch,idempotency_key,fingerprint,reason,updated_at)
			VALUES($1,$2,$3,'Abandoned upload',$4,$5,1,'uploading',$6,0,$1,decode(repeat('ab',32),'hex'),
			       'Startup expiry fixture',clock_timestamp()-interval '2 hours')`,
			"", id, x.f.Tenant, workspace, wire.ModelCatalogDigest(), modelartifact.MinimumArchiveBytes(), x.f.User)
		return id, chunk
	}
	target, targetChunk := seed(x.f.Workspace)
	retained, retainedChunk := seed(other)
	apply := func(t *testing.T, workspace string) {
		t.Helper()
		policy := config.DefaultRuntimePolicy()
		policy.Models.ImportPollSeconds = 1
		if workspace != "" {
			policy.Models.ImportExpiry = &config.ModelImportExpiryPolicy{WorkspaceID: workspace, InactiveSeconds: 3600}
		}
		x.c.Runtime = &policy
		raw, err := yaml.Marshal(x.c)
		if err != nil {
			t.Fatal(err)
		}
		x.c, err = config.LoadProtected(x.private(t, "model-expiry.yaml", raw))
		if err != nil {
			t.Fatal(err)
		}
		x.owner.Config = x.c
	}
	state := func(id string) string {
		t.Helper()
		var state string
		if err := x.f.PostgreSQL.QueryRow(x.ctx, "SELECT state||'/'||COALESCE(error_code,'') FROM model_imports WHERE id=$1", id).Scan(&state); err != nil {
			t.Fatal(err)
		}
		return state
	}
	requireStaged := func(id, path string) {
		t.Helper()
		if current := state(id); current != "uploading/" {
			t.Fatalf("out-of-scope upload changed to %s", current)
		}
		if _, err := os.Stat(path); err != nil {
			t.Fatal("out-of-scope staging missing", err)
		}
	}

	apply(t, "")
	stop := x.serve(t)
	select {
	case <-time.After(2 * time.Second):
	case <-x.ctx.Done():
		t.Fatal(x.ctx.Err())
	}
	stop()
	requireStaged(target, targetChunk)

	for _, probe := range []struct{ name, workspace string }{
		{"missing", uuid.NewString()}, {"disabled", disabled}, {"foreign", foreign},
	} {
		t.Run(probe.name, func(t *testing.T) {
			apply(t, probe.workspace)
			ctx, cancel := context.WithTimeout(x.ctx, 3*time.Second)
			defer cancel()
			err := serveSelected(ctx, ctx, x.c, x.d, x.owner, x.mail)
			if err == nil || err.Error() != "MODEL_IMPORT_EXPIRY_WORKSPACE_UNAVAILABLE" {
				t.Fatalf("untrusted workspace did not refuse serving startup: %v", err)
			}
		})
	}
	requireStaged(target, targetChunk)
	apply(t, x.f.Workspace)
	stop = x.serve(t)
	deadline := time.NewTimer(8 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for state(target) != "cancelled/MODEL_IMPORT_EXPIRED" {
		select {
		case <-deadline.C:
			t.Fatal("configured worker did not settle abandoned upload")
		case <-ticker.C:
		}
	}
	stop()
	if _, err := os.Stat(targetChunk); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("configured worker did not remove staging", err)
	}
	requireStaged(retained, retainedChunk)
	var events int
	if err := x.f.PostgreSQL.QueryRow(x.ctx, `SELECT count(*) FROM audit_events
		WHERE target_id=$1 AND workspace_id=$2 AND audience='system'
		  AND action IN ('model.import_expiry_requested','model.import_expired')`, target, x.f.Workspace).Scan(&events); err != nil || events != 2 {
		t.Fatalf("expected two scoped expiry audit events, got %d, %v", events, err)
	}
}
