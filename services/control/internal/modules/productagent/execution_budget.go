package productagent

import (
	"context"
	"errors"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// BudgetAccess is an explicit trusted user policy, separate from installation policy.
type BudgetAccess string

const (
	// BudgetAccessEnabled permits checking this user's finite allowance.
	BudgetAccessEnabled BudgetAccess = "enabled"
	// BudgetAccessDisabled denies admission regardless of remaining allowance.
	BudgetAccessDisabled BudgetAccess = "disabled"
)

// BudgetCeiling bounds admitted executions and their total model tokens in one window.
// Zero is zero capacity. There is no unlimited sentinel.
type BudgetCeiling struct {
	Executions int64
	Tokens     int64
}

// ExecutionRunLimits bounds admitted steps of one run. Zero permits no run.
type ExecutionRunLimits struct {
	Installation int64
	User         int64
}

// ExecutionBudgetPolicy comes from the installation's trusted policy owner.
// Window boundaries must remain stable across replicas and policy revisions.
type ExecutionBudgetPolicy struct {
	UserAccess   BudgetAccess
	WindowStart  time.Time
	WindowEnd    time.Time
	Installation BudgetCeiling
	User         BudgetCeiling
	RunSteps     ExecutionRunLimits
}

// BudgetSubject is resolved from current server authority before policy is read.
type BudgetSubject struct {
	TenantID, UserID, WorkspaceID, Audience string
}

// ExecutionBudgetPolicySource reads a complete local policy on the supplied
// transaction, honors cancellation, and returns an error for an absent decision.
type ExecutionBudgetPolicySource func(context.Context, transaction.Executor, BudgetSubject) (ExecutionBudgetPolicy, error)

// BudgetExecution binds a server-issued UUID to one exact, bounded execution.
// ExecutionSHA256 hashes the trusted adapter's canonical action, not model prose.
type BudgetExecution struct {
	ID              string
	ExecutionSHA256 string
	MaximumTokens   int64
}

// BudgetReservationState describes durable admission without authorizing a replay.
type BudgetReservationState string

const (
	// BudgetNew permits one execution only after Reserve has committed successfully.
	BudgetNew BudgetReservationState = "new"
	// BudgetHeld means the execution may already have started; never execute it again.
	BudgetHeld BudgetReservationState = "held"
	// BudgetSettled means trusted usage has already settled this execution.
	BudgetSettled BudgetReservationState = "settled"
)

// BudgetReservation is empty on every failed or uncertain transaction outcome.
type BudgetReservation struct {
	ID    string
	State BudgetReservationState
}

// BudgetSettlement carries usage from an authenticated, trusted gateway receipt.
// It must not be populated directly from browser input or model-generated text.
// The deterministic run guard closes only zero-token holds with a local status
// digest; that digest is not evidence of model usage.
type BudgetSettlement struct {
	ID            string
	ActualTokens  int64
	ReceiptSHA256 string
}

// ErrBudgetExceeded means one of the current finite ceilings has no capacity.
var ErrBudgetExceeded = errors.New("RATE_LIMITED")

// ExecutionBudgets reserves shared PostgreSQL capacity using current session and
// workspace authority. It owns neither connection configuration nor policy storage.
// Production adapters must commit a new reservation before starting any work.
type ExecutionBudgets struct {
	commands *Service
	policy   ExecutionBudgetPolicySource
}

// NewExecutionBudgets binds a reservation boundary to the existing command
// service. A nil policy source makes admission unavailable; it never defaults on.
func NewExecutionBudgets(commands *Service, policy ExecutionBudgetPolicySource) (*ExecutionBudgets, error) {
	if commands == nil || commands.identity == nil || commands.applications == nil ||
		commands.applications.Workspaces == nil || commands.applications.Workspaces.Identity != commands.identity {
		return nil, errExecutionPolicyUnavailable
	}
	return &ExecutionBudgets{commands: commands, policy: policy}, nil
}

// Reserve rechecks current authority and atomically charges both finite ceilings.
// Only BudgetNew allows the trusted adapter to start one execution. A held replay
// or unknown commit must be reconciled using the original ID, never re-executed.
// The action owner must verify its risk policy and required human confirmation
// before reserving an effect; this boundary supplies no action authorization.
func (b *ExecutionBudgets) Reserve(ctx context.Context, actor identity.Auth, execution BudgetExecution) (BudgetReservation, error) {
	if !wire.ValidID(execution.ID) || !budgetDigest(execution.ExecutionSHA256) || execution.MaximumTokens < 0 {
		return BudgetReservation{}, errors.New("VALIDATION_FAILED")
	}
	var result BudgetReservation
	err := b.withSubject(ctx, actor, func(ctx context.Context, tx transaction.Executor, subject BudgetSubject) error {
		policy, err := b.readPolicy(ctx, tx, subject)
		if err != nil {
			return err
		}
		result, err = reserveBudgetWithin(ctx, tx, subject, execution, policy)
		return err
	})
	if err != nil {
		return BudgetReservation{}, err
	}
	return result, nil
}

func (b *ExecutionBudgets) readPolicy(ctx context.Context, tx transaction.Executor, subject BudgetSubject) (ExecutionBudgetPolicy, error) {
	if err := b.commands.admitExecution(ctx); err != nil {
		return ExecutionBudgetPolicy{}, err
	}
	if b.policy == nil {
		return ExecutionBudgetPolicy{}, errExecutionPolicyUnavailable
	}
	policy, err := b.policy(ctx, tx, subject)
	if err != nil {
		return ExecutionBudgetPolicy{}, errExecutionPolicyUnavailable
	}
	if policy.UserAccess == BudgetAccessDisabled {
		return ExecutionBudgetPolicy{}, ErrCommandDenied
	}
	if !validBudgetPolicy(policy) {
		return ExecutionBudgetPolicy{}, errExecutionPolicyUnavailable
	}
	return policy, nil
}

// Settle returns unused held tokens exactly once while keeping the execution
// unit charged. Disablement does not prevent settlement of known usage. Missing
// usage, cancellation and process restart never release a hold automatically.
func (b *ExecutionBudgets) Settle(ctx context.Context, actor identity.Auth, settlement BudgetSettlement) (BudgetReservation, error) {
	if !wire.ValidID(settlement.ID) || !budgetDigest(settlement.ReceiptSHA256) || settlement.ActualTokens < 0 {
		return BudgetReservation{}, errors.New("VALIDATION_FAILED")
	}
	var result BudgetReservation
	err := b.withSubject(ctx, actor, func(ctx context.Context, tx transaction.Executor, subject BudgetSubject) error {
		var err error
		result, err = settleBudgetWithin(ctx, tx, subject, settlement)
		return err
	})
	if err != nil {
		return BudgetReservation{}, err
	}
	return result, nil
}

func (b *ExecutionBudgets) withSubject(ctx context.Context, actor identity.Auth, fn func(context.Context, transaction.Executor, BudgetSubject) error) error {
	if b == nil || b.commands == nil || ctx == nil || actor.Me.Workspace == nil ||
		actor.Me.Audience != "admin" && actor.Me.Audience != "portal" {
		return ErrCommandDenied
	}
	return b.commands.withSession(ctx, actor, func(ctx context.Context, tx transaction.Executor) error {
		if tx.Dialect() != transaction.PostgreSQL {
			return errExecutionPolicyUnavailable
		}
		w, err := b.commands.applications.Workspaces.ReadScopeWithin(ctx, tx, actor, actor.Me.Workspace.ID)
		if err != nil {
			return err
		}
		return fn(ctx, tx, BudgetSubject{TenantID: w.TenantID, UserID: actor.Me.User.ID, WorkspaceID: w.ID, Audience: actor.Me.Audience})
	})
}

func validBudgetPolicy(p ExecutionBudgetPolicy) bool {
	return p.UserAccess == BudgetAccessEnabled && !p.WindowStart.IsZero() && p.WindowStart.Before(p.WindowEnd) &&
		p.WindowStart.Equal(p.WindowStart.Truncate(time.Microsecond)) && p.WindowEnd.Equal(p.WindowEnd.Truncate(time.Microsecond)) &&
		p.Installation.Executions >= 0 && p.Installation.Tokens >= 0 && p.User.Executions >= 0 && p.User.Tokens >= 0 &&
		p.RunSteps.Installation >= 0 && p.RunSteps.User >= 0
}

func budgetDigest(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func budgetFits(used BudgetCeiling, ceiling BudgetCeiling, tokens int64) bool {
	return used.Executions >= 0 && used.Tokens >= 0 && tokens >= 0 && used.Executions < ceiling.Executions &&
		used.Tokens <= ceiling.Tokens && tokens <= ceiling.Tokens-used.Tokens
}
