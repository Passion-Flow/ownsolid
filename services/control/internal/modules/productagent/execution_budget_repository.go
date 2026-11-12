package productagent

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

type budgetWindow struct{ start, end time.Time }

type storedBudgetReservation struct {
	execution BudgetExecution
	window    budgetWindow
	state     BudgetReservationState
	actual    sql.NullInt64
	receipt   sql.NullString
}

func budgetSQL(query string, args ...any) transaction.Statement {
	return transaction.SQL(transaction.PostgreSQL, query, args...)
}

func reserveBudgetWithin(ctx context.Context, tx transaction.Executor, subject BudgetSubject, execution BudgetExecution, policy ExecutionBudgetPolicy) (BudgetReservation, error) {
	n, err := tx.Exec(ctx, budgetSQL(`INSERT INTO product_agent_budget_reservations
		(id,tenant_id,user_id,workspace_id,audience,execution_sha256,maximum_tokens,window_start,window_end,state)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,'held') ON CONFLICT(id) DO NOTHING`,
		execution.ID, subject.TenantID, subject.UserID, subject.WorkspaceID, subject.Audience,
		execution.ExecutionSHA256, execution.MaximumTokens, policy.WindowStart, policy.WindowEnd))
	if err != nil {
		return BudgetReservation{}, err
	}
	if n == 0 {
		stored, err := readBudgetReservation(ctx, tx, subject, execution.ID)
		if err != nil {
			return BudgetReservation{}, err
		}
		if stored.execution != execution {
			return BudgetReservation{}, errors.New("IDEMPOTENCY_CONFLICT")
		}
		return BudgetReservation{ID: execution.ID, State: stored.state}, nil
	}
	w := budgetWindow{policy.WindowStart, policy.WindowEnd}
	used, err := lockBudgetCounters(ctx, tx, subject, w, true)
	if err != nil {
		return BudgetReservation{}, err
	}
	var current bool
	if err := tx.QueryRow(ctx, budgetSQL(`SELECT $1::timestamptz <= statement_timestamp() AND statement_timestamp() < $2::timestamptz`, w.start, w.end)).Scan(&current); err != nil {
		return BudgetReservation{}, err
	}
	if !current {
		return BudgetReservation{}, errExecutionPolicyUnavailable
	}
	if !budgetFits(used[0], policy.Installation, execution.MaximumTokens) || !budgetFits(used[1], policy.User, execution.MaximumTokens) {
		return BudgetReservation{}, ErrBudgetExceeded
	}
	if err := changeBudgetCounters(ctx, tx, subject, w, 1, execution.MaximumTokens); err != nil {
		return BudgetReservation{}, err
	}
	return BudgetReservation{ID: execution.ID, State: BudgetNew}, nil
}

func readBudgetReservation(ctx context.Context, tx transaction.Executor, subject BudgetSubject, id string) (storedBudgetReservation, error) {
	var stored storedBudgetReservation
	stored.execution.ID = id
	err := tx.QueryRow(ctx, budgetSQL(`SELECT execution_sha256,maximum_tokens,window_start,window_end,state,actual_tokens,receipt_sha256
		FROM product_agent_budget_reservations WHERE id=$1 AND tenant_id=$2 AND user_id=$3 AND workspace_id=$4 AND audience=$5 FOR UPDATE`,
		id, subject.TenantID, subject.UserID, subject.WorkspaceID, subject.Audience)).Scan(
		&stored.execution.ExecutionSHA256, &stored.execution.MaximumTokens, &stored.window.start, &stored.window.end,
		&stored.state, &stored.actual, &stored.receipt)
	if errors.Is(err, sql.ErrNoRows) {
		return storedBudgetReservation{}, errors.New("RESOURCE_NOT_FOUND")
	}
	return stored, err
}

