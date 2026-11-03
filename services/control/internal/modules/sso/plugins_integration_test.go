//go:build plugin_database_integration

package sso

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"net/url"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/models"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/plugins"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	wire "github.com/Passion-Flow/ownsolid/api/plugins/v1"
)

// Provider connector/binding rows are explicit fixtures. Issuance and source
// revocation checks use actual persistent identity transactions, not a fake guard.
func TestM14ActualEveryPluginOperationRechecksRevokedSSOSource(t *testing.T) {
	for _, revoked := range []string{"connector", "binding"} {
		t.Run(revoked, func(t *testing.T) {
			f := sourceFixture(t)
			ctx := context.Background()
			accounts := f.s.Identity
			public, private, e := ed25519.GenerateKey(nil)
			if e != nil {
				t.Fatal(e)
			}
			keyID, _ := wire.KeyID(public)
			accounts.Config.Plugins = config.PluginConfiguration{Enabled: true, TrustedKeys: []config.PluginTrustKey{{KeyID: keyID, PublicKey: base64.StdEncoding.EncodeToString(public)}}}
			w := &workspaces.Service{DB: accounts.DB, Identity: accounts}
			s := &plugins.Service{DB: accounts.DB, Identity: accounts, Workspaces: w, Models: &models.Service{DB: accounts.DB, Identity: accounts, Workspaces: w}}
			if e = s.Start(ctx); e != nil {
				t.Fatal(e)
			}
			defer func() {
				operation, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if e := s.Stop(operation); e != nil {
					t.Error(e)
				}
			}()
			issued := f.issue(t, "")
			a, e := accounts.Authenticate(ctx, issued.Token, "admin")
			if e != nil {
				t.Fatal(e)
			}
			workspace := a.Me.Workspace.ID
			raw, e := wire.Sign(wire.Manifest{PluginID: wire.PluginID, Version: "1.0.0", ABI: wire.ABI, Executor: wire.Executor, Capabilities: []string{wire.Capability}, License: wire.License}, private, accounts.Config.Policy().Plugins.PackageLimits())
			if e != nil {
				t.Fatal(e)
			}
			input := plugins.ImportRequest{Capsule: base64.StdEncoding.EncodeToString(raw), SHA256: wire.Digest(raw), Reason: "Actual synthetic SSO plugin"}
			intent := uuid.NewString()
			version, e := s.Import(ctx, a, input, intent, uuid.NewString())
			if e != nil {
				t.Fatal(e)
			}
			version, e = s.Install(ctx, a, version.ID, plugins.RevisionReason{ExpectedRevision: version.Revision, Reason: "Actual SSO install"}, uuid.NewString(), uuid.NewString())
			if e != nil {
				t.Fatal(e)
			}
			grant, e := s.Grant(ctx, a, workspace, plugins.GrantRequest{PluginVersionID: version.ID, Reason: "Actual SSO grant"}, uuid.NewString(), uuid.NewString())
			if e != nil {
				t.Fatal(e)
			}
			refs, e := s.References(ctx, a, version.ID)
			if e != nil {
				t.Fatal(e)
			}
			transaction := func(fn func(pgx.Tx) error) error {
				tx, e := accounts.DB.Begin(ctx)
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
			run := uuid.NewString()
			question := "Actual current SSO directory"
			var completed *plugins.CompletedModelList
			if e = transaction(func(tx pgx.Tx) error {
				var e error
				completed, e = s.ExecuteModelListInTransaction(ctx, tx, a, workspace, version.ID, run, question)
				return e
			}); e != nil {
				t.Fatal(e)
			}
			result, _ := json.Marshal(completed.Result())
			id := f.source.ConnectorID
			query := `UPDATE sso_connectors SET enabled=false,revision=revision+1,auth_version=auth_version+1,updated_at=clock_timestamp() WHERE id=$1`
			if revoked == "binding" {
				id = f.source.BindingID
				query = `UPDATE sso_bindings SET active=false,revision=revision+1 WHERE id=$1`
			}
			if _, e = accounts.DB.Exec(ctx, query, id); e != nil {
				t.Fatal(e)
			}
			checks := map[string]func() error{
				"configuration": func() error { _, e := s.Configuration(ctx, a); return e },
				"list":          func() error { _, e := s.List(ctx, a, url.Values{}); return e },
				"get":           func() error { _, e := s.Get(ctx, a, version.ID); return e },
				"import":        func() error { _, e := s.Import(ctx, a, input, uuid.NewString(), uuid.NewString()); return e },
				"import_replay": func() error { _, e := s.Import(ctx, a, input, intent, uuid.NewString()); return e },
				"install": func() error {
					_, e := s.Install(ctx, a, version.ID, plugins.RevisionReason{ExpectedRevision: version.Revision, Reason: "Refuse revoked source"}, uuid.NewString(), uuid.NewString())
					return e
				},
				"references": func() error { _, e := s.References(ctx, a, version.ID); return e },
				"uninstall": func() error {
					_, e := s.Uninstall(ctx, a, version.ID, plugins.UninstallRequest{ExpectedRevision: version.Revision, ReferencesSHA256: refs.SHA256, Reason: "Refuse revoked source"}, uuid.NewString(), uuid.NewString())
					return e
				},
				"grants": func() error { _, e := s.Grants(ctx, a, workspace, url.Values{}); return e },
				"grant": func() error {
					_, e := s.Grant(ctx, a, workspace, plugins.GrantRequest{PluginVersionID: version.ID, Reason: "Refuse revoked source"}, uuid.NewString(), uuid.NewString())
					return e
				},
				"grant_update": func() error {
					_, e := s.UpdateGrant(ctx, a, workspace, grant.ID, plugins.GrantUpdate{Enabled: false, ExpectedRevision: grant.Revision, Reason: "Refuse revoked source"}, uuid.NewString(), uuid.NewString())
					return e
				},
				"reference": func() error {
					return transaction(func(tx pgx.Tx) error { return s.CheckToolInTransaction(ctx, tx, a, workspace, version.ID) })
				},
				"invoke": func() error {
					return transaction(func(tx pgx.Tx) error {
						_, e := s.ExecuteModelListInTransaction(ctx, tx, a, workspace, version.ID, uuid.NewString(), question)
						return e
					})
				},
				"final_completion": func() error {
					return transaction(func(tx pgx.Tx) error {
						return s.RecheckCompletedModelListInTransaction(ctx, tx, a, workspace, version.ID, run, question, completed)
					})
				},
				"history": func() error {
					return transaction(func(tx pgx.Tx) error {
						return s.RecheckStoredModelListInTransaction(ctx, tx, a, workspace, version.ID, completed.StoredProof(), result)
					})
				},
			}
			var before, after int
			count := `SELECT (SELECT count(*) FROM plugin_versions)+(SELECT count(*) FROM plugin_grants)+(SELECT count(*) FROM plugin_mutation_intents)+(SELECT count(*) FROM audit_events)`
			if e = accounts.DB.QueryRow(ctx, count).Scan(&before); e != nil {
				t.Fatal(e)
			}
			for name, check := range checks {
				t.Run(name, func(t *testing.T) { requireCode(t, check(), "AUTH_SESSION_EXPIRED") })
			}
			if e = accounts.DB.QueryRow(ctx, count).Scan(&after); e != nil || before != after {
				t.Fatal("revoked source mutated plugin authority", e, before, after)
			}
			local, e := accounts.Authenticate(ctx, f.local, "admin")
			if e != nil {
				t.Fatal(e)
			}
			if _, e = s.Get(ctx, local, version.ID); e != nil {
				t.Fatal("local emergency metadata recovery", e)
			}
		})
	}
}
