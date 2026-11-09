package metering

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/gateway/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const testGatewayRequestSQL = `CREATE TABLE gateway_requests (
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL, workspace_id uuid NOT NULL, gateway_id uuid NOT NULL,
  admitted_at timestamptz NOT NULL, state text NOT NULL, outcome jsonb, outcome_sha256 text,
  maximum_output_tokens integer NOT NULL, maximum_context_tokens integer NOT NULL,
  site_id varchar(32) NOT NULL DEFAULT local_site_id(), cell_id varchar(32) NOT NULL DEFAULT local_cell_id()
)`

func TestPostgresGatewayReceiptProjectsVerifiedUsageAndBudget(t *testing.T) {
	pool := newTestLedger(t)
	scope, foreign := addTestScope(t, pool), addTestScope(t, pool)
	gateway := uuid.New()
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	if _, err := pool.Exec(ctx, testGatewayRequestSQL); err != nil {
		t.Fatal(err)
	}
	store, err := transaction.FromPostgreSQL(pool)
	if err != nil {
		t.Fatal(err)
	}
	record := func(tx transaction.Executor, at time.Time, outcome wire.Outcome) wire.Acknowledgment {
		t.Helper()
		raw, err := outcome.Canonical()
		if err != nil {
			t.Fatal(err)
		}
		digest, err := outcome.Digest()
		if err != nil {
			t.Fatal(err)
		}
		_, err = tx.Exec(ctx, transaction.SQL(transaction.PostgreSQL,
			`INSERT INTO gateway_requests
 (id,tenant_id,workspace_id,gateway_id,admitted_at,state,outcome,outcome_sha256,maximum_output_tokens,maximum_context_tokens)
 VALUES($1,$2,$3,$4,$5,'reported',$6,$7,16,128)`,
			outcome.RequestID, scope.TenantID, scope.WorkspaceID, gateway, at, raw, digest))
		if err != nil {
			t.Fatal(err)
		}
		return wire.Acknowledgment{RequestID: outcome.RequestID, CanonicalSHA256: digest}
	}
	receive := func(scope Scope, gateway uuid.UUID, receipt wire.Acknowledgment, wantInsert bool, wantErr error) {
		t.Helper()
		var inserted bool
		err := store.Within(ctx, false, func(ctx context.Context, tx transaction.Executor) error {
			var err error
			inserted, err = AppendGatewayReceiptWithin(ctx, tx, scope, gateway, receipt)
			return err
		})
		if !errors.Is(err, wantErr) || inserted != wantInsert {
			t.Fatalf("receipt append = %v, %v, want %v, %v", inserted, err, wantInsert, wantErr)
		}
	}

	cutover := time.Date(2026, 11, 9, 9, 0, 0, 0, time.UTC)
	start, end := cutover.Add(-time.Hour), cutover.Add(time.Hour)
	oldTime := cutover.Add(-time.Microsecond)
	outcome := func(prompt, completion uint32) wire.Outcome {
		return wire.Outcome{RequestID: uuid.NewString(), Status: "succeeded",
			Usage: &wire.Usage{PromptTokens: prompt, CompletionTokens: completion, TotalTokens: prompt + completion}}
	}
	recent := record(store, cutover, outcome(7, 1))
	receive(scope, gateway, recent, true, nil)
	late := record(store, oldTime, outcome(4, 0))
	receive(scope, gateway, late, true, nil)
	receive(scope, gateway, late, false, nil)
	receive(foreign, gateway, late, false, ErrGatewayReceiptNotFound)
	receive(Scope{TenantID: foreign.TenantID, WorkspaceID: scope.WorkspaceID}, gateway, late, false, ErrGatewayReceiptNotFound)
	receive(scope, uuid.New(), late, false, ErrGatewayReceiptNotFound)
	for _, foreignLocation := range []struct{ site, cell string }{
		{"fixture-site", "sibling-cell"},
		{"foreign-site", "fixture-cell"},
	} {
		otherCell := record(store, cutover, outcome(5, 0))
		if _, err := pool.Exec(ctx, "UPDATE gateway_requests SET site_id=$2,cell_id=$3 WHERE id=$1",
			otherCell.RequestID, foreignLocation.site, foreignLocation.cell); err != nil {
			t.Fatal(err)
		}
		receive(scope, gateway, otherCell, false, ErrGatewayReceiptNotFound)
	}

	badAck := late
	badAck.CanonicalSHA256 = recent.CanonicalSHA256
	receive(scope, gateway, badAck, false, ErrGatewayReceiptInvalid)

	code := "client_cancelled"
	unknown := record(store, cutover, wire.Outcome{RequestID: uuid.NewString(), Status: code, ErrorCode: &code})
	receive(scope, gateway, unknown, true, nil)
	receive(scope, gateway, unknown, false, nil)
	corrupt := record(store, cutover, outcome(2, 1))
	if _, err := pool.Exec(ctx, "UPDATE gateway_requests SET outcome=jsonb_set(outcome,'{output_started}','true') WHERE id=$1", corrupt.RequestID); err != nil {
		t.Fatal(err)
	}
	receive(scope, gateway, corrupt, false, ErrGatewayReceiptInvalid)
	excess := record(store, cutover, outcome(7, 17))
	receive(scope, gateway, excess, false, ErrGatewayReceiptInvalid)
	if inserted, err := AppendGatewayReceiptWithin(ctx, store, scope, gateway, recent); inserted || !errors.Is(err, ErrUnavailable) {
		t.Fatalf("receipt accepted without transaction: %v, %v", inserted, err)
	}
	raw := Event{Producer: gatewayReceiptProducer, ID: corrupt.RequestID, OccurredAt: cutover, Usage: Usage{Tokens: 999}}
	if inserted, err := writeEvent(ctx, pool, scope, raw); inserted || !errors.Is(err, ErrInvalidEvent) {
		t.Fatalf("generic producer bypassed receipt verification: %v, %v", inserted, err)
	}

	rollback := errors.New("fixture rollback")
	var undone wire.Acknowledgment
	err = store.Within(ctx, false, func(ctx context.Context, tx transaction.Executor) error {
		undone = record(tx, cutover, outcome(1, 0))
		if inserted, err := AppendGatewayReceiptWithin(ctx, tx, scope, gateway, undone); !inserted || err != nil {
			t.Fatalf("append before rollback: %v, %v", inserted, err)
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatal("receipt transaction did not roll back", err)
	}
	receive(scope, gateway, undone, false, ErrGatewayReceiptNotFound)

	var matchingRows, localRows int
	if err := pool.QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE site_id='fixture-site' AND cell_id='fixture-cell')
 FROM usage_ledger WHERE producer=$1`, gatewayReceiptProducer).Scan(&matchingRows, &localRows); err != nil ||
		matchingRows != 3 || localRows != matchingRows {
		t.Fatalf("verified receipts did not keep their local scope: %d of %d, %v", localRows, matchingRows, err)
	}

	totals := readTotals(t, pool, scope, start, end)
	if want := (Totals{GPUMicroseconds: "0", Tokens: "12", StorageByteSeconds: "0", Requests: "3", UnmeasuredTokenRequests: "1"}); totals != want {
		t.Fatalf("totals = %+v, want %+v", totals, want)
	}
	card := testCard(t, `{"currency":"XTS","versions":[
 {"id":"old","effective_at":"2026-11-09T08:00:00Z","rates":[{"meter":"tokens","units":1,"amount_micros":2},{"meter":"requests","units":1,"amount_micros":1}]},
 {"id":"new","effective_at":"2026-11-09T09:00:00Z","rates":[{"meter":"tokens","units":1,"amount_micros":3},{"meter":"requests","units":1,"amount_micros":4}]}]}`)
	old := testShowback(t, pool, scope, start, cutover, card)
	if !old.FullyRated || old.KnownAmountMicros != "9" || old.UnmeasuredTokenRequests != "0" || len(old.Lines) != 2 || old.Lines[0].Quantity != "4" {
		t.Fatalf("late receipt or known zero completion rated incorrectly: %+v", old)
	}
	err = pgx.BeginTxFunc(ctx, pool, reportSnapshot, func(tx pgx.Tx) error {
		report, err := ShowbackWithin(ctx, tx, scope, start, end, card)
		if err != nil {
			return err
		}
		if report.FullyRated || report.KnownAmountMicros != "41" || report.UnmeasuredTokenRequests != "1" {
			t.Fatalf("showback hid unmeasured usage: %+v", report)
		}
		if len(report.Lines) != 4 || !reflect.DeepEqual(report.Lines[:2], old.Lines) {
			t.Fatalf("rate cutover changed the late receipt: %+v", report.Lines)
		}
		for _, tc := range []struct {
			limit string
			state BudgetState
		}{{"100", BudgetUnmeasuredUsage}, {"41", BudgetLimitReached}} {
			policy := BudgetPolicy{Currency: "XTS", LimitMicros: tc.limit, WarningBasisPoints: 5000}
			budget, err := BudgetWithin(ctx, tx, scope, start, end, card, policy)
			if err != nil {
				return err
			}
			if budget.State != tc.state || budget.FullyRated != report.FullyRated ||
				budget.KnownAmountMicros != report.KnownAmountMicros || budget.UnmeasuredTokenRequests != report.UnmeasuredTokenRequests {
				t.Fatalf("budget disagrees with report: %+v, want %s", budget, tc.state)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	moved := record(store, cutover, outcome(8, 0))
	changed := false
	err = store.Within(ctx, false, func(ctx context.Context, tx transaction.Executor) error {
		fault := &beforeReceiptWrite{Executor: tx, run: func() error {
			changed = true
			return store.Within(ctx, false, func(ctx context.Context, other transaction.Executor) error {
				if _, err := other.Exec(ctx, transaction.SQL(transaction.PostgreSQL,
					"UPDATE cells SET is_local=false WHERE is_local")); err != nil {
					return err
				}
				_, err := other.Exec(ctx, transaction.SQL(transaction.PostgreSQL,
					"UPDATE cells SET is_local=true WHERE id='sibling-cell'"))
				return err
			})
		}}
		_, err := AppendGatewayReceiptWithin(ctx, fault, scope, gateway, moved)
		return err
	})
	if !changed || !errors.Is(err, ErrUnavailable) {
		t.Fatalf("local cell change did not fail closed: moved=%v, %v", changed, err)
	}
	var movedRows int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM usage_ledger WHERE event_id=$1", moved.RequestID).Scan(&movedRows); err != nil || movedRows != 0 {
		t.Fatalf("receipt was charged after its local cell changed: %d, %v", movedRows, err)
	}
}

// beforeReceiptWrite runs the injected location change after receipt selection
// and before the first ledger statement.
type beforeReceiptWrite struct {
	transaction.Executor
	run func() error
}

func (tx *beforeReceiptWrite) Exec(ctx context.Context, statement transaction.Statement) (int64, error) {
	if tx.run != nil {
		run := tx.run
		tx.run = nil
		if err := run(); err != nil {
			return 0, err
		}
	}
	return tx.Executor.Exec(ctx, statement)
}
