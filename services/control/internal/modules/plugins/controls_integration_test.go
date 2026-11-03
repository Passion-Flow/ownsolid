//go:build plugin_database_integration

package plugins

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	agentwire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	lifecycle "github.com/Passion-Flow/ownsolid/api/model-lifecycle/v1"
	wire "github.com/Passion-Flow/ownsolid/api/plugins/v1"
)

func TestM14ActualConcurrentRevisionTrustRestartLimitsAndRetentionRollback(t *testing.T) {
	s, admin, actor, private := pluginFixture(t)
	ctx := context.Background()
	workspace := actor.Me.Workspace.ID
	version, e := s.Import(ctx, admin, pluginInput(t, private, "1.0.0"), uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	version, e = s.Install(ctx, admin, version.ID, RevisionReason{ExpectedRevision: version.Revision, Reason: "Actual operational control fixture"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	grant, e := s.Grant(ctx, admin, workspace, GrantRequest{PluginVersionID: version.ID, Reason: "Actual exact version grant"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	t.Run("concurrent_revoke_has_one_revision_winner", func(t *testing.T) {
		var joined sync.WaitGroup
		results := make(chan error, 2)
		for range 2 {
			joined.Add(1)
			go func() {
				defer joined.Done()
				_, e := s.UpdateGrant(ctx, admin, workspace, grant.ID, GrantUpdate{Enabled: false, ExpectedRevision: grant.Revision, Reason: "Actual concurrent revision fence"}, uuid.NewString(), uuid.NewString())
				results <- e
			}()
		}
		joined.Wait()
		close(results)
		success, conflict := 0, 0
		for e := range results {
			if e == nil {
				success++
			} else if e.Error() == "PLUGIN_REVISION_CONFLICT" {
				conflict++
			} else {
				t.Fatal(e)
			}
		}
		if success != 1 || conflict != 1 {
			t.Fatal("nonexact concurrent outcomes", success, conflict)
		}
		var revision int64
		if e = s.DB.QueryRow(ctx, "SELECT revision FROM plugin_grants WHERE id=$1", grant.ID).Scan(&revision); e != nil || revision != grant.Revision+1 {
			t.Fatal("revision advanced more than once", e)
		}
		grant, e = s.UpdateGrant(ctx, admin, workspace, grant.ID, GrantUpdate{Enabled: true, ExpectedRevision: revision, Reason: "Explicit reviewed re-enable"}, uuid.NewString(), uuid.NewString())
		if e != nil {
			t.Fatal(e)
		}
	})
	run, question := uuid.NewString(), "Actual current caller empty SQL directory"
	var completed *CompletedModelList
	if e = modelTransaction(t, s, actor, func(tx pgx.Tx) error {
		var e error
		completed, e = s.ExecuteModelListInTransaction(ctx, tx, actor, workspace, version.ID, run, question)
		return e
	}); e != nil {
		t.Fatal(e)
	}
	result, _ := json.Marshal(completed.Result())
	restart := func(policy config.PluginPolicy, keys []config.PluginTrustKey) {
		t.Helper()
		if e := s.Stop(ctx); e != nil {
			t.Fatal(e)
		}
		configuration := s.Identity.Config.Policy()
		configuration.Plugins = policy
		s.Identity.Config.Runtime = &configuration
		s.Identity.Config.Plugins.TrustedKeys = keys
		if e := s.Start(ctx); e != nil {
			t.Fatal(e)
		}
	}
	originalKeys := append([]config.PluginTrustKey(nil), s.Identity.Config.Plugins.TrustedKeys...)
	originalPolicy := s.Identity.Config.Policy().Plugins
	t.Run("supported_trust_restart_withholds_old_completion_and_history", func(t *testing.T) {
		public, _, e := ed25519.GenerateKey(nil)
		if e != nil {
			t.Fatal(e)
		}
		id, _ := wire.KeyID(public)
		restart(originalPolicy, []config.PluginTrustKey{{KeyID: id, PublicKey: base64.StdEncoding.EncodeToString(public)}})
		if e = modelTransaction(t, s, actor, func(tx pgx.Tx) error {
			return s.RecheckCompletedModelListInTransaction(ctx, tx, actor, workspace, version.ID, run, question, completed)
		}); e == nil || e.Error() != "PLUGIN_TRUST_KEY_INVALID" {
			t.Fatal("trust withdrawal failed completion fence", e)
		}
		expectCode(t, modelTransaction(t, s, actor, func(tx pgx.Tx) error {
			return s.RecheckStoredModelListInTransaction(ctx, tx, actor, workspace, version.ID, completed.StoredProof(), result)
		}), "PLUGIN_TRUST_KEY_INVALID")
		restart(originalPolicy, originalKeys)
		if e = modelTransaction(t, s, actor, func(tx pgx.Tx) error {
			return s.RecheckStoredModelListInTransaction(ctx, tx, actor, workspace, version.ID, completed.StoredProof(), result)
		}); e != nil {
			t.Fatal("explicit restored trust did not restore unchanged history", e)
		}
	})
	t.Run("complete_count_bytes_and_version_grant_limits_never_truncate", func(t *testing.T) {
		policy := originalPolicy
		policy.MaximumModels = 1
		policy.MaximumVersions = 1
		policy.MaximumGrants = 1
		policy.ResultBytes = 1024
		restart(policy, originalKeys)
		_, e := s.Import(ctx, admin, pluginInput(t, private, "1.1.0"), uuid.NewString(), uuid.NewString())
		expectCode(t, e, "PLUGIN_LIMIT_EXCEEDED")
		other, e := s.Workspaces.Create(ctx, admin, "Actual grant limit scope", admin.Me.User.ID, uuid.NewString())
		if e != nil {
			t.Fatal(e)
		}
		_, e = s.Grant(ctx, admin, other.ID, GrantRequest{PluginVersionID: version.ID, Reason: "Actual grant count limit"}, uuid.NewString(), uuid.NewString())
		expectCode(t, e, "PLUGIN_LIMIT_EXCEEDED")
		// Explicit approved-catalog metadata rows test only current SQL result bounds;
		// these rows are not proof of model-file ingestion or inference hardware.
		var tenant string
		if e = s.DB.QueryRow(ctx, "SELECT tenant_id::text FROM workspaces WHERE id=$1", workspace).Scan(&tenant); e != nil {
			t.Fatal(e)
		}
		baseID := uuid.NewString()
		type descriptor struct {
			id, kind, digest, name string
			catalog                []byte
		}
		descriptors := []descriptor{}
		for i := range 4 {
			id, kind := baseID, "approved_base"
			catalog, _ := json.Marshal(agentwire.ApprovedModel())
			digest := agentwire.ModelCatalogDigest()
			if i > 0 {
				id, kind = uuid.NewString(), "merged_lora"
				files := lifecycle.ApprovedBaseFiles()
				for j := range files {
					if files[j].Path == "model.safetensors" {
						files[j].SizeBytes = 1192135096
						files[j].SHA256 = strings.Repeat("c", 64)
					}
				}
				c := lifecycle.DerivedCatalog{SchemaVersion: 1, Kind: kind, Scope: lifecycle.Scope{TenantID: tenant, WorkspaceID: workspace, PreparationID: uuid.NewString()}, ModelID: id, BaseModelID: baseID, BaseCatalogSHA256: lifecycle.ApprovedModelCatalogSHA256, AttemptID: uuid.NewString(), ArtifactID: id, ReportSHA256: strings.Repeat("d", 64), RecipeSHA256: lifecycle.RecipeDigest(), BaseRepository: lifecycle.ApprovedBaseRepository, BaseRevision: lifecycle.ApprovedBaseRevision, License: lifecycle.ApprovedBaseLicense, Format: "safetensors", Architecture: lifecycle.ApprovedBaseArchitecture, ModelType: lifecycle.ApprovedBaseModelType, Precision: "BF16", MaximumContextTokens: lifecycle.MaximumContextTokens, Files: files}
				var e error
				catalog, e = c.CanonicalBytes()
				if e != nil {
					t.Fatal("explicit metadata descriptor", e)
				}
				digest, e = c.Digest()
				if e != nil {
					t.Fatal(e)
				}
			}
			descriptors = append(descriptors, descriptor{id: id, kind: kind, digest: digest, name: fmt.Sprintf("%d-", i) + strings.Repeat("m", 90), catalog: catalog})
		}
		insert := func(tx pgx.Tx, d descriptor) error {
			_, e := tx.Exec(ctx, `INSERT INTO model_versions(id,tenant_id,workspace_id,name,model_key,catalog_digest,manifest,state,kind,created_by) VALUES($1,$2,$3,$4,$5,$6,$7,'ready',$8,$9)`, d.id, tenant, workspace, d.name, agentwire.ApprovedModel().ModelKey, d.digest, d.catalog, d.kind, admin.Me.User.ID)
			return e
		}
		// Synthetic derivative descriptors exist only in this uncommitted fixture
		// transaction. A rejected tool call rolls them back before deferred review
		// constraints; no guard is disabled and no fictitious approval is committed.
		invoke := func() error {
			return modelTransaction(t, s, actor, func(tx pgx.Tx) error {
				for _, d := range descriptors {
					if e := insert(tx, d); e != nil {
						return e
					}
				}
				_, e := s.ExecuteModelListInTransaction(ctx, tx, actor, workspace, version.ID, uuid.NewString(), question)
				return e
			})
		}
		expectCode(t, invoke(), "PLUGIN_MODEL_LIMIT_EXCEEDED")
		policy.MaximumModels = 100
		restart(policy, originalKeys)
		expectCode(t, invoke(), "PLUGIN_RESULT_LIMIT_EXCEEDED")
		restart(originalPolicy, originalKeys)
		if e = modelTransaction(t, s, actor, func(tx pgx.Tx) error { return insert(tx, descriptors[0]) }); e != nil {
			t.Fatal("actual committed approved-catalog metadata addition", e)
		}
		if e = modelTransaction(t, s, actor, func(tx pgx.Tx) error {
			return s.RecheckStoredModelListInTransaction(ctx, tx, actor, workspace, version.ID, completed.StoredProof(), result)
		}); e != nil {
			t.Fatal("new models invalidated an unchanged historical snapshot", e)
		}
	})
	t.Run("bounded_expired_intent_cleanup_audit_failure_rolls_back", func(t *testing.T) {
		policy := originalPolicy
		policy.MaintenanceBatch = 1
		restart(policy, originalKeys)
		if _, e = s.DB.Exec(ctx, `UPDATE plugin_mutation_intents SET created_at=clock_timestamp()-make_interval(secs=>$1) WHERE idempotency_key IN (SELECT idempotency_key FROM plugin_mutation_intents ORDER BY created_at,idempotency_key LIMIT 2)`, s.Identity.Config.Policy().Identity.IdempotencySeconds+60); e != nil {
			t.Fatal(e)
		}
		var before, after, versions, grants int
		if e = s.DB.QueryRow(ctx, "SELECT count(*) FROM plugin_mutation_intents").Scan(&before); e != nil {
			t.Fatal(e)
		}
		if _, e = s.DB.Exec(ctx, `CREATE FUNCTION plugin_retention_refusal() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='plugin.intent_retention' THEN RAISE EXCEPTION 'SYNTHETIC_PLUGIN_RETENTION_REFUSAL'; END IF; RETURN NEW; END $$;CREATE TRIGGER plugin_retention_refusal BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION plugin_retention_refusal()`); e != nil {
			t.Fatal(e)
		}
		if _, e = s.Cleanup(ctx); e == nil {
			t.Fatal("retention audit failure accepted")
		}
		if e = s.DB.QueryRow(ctx, "SELECT count(*) FROM plugin_mutation_intents").Scan(&after); e != nil || after != before {
			t.Fatal("retention audit failure deleted intents", e)
		}
		if _, e = s.DB.Exec(ctx, "DROP TRIGGER plugin_retention_refusal ON audit_events;DROP FUNCTION plugin_retention_refusal()"); e != nil {
			t.Fatal(e)
		}
		for range 2 {
			if n, e := s.Cleanup(ctx); e != nil || n != 1 {
				t.Fatal("retention ignored configured batch", e, n)
			}
		}
		if n, e := s.Cleanup(ctx); e != nil || n != 0 {
			t.Fatal("retention removed live intents", e, n)
		}
		if e = s.DB.QueryRow(ctx, "SELECT (SELECT count(*) FROM plugin_versions),(SELECT count(*) FROM plugin_grants)").Scan(&versions, &grants); e != nil || versions != 1 || grants != 1 {
			t.Fatal("retention destroyed signed provenance", e, versions, grants)
		}
	})
	t.Run("uninstall_fences_captured_completion", func(t *testing.T) {
		refs, e := s.References(ctx, admin, version.ID)
		if e != nil {
			t.Fatal(e)
		}
		_, e = s.Uninstall(ctx, admin, version.ID, UninstallRequest{ExpectedRevision: version.Revision, ReferencesSHA256: refs.SHA256, Reason: "Actual captured completion uninstall fence"}, uuid.NewString(), uuid.NewString())
		if e != nil {
			t.Fatal(e)
		}
		expectCode(t, modelTransaction(t, s, actor, func(tx pgx.Tx) error {
			return s.RecheckCompletedModelListInTransaction(ctx, tx, actor, workspace, version.ID, run, question, completed)
		}), "PLUGIN_NOT_INSTALLED")
	})
}

func TestM14ActualConcurrentInstallGrantAndUninstallHaveExactOutcomes(t *testing.T) {
	s, admin, actor, private := pluginFixture(t)
	ctx := context.Background()
	version, e := s.Import(ctx, admin, pluginInput(t, private, "1.0.0"), uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	race := func(name, code string, call func() error) {
		t.Helper()
		t.Run(name, func(t *testing.T) {
			var joined sync.WaitGroup
			results := make(chan error, 2)
			for range 2 {
				joined.Add(1)
				go func() { defer joined.Done(); results <- call() }()
			}
			joined.Wait()
			close(results)
			success, conflict := 0, 0
			for e := range results {
				if e == nil {
					success++
				} else if e.Error() == code {
					conflict++
				} else {
					t.Fatal(e)
				}
			}
			if success != 1 || conflict != 1 {
				t.Fatal("nonexact durable concurrent outcomes", success, conflict)
			}
		})
	}
	race("install", "PLUGIN_REVISION_CONFLICT", func() error {
		_, e := s.Install(ctx, admin, version.ID, RevisionReason{ExpectedRevision: version.Revision, Reason: "Actual concurrent explicit install"}, uuid.NewString(), uuid.NewString())
		return e
	})
	version, e = s.Get(ctx, admin, version.ID)
	if e != nil || version.State != InstalledState || version.Revision != 2 {
		t.Fatal("concurrent install state", e)
	}
	workspace := actor.Me.Workspace.ID
	race("grant", "PLUGIN_GRANT_EXISTS", func() error {
		_, e := s.Grant(ctx, admin, workspace, GrantRequest{PluginVersionID: version.ID, Reason: "Actual concurrent exact grant"}, uuid.NewString(), uuid.NewString())
		return e
	})
	refs, e := s.References(ctx, admin, version.ID)
	if e != nil {
		t.Fatal(e)
	}
	race("uninstall", "PLUGIN_REVISION_CONFLICT", func() error {
		_, e := s.Uninstall(ctx, admin, version.ID, UninstallRequest{ExpectedRevision: version.Revision, ReferencesSHA256: refs.SHA256, Reason: "Actual concurrent explicit uninstall"}, uuid.NewString(), uuid.NewString())
		return e
	})
	var installed, granted, uninstalled, grants int
	var enabled bool
	if e = s.DB.QueryRow(ctx, `SELECT (SELECT count(*) FROM audit_events WHERE action='plugin.installed'),(SELECT count(*) FROM audit_events WHERE action='plugin.granted'),(SELECT count(*) FROM audit_events WHERE action='plugin.uninstalled'),(SELECT count(*) FROM plugin_grants),(SELECT enabled FROM plugin_grants WHERE plugin_version_id=$1)`, version.ID).Scan(&installed, &granted, &uninstalled, &grants, &enabled); e != nil || installed != 1 || granted != 1 || uninstalled != 1 || grants != 1 || enabled {
		t.Fatal("duplicate concurrent provenance/audit or enabled uninstalled grant", e)
	}
}
