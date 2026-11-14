package productagent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"unicode/utf8"

	wire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// ExecutionRunState is durable control state, independent of a worker process.
type ExecutionRunState string

const (
	// ExecutionRunActive admits bounded steps when current policy permits them.
	ExecutionRunActive ExecutionRunState = "active"
	// ExecutionRunStopped permanently refuses further steps of this run.
	ExecutionRunStopped ExecutionRunState = "stopped"
)

// ExecutionRun contains scoped control metadata, never a command or result body.
type ExecutionRun struct {
	ID                string
	State             ExecutionRunState
	MaximumSteps      int64
	AdmittedSteps     int64
	ActiveExecutionID string
}

// ExecutionRuns guards deterministic commands with durable run and step claims.
// It performs no model calls and exposes no general operation callback.
type ExecutionRuns struct{ budgets *ExecutionBudgets }

var errRunConflict = errors.New("VERSION_CONFLICT")

// NewExecutionRuns reuses the budget's authority, policy and borrowed storage.
// RunSteps in that trusted policy must explicitly provide capacity before Start.
func NewExecutionRuns(budgets *ExecutionBudgets) (*ExecutionRuns, error) {
	if budgets == nil || budgets.commands == nil {
		return nil, errExecutionPolicyUnavailable
	}
	return &ExecutionRuns{budgets: budgets}, nil
}

// Start binds a server-issued run ID to a trusted intent digest and current scope.
// A repeated identity never resets its initial ceiling, counter or stop state.
func (r *ExecutionRuns) Start(ctx context.Context, actor identity.Auth, id, intentSHA256 string) (ExecutionRun, error) {
	if !wire.ValidID(id) || !budgetDigest(intentSHA256) {
		return ExecutionRun{}, errors.New("VALIDATION_FAILED")
	}
	var result ExecutionRun
	err := r.withSubject(ctx, actor, func(ctx context.Context, tx transaction.Executor, subject BudgetSubject) error {
		policy, err := r.budgets.readPolicy(ctx, tx, subject)
		if err != nil {
			return err
		}
		result, err = startExecutionRunWithin(ctx, tx, subject, id, intentSHA256, policy)
		return err
	})
	if err != nil {
		return ExecutionRun{}, err
	}
	return result, nil
}

// Inspect rechecks current authority before returning control metadata. It works
// while execution policy is unavailable so an owner can inspect and cancel a run.
func (r *ExecutionRuns) Inspect(ctx context.Context, actor identity.Auth, id string) (ExecutionRun, error) {
	return r.control(ctx, actor, id, false)
}

// Cancel durably stops the owner's run without releasing any reservation.
// An already admitted read may finish; its result is suppressed by the final fence.
// Cancellation never performs a product write or bypasses human confirmation.
func (r *ExecutionRuns) Cancel(ctx context.Context, actor identity.Auth, id string) (ExecutionRun, error) {
	return r.control(ctx, actor, id, true)
}

func (r *ExecutionRuns) control(ctx context.Context, actor identity.Auth, id string, stop bool) (ExecutionRun, error) {
	if !wire.ValidID(id) {
		return ExecutionRun{}, errors.New("VALIDATION_FAILED")
	}
	var result ExecutionRun
	err := r.withSubject(ctx, actor, func(ctx context.Context, tx transaction.Executor, subject BudgetSubject) error {
		stored, err := readExecutionRun(ctx, tx, subject, id)
		if err != nil {
			return err
		}
		result = stored.run
		if stop {
			if err := stopExecutionRun(ctx, tx, id); err != nil {
				return err
			}
			result.State = ExecutionRunStopped
		}
		return nil
	})
	if err != nil {
		return ExecutionRun{}, err
	}
	return result, nil
}

