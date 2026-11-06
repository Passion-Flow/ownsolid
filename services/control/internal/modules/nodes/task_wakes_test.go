package nodes_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
)

func openSeat(context.Context) (func(), error) { return func() {}, nil }

// claimed runs a claim held open on s and reports its result on the returned channel.
func claimed(s *nodes.Service, node wire.Enrolled, wait int) <-chan wire.Claim {
	out := make(chan wire.Claim, 1)
	go func() {
		claim, e := s.ClaimWaiting(context.Background(), node.NodeID, node.Credential, uuid.NewString(), wire.ClaimRequest{WaitSeconds: wait}, openSeat)
		if e != nil {
			claim = wire.Claim{}
		}
		out <- claim
	}()
	return out
}

// secondGateway is another control process on the same database. It shares nothing in memory with
// the service that wrote the task, as a separate replica would not.
func secondGateway(t *testing.T, s *nodes.Service) (*nodes.Service, func()) {
	t.Helper()
	other := &nodes.Service{DB: s.DB, Identity: s.Identity, Workspaces: &workspaces.Service{DB: s.DB, Identity: s.Identity}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		other.RunWakes(ctx)
	}()
	waitUntil(t, "the reader to start", func() bool { return other.WakesDelivering() })
	return other, func() { cancel(); <-done }
}

func waitUntil(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if ok() {
			return
		}
	}
	t.Fatal("timed out waiting for", what)
}

// waitForHeldClaim gives a claim time to find no work and start waiting.
const waitForHeldClaim = 300 * time.Millisecond

