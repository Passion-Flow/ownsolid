//go:build plugin_database_integration

package plugins

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	agentwire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/models"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	"github.com/Passion-Flow/ownsolid/services/control/migrations"
	wire "github.com/Passion-Flow/ownsolid/api/plugins/v1"
)

func pluginFixture(t *testing.T) (*Service, identity.Auth, identity.Auth, ed25519.PrivateKey) {
	t.Helper()
	fixture.PreflightOwnedPostgres(t)
	account := fixture.New(t)
	password := fixture.Bootstrap(t, account)
	admin := fixture.Login(t, account, "admin@example.invalid", password, "admin")
	portal := fixture.Login(t, account, "admin@example.invalid", password, "portal")
	public, private, e := ed25519.GenerateKey(nil)
	if e != nil {
		t.Fatal(e)
	}
	id, _ := wire.KeyID(public)
	account.Config.Plugins = config.PluginConfiguration{Enabled: true, TrustedKeys: []config.PluginTrustKey{{KeyID: id, PublicKey: base64.StdEncoding.EncodeToString(public)}}}
	ws := &workspaces.Service{DB: account.DB, Identity: account}
	s := &Service{DB: account.DB, Identity: account, Workspaces: ws, Models: &models.Service{DB: account.DB, Identity: account, Workspaces: ws}}
	if e = s.Start(context.Background()); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if e := s.Stop(ctx); e != nil {
			t.Error(e)
		}
	})
	return s, admin, portal, private
}
func pluginInput(t *testing.T, private ed25519.PrivateKey, version string) ImportRequest {
	t.Helper()
	raw, e := wire.Sign(wire.Manifest{PluginID: wire.PluginID, Version: version, ABI: wire.ABI, Executor: wire.Executor, Capabilities: []string{wire.Capability}, License: wire.License}, private, wire.Limits{CapsuleBytes: wire.DefaultCapsuleBytes, ManifestBytes: wire.DefaultManifestBytes})
	if e != nil {
		t.Fatal(e)
	}
	return ImportRequest{Capsule: base64.StdEncoding.EncodeToString(raw), SHA256: wire.Digest(raw), Reason: "Actual synthetic signed plugin acceptance"}
}
func expectCode(t *testing.T, e error, code string) {
	t.Helper()
	if e == nil || e.Error() != code {
		t.Fatalf("expected %s, got %v", code, e)
	}
}
func modelTransaction(t *testing.T, s *Service, a identity.Auth, fn func(pgx.Tx) error) error {
	t.Helper()
	ctx := context.Background()
	tx, e := s.DB.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, "SELECT id FROM users WHERE id=$1 FOR UPDATE", a.Me.User.ID); e != nil {
		t.Fatal(e)
	}
	if e = fn(tx); e != nil {
		return e
	}
	return tx.Commit(ctx)
}
func TestM14ActualSignedLifecycleGrantQueryRevocationAndAuditAtomicity(t *testing.T) {
	s, a, portal, private := pluginFixture(t)
	ctx := context.Background()
	workspace := portal.Me.Workspace.ID
	input := pluginInput(t, private, "1.0.0")
	intent := uuid.NewString()
	var version Version
	t.Run("import_exact_replay_and_conflicting_intent", func(t *testing.T) {
		var e error
		version, e = s.Import(ctx, a, input, intent, uuid.NewString())
		if e != nil || version.State != ImportedState || !version.Trusted || version.Available || version.License != wire.License {
			t.Fatal("actual import", e, version)
		}
		replay, e := s.Import(ctx, a, input, intent, uuid.NewString())
		if e != nil || replay.ID != version.ID {
			t.Fatal("exact replay", e)
		}
		other := input
		other.Reason = "Changed request"
		_, e = s.Import(ctx, a, other, intent, uuid.NewString())
		expectCode(t, e, "IDEMPOTENCY_CONFLICT")
		var count int
		if e = s.DB.QueryRow(ctx, "SELECT count(*) FROM audit_events WHERE action='plugin.imported'").Scan(&count); e != nil || count != 1 {
			t.Fatal("duplicate import audit", e, count)
		}
	})
	t.Run("portal_cannot_manage_and_tamper_never_persists", func(t *testing.T) {
		_, e := s.Import(ctx, portal, input, uuid.NewString(), uuid.NewString())
		expectCode(t, e, "ACCESS_DENIED")
		bad := input
		bad.SHA256 = strings.Repeat("0", 64)
		_, e = s.Import(ctx, a, bad, uuid.NewString(), uuid.NewString())
		if e == nil {
			t.Fatal("tampered capsule accepted")
		}
		var count int
		if e = s.DB.QueryRow(ctx, "SELECT count(*) FROM plugin_versions").Scan(&count); e != nil || count != 1 {
			t.Fatal(e, count)
		}
	})
	t.Run("uninstalled_grant_denied_then_actual_install", func(t *testing.T) {
		_, e := s.Grant(ctx, a, workspace, GrantRequest{PluginVersionID: version.ID, Reason: "Before install"}, uuid.NewString(), uuid.NewString())
		expectCode(t, e, "PLUGIN_NOT_INSTALLED")
		version, e = s.Install(ctx, a, version.ID, RevisionReason{ExpectedRevision: version.Revision, Reason: "Actual install"}, uuid.NewString(), uuid.NewString())
		if e != nil || version.State != InstalledState || !version.Available || version.Revision != 2 {
			t.Fatal("actual install", e, version)
		}
	})
	var grant Grant
	t.Run("actual_exact_version_grant_and_paging", func(t *testing.T) {
		var e error
		grant, e = s.Grant(ctx, a, workspace, GrantRequest{PluginVersionID: version.ID, Reason: "Explicit exact version"}, uuid.NewString(), uuid.NewString())
		if e != nil || !grant.Enabled || grant.Version.ID != version.ID {
			t.Fatal(e)
		}
		page, e := s.Grants(ctx, portal, workspace, url.Values{"limit": {"1"}})
		if e != nil || len(page.Items) != 1 || page.Items[0].ID != grant.ID || !page.Items[0].Version.Available {
			t.Fatal("portal grant selection", e, page)
		}
		_, e = s.Grant(ctx, a, workspace, GrantRequest{PluginVersionID: version.ID, Reason: "No duplicate grant"}, uuid.NewString(), uuid.NewString())
		expectCode(t, e, "PLUGIN_GRANT_EXISTS")
		_, e = s.Grants(ctx, portal, uuid.NewString(), url.Values{})
		if e == nil {
			t.Fatal("foreign Workspace grant leak")
		}
	})
	run := uuid.NewString()
	question := "Read the current model directory"
	var completed *CompletedModelList
	t.Run("real_empty_query_no_fabricated_output_and_opaque_binding", func(t *testing.T) {
		e := modelTransaction(t, s, portal, func(tx pgx.Tx) error {
			var e error
			completed, e = s.ExecuteModelListInTransaction(ctx, tx, portal, workspace, version.ID, run, question)
			return e
		})
		if e != nil || completed == nil || completed.Result().Items == nil || len(completed.Result().Items) != 0 {
			t.Fatal("real empty model query", e)
		}
		e = modelTransaction(t, s, portal, func(tx pgx.Tx) error {
			return s.RecheckCompletedModelListInTransaction(ctx, tx, portal, workspace, version.ID, uuid.NewString(), question, completed)
		})
		expectCode(t, e, "PLUGIN_AUTHORITY_INVALID")
		e = modelTransaction(t, s, portal, func(tx pgx.Tx) error {
			return s.RecheckCompletedModelListInTransaction(ctx, tx, portal, workspace, version.ID, run, question, completed)
		})
		if e != nil {
			t.Fatal("bound final capability", e)
		}
	})
	t.Run("revoke_fences_completion_and_history_then_explicit_enable", func(t *testing.T) {
		var e error
		grant, e = s.UpdateGrant(ctx, a, workspace, grant.ID, GrantUpdate{Enabled: false, ExpectedRevision: grant.Revision, Reason: "Actual grant revocation"}, uuid.NewString(), uuid.NewString())
		if e != nil || grant.Enabled {
			t.Fatal(e)
		}
		e = modelTransaction(t, s, portal, func(tx pgx.Tx) error {
			return s.RecheckCompletedModelListInTransaction(ctx, tx, portal, workspace, version.ID, run, question, completed)
		})
		expectCode(t, e, "PLUGIN_GRANT_REVOKED")
		result, _ := json.Marshal(completed.Result())
		e = modelTransaction(t, s, portal, func(tx pgx.Tx) error {
			return s.RecheckStoredModelListInTransaction(ctx, tx, portal, workspace, version.ID, completed.StoredProof(), result)
		})
		expectCode(t, e, "PLUGIN_GRANT_REVOKED")
		grant, e = s.UpdateGrant(ctx, a, workspace, grant.ID, GrantUpdate{Enabled: true, ExpectedRevision: grant.Revision, Reason: "Explicit reviewed re-enable"}, uuid.NewString(), uuid.NewString())
		if e != nil {
			t.Fatal(e)
		}
		e = modelTransaction(t, s, portal, func(tx pgx.Tx) error {
			return s.RecheckCompletedModelListInTransaction(ctx, tx, portal, workspace, version.ID, run, question, completed)
		})
		expectCode(t, e, "PLUGIN_AUTHORITY_CHANGED")
		e = modelTransaction(t, s, portal, func(tx pgx.Tx) error {
			return s.RecheckStoredModelListInTransaction(ctx, tx, portal, workspace, version.ID, completed.StoredProof(), result)
		})
		if e != nil {
			t.Fatal("unchanged authorized history", e)
		}
	})
	t.Run("actual_model_metadata_query_and_retired_reference_withholding", func(t *testing.T) {
		// This direct approved-catalog row is explicitly a metadata fixture. It proves
		// real caller-scoped SQL/manifest validation, not model-file import or hardware.
		var tenant string
		if e := s.DB.QueryRow(ctx, "SELECT tenant_id::text FROM workspaces WHERE id=$1", workspace).Scan(&tenant); e != nil {
			t.Fatal(e)
		}
		catalog := agentwire.ApprovedModel()
		raw, _ := json.Marshal(catalog)
		model := uuid.NewString()
		if _, e := s.DB.Exec(ctx, "INSERT INTO model_versions(id,tenant_id,workspace_id,name,model_key,catalog_digest,manifest,state,kind,created_by) VALUES($1,$2,$3,'Caf\u00e9 model',$4,$5,$6,'ready','approved_base',$7)", model, tenant, workspace, catalog.ModelKey, agentwire.ModelCatalogDigest(), raw, a.Me.User.ID); e != nil {
			t.Fatal(e)
		}
		var current *CompletedModelList
		e := modelTransaction(t, s, portal, func(tx pgx.Tx) error {
			var e error
			current, e = s.ExecuteModelListInTransaction(ctx, tx, portal, workspace, version.ID, uuid.NewString(), question)
			return e
		})
		if e != nil || len(current.Result().Items) != 1 || current.Result().Items[0].ID != model {
			t.Fatal("actual returned ready model", e)
		}
		result, _ := json.Marshal(current.Result())
		if strings.Contains(string(result), "manifest") || strings.Contains(string(result), "path") || strings.Contains(string(result), "usage") {
			t.Fatal("private artifact or invented usage in result")
		}
		if _, e = s.DB.Exec(ctx, "UPDATE model_versions SET state='deleted' WHERE id=$1", model); e != nil {
			t.Fatal(e)
		}
		e = modelTransaction(t, s, portal, func(tx pgx.Tx) error {
			return s.RecheckStoredModelListInTransaction(ctx, tx, portal, workspace, version.ID, current.StoredProof(), result)
		})
		expectCode(t, e, "PLUGIN_MODEL_REFERENCE_CHANGED")
	})
	t.Run("audit_failure_rolls_back_real_import_and_grant_mutation", func(t *testing.T) {
		if _, e := s.DB.Exec(ctx, `CREATE FUNCTION plugin_test_reject_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action LIKE 'plugin.%' THEN RAISE EXCEPTION 'SYNTHETIC_PLUGIN_AUDIT_REFUSAL'; END IF; RETURN NEW; END $$; CREATE TRIGGER plugin_test_reject_audit BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION plugin_test_reject_audit()`); e != nil {
			t.Fatal(e)
		}
		_, e := s.Import(ctx, a, pluginInput(t, private, "1.0.1"), uuid.NewString(), uuid.NewString())
		if e == nil {
			t.Fatal("failed audit committed import")
		}
		_, e = s.UpdateGrant(ctx, a, workspace, grant.ID, GrantUpdate{Enabled: false, ExpectedRevision: grant.Revision, Reason: "Must roll back"}, uuid.NewString(), uuid.NewString())
		if e == nil {
			t.Fatal("failed audit committed revocation")
		}
		var count int
		var enabled bool
		var revision int64
		if e = s.DB.QueryRow(ctx, "SELECT count(*) FROM plugin_versions").Scan(&count); e != nil || count != 1 {
			t.Fatal(e, count)
		}
		if e = s.DB.QueryRow(ctx, "SELECT enabled,revision FROM plugin_grants WHERE id=$1", grant.ID).Scan(&enabled, &revision); e != nil || !enabled || revision != grant.Revision {
			t.Fatal("grant audit rollback", e)
		}
		if _, e = s.DB.Exec(ctx, "DROP TRIGGER plugin_test_reject_audit ON audit_events; DROP FUNCTION plugin_test_reject_audit()"); e != nil {
			t.Fatal(e)
		}
	})
	t.Run("concurrent_import_single_provenance_and_cursor_scope", func(t *testing.T) {
		next := pluginInput(t, private, "1.0.1")
		var wg sync.WaitGroup
		results := make(chan error, 2)
		for i := 0; i < 2; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, e := s.Import(ctx, a, next, uuid.NewString(), uuid.NewString())
				results <- e
			}()
		}
		wg.Wait()
		close(results)
		for e := range results {
			if e != nil {
				t.Fatal(e)
			}
		}
		first, e := s.List(ctx, a, url.Values{"limit": {"1"}})
		if e != nil || len(first.Items) != 1 || first.NextCursor == nil {
			t.Fatal("actual bounded cursor", e)
		}
		second, e := s.List(ctx, a, url.Values{"limit": {"1"}, "cursor": {*first.NextCursor}})
		if e != nil || len(second.Items) != 1 || second.Items[0].ID == first.Items[0].ID || second.NextCursor != nil {
			t.Fatal("actual next page", e)
		}
		_, e = s.Grants(ctx, a, workspace, url.Values{"cursor": {*first.NextCursor}})
		expectCode(t, e, "INVALID_CURSOR")
	})
	t.Run("reference_preview_uninstall_reinstall_and_retained_provenance", func(t *testing.T) {
		refs, e := s.References(ctx, a, version.ID)
		if e != nil || refs.Items == nil || refs.Count != 0 {
			t.Fatal("real empty reference query", e)
		}
		_, e = s.Uninstall(ctx, a, version.ID, UninstallRequest{ExpectedRevision: version.Revision, ReferencesSHA256: strings.Repeat("0", 64), Reason: "Reject stale preview"}, uuid.NewString(), uuid.NewString())
		expectCode(t, e, "PLUGIN_REFERENCES_CHANGED")
		version, e = s.Uninstall(ctx, a, version.ID, UninstallRequest{ExpectedRevision: version.Revision, ReferencesSHA256: refs.SHA256, Reason: "Explicit uninstallation"}, uuid.NewString(), uuid.NewString())
		if e != nil || version.State != UninstalledState {
			t.Fatal(e)
		}
		foreign, e := s.Workspaces.Create(ctx, a, "Separate plugin scope", a.Me.User.ID, uuid.NewString())
		if e != nil {
			t.Fatal(e)
		}
		for _, candidate := range []string{version.ID, uuid.NewString()} {
			e = modelTransaction(t, s, portal, func(tx pgx.Tx) error { return s.CheckToolInTransaction(ctx, tx, portal, foreign.ID, candidate) })
			expectCode(t, e, "PLUGIN_GRANT_NOT_FOUND")
		}
		version, e = s.Install(ctx, a, version.ID, RevisionReason{ExpectedRevision: version.Revision, Reason: "Explicit reinstall without regrant"}, uuid.NewString(), uuid.NewString())
		if e != nil {
			t.Fatal(e)
		}
		page, e := s.Grants(ctx, portal, workspace, url.Values{})
		if e != nil || len(page.Items) != 0 {
			t.Fatal("reinstall silently regranted", e)
		}
		_, e = s.DB.Exec(ctx, "UPDATE plugin_versions SET key_id=$2 WHERE id=$1", version.ID, strings.Repeat("0", 64))
		if e == nil {
			t.Fatal("immutable signed provenance changed")
		}
		_, e = s.DB.Exec(ctx, "DELETE FROM plugin_versions WHERE id=$1", version.ID)
		if e == nil {
			t.Fatal("signed provenance deleted")
		}
	})
}

