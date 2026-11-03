package monitoring

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

type monitoringRepositoryProbe struct {
	transaction.Executor
	dialect   transaction.Dialect
	stamp     time.Time
	err       error
	count     int64
	calls     int
	statement transaction.Statement
}

func (p *monitoringRepositoryProbe) Dialect() transaction.Dialect { return p.dialect }
func (p *monitoringRepositoryProbe) Exec(_ context.Context, q transaction.Statement) (int64, error) {
	p.calls++
	p.statement = q
	return p.count, p.err
}
func (p *monitoringRepositoryProbe) QueryRow(_ context.Context, q transaction.Statement) transaction.Row {
	p.calls++
	p.statement = q
	return monitoringClockRow{p}
}

type monitoringClockRow struct{ p *monitoringRepositoryProbe }

func (r monitoringClockRow) Scan(dest ...any) error {
	if r.p.err != nil {
		return r.p.err
	}
	*dest[0].(*time.Time) = r.p.stamp
	return nil
}

type monitoringRepositoryPool struct{ *monitoringRepositoryProbe }

func (p *monitoringRepositoryPool) Within(context.Context, bool, func(context.Context, transaction.Executor) error) error {
	panic("repository helper must not open a transaction")
}

func TestMonitoringNativeInstallationAndClockSQL(t *testing.T) {
	q := &monitoringRepositoryProbe{dialect: transaction.Dameng}
	if !reflect.DeepEqual(installationStatement(q), transaction.SQL(transaction.Dameng, `SELECT T."ID" FROM "INSTALLATION_STATE" I JOIN "TENANTS" T ON T."ID"=I."TENANT_ID" WHERE I."ID"=1 AND I."BOOTSTRAP_COMPLETE"=1 AND T."STATUS"='active' FOR UPDATE OF I."ID",T."ID"`)) {
		t.Fatal("installation must lock exact bootstrap/active-tenant facts")
	}
	if !reflect.DeepEqual(observationClockStatement(q), transaction.SQL(transaction.Dameng, `SELECT SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)) FROM DUAL`)) {
		t.Fatal("clock must be server UTC with microsecond precision")
	}
	stamp := time.Date(2026, 11, 3, 1, 2, 3, 123456000, time.UTC)
	q.stamp = stamp
	got, e := observationTimeWithin(t.Context(), q)
	if e != nil || got != stamp || q.calls != 1 {
		t.Fatal("exact clock not retained", e)
	}
	for _, bad := range []time.Time{time.Time{}, stamp.Add(time.Nanosecond), time.Date(1969, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)} {
		q.stamp = bad
		if got, e := observationTimeWithin(t.Context(), q); e == nil || !got.IsZero() {
			t.Fatal("invalid clock produced a value")
		}
	}
	q.err = &transaction.Error{Kind: transaction.Unavailable}
	if got, e := observationTimeWithin(t.Context(), q); e != q.err || !got.IsZero() {
		t.Fatal("failed clock produced a value")
	}
	before := q.calls
	if _, e := observationTimeWithin(t.Context(), &monitoringRepositoryPool{q}); e == nil || q.calls != before {
		t.Fatal("pool admitted")
	}
}

func TestMonitoringNativeNodeKeysetBindingsAndPolicy(t *testing.T) {
	q := &monitoringRepositoryProbe{dialect: transaction.Dameng}
	tenant, workspace, id := uuid.NewString(), uuid.NewString(), uuid.NewString()
	stamp := time.Date(2026, 11, 3, 1, 2, 3, 123456000, time.UTC)
	policy := Defaults()
	policy.NodePageSize = 100
	first := `SELECT "ID","CREATED_AT" FROM "MANAGED_NODES" WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=?`
	order := ` ORDER BY "CREATED_AT" DESC,"ID" DESC LIMIT 101`
	if !reflect.DeepEqual(nodePageStatement(q, tenant, workspace, policy, time.Time{}, ""), transaction.SQL(transaction.Dameng, first+order, tenant, workspace)) {
		t.Fatal("first page scope or bound changed")
	}
	if !reflect.DeepEqual(nodePageStatement(q, tenant, workspace, policy, stamp, id), transaction.SQL(transaction.Dameng, first+` AND ("CREATED_AT"<? OR ("CREATED_AT"=? AND "ID"<?))`+order, tenant, workspace, stamp, stamp, id)) {
		t.Fatal("tied timestamp keyset or duplicate binding order changed")
	}
	invalid := transaction.SQL(transaction.Dameng, "")
	for _, bad := range []time.Time{time.Time{}, stamp.Add(time.Nanosecond), stamp.In(time.FixedZone("offset", 3600)), stamp.In(time.FixedZone("zero", 0)), time.Date(1969, 1, 1, 0, 0, 0, 0, time.UTC)} {
		if !reflect.DeepEqual(nodePageStatement(q, tenant, workspace, policy, bad, id), invalid) {
			t.Fatal("unrepresentable keyset accepted")
		}
	}
	for _, size := range []int{-1, 0, 101, int(^uint(0) >> 1)} {
		policy.NodePageSize = size
		if !reflect.DeepEqual(nodePageStatement(q, tenant, workspace, policy, stamp, id), invalid) {
			t.Fatal("native limit not bounded by owner policy")
		}
	}
	policy = Defaults()
	for _, args := range [][3]string{{"tenant' OR 1=1 --", workspace, id}, {tenant, "", id}, {tenant, workspace, "bad"}} {
		if !reflect.DeepEqual(nodePageStatement(q, args[0], args[1], policy, stamp, args[2]), invalid) {
			t.Fatal("noncanonical scope/cursor admitted")
		}
	}
}

