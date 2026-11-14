//go:build repository_integration

package training_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/training"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

func acceptedAdapterSelected(g acceptedAdapterStory, store transaction.Store) acceptedAdapterStory {
	account := *g.owner
	account.DB, account.Storage = nil, store
	s := *g.s
	s.DB, s.Identity = nil, &account
	s.Workspaces = &workspaces.Service{Identity: &account}
	g.owner, g.s = &account, &s
	return g
}

func TestActualPairedAcceptedAdapterSnapshot(t *testing.T) {
	t.Run("selected_store_without_legacy_pool_matches_borrowed_source", func(t *testing.T) {
		g := acceptedAdapterSetup(t)
		var borrowed training.AcceptedAdapter
		if e := g.within(func(ctx context.Context, q transaction.Executor) error {
			var e error
			borrowed, e = g.read(ctx, q)
			return e
		}); e != nil {
			t.Fatal(e)
		}
		g = acceptedAdapterSelected(g, g.f.Store)
		got, e := g.s.AcceptedAdapterSnapshot(g.ctx, g.a, g.f.Workspace, g.job)
		if e != nil || !reflect.DeepEqual(got, borrowed) || got.IdentitySHA256() != borrowed.IdentitySHA256() {
			t.Fatal("selected snapshot differs from locked accepted source", e)
		}
		got, e = g.s.AcceptedAdapterSnapshot(g.ctx, g.a, uuid.NewString(), g.job)
		acceptedAdapterWant(t, got, e, "RESOURCE_NOT_FOUND")
		got, e = g.s.AcceptedAdapterSnapshot(g.ctx, g.a, g.f.Workspace, uuid.NewString())
		acceptedAdapterWant(t, got, e, "TRAINING_NOT_FOUND")
		ctx, cancel := context.WithCancel(g.ctx)
		cancel()
		got, e = g.s.AcceptedAdapterSnapshot(ctx, g.a, g.f.Workspace, g.job)
		acceptedAdapterWant(t, got, e, string(transaction.Cancelled))
		g.exec(t, "UPDATE users SET auth_epoch=auth_epoch+1 WHERE id=$1", "UPDATE users SET auth_epoch=auth_epoch+1 WHERE id=?", g.f.User)
		got, e = g.s.AcceptedAdapterSnapshot(g.ctx, g.a, g.f.Workspace, g.job)
		acceptedAdapterWant(t, got, e, "AUTH_SESSION_EXPIRED")
	})

	t.Run("unknown_commit_exposes_zero_and_explicit_fresh_read_reconciles", func(t *testing.T) {
		g := acceptedAdapterSetup(t)
		store := &trainingCreatorStore{Store: g.f.Store, at: 1, unknown: true}
		x := acceptedAdapterSelected(g, store)
		got, e := x.s.AcceptedAdapterSnapshot(x.ctx, x.a, x.f.Workspace, x.job)
		acceptedAdapterWant(t, got, e, string(transaction.CommitUnknown))
		if store.calls != 1 {
			t.Fatal("snapshot retried an uncertain transaction")
		}
		g = acceptedAdapterSelected(g, g.f.Store)
		got, e = g.s.AcceptedAdapterSnapshot(g.ctx, g.a, g.f.Workspace, g.job)
		if e != nil || got.Job.ID != g.job || !reflect.DeepEqual(got.Command, g.c) || !reflect.DeepEqual(got.Descriptor, g.descriptor) {
			t.Fatal("explicit accepted snapshot reconciliation", e)
		}
	})
}
