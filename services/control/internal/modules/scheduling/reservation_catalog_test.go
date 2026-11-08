package scheduling

import (
	"errors"
	"os"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestPostgresReservationCatalogRoundTrip seeds the real lineage's catalog
// plus the candidate tables and checks the same rows after pg_dump/pg_restore.
func TestPostgresReservationCatalogRoundTrip(t *testing.T) {
	cfg := schedulingTestConfig(t, "OWNSOLID_SCHEDULING_CATALOG_DSN_FILE")
	phase := os.Getenv("OWNSOLID_SCHEDULING_CATALOG_PHASE")
	fresh := cfg.ConnConfig.Database == "lane_sch_catalog_fresh_test"
	restored := cfg.ConnConfig.Database == "lane_sch_catalog_restored_test"
	if !(phase == "seed" && fresh || phase == "verify" && (fresh || restored)) {
		t.Fatal("catalog round trip requires its exact disposable database and seed or verify phase")
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = "public"
	pool, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	store, err := transaction.FromPostgreSQL(pool)
	if err != nil {
		t.Fatal(err)
	}

	a := Attempt{
		TenantID:    "0527bb53-d55b-4def-ade1-8239299b8311",
		WorkspaceID: "35eddb50-24da-4ab5-b291-5dd63b59df48",
		ID:          "e91b8b03-23cf-4dd6-81a4-5d687ba28b5d",
		Number:      1,
		Kind:        Training,
		SourceID:    "6a4f975a-ff59-4c4b-8df0-3f959560f67e",
	}
	finished := a
	finished.ID = "c97b1d65-73e9-4d75-b724-f06059695e0f"
	finished.SourceID = "30bb4c5f-d6d6-40f7-94dc-92b3b2ca07c1"
	if phase == "seed" {
		if err := pgx.BeginFunc(t.Context(), pool, func(tx pgx.Tx) error {
			if _, err := tx.Exec(t.Context(), "INSERT INTO tenants(id,name,status) VALUES ($1,'Scheduling catalog fixture','active')", a.TenantID); err != nil {
				return err
			}
			if _, err := tx.Exec(t.Context(), "INSERT INTO workspaces(id,tenant_id,name,status) VALUES ($1,$2,'Scheduling catalog fixture','active')", a.WorkspaceID, a.TenantID); err != nil {
				return err
			}
			limit := Resources{CPU: 2000, NVIDIAGPU: 4}
			policy := []QuotaNode{
				{ID: "platform", Level: PlatformQuota, Limits: Quota{limit, limit}},
				{ID: a.TenantID, ParentID: "platform", TenantID: a.TenantID, Level: TenantQuota, Limits: Quota{limit, limit}},
				{ID: "department", ParentID: a.TenantID, TenantID: a.TenantID, Level: DepartmentQuota, Limits: Quota{limit, limit}},
				{ID: a.WorkspaceID, ParentID: "department", TenantID: a.TenantID, Level: WorkspaceQuota, Limits: Quota{limit, limit}},
			}
			_, _, err := ReplaceQuotaWithin(t.Context(), tx, 0, policy)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		for _, attempt := range []Attempt{a, finished} {
			if changed, err := reserveInTransaction(t.Context(), store, attempt, reservationIntent(attempt, 2)); err != nil || !changed {
				t.Fatalf("seed hold: %v %v", changed, err)
			}
		}
		if changed, err := releaseInTransaction(t.Context(), store, finished); err != nil || !changed {
			t.Fatalf("seed release: %v %v", changed, err)
		}
	}
	if changed, err := reserveInTransaction(t.Context(), store, a, reservationIntent(a, 2)); err != nil || changed {
		t.Fatalf("persisted hold: %v %v", changed, err)
	}
	if _, err := reserveInTransaction(t.Context(), store, finished, reservationIntent(finished, 2)); !errors.Is(err, ErrReservationReleased) {
		t.Fatalf("persisted released attempt: %v", err)
	}
	next := finished
	next.ID, next.Number = "0010be5c-dd3c-47f5-9602-f96052fd4da4", 2
	if _, err := reserveInTransaction(t.Context(), store, next, reservationIntent(next, 3)); !errors.Is(err, ErrReservationQuota) {
		t.Fatalf("persisted usage was not charged: %v", err)
	}
	var held, released int
	var defaultsOK bool
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FILTER (WHERE released_at IS NULL),
 count(*) FILTER (WHERE released_at IS NOT NULL),bool_and(site_id='default' AND cell_id='default')
 FROM scheduling_reservations`).Scan(&held, &released, &defaultsOK); err != nil || held != 1 || released != 1 || !defaultsOK {
		t.Fatalf("catalog reservation state: held=%d released=%d scoped=%v error=%v", held, released, defaultsOK, err)
	}
}
