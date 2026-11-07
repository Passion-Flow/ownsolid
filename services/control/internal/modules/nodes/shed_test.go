package nodes_test

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"

	"github.com/Passion-Flow/ownsolid/pkg/operatorsocket"
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
	status, e := s.SetLocalShed(cancelled, operatorsocket.ShedRequest{Level: "heartbeats", Reason: "Database saturated"})
	if e != nil || status != (operatorsocket.ShedStatus{Level: "heartbeats", Stored: false}) {
		t.Fatal("a cancelled context reported the level stored:", status, e)
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
	if status = s.LocalShed(); !status.Stored {
		t.Fatal("a stored level is still reported as waiting")
	}
	var audited int
	if e = s.DB.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE action='node.agents_shed' AND audience='operator' AND reason='Database saturated' AND actor_user_id IS NULL`).Scan(&audited); e != nil || audited != 1 {
		t.Fatal(audited, e)
	}
}

func TestALevelHeldOnTheHostActsBeforeTheDatabaseAndClearsWhenStored(t *testing.T) {
	s, _ := setup(t)
	ctx := context.Background()
	shed := &recordingShed{}
	s.Shed = shed
	held := filepath.Join(t.TempDir(), "agent-shed.json")
	if e := operatorsocket.WriteHeld(held, operatorsocket.ShedRequest{Level: "heartbeats", Reason: "Held through an outage"}); e != nil {
		t.Fatal(e)
	}
	s.HoldShedAt(held)
	if shed.Shed() != agentadmission.ShedHeartbeats {
		t.Fatal("a restart let the fleet in before reading the held level")
	}
	if status := s.LocalShed(); status.Stored {
		t.Fatal("a held level was reported as stored")
	}
	s.SyncShed(ctx)
	if _, e := os.Stat(held); !errors.Is(e, fs.ErrNotExist) {
		t.Fatal("a stored level stayed on the host:", e)
	}
	var audited int
	if e := s.DB.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE action='node.agents_shed' AND audience='operator' AND reason='Held through an outage' AND actor_user_id IS NULL`).Scan(&audited); e != nil || audited != 1 {
		t.Fatal(audited, e)
	}
}

func TestALevelSetWhileTheDatabaseIsAwaySurvivesTheProcess(t *testing.T) {
	s, _ := setup(t)
	s.Shed = &recordingShed{}
	held := filepath.Join(t.TempDir(), "agent-shed.json")
	s.HoldShedAt(held)
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := s.SetLocalShed(cancelled, operatorsocket.ShedRequest{Level: "telemetry", Reason: "Database going down"}); e != nil {
		t.Fatal(e)
	}
	if got, found, e := operatorsocket.ReadHeld(held); !found || e != nil || got.Level != "telemetry" {
		t.Fatal(got, found, e)
	}
}

func TestADamagedHeldLevelLeavesTheStoredOneInForce(t *testing.T) {
	s, a := setup(t)
	ctx := context.Background()
	if _, e := s.UpdateAgentShed(ctx, a, "bulk", 1, "Fleet overload drill", uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	shed := &recordingShed{}
	s.Shed = shed
	held := filepath.Join(t.TempDir(), "agent-shed.json")
	if e := os.WriteFile(held, []byte("damaged"), 0o600); e != nil {
		t.Fatal(e)
	}
	s.HoldShedAt(held)
	s.SyncShed(ctx)
	if shed.Shed() != agentadmission.ShedBulk {
		t.Fatalf("this process sheds %s", shed.Shed())
	}
}

func TestEveryLevelTheHostCanNameIsOneAdmissionKnows(t *testing.T) {
	for i, name := range operatorsocket.Levels {
		level, e := agentadmission.ParseShedLevel(name)
		if e != nil || int(level) != i {
			t.Fatal(name, level, e)
		}
	}
	if agentadmission.ShedHeartbeats.String() != operatorsocket.Levels[len(operatorsocket.Levels)-1] {
		t.Fatal("admission has a level the host cannot name")
	}
}

func TestTheHostRefusesWhatAnAdministratorWouldBeRefused(t *testing.T) {
	s, _ := setup(t)
	shed := &recordingShed{}
	s.Shed = shed
	ctx := context.Background()
	for _, r := range []operatorsocket.ShedRequest{{Level: "everything", Reason: "Drill"}, {Level: "bulk", Reason: "  "}} {
		if _, e := s.SetLocalShed(ctx, r); e == nil || e.Error() != "VALIDATION_FAILED" {
			t.Fatal(r, e)
		}
	}
	if shed.Shed() != agentadmission.ShedNone {
		t.Fatal("a refused request reached admission")
	}
}

var (
	_ nodes.ShedSwitch       = (*recordingShed)(nil)
	_ operatorsocket.Control = (*nodes.Service)(nil)
)
