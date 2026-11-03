package recovery

import (
	"context"
	"errors"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

func recoveryError(e error) error {
	if outcome, ok := transaction.CommitOutcome(e); ok {
		return errors.New(string(outcome))
	}
	if e == nil {
		return nil
	}
	if errors.Is(e, context.Canceled) {
		return errors.New(string(transaction.Cancelled))
	}
	if errors.Is(e, context.DeadlineExceeded) {
		return errors.New(string(transaction.Timeout))
	}
	if e.Error() != "" && wire.KnownInferenceError(e.Error()) {
		return errors.New(e.Error())
	}
	switch e.Error() {
	case "OPERATOR_REASON_REQUIRED", "VALIDATION_FAILED", "RESOURCE_NOT_FOUND", "IDEMPOTENCY_CONFLICT", "RECOVERY_RECEIPT_EXPIRED", "RECOVERY_NODE_NOT_FENCED", "RECOVERY_RECEIPT_STATE_INVALID", "STORAGE_UNAVAILABLE", string(transaction.CommitUnknown), string(transaction.CommitRolledBack), string(transaction.Deadlock), string(transaction.LockTimeout), string(transaction.Timeout), string(transaction.Cancelled):
		return errors.New(e.Error())
	}
	return errors.New("STORAGE_UNAVAILABLE")
}
func restoreTransaction(ctx context.Context, accounts *identity.Service, fn func(context.Context, transaction.Executor) error) error {
	if ctx == nil || accounts == nil || fn == nil {
		return errors.New("STORAGE_UNAVAILABLE")
	}
	store, e := accounts.Repository()
	if e != nil {
		return recoveryError(e)
	}
	bounded, cancel := context.WithTimeout(ctx, config.Seconds(accounts.Config.Policy().HTTP.RequestSeconds))
	defer cancel()
	return recoveryError(store.Within(bounded, false, fn))
}
