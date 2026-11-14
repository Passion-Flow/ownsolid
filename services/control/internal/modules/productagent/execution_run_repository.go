package productagent

import (
	"context"
	"database/sql"
	"errors"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

type storedExecutionRun struct {
	run          ExecutionRun
	intentSHA256 string
}

func startExecutionRunWithin(ctx context.Context, tx transaction.Executor, subject BudgetSubject, id, intentSHA256 string, policy ExecutionBudgetPolicy) (ExecutionRun, error) {
	limit := min(policy.RunSteps.Installation, policy.RunSteps.User)
	if limit == 0 {
		return ExecutionRun{}, ErrBudgetExceeded
	}
	if _, err := tx.Exec(ctx, budgetSQL(`INSERT INTO product_agent_runs(id,tenant_id,user_id,workspace_id,audience,intent_sha256,maximum_steps)
		VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(id) DO NOTHING`, id, subject.TenantID, subject.UserID,
		subject.WorkspaceID, subject.Audience, intentSHA256, limit)); err != nil {
		return ExecutionRun{}, err
	}
	stored, err := readExecutionRun(ctx, tx, subject, id)
	if err != nil {
		return ExecutionRun{}, err
	}
	if stored.intentSHA256 != intentSHA256 {
		return ExecutionRun{}, errors.New("IDEMPOTENCY_CONFLICT")
	}
	return stored.run, nil
}

func readExecutionRun(ctx context.Context, tx transaction.Executor, subject BudgetSubject, id string) (storedExecutionRun, error) {
	var stored storedExecutionRun
	var active sql.NullString
	stored.run.ID = id
	err := tx.QueryRow(ctx, budgetSQL(`SELECT state,maximum_steps,admitted_steps,active_execution_id::text,intent_sha256
		FROM product_agent_runs WHERE id=$1 AND tenant_id=$2 AND user_id=$3 AND workspace_id=$4 AND audience=$5 FOR UPDATE`,
		id, subject.TenantID, subject.UserID, subject.WorkspaceID, subject.Audience)).Scan(
		&stored.run.State, &stored.run.MaximumSteps, &stored.run.AdmittedSteps, &active, &stored.intentSHA256)
	if errors.Is(err, sql.ErrNoRows) {
		return storedExecutionRun{}, errors.New("RESOURCE_NOT_FOUND")
	}
	stored.run.ActiveExecutionID = active.String
	return stored, err
}

func stopExecutionRun(ctx context.Context, tx transaction.Executor, id string) error {
	_, err := tx.Exec(ctx, budgetSQL(`UPDATE product_agent_runs SET state='stopped' WHERE id=$1`, id))
	return err
}

func (r *ExecutionRuns) claim(ctx context.Context, actor identity.Auth, runID string, execution BudgetExecution, command string) error {
	return r.withSubject(ctx, actor, func(ctx context.Context, tx transaction.Executor, subject BudgetSubject) error {
		return r.claimWithin(ctx, tx, subject, runID, execution, command)
	})
}

func (r *ExecutionRuns) claimWithin(ctx context.Context, tx transaction.Executor, subject BudgetSubject, runID string, execution BudgetExecution, command string) error {
	stored, err := readExecutionRun(ctx, tx, subject, runID)
	if err != nil {
		return err
	}
	policy, err := r.budgets.readPolicy(ctx, tx, subject)
	if err != nil {
		return err
	}
	var oldFingerprint string
	err = tx.QueryRow(ctx, budgetSQL(`SELECT execution_sha256 FROM product_agent_steps WHERE id=$1 AND run_id=$2`, execution.ID, runID)).Scan(&oldFingerprint)
	if err == nil {
		if oldFingerprint != execution.ExecutionSHA256 {
			return errors.New("IDEMPOTENCY_CONFLICT")
		}
		return errRunConflict
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if stored.run.State != ExecutionRunActive || stored.run.ActiveExecutionID != "" {
		return errRunConflict
	}
	limit := min(stored.run.MaximumSteps, policy.RunSteps.Installation, policy.RunSteps.User)
	if stored.run.AdmittedSteps >= limit {
		return ErrBudgetExceeded
	}
	reservation, err := reserveBudgetWithin(ctx, tx, subject, execution, policy)
	if err != nil {
		return err
	}
	if reservation.State != BudgetNew {
		return errRunConflict
	}
	if _, err := tx.Exec(ctx, budgetSQL(`INSERT INTO product_agent_steps(id,run_id,ordinal,command_id,execution_sha256)
		VALUES($1,$2,$3,$4,$5)`, execution.ID, runID, stored.run.AdmittedSteps+1, command, execution.ExecutionSHA256)); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, budgetSQL(`UPDATE product_agent_runs SET admitted_steps=admitted_steps+1,active_execution_id=$2 WHERE id=$1`, runID, execution.ID))
	return err
}

func (r *ExecutionRuns) beforeDispatch(ctx context.Context, actor identity.Auth, runID, executionID string) error {
	var denied error
	err := r.withSubject(ctx, actor, func(ctx context.Context, tx transaction.Executor, subject BudgetSubject) error {
		stored, err := readExecutionRun(ctx, tx, subject, runID)
		if err != nil {
			return err
		}
		if stored.run.ActiveExecutionID != executionID {
			return errRunConflict
		}
		policy, policyErr := r.budgets.readPolicy(ctx, tx, subject)
		if stored.run.State != ExecutionRunActive {
			denied = errRunConflict
		} else if policyErr != nil {
			denied = policyErr
		} else if stored.run.AdmittedSteps > min(stored.run.MaximumSteps, policy.RunSteps.Installation, policy.RunSteps.User) {
			denied = ErrBudgetExceeded
		}
		if denied != nil {
			// Persist stop independently of returning an admission error.
			return stopExecutionRun(ctx, tx, runID)
		}
		return nil
	})
	if err != nil {
		return err
	}
	return denied
}

func (r *ExecutionRuns) finish(ctx context.Context, actor identity.Auth, runID, executionID string, succeeded bool) error {
	var denied error
	err := r.withSubject(ctx, actor, func(ctx context.Context, tx transaction.Executor, subject BudgetSubject) error {
		stored, err := readExecutionRun(ctx, tx, subject, runID)
		if err != nil {
			return err
		}
		if stored.run.ActiveExecutionID != executionID {
			return errRunConflict
		}
		state := "completed"
		if !succeeded {
			state = "failed"
		}
		policy, policyErr := r.budgets.readPolicy(ctx, tx, subject)
		if stored.run.State != ExecutionRunActive {
			denied = errRunConflict
		} else if policyErr != nil {
			denied = policyErr
		} else if stored.run.AdmittedSteps > min(stored.run.MaximumSteps, policy.RunSteps.Installation, policy.RunSteps.User) {
			denied = ErrBudgetExceeded
		}
		if denied != nil {
			state = "suppressed"
			if err := stopExecutionRun(ctx, tx, runID); err != nil {
				return err
			}
		}
		reservation, err := readBudgetReservation(ctx, tx, subject, executionID)
		if err != nil {
			return err
		}
		if reservation.execution.MaximumTokens != 0 {
			return errExecutionPolicyUnavailable
		}
		// This local status digest closes only a zero-token command. It is not
		// evidence of model usage and cannot refund an execution unit.
		receipt := executionDigest("command_completion_v1", runID, executionID, state)
		if _, err := settleStoredBudgetWithin(ctx, tx, subject, reservation, BudgetSettlement{ID: executionID, ReceiptSHA256: receipt}); err != nil {
			return err
		}
		n, err := tx.Exec(ctx, budgetSQL(`UPDATE product_agent_steps SET state=$3 WHERE id=$1 AND run_id=$2 AND state='pending'`, executionID, runID, state))
		if err != nil {
			return err
		}
		if n != 1 {
			return errRunConflict
		}
		_, err = tx.Exec(ctx, budgetSQL(`UPDATE product_agent_runs SET active_execution_id=NULL WHERE id=$1`, runID))
		return err
	})
	if err != nil {
		return err
	}
	return denied
}
