package deployments

import (
	"context"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// RegistryActionOwner borrows the existing node/deployment transaction. Neither
// method may open another transaction, perform remote IO or grant generic code.
type RegistryActionOwner interface {
	PrepareActionWithin(context.Context, transaction.Executor, identity.Auth, nodes.Node, Deployment, wire.InferenceCommand) (wire.InferenceCommand, error)
	BindActionWithin(context.Context, transaction.Executor, identity.Auth, nodes.Node, nodes.Task, wire.InferenceCommand, bool, string, string) error
}
