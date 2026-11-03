package workspaces_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
)

func TestWorkspaceConfiguredAllocationPolicies(t *testing.T) {
	s, admin, alice, _, _ := setup(t)
	ctx := context.Background()
	policy := s.Identity.Config.Policy()
	policy.Workspaces.MaximumCount = 2 // The existing bootstrap Workspace consumes one row.
	policy.Workspaces.MaximumLiveNotes = 1
	s.Identity.Config.Runtime = &policy
	type created struct {
		w workspaces.Workspace
		e error
	}
	results := make(chan created, 2)
	for i := 0; i < 2; i++ {
		go func(index int) {
			w, e := s.Create(ctx, admin, fmt.Sprintf("Limited %d", index), alice.Me.User.ID, uuid.NewString())
			results <- created{w, e}
		}(i)
	}
	var w workspaces.Workspace
	successes, failures := 0, 0
	for i := 0; i < 2; i++ {
		result := <-results
		if result.e == nil {
			w = result.w
			successes++
		} else {
			expected(t, result.e, "RESOURCE_LIMIT_REACHED")
			if result.w.ID != "" {
				t.Fatal("failed create returned content")
			}
			failures++
		}
	}
	if successes != 1 || failures != 1 {
		t.Fatal("serialized installation limit", successes, failures)
	}
	var rows, audits int
	if e := s.DB.QueryRow(ctx, "SELECT count(*) FROM workspaces").Scan(&rows); e != nil || rows != 2 {
		t.Fatal("allocation escaped cap", rows, e)
	}
	if e := s.DB.QueryRow(ctx, "SELECT count(*) FROM audit_events WHERE action='workspace.created'").Scan(&audits); e != nil || audits != 1 {
		t.Fatal("failed create audited", audits, e)
	}
	policy.Workspaces.MaximumCount = 1
	_, e := s.Create(ctx, admin, "Lowered policy", alice.Me.User.ID, uuid.NewString())
	expected(t, e, "RESOURCE_LIMIT_REACHED")
	if _, e = s.Get(ctx, admin, w.ID, false); e != nil {
		t.Fatal("lowering policy removed existing content", e)
	}
	policy.Workspaces.MaximumCount = 3
	if _, e = s.Create(ctx, admin, "Adjusted capacity", alice.Me.User.ID, uuid.NewString()); e != nil {
		t.Fatal("configuration adjustment recovery", e)
	}

	type noted struct {
		n workspaces.Note
		e error
	}
	notes := make(chan noted, 2)
	for i := 0; i < 2; i++ {
		go func(index int) {
			n, e := s.CreateNote(ctx, alice, w.ID, fmt.Sprintf("Bounded %d", index), "Synthetic text", uuid.NewString())
			notes <- noted{n, e}
		}(i)
	}
	var note workspaces.Note
	successes, failures = 0, 0
	for i := 0; i < 2; i++ {
		result := <-notes
		if result.e == nil {
			note = result.n
			successes++
		} else {
			expected(t, result.e, "RESOURCE_LIMIT_REACHED")
			if result.n.ID != "" {
				t.Fatal("failed note returned content")
			}
			failures++
		}
	}
	if successes != 1 || failures != 1 {
		t.Fatal("serialized live-note limit", successes, failures)
	}
	if _, e = s.ChangeNote(ctx, alice, w.ID, note.ID, "", "", note.Version, uuid.NewString(), true); e != nil {
		t.Fatal("authorized removal", e)
	}
	if _, e = s.CreateNote(ctx, alice, w.ID, "Recovered capacity", "Synthetic", uuid.NewString()); e != nil {
		t.Fatal("normal deletion did not restore capacity", e)
	}
	if e = s.DB.QueryRow(ctx, "SELECT count(*) FROM workspace_notes WHERE workspace_id=$1 AND deleted_at IS NULL", w.ID).Scan(&rows); e != nil || rows != 1 {
		t.Fatal("live-note cap", rows, e)
	}
	policy.Workspaces.MaximumLiveNotes = 2
	if _, e = s.DB.Exec(ctx, `CREATE FUNCTION reject_policy_audit() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RAISE EXCEPTION 'synthetic policy audit failure'; END$$; CREATE TRIGGER reject_policy_audit BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION reject_policy_audit()`); e != nil {
		t.Fatal(e)
	}
	failed, e := s.CreateNote(ctx, alice, w.ID, "Must roll back", "Synthetic", uuid.NewString())
	if e == nil || failed.ID != "" {
		t.Fatal("audit failure returned content", e)
	}
	if e = s.DB.QueryRow(ctx, "SELECT count(*) FROM workspace_notes WHERE workspace_id=$1 AND deleted_at IS NULL", w.ID).Scan(&rows); e != nil || rows != 1 {
		t.Fatal("audit failure consumed capacity", rows, e)
	}
	if _, e = s.DB.Exec(ctx, "DROP TRIGGER reject_policy_audit ON audit_events"); e != nil {
		t.Fatal(e)
	}
	if _, e = s.CreateNote(ctx, alice, w.ID, "Recovered audit", "Synthetic", uuid.NewString()); e != nil {
		t.Fatal("repair did not restore admission", e)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	failed, e = s.CreateNote(cancelled, alice, w.ID, "Cancelled", "Synthetic", uuid.NewString())
	if e == nil || failed.ID != "" {
		t.Fatal("cancelled allocation returned content", e)
	}
}
