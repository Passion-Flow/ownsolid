//go:build repository_integration && fullserve_browser_integration

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Fixed small synthetic workload for T041, not a configurable product limit or
// a substitute for T058's separately retained four-list101-row SQL assertions.
const livePaginationPageSize = 3
const livePaginationMemberEmail = "paging-member@example.invalid"

type livePaginationRow struct {
	ID          string `json:"id"`
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
	Status      string `json:"status"`
	CreatedAt   string `json:"created_at"`
}
type livePaginationFixture struct {
	Rows           []livePaginationRow `json:"rows"`
	SnapshotSHA256 string              `json:"snapshot_sha256"`
	PageSize       int                 `json:"page_size"`
	MemberID       string              `json:"member_id"`
}

func TestActualFullServeHTTPUserPaginationBrowser(t *testing.T) {
	liveActualAccountBrowser(t, true)
}

func livePaginationSnapshot(t *testing.T, x *serverCompositionStory) ([]livePaginationRow, string) {
	t.Helper()
	rows, e := x.f.PostgreSQL.Query(x.ctx, `SELECT id::text,email_display,display_name,status,created_at FROM users WHERE email_canonical LIKE 'paging-list-%' OR email_canonical IN ('paging%literal@example.invalid','pagingxliteral@example.invalid') ORDER BY created_at DESC,id DESC`)
	if e != nil {
		t.Fatal("LIVE_PAGINATION_SEED_READ_FAILED")
	}
	defer rows.Close()
	values := []livePaginationRow{}
	for rows.Next() {
		var row livePaginationRow
		var stamp time.Time
		if e = rows.Scan(&row.ID, &row.Email, &row.DisplayName, &row.Status, &stamp); e != nil {
			t.Fatal("LIVE_PAGINATION_SEED_SCAN_FAILED")
		}
		row.CreatedAt = stamp.UTC().Format(time.RFC3339Nano)
		values = append(values, row)
	}
	if rows.Err() != nil || len(values) != 9 {
		t.Fatal("LIVE_PAGINATION_EXACT_SEED_REQUIRED")
	}
	for _, row := range values {
		if row.CreatedAt != values[0].CreatedAt {
			t.Fatal("LIVE_PAGINATION_REAL_TIED_TIMESTAMPS_REQUIRED")
		}
	}
	raw, e := json.Marshal(values)
	if e != nil {
		t.Fatal("LIVE_PAGINATION_SNAPSHOT_INVALID")
	}
	h := sha256.Sum256(raw)
	return values, hex.EncodeToString(h[:])
}

func liveSeedPagination(t *testing.T, x *serverCompositionStory) *livePaginationFixture {
	t.Helper()
	// Only this fresh disposable schema receives synthetic DML. No hash/credential
	// is emitted into the browser fixture or retained evidence.
	tx, e := x.f.PostgreSQL.Begin(x.ctx)
	if e != nil {
		t.Fatal("LIVE_PAGINATION_SEED_BEGIN_FAILED")
	}
	defer tx.Rollback(x.ctx)
	stamp := time.Now().UTC().Add(-time.Minute).Truncate(time.Microsecond)
	for index := 0; index < 9; index++ {
		address := fmt.Sprintf("paging-list-%02d@example.invalid", index)
		state := "active"
		if index == 5 || index == 6 {
			state = "disabled"
		}
		if index == 7 {
			address = "paging%literal@example.invalid"
		}
		if index == 8 {
			address = "pagingXliteral@example.invalid"
		}
		result, err := tx.Exec(x.ctx, `INSERT INTO users(id,email_canonical,email_display,display_name,locale,status,created_at,updated_at) VALUES($1,lower($2),$2,$3,'en',$4,$5,$5)`, uuid.NewString(), address, "Synthetic pagination "+fmt.Sprint(index), state, stamp)
		if err != nil || result.RowsAffected() != 1 {
			t.Fatal("LIVE_PAGINATION_SEED_USER_FAILED")
		}
	}
	member := uuid.NewString()
	result, e := tx.Exec(x.ctx, `INSERT INTO users(id,email_canonical,email_display,display_name,locale,status) VALUES($1,$2,$2,'Synthetic paging Member','en','active')`, member, livePaginationMemberEmail)
	if e != nil || result.RowsAffected() != 1 {
		t.Fatal("LIVE_PAGINATION_MEMBER_FAILED")
	}
	result, e = tx.Exec(x.ctx, `INSERT INTO password_credentials(user_id,password_phc) SELECT $1,password_phc FROM password_credentials WHERE user_id=$2`, member, x.f.User)
	if e != nil || result.RowsAffected() != 1 {
		t.Fatal("LIVE_PAGINATION_PRIVATE_MEMBER_CREDENTIAL_FAILED")
	}
	result, e = tx.Exec(x.ctx, `INSERT INTO workspace_memberships(tenant_id,workspace_id,user_id,role) VALUES($1,$2,$3,'member')`, x.f.Tenant, x.f.Workspace, member)
	if e != nil || result.RowsAffected() != 1 {
		t.Fatal("LIVE_PAGINATION_MEMBER_ROLE_FAILED")
	}
	if tx.Commit(x.ctx) != nil {
		t.Fatal("LIVE_PAGINATION_SEED_COMMIT_UNCERTAIN")
	}
	rows, digest := livePaginationSnapshot(t, x)
	return &livePaginationFixture{Rows: rows, SnapshotSHA256: digest, PageSize: livePaginationPageSize, MemberID: member}
}

