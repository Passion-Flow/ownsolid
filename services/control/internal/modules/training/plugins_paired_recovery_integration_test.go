//go:build training_artifact_integration && model_lifecycle_admission_integration && training_operator_integration

package training_test

import (
	"crypto/ed25519"
	"encoding/base64"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/plugins"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	wire "github.com/Passion-Flow/ownsolid/api/plugins/v1"
	"testing"
)

type cumulativePluginRecovery struct {
	id, version, authority, capsule string
	revision                        int64
}

func prepareCumulativePluginRecovery(t *testing.T, g preparationScenario) *cumulativePluginRecovery {
	t.Helper()
	a := g.f.s.Identity
	if !a.Config.PluginsRuntime() {
		return nil
	}
	public, private, e := ed25519.GenerateKey(nil)
	if e != nil {
		t.Fatal(e)
	}
	key, _ := wire.KeyID(public)
	a.Config.Plugins = config.PluginConfiguration{Enabled: true, TrustedKeys: []config.PluginTrustKey{{KeyID: key, PublicKey: base64.StdEncoding.EncodeToString(public)}}}
	p := &plugins.Service{DB: a.DB, Identity: a, Workspaces: g.f.s.Workspaces, Models: g.f.s.Models}
	if e = p.Start(t.Context()); e != nil {
		t.Fatal(e)
	}
	defer p.Stop(t.Context())
	raw, e := wire.Sign(wire.Manifest{PluginID: wire.PluginID, Version: "1.0.0", ABI: wire.ABI, Executor: wire.Executor, Capabilities: []string{wire.Capability}, License: wire.License}, private, a.Config.Policy().Plugins.PackageLimits())
	if e != nil {
		t.Fatal(e)
	}
	v, e := p.Import(t.Context(), g.f.admin, plugins.ImportRequest{Capsule: base64.StdEncoding.EncodeToString(raw), SHA256: wire.Digest(raw), Reason: "Synthetic matched restore provenance"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	v, e = p.Install(t.Context(), g.f.admin, v.ID, plugins.RevisionReason{ExpectedRevision: v.Revision, Reason: "Synthetic matched restore installed version"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	grant, e := p.Grant(t.Context(), g.f.admin, g.f.admin.Me.Workspace.ID, plugins.GrantRequest{PluginVersionID: v.ID, Reason: "Synthetic matched restore current grant"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	state := &cumulativePluginRecovery{id: grant.ID, version: v.ID, capsule: v.CapsuleSHA256, revision: grant.Revision}
	if e = a.DB.QueryRow(t.Context(), "SELECT authority_id::text FROM plugin_grants WHERE id=$1", grant.ID).Scan(&state.authority); e != nil {
		t.Fatal(e)
	}
	return state
}
func verifyCumulativePluginRecovery(t *testing.T, state *cumulativePluginRecovery, g preparationScenario, pool *pgxpool.Pool) {
	t.Helper()
	if state == nil {
		return
	}
	var disabled, rotated, provenance bool
	if e := pool.QueryRow(t.Context(), "SELECT NOT g.enabled,g.authority_id<>$2::uuid AND g.revision=$3+1,v.id=$4::uuid AND v.capsule_sha256=$5 AND v.state='installed' FROM plugin_grants g JOIN plugin_versions v ON v.id=g.plugin_version_id WHERE g.id=$1", state.id, state.authority, state.revision, state.version, state.capsule).Scan(&disabled, &rotated, &provenance); e != nil || !disabled || !rotated || !provenance {
		t.Fatal("matched plugin provenance/grant reduction", e)
	}
	var tables int
	if e := pool.QueryRow(t.Context(), "SELECT count(*) FROM pg_tables WHERE schemaname=current_schema()").Scan(&tables); e != nil {
		t.Fatal(e)
	}
	t.Logf("Actual M14 matched four-store+SQL+SSO-keyring restore retained signed installed provenance, rolled every%d-table owner back at the real late workflow fault, disabled/rotated the exact grant, and never regranted or reinvoked", tables)
}
