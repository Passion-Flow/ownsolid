package datasets

import (
	"context"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

type mongoAttachmentKey struct{}

// This private context value can only originate in the connector. HTTP inputs
// cannot select a receipt/fence or turn off ordinary M08 authorization.
func withMongoAttachment(ctx context.Context, receipt MongoDBImport) context.Context {
	return context.WithValue(ctx, mongoAttachmentKey{}, receipt)
}

// Called inside each M08 draft/upload transaction, after its identity/Workspace
// locks. Ordinary text uploads carry no connector value and retain their path.
func (s *Service) mongoAttachmentFence(ctx context.Context, q transaction.Executor, a identity.Auth, w workspaces.Workspace, dataset string) error {
	r, ok := ctx.Value(mongoAttachmentKey{}).(MongoDBImport)
	if !ok {
		return nil
	}
	if q.Dialect() != transaction.PostgreSQL {
		return failure("MONGODB_BUSINESS_DATABASE_UNSUPPORTED")
	}
	_, e := s.mongoAttachmentWithin(ctx, q, a, w, dataset, r)
	return e
}