func liveReadbackPagination(t *testing.T, x *serverCompositionStory, input liveAccountBrowserInput, before *livePaginationFixture) {
	t.Helper()
	rows, digest := livePaginationSnapshot(t, x)
	if before == nil || !reflect.DeepEqual(rows, before.Rows) || digest != before.SnapshotSHA256 {
		t.Fatal("LIVE_PAGINATION_SEED_CHANGED")
	}
	var role string
	var admin bool
	var sessions, users int
	if e := x.f.PostgreSQL.QueryRow(x.ctx, `SELECT m.role,u.is_platform_admin FROM users u JOIN workspace_memberships m ON m.user_id=u.id WHERE u.id=$1 AND m.workspace_id=$2`, before.MemberID, x.f.Workspace).Scan(&role, &admin); e != nil || role != "member" || admin {
		t.Fatal("LIVE_PAGINATION_REAL_MEMBER_REQUIRED")
	}
	if e := x.f.PostgreSQL.QueryRow(x.ctx, `SELECT count(*) FROM sessions WHERE user_id=$1 AND audience='portal' AND revoked_at IS NULL`, before.MemberID).Scan(&sessions); e != nil || sessions != 1 {
		t.Fatal("LIVE_PAGINATION_MEMBER_LOGIN_UNPROVEN")
	}
	if e := x.f.PostgreSQL.QueryRow(x.ctx, `SELECT count(*) FROM users`).Scan(&users); e != nil || users != 11 {
		t.Fatal("LIVE_PAGINATION_TOTAL_USERS_INVALID")
	}
	ids, listed, active, literal := []string{}, []string{}, []string{}, []string{}
	for _, row := range rows {
		ids = append(ids, row.ID)
		if strings.HasPrefix(row.Email, "paging-list-") {
			listed = append(listed, row.ID)
			if row.Status == "active" {
				active = append(active, row.ID)
			}
		}
		if strings.HasPrefix(row.Email, "paging%") {
			literal = append(literal, row.ID)
		}
	}
	raw, e := json.Marshal(map[string]any{"format": "projectone.fullserve.user-pagination-sql.v1", "status": "passed_exact_synthetic_rows_unchanged_and_current_member_only", "seed_snapshot_sha256": before.SnapshotSHA256, "final_snapshot_sha256": digest, "seeded_ids": ids, "list_ids": listed, "active_ids": active, "literal_ids": literal, "member_id": before.MemberID, "seeded_rows": 9, "tied_timestamp_rows": 9, "total_users": users, "page_size": livePaginationPageSize, "member_role": role, "member_is_admin": admin, "member_portal_sessions": sessions})
	if e != nil {
		t.Fatal("LIVE_PAGINATION_READBACK_ENCODING_FAILED")
	}
	liveWriteBrowserEvidence(t, filepath.Join(input.ResultDirectory, "pagination-sql.json"), raw)
	t.Log("LIVE_PAGINATION_SQL_ASSERTIONS exact_rows=9 tied_timestamps=9 unchanged_after_browser=true actual_member_session=true same_database_restart=true")
}
