package nodes_test

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/agentadmission"
)

// recordingShed stands in for the admission controller of this process.
type recordingShed struct{ level atomic.Int32 }

func (r *recordingShed) Shed() agentadmission.ShedLevel {
	return agentadmission.ShedLevel(r.level.Load())
}
func (r *recordingShed) SetShed(l agentadmission.ShedLevel) error {
	r.level.Store(int32(l))
	return nil
}

func TestAnAdministratorShedsAndLiftsAgentsWithAnAuditRecord(t *testing.T) {
	s, a := setup(t)
	ctx := context.Background()
	shed := &recordingShed{}
	s.Shed = shed
	current, e := s.AgentShed(ctx, a)
	if e != nil || current.Level != "none" || current.Version != 1 {
		t.Fatal(current, e)
	}
	updated, e := s.UpdateAgentShed(ctx, a, "telemetry", current.Version, "Fleet overload drill", uuid.NewString())
	if e != nil || updated.Level != "telemetry" || updated.Version != 2 {
		t.Fatal(updated, e)
	}
	if shed.Shed() != agentadmission.ShedTelemetry {
		t.Fatalf("this process sheds %s", shed.Shed())
	}
	var audited int
	if e = s.DB.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE action='node.agents_shed' AND outcome='success' AND reason='Fleet overload drill'`).Scan(&audited); e != nil || audited != 1 {
		t.Fatal(audited, e)
	}
	if _, e = s.UpdateAgentShed(ctx, a, "none", current.Version, "A stale form", uuid.NewString()); e == nil || e.Error() != "VERSION_CONFLICT" {
		t.Fatal("a stale version was accepted:", e)
	}
	if shed.Shed() != agentadmission.ShedTelemetry {
		t.Fatal("a refused change reached admission")
	}
}

func TestShedRefusesWhatItCannotStoreOrWhoMayNot(t *testing.T) {
	s, a := setup(t)
	ctx := context.Background()
	if _, e := s.UpdateAgentShed(ctx, a, "everything", 1, "Fleet overload drill", uuid.NewString()); e == nil || e.Error() != "VALIDATION_FAILED" {
		t.Fatal(e)
	}
	portal := a
	portal.Me.Audience = "portal"
	if _, e := s.UpdateAgentShed(ctx, portal, "bulk", 1, "Fleet overload drill", uuid.NewString()); e == nil || e.Error() != "ACCESS_DENIED" {
		t.Fatal(e)
	}
	if _, e := s.AgentShed(ctx, portal); e == nil || e.Error() != "ACCESS_DENIED" {
		t.Fatal(e)
	}
}

func TestAStoredLevelReachesAProcessThatDidNotSetIt(t *testing.T) {
	s, a := setup(t)
	ctx := context.Background()
	if _, e := s.UpdateAgentShed(ctx, a, "bulk", 1, "Fleet overload drill", uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	restarted := &recordingShed{}
	s.Shed = restarted
	s.SyncShed(ctx)
	if restarted.Shed() != agentadmission.ShedBulk {
		t.Fatalf("a restart came back at %s", restarted.Shed())
	}
}

func TestAHostLevelActsWithoutTheDatabaseAndIsStoredWhenItReturns(t *testing.T) {
	s, _ := setup(t)
	ctx := context.Background()
	shed := &recordingShed{}
	s.Shed = shed
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if stored := s.SetLocalShed(cancelled, agentadmission.ShedHeartbeats, "Database saturated"); stored {
		t.Fatal("a cancelled context reported the level stored")
	}
	if shed.Shed() != agentadmission.ShedHeartbeats {
		t.Fatal("the host level did not act at once")
	}
	var level string
	read := func() string {
		t.Helper()
		if e := s.DB.QueryRow(ctx, `SELECT shed_level FROM agent_pacing`).Scan(&level); e != nil {
			t.Fatal(e)
		}
		return level
	}
	if read() != "none" {
		t.Fatal("the level was stored by a cancelled call")
	}
	s.SyncShed(ctx)
	if read() != "heartbeats" || shed.Shed() != agentadmission.ShedHeartbeats {
		t.Fatalf("after a sync the database says %s and this process %s", level, shed.Shed())
	}
	var audited int
	if e := s.DB.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE action='node.agents_shed' AND audience='operator' AND reason='Database saturated' AND actor_user_id IS NULL`).Scan(&audited); e != nil || audited != 1 {
		t.Fatal(audited, e)
	}
}

var _ nodes.ShedSwitch = (*recordingShed)(nil)