// RunCommand claims and executes one fixed navigation or typed read command.
// The server supplies both IDs. A replay returns a conflict without executing or
// returning cached data. Writes still fail the command service's risk admission.
func (r *ExecutionRuns) RunCommand(ctx context.Context, actor identity.Auth, runID, executionID, input, cursor string) (Result, error) {
	if !wire.ValidID(runID) || !wire.ValidID(executionID) || !utf8.ValidString(input) || !utf8.ValidString(cursor) {
		return Result{}, errors.New("VALIDATION_FAILED")
	}
	if r == nil || r.budgets == nil || ctx == nil {
		return Result{}, ErrCommandDenied
	}
	commands := r.budgets.commands
	ctx, cancel := context.WithTimeout(ctx, config.Seconds(commands.identity.Config.Policy().HTTP.RequestSeconds))
	defer cancel()
	cmd, err := commands.admitRunCommand(actor, input, cursor)
	if err != nil {
		return Result{}, err
	}
	execution := BudgetExecution{ID: executionID, ExecutionSHA256: cmd.fingerprint(cursor)}
	if err := r.claim(ctx, actor, runID, execution, cmd.id); err != nil {
		return Result{}, err
	}
	return r.dispatchClaimed(ctx, actor, runID, executionID, input, cursor)
}

func (r *ExecutionRuns) dispatchClaimed(ctx context.Context, actor identity.Auth, runID, executionID, input, cursor string) (Result, error) {
	if err := r.beforeDispatch(ctx, actor, runID, executionID); err != nil {
		return Result{}, err
	}
	result, operationErr := r.budgets.commands.RunForRequest(ctx, actor, input, cursor, executionID)
	if outcome, ok := transaction.CommitOutcome(operationErr); ok && outcome != transaction.CommitRolledBack {
		// The command audit may have committed. A new process cannot infer that
		// the operation did not happen from the missing reply.
		return Result{}, operationErr
	}
	if err := r.finish(ctx, actor, runID, executionID, operationErr == nil); err != nil {
		return Result{}, err
	}
	if operationErr != nil {
		return Result{}, operationErr
	}
	return result, nil
}

// RunCommandForRequest guards one deterministic HTTP command without accepting
// a caller's run identity or limits. The dispatcher supplies its request UUID.
// Replaying that UUID never dispatches a completed or uncertain step again.
func (r *ExecutionRuns) RunCommandForRequest(ctx context.Context, actor identity.Auth, input, cursor, requestID string) (Result, error) {
	if !wire.ValidID(requestID) {
		return Result{}, ErrInvalidRequestID
	}
	if r == nil || r.budgets == nil || ctx == nil {
		return Result{}, ErrCommandDenied
	}
	if !utf8.ValidString(input) || !utf8.ValidString(cursor) {
		return Result{}, ErrInvalidCommand
	}
	commands := r.budgets.commands
	cmd, err := commands.admitRunCommand(actor, input, cursor)
	if err != nil {
		return Result{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, config.Seconds(commands.identity.Config.Policy().HTTP.RequestSeconds))
	defer cancel()
	fingerprint := cmd.fingerprint(cursor)
	err = r.withSubject(ctx, actor, func(ctx context.Context, tx transaction.Executor, subject BudgetSubject) error {
		policy, err := r.budgets.readPolicy(ctx, tx, subject)
		if err != nil {
			return err
		}
		if _, err := startExecutionRunWithin(ctx, tx, subject, requestID, fingerprint, policy); err != nil {
			return err
		}
		return r.claimWithin(ctx, tx, subject, requestID, BudgetExecution{ID: requestID, ExecutionSHA256: fingerprint}, cmd.id)
	})
	if err != nil {
		return Result{}, err
	}
	return r.dispatchClaimed(ctx, actor, requestID, requestID, input, cursor)
}

func (r *ExecutionRuns) withSubject(ctx context.Context, actor identity.Auth, fn func(context.Context, transaction.Executor, BudgetSubject) error) error {
	if r == nil || r.budgets == nil {
		return ErrCommandDenied
	}
	return r.budgets.withSubject(ctx, actor, fn)
}

func commandFingerprint(command, cursor string) string {
	return executionDigest("command_v1", command, cursor)
}

func executionDigest(fields ...string) string {
	encoded, _ := json.Marshal(fields)
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func (s *Service) admitRunCommand(actor identity.Auth, input, cursor string) (command, error) {
	cmd, err := s.resolveCommand(input)
	if err != nil {
		return command{}, err
	}
	if _, err := s.admit(cmd, actor, cursor); err != nil {
		return command{}, err
	}
	return cmd, nil
}
