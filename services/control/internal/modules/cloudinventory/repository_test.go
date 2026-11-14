package cloudinventory

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

type awsRecordingExecutor struct {
	transaction.Executor
	dialect    transaction.Dialect
	statements []transaction.Statement
	affected   int64
	fail       error
}

func (q *awsRecordingExecutor) Dialect() transaction.Dialect { return q.dialect }
func (q *awsRecordingExecutor) Exec(_ context.Context, s transaction.Statement) (int64, error) {
	q.statements = append(q.statements, s)
	return q.affected, q.fail
}

func TestAWSNativeUpsertPreservesIdentityAndDirectObservation(t *testing.T) {
	observed := time.Date(2026, 11, 3, 0, 0, 0, 123456000, time.UTC)
	raw := "{\"instance_id\":\"i-0123456789abcdef0\",\"tags\":{\"unicode\":\"\u539f\u59cb\u89c2\u5bdf\"}}"
	instance := Instance{InstanceID: "i-0123456789abcdef0"}
	for _, n := range []int64{0, 1} {
		q := &awsRecordingExecutor{dialect: transaction.Dameng, affected: n}
		if e := upsertResource(context.Background(), q, "new-id", "tenant", "workspace", "123456789012", "us-east-1", instance, raw, observed); e != nil {
			t.Fatal(e)
		}
		expected := []transaction.Statement{transaction.SQL(transaction.Dameng, awsQueryText(transaction.Dameng, awsUpdateResource), raw, observed, "tenant", "workspace", "123456789012", "us-east-1", instance.InstanceID)}
		if n == 0 {
			expected = append(expected, transaction.SQL(transaction.Dameng, awsQueryText(transaction.Dameng, awsInsertResource), "new-id", "tenant", "workspace", "123456789012", "us-east-1", instance.InstanceID, raw, observed))
		}
		if !reflect.DeepEqual(q.statements, expected) {
			t.Fatal("native upsert changed original scope, CLOB bytes, timestamp, or ID semantics")
		}
	}
}
func TestAWSNativeUpsertDoesNotInsertAfterFailedOrAmbiguousUpdate(t *testing.T) {
	stop := errors.New("synthetic native update failure")
	for _, tc := range []struct {
		n int64
		e error
	}{{0, stop}, {2, nil}, {-1, nil}} {
		q := &awsRecordingExecutor{dialect: transaction.Dameng, affected: tc.n, fail: tc.e}
		e := upsertResource(context.Background(), q, "id", "tenant", "workspace", "123456789012", "us-east-1", Instance{InstanceID: "i-0123456789abcdef0"}, `{}`, time.Now().UTC())
		if e == nil || len(q.statements) != 1 {
			t.Fatal("failed/invalid native update triggered insert or success", e)
		}
		if tc.e != nil && !errors.Is(e, tc.e) {
			t.Fatal("native update failure replaced", e)
		}
	}
	q := &awsRecordingExecutor{dialect: "future"}
	wantCode(t, upsertResource(context.Background(), q, "id", "tenant", "workspace", "account", "region", Instance{}, `{}`, time.Now()), "AWS_BUSINESS_DATABASE_UNSUPPORTED")
	if len(q.statements) != 0 {
		t.Fatal("unknown dialect executed SQL")
	}
}
func TestAWSPostgresAndMySQLKeepSingleNativeUpsert(t *testing.T) {
	observed := time.Now().UTC().Truncate(time.Microsecond)
	for _, d := range []transaction.Dialect{transaction.PostgreSQL, transaction.MySQL} {
		q := &awsRecordingExecutor{dialect: d}
		if e := upsertResource(context.Background(), q, "id", "tenant", "workspace", "account", "region", Instance{InstanceID: "instance"}, `{}`, observed); e != nil {
			t.Fatal(e)
		}
		expected := []transaction.Statement{transaction.SQL(d, awsQueryText(d, awsUpsert), "id", "tenant", "workspace", "account", "region", "instance", `{}`, observed)}
		if !reflect.DeepEqual(q.statements, expected) {
			t.Fatal("selected upsert lost original arguments")
		}
	}
}
func TestAWSExplicitQueriesHaveClosedVendorDispatch(t *testing.T) {
	// Parameter counts are repository contracts. This catches a forgotten native
	// branch or shifted scope argument when a new query is added to the owner.
	counts := map[awsQuery]int{awsReceipt: 3, awsNewerReceipts: 5, awsReceiptCount: 2, awsMarkMissing: 4, awsUpsert: 8, awsResourceCount: 2, awsInsertReceipt: 10, awsAuditImport: 7, awsListResources: 4, awsUpdateResource: 7, awsInsertResource: 8}
	for query, count := range counts {
		for _, d := range []transaction.Dialect{transaction.PostgreSQL, transaction.MySQL, transaction.Dameng} {
			nativeOnly := query == awsUpdateResource || query == awsInsertResource
			unsupported := nativeOnly && d != transaction.Dameng || query == awsUpsert && d == transaction.Dameng
			text := awsQueryText(d, query)
			if unsupported {
				if text != "" {
					t.Fatal("unsupported query fell back to another vendor")
				}
				continue
			}
			if text == "" {
				t.Fatal("missing selected query", query, d)
			}
			if d == transaction.PostgreSQL {
				if strings.Count(text, "$") != count {
					t.Fatal("PostgreSQL argument contract", query)
				}
			} else if strings.Count(text, "?") != count {
				t.Fatal("selected argument contract", query, d)
			}
			if d == transaction.Dameng && (strings.Contains(text, "::") || strings.Contains(text, "ON DUPLICATE") || strings.Contains(text, "EXCLUDED") || strings.Contains(text, "clock_timestamp") || strings.Contains(text, "TO_CHAR")) {
				t.Fatal("native SQL used a foreign dialect or CLOB conversion", query)
			}
		}
		if awsQueryText("future", query) != "" {
			t.Fatal("unknown vendor fell back")
		}
	}
	if awsQueryText(transaction.Dameng, 255) != "" {
		t.Fatal("unknown query fell back")
	}
	if !strings.Contains(awsQueryText(transaction.Dameng, awsListResources), "CASE WHEN MISSING=1 THEN 1 ELSE 0 END") || !strings.HasSuffix(awsQueryText(transaction.Dameng, awsListResources), "LIMIT ?") {
		t.Fatal("native numeric BIT/bounded page contract")
	}
	if !strings.Contains(awsQueryText(transaction.Dameng, awsAuditImport), "SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6))") {
		t.Fatal("native UTC microsecond audit contract")
	}
}

