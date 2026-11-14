package nodes_test

import (
	"context"
	"sync"
	"testing"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/google/uuid"
)

func TestARC007AgentsAreToldTheCurrentPacing(t *testing.T) {
	s, a := setup(t)
	ctx := context.Background()
	node := enroll(t, s, a, "Paced node")
	if node.HeartbeatSeconds != wire.HeartbeatSeconds {
		t.Fatalf("enrolled at %d", node.HeartbeatSeconds)
	}
	beat := func() wire.Pacing {
		t.Helper()
		d, e := s.Heartbeat(ctx, node.NodeID, node.Credential, handshake())
		if e != nil || d.Pacing == nil {
			t.Fatal("heartbeat", d, e)
		}
		return *d.Pacing
	}
	if got := beat(); got != wire.DefaultPacing() {
		t.Fatalf("seeded pacing %+v", got)
	}
	stored, e := s.AgentPacing(ctx, a)
	if e != nil || stored.Pacing != wire.DefaultPacing() || stored.Version != 1 || stored.UpdatedAt.IsZero() {
		t.Fatal("stored pacing", stored, e)
	}
	slow := wire.Pacing{HeartbeatSeconds: 15, JitterPercent: 20, ReconnectBaseSeconds: 5, ReconnectMaxSeconds: 300}
	updated, e := s.UpdateAgentPacing(ctx, a, slow, 1, "Reducing load during a maintenance window", uuid.NewString())
	if e != nil || updated.Pacing != slow || updated.Version != 2 {
		t.Fatal("update", updated, e)
	}
	// The very next reply carries the change, with no restart.
	if got := beat(); got != slow {
		t.Fatalf("after the change agents are told %+v", got)
	}
	if again, e := s.AgentPacing(ctx, a); e != nil || again.Pacing != slow || again.Version != 2 {
		t.Fatal("read after update", again, e)
	}
	late := enroll(t, s, a, "Paced late")
	if late.HeartbeatSeconds != slow.HeartbeatSeconds {
		t.Fatalf("a node enrolled after the change starts at %d", late.HeartbeatSeconds)
	}
	var audits int
	if e = s.DB.QueryRow(ctx, "SELECT count(*) FROM audit_events WHERE action='node.pacing_updated' AND actor_user_id=$1 AND outcome='success' AND reason IS NOT NULL", a.Me.User.ID).Scan(&audits); e != nil || audits != 1 {
		t.Fatal("audit", audits, e)
	}
}

func TestARC007UpdateIsRefusedOutsideItsBounds(t *testing.T) {
	s, a := setup(t)
	ctx := context.Background()
	valid := wire.DefaultPacing()
	code := func(e error) string {
		if e == nil {
			return ""
		}
		return e.Error()
	}
	const reason = "Testing the bounds"
	for name, next := range map[string]wire.Pacing{
		"zero":                {},
		"heartbeat too short": {HeartbeatSeconds: 4, ReconnectBaseSeconds: 1, ReconnectMaxSeconds: 60},
		"reconnect inverted":  {HeartbeatSeconds: 10, ReconnectBaseSeconds: 60, ReconnectMaxSeconds: 1},
	} {
		if _, e := s.UpdateAgentPacing(ctx, a, next, 1, reason, uuid.NewString()); code(e) != "VALIDATION_FAILED" {
			t.Errorf("%s: %v", name, e)
		}
	}
	if _, e := s.UpdateAgentPacing(ctx, a, valid, 1, "", uuid.NewString()); code(e) != "VALIDATION_FAILED" {
		t.Errorf("no reason: %v", e)
	}
	if _, e := s.UpdateAgentPacing(ctx, a, valid, 0, reason, uuid.NewString()); code(e) != "VALIDATION_FAILED" {
		t.Errorf("no version: %v", e)
	}
	// Valid for the protocol, but healthy nodes would look offline and lose inference.
	window := s.Identity.Config.Policy().Nodes.OnlineSeconds
	tooSlow := wire.Pacing{HeartbeatSeconds: window, ReconnectBaseSeconds: 1, ReconnectMaxSeconds: 60}
	if _, e := s.UpdateAgentPacing(ctx, a, tooSlow, 1, reason, uuid.NewString()); code(e) != "AGENT_PACING_EXCEEDS_ONLINE_WINDOW" {
		t.Errorf("beyond the online window of %d: %v", window, e)
	}
	if got, e := s.AgentPacing(ctx, a); e != nil || got.Version != 1 || got.Pacing != valid {
		t.Fatal("a refused update changed the row", got, e)
	}
	var audits int
	if e := s.DB.QueryRow(ctx, "SELECT count(*) FROM audit_events WHERE action='node.pacing_updated'").Scan(&audits); e != nil || audits != 0 {
		t.Fatal("a refused update was audited", audits, e)
	}
}

