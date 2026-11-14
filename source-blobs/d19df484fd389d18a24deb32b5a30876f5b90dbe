//go:build repository_integration

package models_test

import (
	"context"
	"crypto/rand"
	_ "embed"
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
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

//go:embed testdata/model_family_candidate.up.sql
var familyCandidateUp string

//go:embed testdata/model_family_candidate.down.sql
var familyCandidateDown string

func familyInstall(t *testing.T, x modelReadFixture) {
	t.Helper()
	if x.f.Store.Dialect() != transaction.PostgreSQL {
		t.Fatal("Explicit PostgreSQL candidate required")
	}
	if err := x.f.Store.Within(x.ctx, false, func(ctx context.Context, q transaction.Executor) error {
		_, err := q.Exec(ctx, transaction.SQL(transaction.PostgreSQL, familyCandidateUp))
		return err
	}); err != nil {
		t.Fatal("candidate DDL", err)
	}
}

func familySetup(t *testing.T) modelReadFixture {
	t.Helper()
	x := modelReadSetup(t)
	x.owner.HMACKey = make([]byte, 32)
	if _, err := rand.Read(x.owner.HMACKey); err != nil {
		t.Fatal(err)
	}
	familyInstall(t, x)
	return x
}

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

func TestActualModelFamilyCandidateBackfillAndLifecycle(t *testing.T) {
	x := modelReadSetup(t)
	old := uuid.NewString()
	if err := familyInsert(x.ctx, x.f.Store, x, old, "deleted", time.Now().UTC().Truncate(time.Microsecond)); err != nil {
		t.Fatal(err)
	}
	// The retained deployment keeps its exact version reference across both DDL directions.
	dep := retirementDeployment(t, x, x.f.Store, retirementNode(t, x), x.model, true)
	fault := errors.New("explicit candidate rollback")
	if err := x.f.Store.Within(x.ctx, false, func(ctx context.Context, q transaction.Executor) error {
		if _, err := q.Exec(ctx, transaction.SQL(transaction.PostgreSQL, familyCandidateUp)); err != nil {
			return err
		}
		return fault
	}); !errors.Is(err, fault) {
		t.Fatal("DDL rollback", err)
	}
	familyDenied(t, x, x.admin, x.f.Workspace, x.model, models.HistoryQuery{}, "MODEL_HISTORY_UNAVAILABLE")
	familyInstall(t, x)
	first, second := familyID(t, x, x.model), familyID(t, x, old)
	if first == second || first == x.model || second == old || familyCount(t, x, "model_families") != 2 {
		t.Fatal("legacy IDs inferred a group")
	}
	x.s.Referenced = func(context.Context, transaction.Executor, string, string, string) (bool, error) { return false, nil }
	retirementRetire(t, x, x.model)
	newID := uuid.NewString()
	if err := familyInsert(x.ctx, x.f.Store, x, newID, "ready", time.Now().UTC().Truncate(time.Microsecond)); err != nil {
		t.Fatal(err)
	}
	if familyID(t, x, newID) == first {
		t.Fatal("same-digest reimport inferred family")
	}
	if err := x.f.Store.Within(x.ctx, false, func(ctx context.Context, q transaction.Executor) error {
		return x.s.ReadyWithin(ctx, q, x.f.Tenant, x.f.Workspace, x.model)
	}); err == nil || err.Error() != "RESOURCE_NOT_FOUND" {
		t.Fatal("retired exact execution reference admitted", err)
	}
	if err := x.f.Store.Within(x.ctx, false, func(ctx context.Context, q transaction.Executor) error {
		_, err := q.Exec(ctx, transaction.SQL(transaction.PostgreSQL, familyCandidateDown))
		return err
	}); err != nil {
		t.Fatal("candidate rollback DDL", err)
	}
	var retained string
	if err := x.f.Store.QueryRow(x.ctx, transaction.SQL(transaction.PostgreSQL, `SELECT model_id::text FROM deployments WHERE id=$1`, dep)).Scan(&retained); err != nil || retained != x.model {
		t.Fatal("deployment reference changed", err)
	}
	if value, err := x.s.Get(x.ctx, x.admin, x.f.Workspace, newID); err != nil || value.ID != newID {
		t.Fatal("legacy read after DDL removal", err)
	}
}

func TestActualModelFamilyHistoryPagesAndAuthority(t *testing.T) {
	for _, name := range []string{"pages_filters_and_exact_ids", "invalid_inputs", "revoked_session", "disabled_user", "demoted_admin", "disabled_workspace", "foreign_scope", "unknown_outcome", "damaged_page"} {
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
	for _, name := range []string{"rollback", "orphan", "audit_rollback", "immutable", "matching_concurrency", "conflicting_concurrency"} {
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
			case "orphan":
				err := x.f.Store.Within(x.ctx, false, func(ctx context.Context, q transaction.Executor) error {
					_, err := x.s.StageVersionFamilyWithin(ctx, q, x.admin, x.f.Workspace, id, "", "Synthetic orphan", uuid.NewString())
					return err
				})
				if err == nil || familyCount(t, x, "model_families") != 1 || familyCount(t, x, "model_family_members") != 1 || x.audits(t) != before {
					t.Fatal("orphan membership committed", err)
				}
			case "audit_rollback":
				x.exec(t, `CREATE FUNCTION reject_family_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='model.version_family_selected' THEN RAISE EXCEPTION 'SYNTHETIC_AUDIT_FAILURE'; END IF; RETURN NEW; END $$; CREATE TRIGGER reject_family_audit BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION reject_family_audit()`, "")
				family, err := familyPublish(x, id, "", nil)
				if err == nil || family != "" || familyCount(t, x, "model_families") != 1 || familyCount(t, x, "model_family_members") != 1 {
					t.Fatal("audit failure left membership", err)
				}
			case "immutable":
				for _, sql := range []string{`UPDATE model_family_members SET family_id=gen_random_uuid()`, `DELETE FROM model_family_members`, `UPDATE model_families SET initial_version_id=gen_random_uuid()`, `DELETE FROM model_families`, `TRUNCATE model_families CASCADE`} {
					if _, err := x.f.Store.Exec(x.ctx, transaction.SQL(transaction.PostgreSQL, sql)); err == nil {
						t.Fatal("mutable family identity", sql)
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
				observer, err := pgx.ConnectConfig(x.ctx, x.f.PostgreSQL.Config().ConnConfig.Copy())
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
				if familyID(t, x, id) != familyID(t, x, x.model) || x.audits(t) != before+1 {
					t.Fatal("concurrent membership/audit duplicated")
				}
			}
		})
	}
}

func TestActualModelFamilyRetainedDerivedGraph(t *testing.T) {
	x := familySetup(t)
	c, want := seedModelHTTPLineage(t, x, nil)
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
	x.exec(t, `ALTER TABLE model_preparation_artifacts DISABLE TRIGGER model_preparation_artifact_immutable`, "")
	x.exec(t, `UPDATE model_preparation_artifacts SET manifest_sha256=$1 WHERE id=$2`, "", strings.Repeat("f", 64), c.ArtifactID)
	x.exec(t, `ALTER TABLE model_preparation_artifacts ENABLE TRIGGER model_preparation_artifact_immutable`, "")
	familyDenied(t, x, x.admin, x.f.Workspace, c.ModelID, models.HistoryQuery{}, "MODEL_DERIVED_CATALOG_INVALID")
	if detail, err = x.s.HistoricalVersion(x.ctx, x.admin, x.f.Workspace, c.ModelID, c.ModelID); err == nil || detail.ID != "" || detail.DatasetLineage != nil {
		t.Fatal("damaged graph leaked detail", err)
	}
}

func TestActualModelFamilyDerivedRegistration(t *testing.T) {
	g := registrationSetup(t)
	familyInstall(t, g.x)
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
}