func TestATaskWrittenOnOneGatewayWakesAClaimHeldOnAnother(t *testing.T) {
	s, a := setup(t)
	node := enroll(t, s, a, "Woken across gateways")
	other, stop := secondGateway(t, s)
	defer stop()
	held := claimed(other, node, 30)
	time.Sleep(waitForHeldClaim)
	started := time.Now()
	if _, e := s.CreateTask(context.Background(), a, node.NodeID, wire.CollectFacts, uuid.NewString(), uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	select {
	case claim := <-held:
		if claim.Lease == nil {
			t.Fatalf("claim ended without the task: %+v", claim)
		}
		// Under the 30 second wait, with room for a moment's hold on the watermark.
		if took := time.Since(started); took > 15*time.Second {
			t.Fatalf("woken after %v", took)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("a claim on the other gateway was not woken")
	}
}

func TestARolledBackTaskWakesNobody(t *testing.T) {
	s, a := setup(t)
	node := enroll(t, s, a, "Not woken")
	other, stop := secondGateway(t, s)
	defer stop()
	ctx := context.Background()
	before := wakeEvents(other)
	tx, e := s.DB.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = tx.Exec(ctx, `INSERT INTO control_outbox(topic,key) VALUES('node.task_written',$1)`, node.NodeID); e != nil {
		t.Fatal(e)
	}
	if e = tx.Rollback(ctx); e != nil {
		t.Fatal(e)
	}
	time.Sleep(600 * time.Millisecond)
	if got := wakeEvents(other); got != before {
		t.Fatalf("read %v events of a rollback", got-before)
	}
}

// Agents reach the database only through control, and a held claim costs it nothing: more claims than
// the pool has connections wait at once, holding none of them (ARC-012).
func TestHeldClaimsHoldNoDatabaseConnection(t *testing.T) {
	s, a := setup(t)
	node := enroll(t, s, a, "Patient node")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	held := int(s.DB.Config().MaxConns) + 5
	finished := make(chan error, held)
	for range held {
		go func() {
			_, e := s.ClaimWaiting(ctx, node.NodeID, node.Credential, uuid.NewString(), wire.ClaimRequest{WaitSeconds: 30}, openSeat)
			finished <- e
		}()
	}
	waitUntil(t, "every claim to find no work", func() bool { return s.DB.Stat().AcquiredConns() == 0 && s.WatchedNodes() == 1 })
	time.Sleep(waitForHeldClaim)
	if acquired := s.DB.Stat().AcquiredConns(); acquired != 0 {
		t.Fatalf("%d held claims hold %d connections", held, acquired)
	}
	if _, e := s.CreateTask(context.Background(), a, node.NodeID, wire.CollectFacts, uuid.NewString(), uuid.NewString()); e != nil {
		t.Fatal("the pool was not free for other work:", e)
	}
	cancel()
	for range held {
		if e := <-finished; e != nil && e.Error() != "NODE_BUSY" {
			t.Log(e)
		}
	}
}

func TestAClaimStillFindsItsTaskWithoutAWake(t *testing.T) {
	s, a := setup(t)
	ctx := context.Background()
	node := enroll(t, s, a, "Late wake")
	// No reader runs here, and this write's wake fired before any claim watched.
	if _, e := s.CreateTask(ctx, a, node.NodeID, wire.CollectFacts, uuid.NewString(), uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	select {
	case claim := <-claimed(s, node, 30):
		if claim.Lease == nil {
			t.Fatalf("%+v", claim)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a claim did not look at the task table first")
	}
}

func TestAHeldClaimEndsAtItsWaitWhenDeliveryIsHeldUp(t *testing.T) {
	s, a := setup(t)
	ctx := context.Background()
	node := enroll(t, s, a, "Held back")
	other, stop := secondGateway(t, s)
	defer stop()
	// A long write transaction holds the watermark, so the wake cannot be read yet.
	holder, e := s.DB.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer holder.Rollback(ctx)
	if _, e = holder.Exec(ctx, `SELECT pg_current_xact_id()`); e != nil {
		t.Fatal(e)
	}
	held := claimed(other, node, 2)
	time.Sleep(waitForHeldClaim)
	if _, e = s.CreateTask(ctx, a, node.NodeID, wire.CollectFacts, uuid.NewString(), uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	select {
	case claim := <-held:
		if claim.Lease != nil {
			t.Fatal("the wake passed a running transaction")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("a held claim outlived its wait")
	}
	// The agent claims again, and the task is there whatever delivery did.
	claim, e := other.Claim(ctx, node.NodeID, node.Credential, uuid.NewString())
	if e != nil || claim.Lease == nil {
		t.Fatal(claim, e)
	}
	before := wakeEvents(other)
	if e = holder.Rollback(ctx); e != nil {
		t.Fatal(e)
	}
	for deadline := time.Now().Add(20 * time.Second); wakeEvents(other) <= before; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("delivery did not resume")
		}
	}
}

func TestSweepSettlesAnExpiredTaskOfASilentNodeAndTrimsTheOutbox(t *testing.T) {
	s, a := setup(t)
	ctx := context.Background()
	node := enroll(t, s, a, "Silent")
	task, e := s.CreateTask(ctx, a, node.NodeID, wire.CollectFacts, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.DB.Exec(ctx, `UPDATE node_tasks SET deadline_at=clock_timestamp()-interval '1 minute' WHERE id=$1`, task.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = s.DB.Exec(ctx, `UPDATE control_outbox SET created_at=clock_timestamp()-interval '1 day'`); e != nil {
		t.Fatal(e)
	}
	runOnePass(t, s)
	var state string
	var remaining int
	if e = s.DB.QueryRow(ctx, `SELECT state FROM node_tasks WHERE id=$1`, task.ID).Scan(&state); e != nil || state == "pending" {
		t.Fatal(state, e)
	}
	if e = s.DB.QueryRow(ctx, `SELECT count(*) FROM control_outbox WHERE created_at<clock_timestamp()-interval '1 hour'`).Scan(&remaining); e != nil || remaining != 0 {
		t.Fatal(remaining, e)
	}
}

// runOnePass runs the reconciliation loop until it has made one pass.
func runOnePass(t *testing.T, s *nodes.Service) {
	t.Helper()
	policy := config.DefaultRuntimePolicy()
	policy.Nodes.ReconcileSeconds = 1
	s.Identity.Config.Runtime = &policy
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	s.Run(ctx)
}

func wakeEvents(s *nodes.Service) float64 {
	for _, v := range s.WakeSamples() {
		if v.Name == "ownsolid_node_task_wake_events_total" {
			return v.Value
		}
	}
	return -1
}