func TestM14ActualMigrationEmptyRollbackPopulated13UpgradeAndUsedDowngradeRefusal(t *testing.T) {
	fixture.PreflightOwnedPostgres(t)
	s := fixture.NewAtVersion(t, 13)
	password := fixture.Bootstrap(t, s)
	ctx := context.Background()
	if e := database.Migrate(ctx, s.DB); e != nil {
		t.Fatal("actual populated13 upgrade", e)
	}
	down, e := migrations.Files.ReadFile("postgresql/000014_plugins.down.sql")
	if e != nil {
		t.Fatal(e)
	}
	tx, e := s.DB.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback(ctx)
	// Reverse unused successor16 before15 to preserve the historical ledger.
	localizationDown, e := migrations.Files.ReadFile("postgresql/000016_localization.down.sql")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = tx.Exec(ctx, string(localizationDown)); e != nil {
		t.Fatal("unused M16 successor rollback", e)
	}
	if _, e = tx.Exec(ctx, "DELETE FROM schema_migrations WHERE version=16"); e != nil {
		t.Fatal(e)
	}
	portalDown, e := migrations.Files.ReadFile("postgresql/000015_portal.down.sql")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = tx.Exec(ctx, string(portalDown)); e != nil {
		t.Fatal("empty M15 successor rollback", e)
	}
	if _, e = tx.Exec(ctx, "DELETE FROM schema_migrations WHERE version=15"); e != nil {
		t.Fatal(e)
	}
	if _, e = tx.Exec(ctx, string(down)); e != nil {
		t.Fatal("empty M14 rollback", e)
	}
	if _, e = tx.Exec(ctx, "DELETE FROM schema_migrations WHERE version=14"); e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(ctx); e != nil {
		t.Fatal(e)
	}
	if e = database.Migrate(ctx, s.DB); e != nil {
		t.Fatal("actual repeat up migration", e)
	}
	_ = fixture.Login(t, s, "admin@example.invalid", password, "portal")
	// Direct intent fixture tests the downgrade's refusal to discard used state;
	// it is not evidence of a successful plugin mutation.
	var tenant, user string
	if e = s.DB.QueryRow(ctx, "SELECT tenant_id::text,primary_admin_user_id::text FROM installation_state WHERE id=1").Scan(&tenant, &user); e != nil {
		t.Fatal(e)
	}
	if _, e = s.DB.Exec(ctx, "INSERT INTO plugin_mutation_intents(tenant_id,actor_id,idempotency_key,purpose,request_hash,entity_id) VALUES($1,$2,$3,'synthetic-downgrade-guard',$4,$5)", tenant, user, uuid.NewString(), make([]byte, 32), uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	tx, e = s.DB.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, string(down)); e == nil || !strings.Contains(e.Error(), "PLUGIN_DOWNGRADE_REQUIRES_VERIFIED_BACKUP") {
		t.Fatal("used data silently downgraded", e)
	}
}