func settleBudgetWithin(ctx context.Context, tx transaction.Executor, subject BudgetSubject, settlement BudgetSettlement) (BudgetReservation, error) {
	stored, err := readBudgetReservation(ctx, tx, subject, settlement.ID)
	if err != nil {
		return BudgetReservation{}, err
	}
	if settlement.ActualTokens > stored.execution.MaximumTokens {
		return BudgetReservation{}, errors.New("VALIDATION_FAILED")
	}
	result := BudgetReservation{ID: settlement.ID, State: BudgetSettled}
	if stored.state == BudgetSettled {
		if !stored.actual.Valid || !stored.receipt.Valid || stored.actual.Int64 != settlement.ActualTokens || stored.receipt.String != settlement.ReceiptSHA256 {
			return BudgetReservation{}, errors.New("IDEMPOTENCY_CONFLICT")
		}
		return result, nil
	}
	if stored.state != BudgetHeld || stored.actual.Valid || stored.receipt.Valid {
		return BudgetReservation{}, errExecutionPolicyUnavailable
	}
	used, err := lockBudgetCounters(ctx, tx, subject, stored.window, false)
	if err != nil {
		return BudgetReservation{}, err
	}
	for _, counter := range used {
		if counter.Executions < 1 || counter.Tokens < stored.execution.MaximumTokens {
			return BudgetReservation{}, errExecutionPolicyUnavailable
		}
	}
	// Keep the execution charged even when the gateway proved zero token usage.
	if err := changeBudgetCounters(ctx, tx, subject, stored.window, 0, settlement.ActualTokens-stored.execution.MaximumTokens); err != nil {
		return BudgetReservation{}, err
	}
	n, err := tx.Exec(ctx, budgetSQL(`UPDATE product_agent_budget_reservations SET state='settled',actual_tokens=$2,receipt_sha256=$3
		WHERE id=$1 AND state='held'`, settlement.ID, settlement.ActualTokens, settlement.ReceiptSHA256))
	if err != nil {
		return BudgetReservation{}, err
	}
	if n != 1 {
		return BudgetReservation{}, errExecutionPolicyUnavailable
	}
	return result, nil
}

// The fixed installation-before-user order is shared by admission and settlement.
func lockBudgetCounters(ctx context.Context, tx transaction.Executor, subject BudgetSubject, w budgetWindow, create bool) ([2]BudgetCeiling, error) {
	var used [2]BudgetCeiling
	for i := range used {
		insert := budgetSQL(`INSERT INTO product_agent_budget_installations(window_start,window_end) VALUES($1,$2) ON CONFLICT DO NOTHING`, w.start, w.end)
		read := budgetSQL(`SELECT executions,tokens FROM product_agent_budget_installations WHERE window_start=$1 AND window_end=$2 FOR UPDATE`, w.start, w.end)
		if i == 1 {
			insert = budgetSQL(`INSERT INTO product_agent_budget_users(user_id,window_start,window_end) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, subject.UserID, w.start, w.end)
			read = budgetSQL(`SELECT executions,tokens FROM product_agent_budget_users WHERE user_id=$1 AND window_start=$2 AND window_end=$3 FOR UPDATE`, subject.UserID, w.start, w.end)
		}
		if create {
			if _, err := tx.Exec(ctx, insert); err != nil {
				return [2]BudgetCeiling{}, err
			}
		}
		if err := tx.QueryRow(ctx, read).Scan(&used[i].Executions, &used[i].Tokens); err != nil {
			return [2]BudgetCeiling{}, err
		}
	}
	return used, nil
}

func changeBudgetCounters(ctx context.Context, tx transaction.Executor, subject BudgetSubject, w budgetWindow, executions, tokens int64) error {
	for _, query := range []transaction.Statement{
		budgetSQL(`UPDATE product_agent_budget_installations SET executions=executions+$3,tokens=tokens+$4 WHERE window_start=$1 AND window_end=$2`, w.start, w.end, executions, tokens),
		budgetSQL(`UPDATE product_agent_budget_users SET executions=executions+$4,tokens=tokens+$5 WHERE user_id=$1 AND window_start=$2 AND window_end=$3`, subject.UserID, w.start, w.end, executions, tokens),
	} {
		n, err := tx.Exec(ctx, query)
		if err != nil {
			return err
		}
		if n != 1 {
			return errExecutionPolicyUnavailable
		}
	}
	return nil
}
