package nodes_test

import (
	"context"
	"testing"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
)

// viewedHeartbeats starts the outbox reader and warms the node's view.
func viewedHeartbeats(t *testing.T) (*nodes.Service, wire.Enrolled) {
	t.Helper()
	s, node := liveHeartbeats(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.RunWakes(ctx)
	}()
	t.Cleanup(func() { cancel(); <-done })
	waitUntil(t, "the cache to open", s.ViewsOpen)
	waitUntil(t, "the view to answer", func() bool {
		hits := s.ViewHits()
		if _, e := s.Heartbeat(context.Background(), node.NodeID, node.Credential, handshake()); e != nil {
			t.Fatal(e)
		}
		return s.ViewHits() > hits
	})
	return s, node
}

func TestARepeatedHeartbeatRunsNoStatement(t *testing.T) {
	s, node := viewedHeartbeats(t)
	ctx := context.Background()
	// Holding the table proves it: any statement on it would wait out its lock timeout.
	lock, e := s.DB.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer lock.Rollback(ctx)
	if _, e = lock.Exec(ctx, `LOCK TABLE managed_nodes IN ACCESS EXCLUSIVE MODE`); e != nil {
		t.Fatal(e)
	}
	bounded, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	out, e := s.Heartbeat(bounded, node.NodeID, node.Credential, handshake())
	if e != nil || out.Status != "active" {
		t.Fatal(out, e)
	}
}

func TestAWrongCredentialIsNotAnsweredFromTheView(t *testing.T) {
	s, node := viewedHeartbeats(t)
	_, e := s.Heartbeat(context.Background(), node.NodeID, security.Token(), handshake())
	expected(t, e, "AGENT_UNAUTHORIZED")
}

func TestAChangeReachesTheNextHeartbeatWithinAPoll(t *testing.T) {
	const scope = `(SELECT workspace_id FROM managed_nodes WHERE id=$1)`
	for name, c := range map[string]struct {
		change, status, code string
	}{
		"a disabled node":       {change: `UPDATE managed_nodes SET status='disabled' WHERE id=$1`, status: "disabled"},
		"a removed node":        {change: `UPDATE managed_nodes SET status='removed',credential_hash=NULL WHERE id=$1`, code: "AGENT_UNAUTHORIZED"},
		"a replaced credential": {change: `UPDATE managed_nodes SET credential_hash=sha256('replaced') WHERE id=$1`, code: "AGENT_UNAUTHORIZED"},
		"a disabled workspace":  {change: `UPDATE workspaces SET status='disabled' WHERE id=` + scope, status: "disabled"},
		"a disabled tenant":     {change: `UPDATE tenants SET status='disabled' WHERE id=(SELECT tenant_id FROM managed_nodes WHERE id=$1)`, code: "AGENT_UNAUTHORIZED"},
	} {
		t.Run(name, func(t *testing.T) {
			s, node := viewedHeartbeats(t)
			ctx := context.Background()
			before := s.ViewInvalidations()
			if _, e := s.DB.Exec(ctx, c.change, node.NodeID); e != nil {
				t.Fatal(e)
			}
			started := time.Now()
			waitUntil(t, "the event to arrive", func() bool { return s.ViewInvalidations() > before })
			if took := time.Since(started); took > 2*time.Second {
				t.Fatalf("the view outlived the change by %s", took)
			}
			out, e := s.Heartbeat(ctx, node.NodeID, node.Credential, handshake())
			if c.code != "" {
				expected(t, e, c.code)
				return
			}
			if e != nil || out.Status != c.status {
				t.Fatal(out, e)
			}
		})
	}
}

func TestOnlyAChangeOfTheViewReachesTheOutbox(t *testing.T) {
	s, node := liveHeartbeats(t)
	ctx := context.Background()
	count := func() int {
		var n int
		if e := s.DB.QueryRow(ctx, `SELECT count(*) FROM control_outbox WHERE topic IN ('node.view_changed','node.views_changed')`).Scan(&n); e != nil {
			t.Fatal(e)
		}
		return n
	}
	before := count()
	for _, repeat := range []string{
		`UPDATE managed_nodes SET last_heartbeat_at=clock_timestamp(),updated_at=clock_timestamp() WHERE id=$1`,
		`UPDATE managed_nodes SET status=status,protocol_version=protocol_version,capabilities=capabilities WHERE id=$1`,
		`UPDATE workspaces SET status=status WHERE id=(SELECT workspace_id FROM managed_nodes WHERE id=$1)`,
		`UPDATE tenants SET status=status WHERE id=(SELECT tenant_id FROM managed_nodes WHERE id=$1)`,
	} {
		if _, e := s.DB.Exec(ctx, repeat, node.NodeID); e != nil {
			t.Fatal(repeat, e)
		}
	}
	if got := count(); got != before {
		t.Fatalf("%d events from writes that changed no view", got-before)
	}
	if _, e := s.DB.Exec(ctx, `UPDATE managed_nodes SET status='disabled' WHERE id=$1`, node.NodeID); e != nil {
		t.Fatal(e)
	}
	if got := count(); got != before+1 {
		t.Fatalf("%d events from one change", got-before)
	}
}

func TestARollbackAnnouncesNothing(t *testing.T) {
	s, node := viewedHeartbeats(t)
	ctx := context.Background()
	tx, e := s.DB.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = tx.Exec(ctx, `UPDATE managed_nodes SET status='disabled' WHERE id=$1`, node.NodeID); e != nil {
		t.Fatal(e)
	}
	if e = tx.Rollback(ctx); e != nil {
		t.Fatal(e)
	}
	var n int
	if e = s.DB.QueryRow(ctx, `SELECT count(*) FROM control_outbox WHERE topic='node.view_changed' AND key=$1`, node.NodeID).Scan(&n); e != nil || n != 0 {
		t.Fatal(n, e)
	}
}

func TestAHeldClaimEndsWhenItsNodeChanges(t *testing.T) {
	s, node := viewedHeartbeats(t)
	held := claimed(s, node, 30)
	waitUntil(t, "the claim to wait", func() bool { return s.WatchedNodes() == 1 })
	time.Sleep(waitForHeldClaim)
	if _, e := s.DB.Exec(context.Background(), `UPDATE managed_nodes SET status='disabled' WHERE id=$1`, node.NodeID); e != nil {
		t.Fatal(e)
	}
	select {
	case claim := <-held:
		if claim.Lease != nil {
			t.Fatal("a disabled node got a lease", claim)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the claim held on after its node changed")
	}
}

func TestStoppingTheReaderClosesTheCache(t *testing.T) {
	s, node := liveHeartbeats(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.RunWakes(ctx)
	}()
	waitUntil(t, "the cache to open", s.ViewsOpen)
	cancel()
	<-done
	if s.ViewsOpen() {
		t.Fatal("a cache with no reader stayed open")
	}
	hits := s.ViewHits()
	if _, e := s.Heartbeat(context.Background(), node.NodeID, node.Credential, handshake()); e != nil {
		t.Fatal(e)
	}
	if s.ViewHits() != hits {
		t.Fatal("a closed cache answered a heartbeat")
	}
}
