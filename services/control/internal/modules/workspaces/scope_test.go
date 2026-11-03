package workspaces

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

func TestWorkspaceScopeInputsAndAuthorityFailBeforeSQL(t *testing.T) {
	ctx := context.Background()
	s := &Service{}
	for _, id := range []string{"invalid", uuid.Nil.String(), strings.ToUpper(uuid.NewString())} {
		if _, e := s.Get(ctx, identity.Auth{}, id, false); e == nil || e.Error() != "RESOURCE_NOT_FOUND" {
			t.Fatal(e)
		}
		if _, e := s.GetNote(ctx, identity.Auth{}, uuid.NewString(), id); e == nil || e.Error() != "RESOURCE_NOT_FOUND" {
			t.Fatal(e)
		}
	}
	if _, e := s.Get(ctx, identity.Auth{}, uuid.NewString(), false); e == nil || e.Error() != "WORKSPACE_IDENTITY_REQUIRED" {
		t.Fatal(e)
	}
	if _, e := s.RecheckScope(ctx, nil, identity.Auth{}, uuid.NewString()); e == nil || e.Error() != "WORKSPACE_TRANSACTION_REQUIRED" {
		t.Fatal(e)
	}
	if _, e := s.MembershipAuthority(ctx, nil, uuid.NewString(), uuid.NewString(), uuid.NewString()); e == nil || e.Error() != "WORKSPACE_TRANSACTION_REQUIRED" {
		t.Fatal(e)
	}
}

// These unconnected pool values and dialect double prove rejection before any
// connection/query is used. They are confined to this input-boundary unit test.
type wrongScopeDialect struct{ transaction.Executor }

func (wrongScopeDialect) Dialect() transaction.Dialect { return transaction.MySQL }
func TestWorkspaceScopeRejectsPoolAndMismatchedAuthority(t *testing.T) {
	pool, other := &pgxpool.Pool{}, &pgxpool.Pool{}
	s := &Service{DB: pool, Identity: &identity.Service{DB: other}}
	if _, e := s.repository(); e == nil || e.Error() != "WORKSPACE_STORAGE_MISMATCH" {
		t.Fatal(e)
	}
	s.Identity.DB = pool
	store, e := s.repository()
	if e != nil {
		t.Fatal(e)
	}
	if e = s.executor(store); e == nil || e.Error() != "WORKSPACE_TRANSACTION_REQUIRED" {
		t.Fatal("pool accepted as caller transaction", e)
	}
	if e = s.executor(nil); e == nil || e.Error() != "WORKSPACE_TRANSACTION_REQUIRED" {
		t.Fatal(e)
	}
	if e = s.executor(wrongScopeDialect{}); e == nil || e.Error() != "WORKSPACE_STORAGE_MISMATCH" {
		t.Fatal(e)
	}
	if _, e = s.MembershipAuthorityWithin(context.Background(), store, uuid.NewString(), uuid.NewString(), uuid.NewString()); e == nil || e.Error() != "WORKSPACE_TRANSACTION_REQUIRED" {
		t.Fatal(e)
	}
}
