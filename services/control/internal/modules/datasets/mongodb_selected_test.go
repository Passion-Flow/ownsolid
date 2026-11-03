package datasets

import (
	"context"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"testing"
)

// Test-only dialect double: every embedded query method is deliberately nil.
// Unsupported selection must return before any SQL, source or file operation.
type mongoUnsupportedStore struct{ transaction.Store }

func (mongoUnsupportedStore) Dialect() transaction.Dialect { return transaction.MySQL }
func TestMongoImportUnsupportedBusinessDialectDoesNotExecute(t *testing.T) {
	s := &Service{Identity: &identity.Service{Storage: mongoUnsupportedStore{}}}
	called := false
	e := s.mongoTransaction(context.Background(), identity.Auth{}, "unused", true, func(context.Context, transaction.Executor, workspaces.Workspace) error { called = true; return nil })
	if e == nil || e.Error() != "MONGODB_BUSINESS_DATABASE_UNSUPPORTED" || called {
		t.Fatal("unsupported dialect performed work", e, called)
	}
	out, e := s.MongoDBSources(context.Background(), identity.Auth{}, "unused", "unused")
	if e == nil || e.Error() != "MONGODB_BUSINESS_DATABASE_UNSUPPORTED" || out != nil {
		t.Fatal("unsupported source request projected data", out, e)
	}
}
func TestMongoImportOwnedErrorBoundaryPreservesSafeCodes(t *testing.T) {
	e := datasetRuntimeError(failure("MONGODB_BUSY"))
	if e == nil || e.Error() != "MONGODB_BUSY" {
		t.Fatal("owned source concurrency failure concealed", e)
	}
}