type awsInsertFailureExecutor struct {
	awsRecordingExecutor
	insertFailure error
}

func (q *awsInsertFailureExecutor) Exec(ctx context.Context, s transaction.Statement) (int64, error) {
	q.statements = append(q.statements, s)
	if len(q.statements) == 2 {
		return 0, q.insertFailure
	}
	return 0, nil
}
func TestAWSNativeInsertFailureRetainsOriginalCommitClassification(t *testing.T) {
	stop := &transaction.Error{Kind: transaction.CommitUnknown}
	q := &awsInsertFailureExecutor{awsRecordingExecutor: awsRecordingExecutor{dialect: transaction.Dameng}, insertFailure: stop}
	e := upsertResource(context.Background(), q, "id", "tenant", "workspace", "account", "region", Instance{InstanceID: "instance"}, `{}`, time.Now().UTC())
	if !errors.Is(e, stop) || len(q.statements) != 2 {
		t.Fatal("insert failure swallowed or replayed", e)
	}
}

// assertActualAWSRepositorySQL is called only by explicitly tagged actual vendor
// tests. The caller owns an empty disposable schema/catalog and real principals.
// No provider mock or SQL fake is substituted in this verification.
func assertActualAWSRepositorySQL(t *testing.T, store transaction.Store, tenant, workspace, actor string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	w := workspaces.Workspace{TenantID: tenant, ID: workspace}
	started := time.Now().UTC().Truncate(time.Microsecond)
	key, resourceID := uuid.NewString(), uuid.NewString()
	instance := Instance{InstanceID: "i-0123456789abcdef0", InstanceType: "m5.large", State: "running", AvailabilityZone: "us-east-1a"}
	raw, e := json.Marshal(map[string]string{"instance_id": instance.InstanceID, "instance_type": instance.InstanceType, "state": instance.State, "availability_zone": instance.AvailabilityZone, "synthetic_unicode": "\u4e16\u754c"})
	if e != nil {
		t.Fatal(e)
	}
	receipt := func(q transaction.Executor, k string) error {
		if _, e := q.Exec(ctx, awsSQL(q, awsInsertReceipt, tenant, workspace, k, actor, "synthetic", "123456789012", "us-east-1", started, started.Add(time.Microsecond), 1)); e != nil {
			return e
		}
		_, e := q.Exec(ctx, awsSQL(q, awsAuditImport, uuid.NewString(), actor, tenant, workspace, "admin", k, uuid.NewString()))
		return e
	}
	scan := func(q transaction.Executor, wantMissing bool) {
		t.Helper()
		rows, e := q.Query(ctx, awsSQL(q, awsListResources, tenant, workspace, uuid.Nil.String(), 2))
		if e != nil {
			t.Fatal(e)
		}
		defer rows.Close()
		if !rows.Next() {
			t.Fatal("actual scoped resource missing", rows.Err())
		}
		var id, account, region string
		var b []byte
		var at time.Time
		var missing bool
		if e = rows.Scan(&id, &account, &region, &b, &at, &missing); e != nil {
			t.Fatal("actual CLOB/BIT/time projection", e)
		}
		var value Instance
		var document map[string]string
		if json.Unmarshal(b, &value) != nil || json.Unmarshal(b, &document) != nil || value.InstanceID != instance.InstanceID || document["synthetic_unicode"] != "\u4e16\u754c" || id != resourceID || account != "123456789012" || region != "us-east-1" || !at.Equal(started) || missing != wantMissing || rows.Next() || rows.Err() != nil {
			t.Fatal("actual scoped CLOB/BIT/page/ID contract")
		}
	}
	e = store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
		lock := awsSQL(q, awsResourceCount, tenant, workspace)
		switch q.Dialect() {
		case transaction.PostgreSQL:
			lock = transaction.SQL(transaction.PostgreSQL, `SELECT id::text FROM workspaces WHERE tenant_id=$1 AND id=$2 FOR UPDATE`, tenant, workspace)
		case transaction.MySQL:
			lock = transaction.SQL(transaction.MySQL, `SELECT id FROM workspaces WHERE tenant_id=? AND id=? FOR UPDATE`, tenant, workspace)
		case transaction.Dameng:
			lock = transaction.SQL(transaction.Dameng, `SELECT ID FROM WORKSPACES WHERE TENANT_ID=? AND ID=? FOR UPDATE`, tenant, workspace)
		default:
			return fail("AWS_BUSINESS_DATABASE_UNSUPPORTED")
		}
		var locked string
		if e := q.QueryRow(ctx, lock).Scan(&locked); e != nil || locked != workspace {
			t.Fatal("actual Workspace fence", e)
		}
		var count int
		for _, query := range []awsQuery{awsResourceCount, awsReceiptCount} {
			if e := q.QueryRow(ctx, awsSQL(q, query, tenant, workspace)).Scan(&count); e != nil || count != 0 {
				t.Fatal("empty actual AWS scope required", e)
			}
		}
		if _, e := readReceipt(ctx, q, w, actor, key); !errors.Is(e, sql.ErrNoRows) {
			t.Fatal("actual missing receipt", e)
		}
		if _, e := q.Exec(ctx, awsSQL(q, awsMarkMissing, tenant, workspace, "123456789012", "us-east-1")); e != nil {
			return e
		}
		if e := upsertResource(ctx, q, resourceID, tenant, workspace, "123456789012", "us-east-1", instance, string(raw), started); e != nil {
			return e
		}
		scan(q, false)
		if _, e := q.Exec(ctx, awsSQL(q, awsMarkMissing, tenant, workspace, "123456789012", "us-east-1")); e != nil {
			return e
		}
		scan(q, true)
		if e := upsertResource(ctx, q, uuid.NewString(), tenant, workspace, "123456789012", "us-east-1", instance, string(raw), started); e != nil {
			return e
		}
		scan(q, false)
		if e := receipt(q, key); e != nil {
			return e
		}
		got, e := readReceipt(ctx, q, w, actor, key)
		if e != nil || got.Key != key || got.Imported != 1 || !got.StartedAt.Equal(started) {
			t.Fatal("actual receipt roundtrip", e)
		}
		if _, e := readReceipt(ctx, q, w, uuid.NewString(), key); e == nil || e.Error() != "IDEMPOTENCY_CONFLICT" {
			t.Fatal("actual actor-bound receipt", e)
		}
		if e = q.QueryRow(ctx, awsSQL(q, awsNewerReceipts, tenant, workspace, "123456789012", "us-east-1", started)).Scan(&count); e != nil || count != 1 {
			t.Fatal("actual supersession timestamp contract", e)
		}
		return nil
	})
	if e != nil {
		t.Fatal("actual SQL import transaction", e)
	}
	// A late callback failure must roll back resource, receipt and audit together.
	rollbackKey := uuid.NewString()
	stop := errors.New("synthetic late callback refusal")
	e = store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
		if _, e := q.Exec(ctx, awsSQL(q, awsMarkMissing, tenant, workspace, "123456789012", "us-east-1")); e != nil {
			return e
		}
		if e := receipt(q, rollbackKey); e != nil {
			return e
		}
		return stop
	})
	if !errors.Is(e, stop) {
		t.Fatal("actual rollback outcome", e)
	}
	if _, e = readReceipt(ctx, store, w, actor, rollbackKey); !errors.Is(e, sql.ErrNoRows) {
		t.Fatal("rolled back receipt visible", e)
	}
	scan(store, false)
	var audits int
	audit := map[transaction.Dialect]string{transaction.PostgreSQL: `SELECT count(*) FROM audit_events WHERE tenant_id=$1 AND workspace_id=$2 AND action='aws.inventory_imported'`, transaction.MySQL: `SELECT count(*) FROM audit_events WHERE tenant_id=? AND workspace_id=? AND action='aws.inventory_imported'`, transaction.Dameng: `SELECT COUNT(*) FROM AUDIT_EVENTS WHERE TENANT_ID=? AND WORKSPACE_ID=? AND ACTION='aws.inventory_imported'`}
	if e = store.QueryRow(ctx, transaction.SQL(store.Dialect(), audit[store.Dialect()], tenant, workspace)).Scan(&audits); e != nil || audits != 1 {
		t.Fatal("actual audit rollback", e, audits)
	}
	// Receipt immutability is the actual installed guard, never an allowing fake.
	immutable := map[transaction.Dialect]string{transaction.PostgreSQL: `UPDATE aws_import_receipts SET imported_count=99 WHERE tenant_id=$1 AND workspace_id=$2 AND idempotency_key=$3`, transaction.MySQL: `UPDATE aws_import_receipts SET imported_count=99 WHERE tenant_id=? AND workspace_id=? AND idempotency_key=?`, transaction.Dameng: `UPDATE AWS_IMPORT_RECEIPTS SET IMPORTED_COUNT=99 WHERE TENANT_ID=? AND WORKSPACE_ID=? AND IDEMPOTENCY_KEY=?`}
	if _, e = store.Exec(ctx, transaction.SQL(store.Dialect(), immutable[store.Dialect()], tenant, workspace, key)); e == nil {
		t.Fatal("actual immutable receipt guard absent")
	}
	got, e := readReceipt(ctx, store, w, actor, key)
	if e != nil || got.Imported != 1 {
		t.Fatal("immutable receipt changed", e)
	}
}
