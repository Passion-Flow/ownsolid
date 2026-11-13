//go:build repository_integration

package models_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/models"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/migrations"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func familyInsert(ctx context.Context, q transaction.Executor, x modelReadFixture, id, state string, stamp time.Time) error {
	raw, _ := json.Marshal(wire.ApprovedModel())
	_, err := q.Exec(ctx, transaction.SQL(transaction.PostgreSQL, `INSERT INTO model_versions
(id,tenant_id,workspace_id,name,model_key,catalog_digest,manifest,state,created_by,created_at)
VALUES($1,$2,$3,$4,'qwen3-0.6b',$5,$6::jsonb,$7,$8,$9)`, id, x.f.Tenant, x.f.Workspace,
		"Synthetic \u4e16\u754c \u0627\u0644\u0639\u0631\u0628\u064a\u0629", wire.ModelCatalogDigest(), raw, state, x.f.User, stamp))
	return err
}

func familyID(t *testing.T, x modelReadFixture, id string) string {
	t.Helper()
	var value string
	if err := x.f.Store.QueryRow(x.ctx, transaction.SQL(transaction.PostgreSQL, `SELECT family_id::text FROM model_family_members WHERE version_id=$1`, id)).Scan(&value); err != nil {
		t.Fatal(err)
	}
	return value
}

