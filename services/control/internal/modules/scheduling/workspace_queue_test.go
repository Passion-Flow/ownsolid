package scheduling

import (
	"errors"
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestPostgresWorkspaceQueueUsesLiveCommittedShares(t *testing.T) {
	f := newReservationFixture(t)
	a := f.first
	earlier := QueueEntry{JobID: "earlier", QueueID: a.WorkspaceID, Sequence: 1}
	later := QueueEntry{JobID: "later", QueueID: f.other, Sequence: 2}
	priority := QueueEntry{JobID: "priority", QueueID: a.WorkspaceID, Priority: 5, Sequence: 3}
	entries := []QueueEntry{later, earlier, priority}
	assertRank := func(tx pgx.Tx, want []QueueEntry) {
		t.Helper()
		ranked, err := RankWorkspaceQueueWithin(t.Context(), tx, a.TenantID, entries)
		if err != nil || !slices.Equal(ranked, want) {
			t.Fatalf("workspace queue rank: %+v, %v, want %+v", ranked, err, want)
		}
	}
	assertFreshRank := func(want []QueueEntry) {
		t.Helper()
		if err := pgx.BeginTxFunc(t.Context(), f.pool, submissionSnapshot, func(tx pgx.Tx) error {
			assertRank(tx, want)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}

	tied := []QueueEntry{priority, earlier, later}
	b := newBorrowingAttempt(a, f.other)
	if err := pgx.BeginTxFunc(t.Context(), f.pool, submissionSnapshot, func(tx pgx.Tx) error {
		assertRank(tx, tied)
		reserveBorrowing(t, f.store, a, 2)
		reserveBorrowing(t, f.store, b, 1)
		assertRank(tx, tied)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	assertFreshRank([]QueueEntry{priority, later, earlier})

	f.policy[0].Limits.Burst[NVIDIAGPU] = 6
	f.policy[4].Limits.Burst[NVIDIAGPU] = 2
	if err := pgx.BeginFunc(t.Context(), f.pool, func(tx pgx.Tx) error {
		_, _, err := ReplaceQuotaWithin(t.Context(), tx, 1, f.policy)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	assertFreshRank([]QueueEntry{priority, earlier, later})

	f.policy[0].Limits.Burst[NVIDIAGPU] = 4
	f.policy[4].Limits.Burst[NVIDIAGPU] = 4
	if err := pgx.BeginFunc(t.Context(), f.pool, func(tx pgx.Tx) error {
		_, _, err := ReplaceQuotaWithin(t.Context(), tx, 2, f.policy)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	assertFreshRank([]QueueEntry{priority, later, earlier})
	if changed, err := releaseInTransaction(t.Context(), f.store, a); err != nil || !changed {
		t.Fatalf("release workspace hold: %v, %v", changed, err)
	}
	assertFreshRank(tied)
	if changed, err := releaseInTransaction(t.Context(), f.store, b); err != nil || !changed {
		t.Fatalf("release remaining hold: %v, %v", changed, err)
	}
	assertFreshRank(tied)
}

func TestPostgresWorkspaceQueueRejectsForeignAndMissingScope(t *testing.T) {
	f := newReservationFixture(t)
	local := QueueEntry{JobID: "local", QueueID: f.first.WorkspaceID, Sequence: 1}
	missing := QueueEntry{JobID: "missing", QueueID: uuid.NewString(), Sequence: 2}
	err := pgx.BeginTxFunc(t.Context(), f.pool, submissionSnapshot, func(tx pgx.Tx) error {
		for _, tc := range []struct {
			tenant  string
			entries []QueueEntry
		}{
			{uuid.NewString(), []QueueEntry{local}},
			{f.first.TenantID, []QueueEntry{local, missing}},
		} {
			ranked, err := RankWorkspaceQueueWithin(t.Context(), tx, tc.tenant, tc.entries)
			if !errors.Is(err, ErrQuotaScopeNotFound) || len(ranked) != 0 {
				t.Fatalf("invalid scope exposed a partial rank: %+v, %v", ranked, err)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	err = pgx.BeginFunc(t.Context(), f.pool, func(tx pgx.Tx) error {
		_, err := RankWorkspaceQueueWithin(t.Context(), tx, f.first.TenantID, []QueueEntry{local})
		return err
	})
	if !errors.Is(err, ErrQueueSnapshotRequired) {
		t.Fatalf("write transaction accepted for rank: %v", err)
	}
}
