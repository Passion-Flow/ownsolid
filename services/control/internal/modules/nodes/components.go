package nodes

import (
	"context"
	"encoding/json"
	"errors"
	"slices"

	"github.com/jackc/pgx/v5"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// CreateComponentTask preserves the fixed M04 request contract.
func (s *Service) CreateComponentTask(ctx context.Context, a identity.Auth, id string, command wire.ComponentCommand, key, reason, request string, record func(pgx.Tx, Node, Task) error) (Task, error) {
	if e := command.Validate(wire.ComponentOperation(command.Action)); e != nil {
		return Task{}, e
	}
	encoded, _ := json.Marshal(command)
	return s.CreateComponentTaskBound(ctx, a, id, wire.ComponentOperation(command.Action), key, reason, request, digest("component-intent", string(encoded)), func(pgx.Tx, Node) (wire.ComponentCommand, error) { return command, nil }, func(tx pgx.Tx, n Node, t Task, c wire.ComponentCommand) error { return record(tx, n, t) })
}

// CreateComponentTaskBound binds immutable caller intent before building a fresh observed-state command.
func (s *Service) CreateComponentTaskBound(ctx context.Context, a identity.Auth, id, operation, key, reason, request string, fingerprint []byte, build func(pgx.Tx, Node) (wire.ComponentCommand, error), record func(pgx.Tx, Node, Task, wire.ComponentCommand) error) (Task, error) {
	store, e := s.repository()
	if e != nil {
		return Task{}, e
	}
	if store.Dialect() != transaction.PostgreSQL || s.DB == nil {
		return Task{}, fail("NODE_POSTGRESQL_CALLBACK_REQUIRED")
	}
	n, e := s.Get(ctx, a, "", id)
	if e != nil {
		return Task{}, e
	}
	var out Task
	e = s.adminTransaction(ctx, a, n.WorkspaceID, func(tx pgx.Tx, _ workspaces.Workspace) error {
		var e error
		out, e = s.CreateComponentTaskBoundWithin(ctx, transaction.PostgreSQLTransaction(tx), a, id, operation, key, reason, request, fingerprint, func(_ context.Context, _ transaction.Executor, n Node) (wire.ComponentCommand, error) {
			return build(tx, n)
		}, func(_ context.Context, _ transaction.Executor, n Node, t Task, c wire.ComponentCommand) error {
			return record(tx, n, t, c)
		})
		return e
	})
	if e != nil {
		return Task{}, e
	}
	return out, nil
}
func nodeOperationAllowed(n Node, operation string) bool {
	return wire.OperationAllowed(n.ExecutionProfile, operation) && slices.Contains(n.Capabilities, operation)
}

// ReadComponentCommand exposes a strict typed task contract inside the authority transaction.
func (s *Service) ReadComponentCommand(ctx context.Context, tx pgx.Tx, n Node, t Task) (wire.ComponentCommand, error) {
	return s.ReadComponentCommandWithin(ctx, transaction.PostgreSQLTransaction(tx), n, t)
}

// WithLease authorizes a bounded machine effect against the current credential and lease.
func (s *Service) WithLease(ctx context.Context, id, credential, taskID, lease string, fn func(pgx.Tx, Node, Task) error) error {
	return s.withPostgreSQLLease(ctx, id, credential, taskID, lease, fn)
}
func (s *Service) Renew(ctx context.Context, id, credential, taskID, lease string) (wire.RenewedLease, error) {
	return s.renewSelected(ctx, id, credential, taskID, lease)
}
func validateResult(operation string, input wire.Result) error {
	if input.Logs != nil {
		return fail("VALIDATION_FAILED")
	}
	if operation == wire.CollectFacts {
		if input.Component != nil {
			return fail("VALIDATION_FAILED")
		}
		if input.Outcome == "success" && input.Facts != nil && input.ErrorCode == "" {
			return input.Facts.Validate()
		}
		if input.Outcome == "failure" && input.Facts == nil && input.ErrorCode == "DISCOVERY_FAILED" {
			return nil
		}
	} else if wire.IsComponentOperation(operation) && input.Facts == nil && input.Component != nil {
		if e := input.Component.Validate(); e != nil {
			return e
		}
		if input.Outcome == "success" && input.ErrorCode == "" && input.Component.ErrorCode == "" {
			action := operation[len("node_exporter.") : len(operation)-len(".v1")]
			o := input.Component
			if (action == "install" || action == "start") && (!o.Managed || o.State != "running" || o.Metrics == nil) {
				return fail("VALIDATION_FAILED")
			}
			if action == "stop" && (!o.Managed || o.State != "stopped") {
				return fail("VALIDATION_FAILED")
			}
			if action == "remove" && (o.Managed || o.State != "not_installed") {
				return fail("VALIDATION_FAILED")
			}
			return nil
		}
		if input.Outcome == "failure" && input.ErrorCode != "" && wire.ComponentErrors[input.ErrorCode] && input.Component.ErrorCode == input.ErrorCode {
			return nil
		}
	}
	return errors.New("VALIDATION_FAILED")
}