func familyCount(t *testing.T, x modelReadFixture, table string) int {
	t.Helper()
	if table != "model_families" && table != "model_family_members" {
		t.Fatal("test table whitelist")
	}
	var n int
	if err := x.f.Store.QueryRow(x.ctx, transaction.SQL(transaction.PostgreSQL, "SELECT count(*) FROM "+table)).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func familyPublish(x modelReadFixture, id, anchor string, abort error) (string, error) {
	var family string
	err := x.f.Store.Within(x.ctx, false, func(ctx context.Context, q transaction.Executor) error {
		var err error
		family, err = x.s.StageVersionFamilyWithin(ctx, q, x.admin, x.f.Workspace, id, anchor, "Synthetic explicit family selection", uuid.NewString())
		if err != nil {
			return err
		}
		if err = familyInsert(ctx, q, x, id, "deleted", time.Now().UTC().Truncate(time.Microsecond)); err != nil {
			return err
		}
		return abort
	})
	if err != nil {
		return "", err
	}
	return family, nil
}

func familyDenied(t *testing.T, x modelReadFixture, a identity.Auth, workspace, anchor string, input models.HistoryQuery, code string) {
	t.Helper()
	p, err := x.s.History(x.ctx, a, workspace, anchor, input)
	modelReadError(t, err, code)
	if !reflect.DeepEqual(p, models.VersionHistory{}) {
		t.Fatal("failed history released partial projection")
	}
}

func TestActualModelFamilyFormalBackfillAndLifecycle(t *testing.T) {
	x := familyPredecessor(t)
	old := uuid.NewString()
	if err := familyInsert(x.ctx, x.f.Store, x, old, "deleted", time.Now().UTC().Truncate(time.Microsecond)); err != nil {
		t.Fatal(err)
	}
	dep := retirementDeployment(t, x, x.f.Store, retirementNode(t, x), x.model, true)
	var before string
	if err := x.f.PostgreSQL.QueryRow(x.ctx, `SELECT jsonb_agg(to_jsonb(v) ORDER BY id)::text FROM model_versions v`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if version, err := database.CheckPrefixWithin(x.ctx, x.f.Store); err != nil || version != 60 {
		t.Fatal("exact predecessor prefix", version, err)
	}
	if err := database.Check(x.ctx, x.f.PostgreSQL); err == nil || err.Error() != "SCHEMA_INCOMPATIBLE" {
		t.Fatal("predecessor serving fence", err)
	}
	familyDenied(t, x, x.admin, x.f.Workspace, x.model, models.HistoryQuery{}, "MODEL_HISTORY_UNAVAILABLE")
	// Migrate uses a real savepoint; the outer transaction deliberately rolls back
	// both the numbered DDL and its ledger, without substituting a catalog digest.
	outer, err := x.f.PostgreSQL.Begin(x.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer outer.Rollback(context.Background())
	if err := database.Migrate(x.ctx, outer); err != nil {
		t.Fatal("formal migration inside rollback", err)
	}
	if err := outer.Rollback(x.ctx); err != nil {
		t.Fatal(err)
	}
	if version, err := database.CheckPrefixWithin(x.ctx, x.f.Store); err != nil || version != 60 {
		t.Fatal("rollback changed predecessor ledger", version, err)
	}
	familyDenied(t, x, x.admin, x.f.Workspace, x.model, models.HistoryQuery{}, "MODEL_HISTORY_UNAVAILABLE")
	if err := database.Migrate(x.ctx, x.f.PostgreSQL); err != nil {
		t.Fatal("actual formal migration", err)
	}
	familyAdmitted(t, x)
	var after string
	if err := x.f.PostgreSQL.QueryRow(x.ctx, `SELECT jsonb_agg(to_jsonb(v) ORDER BY id)::text FROM model_versions v`).Scan(&after); err != nil || after != before {
		t.Fatal("formal backfill rewrote exact version metadata", err)
	}
	first, second := familyID(t, x, x.model), familyID(t, x, old)
	if first == second || first == x.model || second == old || familyCount(t, x, "model_families") != 2 {
		t.Fatal("legacy IDs inferred a family")
	}
	for range 2 {
		if err := database.Migrate(x.ctx, x.f.PostgreSQL); err != nil {
			t.Fatal("formal migration replay", err)
		}
		if familyID(t, x, x.model) != first || familyID(t, x, old) != second {
			t.Fatal("migration replay changed a family")
		}
	}
	var invalid int
	if err := x.f.PostgreSQL.QueryRow(x.ctx, `SELECT count(*) FROM model_family_members m JOIN model_families f ON f.id=m.family_id JOIN model_versions v ON v.id=m.version_id WHERE
 (f.site_id,f.cell_id,m.site_id,m.cell_id,v.site_id,v.cell_id) IS DISTINCT FROM
 (local_site_id(),local_cell_id(),local_site_id(),local_cell_id(),local_site_id(),local_cell_id())`).Scan(&invalid); err != nil || invalid != 0 {
		t.Fatal("backfill scope mismatch", invalid, err)
	}
	x.s.Referenced = func(context.Context, transaction.Executor, string, string, string) (bool, error) { return false, nil }
	retirementRetire(t, x, x.model)
	detail, err := x.s.HistoricalVersion(x.ctx, x.admin, x.f.Workspace, x.model, x.model)
	if err != nil || detail.State != "retired" || detail.FamilyID != first {
		t.Fatal("retained upgraded exact version", err)
	}
	newID := uuid.NewString()
	if err := familyInsert(x.ctx, x.f.Store, x, newID, "ready", time.Now().UTC().Truncate(time.Microsecond)); err != nil {
		t.Fatal(err)
	}
	if familyID(t, x, newID) == first {
		t.Fatal("reimport inferred a family")
	}
	if err := x.s.ReadyWithin(x.ctx, x.f.Store, x.f.Tenant, x.f.Workspace, x.model); err == nil || err.Error() != "RESOURCE_NOT_FOUND" {
		t.Fatal("retired exact execution admitted", err)
	}
	down, err := migrations.Files.ReadFile("postgresql/000061_model_families.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	downgrade, err := x.f.PostgreSQL.Begin(x.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer downgrade.Rollback(context.Background())
	_, err = downgrade.Exec(x.ctx, string(down))
	var denied *pgconn.PgError
	if !errors.As(err, &denied) || denied.Code != "0A000" || denied.Message != "MODEL_FAMILY_DOWNGRADE_REQUIRES_RETENTION" {
		t.Fatal("populated formal downgrade", err)
	}
	if err := downgrade.Rollback(x.ctx); err != nil {
		t.Fatal(err)
	}
	if familyID(t, x, x.model) != first || familyID(t, x, old) != second {
		t.Fatal("refused downgrade changed a family")
	}
	var retained string
	if err := x.f.Store.QueryRow(x.ctx, transaction.SQL(transaction.PostgreSQL, `SELECT model_id::text FROM deployments WHERE id=$1`, dep)).Scan(&retained); err != nil || retained != x.model {
		t.Fatal("deployment exact reference changed", err)
	}
	if value, err := x.s.Get(x.ctx, x.admin, x.f.Workspace, newID); err != nil || value.ID != newID {
		t.Fatal("legacy local read after upgrade", err)
	}
}

func TestActualModelFamilyHistoryPagesAndAuthority(t *testing.T) {
	for _, name := range []string{"pages_filters_and_exact_ids", "invalid_inputs", "revoked_session", "disabled_user", "demoted_admin", "changed_epoch", "disabled_workspace", "foreign_scope", "unknown_outcome", "damaged_page"} {
		t.Run(name, func(t *testing.T) {
			x := familySetup(t)
			ids := []string{uuid.NewString(), uuid.NewString(), uuid.NewString()}
			sort.Sort(sort.Reverse(sort.StringSlice(ids)))
			stamp := time.Now().UTC().Truncate(time.Microsecond)
			for _, id := range ids {
				err := x.f.Store.Within(x.ctx, false, func(ctx context.Context, q transaction.Executor) error {
					if _, err := x.s.StageVersionFamilyWithin(ctx, q, x.admin, x.f.Workspace, id, x.model, "Synthetic tied-time history", uuid.NewString()); err != nil {
						return err
					}
					return familyInsert(ctx, q, x, id, "deleted", stamp)
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			policy := config.DefaultRuntimePolicy()
			policy.UI.PageSize = 2
			x.owner.Config.Runtime = &policy
			first, err := x.s.History(x.ctx, x.admin, x.f.Workspace, x.model, models.HistoryQuery{})
			if err != nil || len(first.Items) != 2 || first.NextCursor == nil || first.Items[0].ID != ids[0] || first.Items[1].ID != ids[1] {
				t.Fatal("tied-time first page", err)
			}
			query := models.HistoryQuery{Cursor: first.NextCursor}
			switch name {
			case "pages_filters_and_exact_ids":
				one := 1
				query.Limit = &one
				second, err := x.s.History(x.ctx, x.admin, x.f.Workspace, x.model, query)
				if err != nil || len(second.Items) != 1 || second.Items[0].ID != ids[2] || second.NextCursor == nil {
					t.Fatal("second page", err)
				}
				last, err := x.s.History(x.ctx, x.admin, x.f.Workspace, x.model, models.HistoryQuery{Cursor: second.NextCursor})
				if err != nil || len(last.Items) != 1 || last.Items[0].ID != x.model || last.NextCursor != nil {
					t.Fatal("final page", err)
				}
				raw, _ := json.Marshal(last)
				if !strings.Contains(string(raw), `"next_cursor":null`) {
					t.Fatal("continuation not explicit null")
				}
				for _, state := range []string{"ready", "retired"} {
					limit := 100
					p, err := x.s.History(x.ctx, x.admin, x.f.Workspace, x.model, models.HistoryQuery{State: &state, Limit: &limit})
					want := 3
					if state == "ready" {
						want = 1
					}
					if err != nil || len(p.Items) != want || p.NextCursor != nil {
						t.Fatal("state filter", err)
					}
				}
				detail, err := x.s.HistoricalVersion(x.ctx, x.admin, x.f.Workspace, x.model, ids[0])
				if err != nil || detail.ID != ids[0] || detail.FamilyID != first.FamilyID || detail.State != "retired" || len(detail.Manifest) == 0 {
					t.Fatal("retained exact detail", err)
				}
				if v, err := x.s.Get(x.ctx, x.admin, x.f.Workspace, ids[0]); err == nil || v.ID != "" {
					t.Fatal("retired metadata changed ready admission")
				}
				other := uuid.NewString()
				if err := familyInsert(x.ctx, x.f.Store, x, other, "deleted", stamp); err != nil {
					t.Fatal(err)
				}
				if familyID(t, x, other) == first.FamilyID {
					t.Fatal("name/key/digest inferred membership")
				}
				if detail, err := x.s.HistoricalVersion(x.ctx, x.admin, x.f.Workspace, x.model, other); err == nil || detail.ID != "" {
					t.Fatal("foreign family detail", err)
				}
				ready := "ready"
				empty, err := x.s.History(x.ctx, x.admin, x.f.Workspace, other, models.HistoryQuery{State: &ready})
				if err != nil || empty.Items == nil || len(empty.Items) != 0 || empty.NextCursor != nil {
					t.Fatal("empty filtered page", err)
				}
				x.exec(t, `UPDATE model_versions SET manifest='{}'::jsonb WHERE id=$1`, "", other)
				for _, version := range []string{other, uuid.NewString()} {
					detail, err := x.s.HistoricalVersion(x.ctx, x.admin, x.f.Workspace, x.model, version)
					modelReadError(t, err, "RESOURCE_NOT_FOUND")
					if detail.ID != "" || detail.DatasetLineage != nil {
						t.Fatal("foreign or unknown version leaked detail")
					}
				}
				familyDenied(t, x, x.portal, x.f.Workspace, x.model, query, "ACCESS_DENIED")
			case "invalid_inputs":
				for _, limit := range []int{0, -1, 101} {
					familyDenied(t, x, x.admin, x.f.Workspace, x.model, models.HistoryQuery{Limit: &limit}, "VALIDATION_FAILED")
				}
				for _, state := range []string{"", "deleted", "READY"} {
					familyDenied(t, x, x.admin, x.f.Workspace, x.model, models.HistoryQuery{State: &state}, "VALIDATION_FAILED")
				}
				for _, cursor := range []string{"", *first.NextCursor + "=", "invalid"} {
					familyDenied(t, x, x.admin, x.f.Workspace, x.model, models.HistoryQuery{Cursor: &cursor}, "INVALID_CURSOR")
				}
				state := "retired"
				query.State = &state
				familyDenied(t, x, x.admin, x.f.Workspace, x.model, query, "INVALID_CURSOR")
				familyDenied(t, x, x.admin, x.f.Workspace, ids[0], models.HistoryQuery{Cursor: first.NextCursor}, "INVALID_CURSOR")
			case "revoked_session":
				x.exec(t, `UPDATE sessions SET revoked_at=clock_timestamp(),revoke_reason='synthetic_history' WHERE id=$1`, "", x.admin.SessionID)
				familyDenied(t, x, x.admin, x.f.Workspace, x.model, query, "AUTH_SESSION_EXPIRED")
			case "disabled_user":
				x.exec(t, `UPDATE users SET status='disabled' WHERE id=$1`, "", x.f.User)
				familyDenied(t, x, x.admin, x.f.Workspace, x.model, query, "AUTH_SESSION_EXPIRED")
			case "demoted_admin":
				x.exec(t, `UPDATE users SET is_platform_admin=false WHERE id=$1`, "", x.f.User)
				familyDenied(t, x, x.admin, x.f.Workspace, x.model, query, "AUTH_SESSION_EXPIRED")
			case "changed_epoch":
				x.exec(t, `UPDATE users SET auth_epoch=auth_epoch+1 WHERE id=$1`, "", x.f.User)
				familyDenied(t, x, x.admin, x.f.Workspace, x.model, query, "AUTH_SESSION_EXPIRED")
			case "disabled_workspace":
				x.exec(t, `UPDATE workspaces SET status='disabled' WHERE id=$1`, "", x.f.Workspace)
				familyDenied(t, x, x.admin, x.f.Workspace, x.model, query, "ACCESS_DENIED")
			case "foreign_scope":
				other, foreign, fw := uuid.NewString(), uuid.NewString(), uuid.NewString()
				x.exec(t, `INSERT INTO workspaces(id,tenant_id,name,status) VALUES($1,$2,'Other','active')`, "", other, x.f.Tenant)
				x.exec(t, `INSERT INTO tenants(id,name,status) VALUES($1,'Foreign','active')`, "", foreign)
				x.exec(t, `INSERT INTO workspaces(id,tenant_id,name,status) VALUES($1,$2,'Foreign','active')`, "", fw, foreign)
				for _, ws := range []string{other, fw} {
					familyDenied(t, x, x.admin, ws, x.model, query, "RESOURCE_NOT_FOUND")
				}
				if _, err := x.s.HistoricalVersion(x.ctx, x.admin, other, x.model, ids[0]); err == nil {
					t.Fatal("cross-workspace detail")
				}
			case "unknown_outcome":
				owner := *x.owner
				owner.Storage = modelUnknownStore{x.f.Store}
				s := *x.s
				s.Identity = &owner
				s.Workspaces = &workspaces.Service{Identity: &owner}
				x.s = &s
				familyDenied(t, x, x.admin, x.f.Workspace, x.model, query, string(transaction.CommitUnknown))
				if detail, err := s.HistoricalVersion(x.ctx, x.admin, x.f.Workspace, x.model, ids[0]); err == nil || detail.ID != "" {
					t.Fatal("unknown detail projection", err)
				}
			case "damaged_page":
				x.exec(t, `UPDATE model_versions SET manifest='{}'::jsonb WHERE id=$1`, "", ids[1])
				familyDenied(t, x, x.admin, x.f.Workspace, x.model, models.HistoryQuery{}, "MODEL_FILE_INVALID")
			}
		})
	}
}

func TestActualModelFamilySelectionTransactions(t *testing.T) {
	for _, name := range []string{"rollback", "default_rollback", "orphan", "audit_rollback", "immutable", "matching_concurrency", "conflicting_concurrency"} {
		t.Run(name, func(t *testing.T) {
			x := familySetup(t)
			id := uuid.NewString()
			before := x.audits(t)
			switch name {
			case "rollback":
				fault := errors.New("synthetic publication rollback")
				family, err := familyPublish(x, id, x.model, fault)
				if !errors.Is(err, fault) || family != "" || familyCount(t, x, "model_family_members") != 1 || x.audits(t) != before {
					t.Fatal("publication rollback", err)
				}
			case "default_rollback":
				fault := errors.New("synthetic default publication rollback")
				err := x.f.Store.Within(x.ctx, false, func(ctx context.Context, q transaction.Executor) error {
					if err := familyInsert(ctx, q, x, id, "deleted", time.Now().UTC().Truncate(time.Microsecond)); err != nil {
						return err
					}
					return fault
				})
				if !errors.Is(err, fault) || familyCount(t, x, "model_families") != 1 || familyCount(t, x, "model_family_members") != 1 {
					t.Fatal("default publication rollback", err)
				}
			case "orphan":
				err := x.f.Store.Within(x.ctx, false, func(ctx context.Context, q transaction.Executor) error {
					_, err := x.s.StageVersionFamilyWithin(ctx, q, x.admin, x.f.Workspace, id, "", "Synthetic orphan", uuid.NewString())
					return err
				})
				if err == nil || familyCount(t, x, "model_families") != 1 || familyCount(t, x, "model_family_members") != 1 || x.audits(t) != before {
					t.Fatal("orphan membership committed", err)
				}
			case "audit_rollback":
				familyOperatorExec(t, x, `CREATE FUNCTION reject_family_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='model.version_family_selected' THEN RAISE EXCEPTION 'SYNTHETIC_AUDIT_FAILURE'; END IF; RETURN NEW; END $$; CREATE TRIGGER reject_family_audit BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION reject_family_audit()`)
				family, err := familyPublish(x, id, "", nil)
				if err == nil || family != "" || familyCount(t, x, "model_families") != 1 || familyCount(t, x, "model_family_members") != 1 {
					t.Fatal("audit failure left membership", err)
				}
			case "immutable":
				for _, sql := range []string{`UPDATE model_family_members SET family_id=gen_random_uuid()`, `DELETE FROM model_family_members`, `UPDATE model_families SET initial_version_id=gen_random_uuid()`, `DELETE FROM model_families`} {
					_, err := x.f.PostgreSQL.Exec(x.ctx, sql)
					var denied *pgconn.PgError
					if !errors.As(err, &denied) || denied.Code != "23514" || denied.Message != "MODEL_FAMILY_IMMUTABLE" {
						t.Fatal("immutable domain trigger", sql, err)
					}
				}
			case "matching_concurrency", "conflicting_concurrency":
				anchor := x.model
				if name == "conflicting_concurrency" {
					anchor = uuid.NewString()
					if err := familyInsert(x.ctx, x.f.Store, x, anchor, "deleted", time.Now().UTC().Truncate(time.Microsecond)); err != nil {
						t.Fatal(err)
					}
				}
				otherUser := uuid.NewString()
				x.exec(t, `INSERT INTO users(id,email_canonical,email_display,display_name,locale,status,is_platform_admin) VALUES($1,$2,$2,'Other publisher','en','active',true)`, "", otherUser, otherUser+"@example.invalid")
				second := x
				second.f.User = otherUser
				secondActor := second.issue(t, "admin")
				// Actor construction has its own real login audit, outside publication.
				before = x.audits(t)
				observer, err := pgx.ConnectConfig(x.ctx, x.operator.Config().ConnConfig.Copy())
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = observer.Close(context.Background()) })
				entered, release := make(chan struct{}), make(chan struct{})
				done := make(chan error, 2)
				var releaseOnce sync.Once
				releasePublisher := func() { releaseOnce.Do(func() { close(release) }) }
				var workers sync.WaitGroup
				t.Cleanup(func() { releasePublisher(); workers.Wait() })
				workers.Add(1)
				go func() {
					defer workers.Done()
					done <- x.f.Store.Within(x.ctx, false, func(ctx context.Context, q transaction.Executor) error {
						if _, err := x.s.StageVersionFamilyWithin(ctx, q, x.admin, x.f.Workspace, id, x.model, "Synthetic first publisher", uuid.NewString()); err != nil {
							return err
						}
						close(entered)
						select {
						case <-release:
						case <-ctx.Done():
							return ctx.Err()
						}
						return familyInsert(ctx, q, x, id, "deleted", time.Now().UTC().Truncate(time.Microsecond))
					})
				}()
				select {
				case <-entered:
				case err := <-done:
					t.Fatal("first publisher", err)
				case <-x.ctx.Done():
					t.Fatal(x.ctx.Err())
				}
				pid := make(chan int, 1)
				workers.Add(1)
				go func() {
					defer workers.Done()
					done <- x.f.Store.Within(x.ctx, false, func(ctx context.Context, q transaction.Executor) error {
						var backend int
						if err := q.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, `SELECT pg_backend_pid()`)).Scan(&backend); err != nil {
							return err
						}
						pid <- backend
						_, err := x.s.StageVersionFamilyWithin(ctx, q, secondActor, x.f.Workspace, id, anchor, "Synthetic second publisher", uuid.NewString())
						return err
					})
				}()
				// Query through the separate operator connection: the business pool has two publishers.
				var backend int
				select {
				case backend = <-pid:
				case <-x.ctx.Done():
					releasePublisher()
					t.Fatal(x.ctx.Err())
				}
				deadline := time.Now().Add(2 * time.Second)
				blocked := false
				for time.Now().Before(deadline) {
					var waiting bool
					if err := observer.QueryRow(x.ctx, `SELECT EXISTS(SELECT 1 FROM pg_locks WHERE pid=$1 AND locktype='advisory' AND NOT granted)`, backend).Scan(&waiting); err != nil {
						releasePublisher()
						t.Fatal(err)
					}
					if waiting {
						blocked = true
						break
					}
					time.Sleep(10 * time.Millisecond)
				}
				releasePublisher()
				var failures []error
				for range 2 {
					if err := <-done; err != nil {
						failures = append(failures, err)
					}
				}
				if !blocked {
					t.Fatal("second publisher did not contend on a real database lock")
				}
				if name == "matching_concurrency" && len(failures) != 0 || name == "conflicting_concurrency" && (len(failures) != 1 || failures[0].Error() != "MODEL_FAMILY_CONFLICT") {
					t.Fatal("concurrent selection", failures)
				}
				var selections int
				if err := x.f.Store.QueryRow(x.ctx, transaction.SQL(transaction.PostgreSQL,
					`SELECT count(*) FROM audit_events WHERE tenant_id=$1 AND workspace_id=$2 AND action='model.version_family_selected' AND target_type='model_version' AND target_id=$3`,
					x.f.Tenant, x.f.Workspace, id)).Scan(&selections); err != nil {
					t.Fatal(err)
				}
				actual, expected, audits := familyID(t, x, id), familyID(t, x, x.model), x.audits(t)
				if actual != expected || selections != 1 || audits != before+1 {
					t.Fatalf("concurrent membership/audit: family=%s want=%s selections=%d audits=%d want=%d", actual, expected, selections, audits, before+1)
				}
			}
		})
	}
}

func TestActualModelFamilyRetainedDerivedGraph(t *testing.T) {
	x := familySetup(t)
	c, want := seedModelHTTPLineage(t, x, nil)
	familyScopedVersion(t, x, c.ModelID)
	if familyID(t, x, c.ModelID) == familyID(t, x, x.model) {
		t.Fatal("derivation parent inferred family")
	}
	detail, err := x.s.HistoricalVersion(x.ctx, x.admin, x.f.Workspace, c.ModelID, c.ModelID)
	if err != nil || detail.DatasetLineage == nil || *detail.DatasetLineage != want {
		t.Fatal("existing retained graph", err)
	}
	x.s.Referenced = func(context.Context, transaction.Executor, string, string, string) (bool, error) { return false, nil }
	retirementRetire(t, x, c.ModelID)
	detail, err = x.s.HistoricalVersion(x.ctx, x.admin, x.f.Workspace, c.ModelID, c.ModelID)
	if err != nil || detail.State != "retired" || detail.DatasetLineage == nil || *detail.DatasetLineage != want {
		t.Fatal("retired retained graph", err)
	}
	if _, err = x.s.DecodeDerivedVersionWithin(x.ctx, x.f.Store, x.f.Tenant, x.f.Workspace, c.ModelID); err == nil || err.Error() != "RESOURCE_NOT_FOUND" {
		t.Fatal("retired derivation executable", err)
	}
	var originalCell string
	if err := x.operator.QueryRow(x.ctx, `SELECT cell_id FROM model_preparation_artifacts WHERE id=$1`, c.ArtifactID).Scan(&originalCell); err != nil {
		t.Fatal(err)
	}
	foreignCell := "model-graph-foreign-cell"
	if foreignCell == originalCell {
		foreignCell = "model-graph-other-cell"
	}
	familyOperatorExec(t, x, `ALTER TABLE model_preparation_artifacts DISABLE TRIGGER model_preparation_artifact_immutable`)
	familyOperatorExec(t, x, `UPDATE model_preparation_artifacts SET cell_id=$1 WHERE id=$2`, foreignCell, c.ArtifactID)
	familyOperatorExec(t, x, `ALTER TABLE model_preparation_artifacts ENABLE TRIGGER model_preparation_artifact_immutable`)
	familyDenied(t, x, x.admin, x.f.Workspace, c.ModelID, models.HistoryQuery{}, "MODEL_DERIVED_CATALOG_INVALID")
	if detail, err = x.s.HistoricalVersion(x.ctx, x.admin, x.f.Workspace, c.ModelID, c.ModelID); err == nil || detail.ID != "" || detail.DatasetLineage != nil {
		t.Fatal("foreign retained artifact released lineage", err)
	}
	familyOperatorExec(t, x, `ALTER TABLE model_preparation_artifacts DISABLE TRIGGER model_preparation_artifact_immutable`)
	familyOperatorExec(t, x, `UPDATE model_preparation_artifacts SET cell_id=$1 WHERE id=$2`, originalCell, c.ArtifactID)
	familyOperatorExec(t, x, `ALTER TABLE model_preparation_artifacts ENABLE TRIGGER model_preparation_artifact_immutable`)
	if detail, err = x.s.HistoricalVersion(x.ctx, x.admin, x.f.Workspace, c.ModelID, c.ModelID); err != nil || detail.DatasetLineage == nil || *detail.DatasetLineage != want {
		t.Fatal("local retained artifact recovery", err)
	}
	familyOperatorExec(t, x, `ALTER TABLE model_preparation_artifacts DISABLE TRIGGER model_preparation_artifact_immutable`)
	x.exec(t, `UPDATE model_preparation_artifacts SET manifest_sha256=$1 WHERE id=$2`, "", strings.Repeat("f", 64), c.ArtifactID)
	familyOperatorExec(t, x, `ALTER TABLE model_preparation_artifacts ENABLE TRIGGER model_preparation_artifact_immutable`)
	familyDenied(t, x, x.admin, x.f.Workspace, c.ModelID, models.HistoryQuery{}, "MODEL_DERIVED_CATALOG_INVALID")
	if detail, err = x.s.HistoricalVersion(x.ctx, x.admin, x.f.Workspace, c.ModelID, c.ModelID); err == nil || detail.ID != "" || detail.DatasetLineage != nil {
		t.Fatal("damaged graph leaked detail", err)
	}
}

func TestActualModelFamilyDerivedRegistration(t *testing.T) {
	g := registrationSetupOn(t, familySetup(t))
	fault := errors.New("synthetic reviewed publication rollback")
	if _, err := registrationOwner(t, g, g.x.f.Store, fault); !errors.Is(err, fault) {
		t.Fatal(err)
	}
	if familyCount(t, g.x, "model_families") != 1 || familyCount(t, g.x, "model_family_members") != 1 {
		t.Fatal("rolled-back derivation kept membership")
	}
	if _, err := registrationOwner(t, g, g.x.f.Store, nil); err != nil {
		t.Fatal("real verified derived registration", err)
	}
	if familyCount(t, g.x, "model_families") != 2 || familyID(t, g.x, g.c.ModelID) == familyID(t, g.x, g.x.model) {
		t.Fatal("derived registration inherited base family")
	}
	familyScopedVersion(t, g.x, g.c.ModelID)
}