func TestARC007TheDatabaseRefusesPacingOutsideTheProtocol(t *testing.T) {
	s, _ := setup(t)
	ctx := context.Background()
	for _, set := range []string{"heartbeat_seconds=4", "heartbeat_seconds=121", "jitter_percent=51", "reconnect_base_seconds=0", "reconnect_max_seconds=0", "reconnect_max_seconds=901", "version=0"} {
		if _, e := s.DB.Exec(ctx, "UPDATE agent_pacing SET "+set); e == nil {
			t.Errorf("%s accepted", set)
		}
	}
	if _, e := s.DB.Exec(ctx, "INSERT INTO agent_pacing(heartbeat_seconds,jitter_percent,reconnect_base_seconds,reconnect_max_seconds,version) VALUES(10,10,1,60,1)"); e == nil {
		t.Error("a second row for the cell accepted")
	}
}

func TestARC007OnlyAPlatformAdministratorSeesOrChangesPacing(t *testing.T) {
	s, a := setup(t)
	ctx := context.Background()
	for name, denied := range map[string]identity.Auth{
		"portal session": func() identity.Auth { d := a; d.Me.Audience = "portal"; return d }(),
		"not a platform admin": func() identity.Auth {
			d := a
			d.Me.User.IsPlatformAdmin = false
			return d
		}(),
	} {
		if _, e := s.AgentPacing(ctx, denied); e == nil || e.Error() != "ACCESS_DENIED" {
			t.Errorf("%s read: %v", name, e)
		}
		if _, e := s.UpdateAgentPacing(ctx, denied, wire.DefaultPacing(), 1, "Trying without rights", uuid.NewString()); e == nil || e.Error() != "ACCESS_DENIED" {
			t.Errorf("%s update: %v", name, e)
		}
	}
	if got, e := s.AgentPacing(ctx, a); e != nil || got.Version != 1 {
		t.Fatal("a denied update changed the row", got, e)
	}
}

// Two administrators editing the same version: exactly one wins, and the other reloads.
func TestARC007ConcurrentUpdatesConflict(t *testing.T) {
	s, a := setup(t)
	ctx := context.Background()
	const writers = 8
	results := make([]error, writers)
	var wg sync.WaitGroup
	for i := range writers {
		wg.Go(func() {
			next := wire.DefaultPacing()
			next.HeartbeatSeconds = 5 + i
			_, results[i] = s.UpdateAgentPacing(ctx, a, next, 1, "Concurrent change", uuid.NewString())
		})
	}
	wg.Wait()
	won := 0
	for _, e := range results {
		switch {
		case e == nil:
			won++
		case e.Error() != "VERSION_CONFLICT":
			t.Errorf("unexpected failure %v", e)
		}
	}
	got, e := s.AgentPacing(ctx, a)
	if e != nil || won != 1 || got.Version != 2 {
		t.Fatal("winners", won, got, e)
	}
}

// The online window was lowered after the row was written, so the row no longer fits it.
func TestARC007APacingRowOutsideTheOnlineWindowFallsBackToTheDefault(t *testing.T) {
	s, a := setup(t)
	ctx := context.Background()
	node := enroll(t, s, a, "Paced fallback")
	if _, e := s.DB.Exec(ctx, "UPDATE agent_pacing SET heartbeat_seconds=120,jitter_percent=50"); e != nil {
		t.Fatal(e)
	}
	fresh := nodes.Service{DB: s.DB, Identity: s.Identity, Workspaces: s.Workspaces}
	d, e := fresh.Heartbeat(ctx, node.NodeID, node.Credential, handshake())
	if e != nil || d.Pacing == nil || *d.Pacing != wire.DefaultPacing() {
		t.Fatal("a pacing that would mark nodes offline reached agents", d, e)
	}
}

// A change made through another control process reaches this one within the interval.
func TestARC007AnotherProcessSeesTheChangeAfterTheRefreshInterval(t *testing.T) {
	s, a := setup(t)
	ctx := context.Background()
	node := enroll(t, s, a, "Paced replica")
	replica := &nodes.Service{DB: s.DB, Identity: s.Identity, Workspaces: s.Workspaces}
	beat := func() wire.Pacing {
		t.Helper()
		d, e := replica.Heartbeat(ctx, node.NodeID, node.Credential, handshake())
		if e != nil || d.Pacing == nil {
			t.Fatal("heartbeat", d, e)
		}
		return *d.Pacing
	}
	if got := beat(); got != wire.DefaultPacing() {
		t.Fatalf("seeded pacing %+v", got)
	}
	slow := wire.Pacing{HeartbeatSeconds: 15, JitterPercent: 5, ReconnectBaseSeconds: 2, ReconnectMaxSeconds: 120}
	if _, e := s.UpdateAgentPacing(ctx, a, slow, 1, "Changing pacing on another replica", uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	deadline := time.Now().Add(2 * nodes.PacingRefreshInterval)
	for beat() != slow {
		if time.Now().After(deadline) {
			t.Fatalf("still told %+v after %s", beat(), 2*nodes.PacingRefreshInterval)
		}
		time.Sleep(100 * time.Millisecond)
	}
}