func TestMonitoringNativeAuditBindsOneRowAndNeverRetries(t *testing.T) {
	q := &monitoringRepositoryProbe{dialect: transaction.Dameng, count: 1}
	audit, actor, workspace, request, tenant := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	want := transaction.SQL(transaction.Dameng, `INSERT INTO "AUDIT_EVENTS"("ID","OCCURRED_AT","ACTOR_USER_ID","AUDIENCE","ACTION","OUTCOME","TARGET_TYPE","TARGET_ID","REQUEST_ID","TENANT_ID","WORKSPACE_ID") VALUES(?,SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)),?,'admin','monitoring.snapshot','success','workspace',?,?,?,?)`, audit, actor, workspace, request, tenant, workspace)
	if e := insertSnapshotAuditWithin(t.Context(), q, audit, actor, workspace, request, tenant); e != nil || !reflect.DeepEqual(q.statement, want) {
		t.Fatal("audit binding/order changed", e)
	}
	if strings.Contains(q.statement.String(), actor) {
		t.Fatal("statement leaked actor")
	}
	for _, count := range []int64{-1, 0, 2} {
		q.count = count
		if e := insertSnapshotAuditWithin(t.Context(), q, audit, actor, workspace, request, tenant); e == nil {
			t.Fatal("non-single audit accepted")
		}
	}
	for _, original := range []error{&transaction.Error{Kind: transaction.CommitUnknown}, &transaction.Error{Kind: transaction.Deadlock}, context.Canceled, errors.New("private driver details")} {
		q.err = original
		before := q.calls
		if e := insertSnapshotAuditWithin(t.Context(), q, audit, actor, workspace, request, tenant); e != original || q.calls != before+1 {
			t.Fatal("audit error changed or operation retried")
		}
	}
	before := q.calls
	if e := insertSnapshotAuditWithin(t.Context(), &monitoringRepositoryPool{q}, audit, actor, workspace, request, tenant); e == nil || q.calls != before {
		t.Fatal("pool admitted as transaction")
	}
	if !reflect.DeepEqual(snapshotAuditStatement(q, audit, actor, workspace, "bad", tenant), transaction.SQL(transaction.Dameng, "")) {
		t.Fatal("noncanonical audit identity admitted")
	}
}

func TestMonitoringRepositoryPreservesLegacyAndFailsClosed(t *testing.T) {
	tenant, workspace, id := uuid.NewString(), uuid.NewString(), uuid.NewString()
	stamp := time.Date(2026, 11, 3, 1, 2, 3, 123456000, time.UTC)
	policy := Defaults()
	for _, d := range []transaction.Dialect{transaction.PostgreSQL, transaction.MySQL} {
		q := &monitoringRepositoryProbe{dialect: d}
		var first, cursor transaction.Statement
		if d == transaction.PostgreSQL {
			first = transaction.SQL(d, `SELECT id::text,created_at FROM managed_nodes WHERE tenant_id=$1 AND workspace_id=$2 ORDER BY created_at DESC,id DESC LIMIT $3`, tenant, workspace, policy.NodePageSize+1)
			cursor = transaction.SQL(d, `SELECT id::text,created_at FROM managed_nodes WHERE tenant_id=$1 AND workspace_id=$2 AND (created_at,id)<($3,$4) ORDER BY created_at DESC,id DESC LIMIT $5`, tenant, workspace, stamp, id, policy.NodePageSize+1)
		} else {
			first = transaction.SQL(d, `SELECT id,created_at FROM managed_nodes WHERE tenant_id=? AND workspace_id=? ORDER BY created_at DESC,id DESC LIMIT ?`, tenant, workspace, policy.NodePageSize+1)
			cursor = transaction.SQL(d, `SELECT id,created_at FROM managed_nodes WHERE tenant_id=? AND workspace_id=? AND (created_at,id)<(?,?) ORDER BY created_at DESC,id DESC LIMIT ?`, tenant, workspace, stamp, id, policy.NodePageSize+1)
		}
		if !reflect.DeepEqual(nodePageStatement(q, tenant, workspace, policy, time.Time{}, ""), first) || !reflect.DeepEqual(nodePageStatement(q, tenant, workspace, policy, stamp, id), cursor) {
			t.Fatal("legacy keyset changed")
		}
	}
	q := &monitoringRepositoryProbe{dialect: transaction.Dialect("unsupported")}
	for _, got := range []transaction.Statement{installationStatement(q), observationClockStatement(q), nodePageStatement(q, tenant, workspace, policy, stamp, id), snapshotAuditStatement(q, id, id, id, id, id)} {
		if !reflect.DeepEqual(got, transaction.SQL(q.Dialect(), "")) {
			t.Fatal("unsupported dialect fell back")
		}
	}
}
