package nodes_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/liveness"
)

// nodeRow is what a heartbeat could have written.
type nodeRow struct {
	xmin                string
	lastSeen, updatedAt time.Time
	facts               string
	connectivity        string
	changedAt           time.Time
}

func readNode(t *testing.T, s *nodes.Service, id string) nodeRow {
	t.Helper()
	var r nodeRow
	if e := s.DB.QueryRow(context.Background(), `SELECT xmin::text,last_heartbeat_at,updated_at,facts::text,connectivity,connectivity_changed_at FROM managed_nodes WHERE id=$1`, id).Scan(&r.xmin, &r.lastSeen, &r.updatedAt, &r.facts, &r.connectivity, &r.changedAt); e != nil {
		t.Fatal(e)
	}
	return r
}

func liveHeartbeats(t *testing.T) (*nodes.Service, wire.Enrolled) {
	t.Helper()
	s, a := setup(t)
	node := enroll(t, s, a, "Liveness node")
	if e := s.EnableLiveness(context.Background()); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Heartbeat(context.Background(), node.NodeID, node.Credential, handshake()); e != nil {
		t.Fatal(e)
	}
	return s, node
}

func TestUnchangedHeartbeatWritesNothingUntilTheFlush(t *testing.T) {
	s, node := liveHeartbeats(t)
	ctx := context.Background()
	before := readNode(t, s, node.NodeID)
	h := handshake()
	h.Facts.ObservedAt = h.Facts.ObservedAt.Add(time.Second)
	h.Facts.DiskAvailableBytes--
	out, e := s.Heartbeat(ctx, node.NodeID, node.Credential, h)
	if e != nil || out.Status != "active" || out.ServerTime.IsZero() || out.Pacing == nil {
		t.Fatal(out, e)
	}
	if after := readNode(t, s, node.NodeID); after.xmin != before.xmin {
		t.Fatalf("an unchanged heartbeat wrote the row: %+v then %+v", before, after)
	}
	if e = s.FlushLiveness(ctx); e != nil {
		t.Fatal(e)
	}
	after := readNode(t, s, node.NodeID)
	if !after.lastSeen.Equal(out.ServerTime) {
		t.Fatalf("flushed %v, heartbeat was %v", after.lastSeen, out.ServerTime)
	}
	if !after.updatedAt.Equal(before.updatedAt) || after.facts != before.facts {
		t.Fatalf("a batched last-seen changed the node: %+v then %+v", before, after)
	}
	// Nothing is pending, so a second pass writes nothing.
	if e = s.FlushLiveness(ctx); e != nil {
		t.Fatal(e)
	}
	if again := readNode(t, s, node.NodeID); again.xmin != after.xmin {
		t.Fatal("an empty flush wrote the row")
	}
}

func TestChangedStateIsWrittenSynchronously(t *testing.T) {
	s, node := liveHeartbeats(t)
	ctx := context.Background()
	before := readNode(t, s, node.NodeID)
	h := handshake()
	h.Facts.Hostname = "renamed-node"
	if _, e := s.Heartbeat(ctx, node.NodeID, node.Credential, h); e != nil {
		t.Fatal(e)
	}
	after := readNode(t, s, node.NodeID)
	if after.xmin == before.xmin || !after.lastSeen.After(before.lastSeen) || after.updatedAt.Equal(before.updatedAt) {
		t.Fatalf("a changed heartbeat was not written: %+v then %+v", before, after)
	}
	// The new state is now the one a repeat can skip.
	if _, e := s.Heartbeat(ctx, node.NodeID, node.Credential, h); e != nil {
		t.Fatal(e)
	}
	if again := readNode(t, s, node.NodeID); again.xmin != after.xmin {
		t.Fatal("the repeat of a new state wrote the row")
	}
}

func TestFastPathRefusesWhatTheSynchronousPathRefuses(t *testing.T) {
	s, node := liveHeartbeats(t)
	ctx := context.Background()
	_, e := s.Heartbeat(ctx, node.NodeID, "bad", handshake())
	expected(t, e, "AGENT_UNAUTHORIZED")
	_, e = s.Heartbeat(ctx, node.NodeID, security.Token(), handshake())
	expected(t, e, "AGENT_UNAUTHORIZED")

	// The server changed the node: the read sees it and the heartbeat takes the full path.
	if _, e = s.DB.Exec(ctx, `UPDATE managed_nodes SET status='disabled' WHERE id=$1`, node.NodeID); e != nil {
		t.Fatal(e)
	}
	out, e := s.Heartbeat(ctx, node.NodeID, node.Credential, handshake())
	if e != nil || out.Status != "disabled" {
		t.Fatal(out, e)
	}
	if _, e = s.DB.Exec(ctx, `UPDATE managed_nodes SET status='active' WHERE id=$1`, node.NodeID); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Heartbeat(ctx, node.NodeID, node.Credential, handshake()); e != nil {
		t.Fatal(e)
	}
	if _, e = s.DB.Exec(ctx, `UPDATE workspaces SET status='disabled' WHERE id=(SELECT workspace_id FROM managed_nodes WHERE id=$1)`, node.NodeID); e != nil {
		t.Fatal(e)
	}
	out, e = s.Heartbeat(ctx, node.NodeID, node.Credential, handshake())
	if e != nil || out.Status != "disabled" {
		t.Fatal(out, e)
	}
	if _, e = s.DB.Exec(ctx, `UPDATE workspaces SET status='active' WHERE id=(SELECT workspace_id FROM managed_nodes WHERE id=$1)`, node.NodeID); e != nil {
		t.Fatal(e)
	}
	if _, e = s.DB.Exec(ctx, `UPDATE managed_nodes SET status='removed',credential_hash=NULL WHERE id=$1`, node.NodeID); e != nil {
		t.Fatal(e)
	}
	_, e = s.Heartbeat(ctx, node.NodeID, node.Credential, handshake())
	expected(t, e, "AGENT_UNAUTHORIZED")
}

func TestStateRefreshForcesTheSynchronousPath(t *testing.T) {
	s, node := liveHeartbeats(t)
	ctx := context.Background()
	policy := config.DefaultRuntimePolicy()
	policy.Nodes.StateRefreshSeconds = 0
	s.Identity.Config.Runtime = &policy
	before := readNode(t, s, node.NodeID)
	if _, e := s.Heartbeat(ctx, node.NodeID, node.Credential, handshake()); e != nil {
		t.Fatal(e)
	}
	if after := readNode(t, s, node.NodeID); after.xmin == before.xmin {
		t.Fatal("a state past its refresh was not written")
	}
}

func TestWithoutTheWorkerEveryHeartbeatWrites(t *testing.T) {
	s, a := setup(t)
	node := enroll(t, s, a, "No worker")
	ctx := context.Background()
	if _, e := s.Heartbeat(ctx, node.NodeID, node.Credential, handshake()); e != nil {
		t.Fatal(e)
	}
	before := readNode(t, s, node.NodeID)
	if _, e := s.Heartbeat(ctx, node.NodeID, node.Credential, handshake()); e != nil {
		t.Fatal(e)
	}
	if readNode(t, s, node.NodeID).xmin == before.xmin {
		t.Fatal("nothing would persist a heartbeat that skipped its write")
	}
}

// recordCommitModes returns a reader of the commit mode of each transaction that writes a node
// row since this call, oldest first. It asks the database itself, not the code under test.
func recordCommitModes(t *testing.T, s *nodes.Service) func() []string {
	t.Helper()
	ctx := context.Background()
	for _, statement := range []string{
		`CREATE TABLE commit_modes(id bigserial PRIMARY KEY, mode text NOT NULL)`,
		`CREATE FUNCTION record_commit_mode() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN INSERT INTO commit_modes(mode) VALUES(current_setting('synchronous_commit')); RETURN NEW; END $$`,
		`CREATE TRIGGER record_commit_mode AFTER UPDATE ON managed_nodes FOR EACH ROW EXECUTE FUNCTION record_commit_mode()`,
	} {
		if _, e := s.DB.Exec(ctx, statement); e != nil {
			t.Fatal(e)
		}
	}
	return func() []string {
		t.Helper()
		rows, e := s.DB.Query(ctx, `SELECT mode FROM commit_modes ORDER BY id`)
		if e != nil {
			t.Fatal(e)
		}
		defer rows.Close()
		var modes []string
		for rows.Next() {
			var mode string
			if e = rows.Scan(&mode); e != nil {
				t.Fatal(e)
			}
			modes = append(modes, mode)
		}
		if e = rows.Err(); e != nil {
			t.Fatal(e)
		}
		return modes
	}
}

func TestOnlyARepeatedStateCommitsWithoutWaitingForTheDisk(t *testing.T) {
	s, node := liveHeartbeats(t)
	ctx := context.Background()
	modes := recordCommitModes(t, s)
	policy := config.DefaultRuntimePolicy()
	policy.Nodes.StateRefreshSeconds = 0
	s.Identity.Config.Runtime = &policy

	if _, e := s.Heartbeat(ctx, node.NodeID, node.Credential, handshake()); e != nil {
		t.Fatal(e)
	}
	if got := modes(); len(got) != 1 || got[0] != "off" {
		t.Fatalf("a refresh of the same state committed in modes %v", got)
	}
	changed := handshake()
	changed.Facts.Hostname = "renamed-node"
	if _, e := s.Heartbeat(ctx, node.NodeID, node.Credential, changed); e != nil {
		t.Fatal(e)
	}
	if got := modes(); len(got) != 2 || got[1] != "on" {
		t.Fatalf("a change of state committed in modes %v", got)
	}
}

func TestAFlushOfSightingsCommitsWithoutWaitingForTheDisk(t *testing.T) {
	s, node := liveHeartbeats(t)
	ctx := context.Background()
	if e := s.FlushLiveness(ctx); e != nil {
		t.Fatal(e)
	}
	modes := recordCommitModes(t, s)
	if _, e := s.Heartbeat(ctx, node.NodeID, node.Credential, handshake()); e != nil {
		t.Fatal(e)
	}
	if e := s.FlushLiveness(ctx); e != nil {
		t.Fatal(e)
	}
	if got := modes(); len(got) != 1 || got[0] != "off" {
		t.Fatalf("a flush of sightings committed in modes %v", got)
	}
}

func TestAConnectivityChangeWaitsForTheDisk(t *testing.T) {
	s, a := setup(t)
	ctx := context.Background()
	node := enroll(t, s, a, "Silent node")
	if _, e := s.DB.Exec(ctx, `UPDATE managed_nodes SET last_heartbeat_at=clock_timestamp()-interval '1 hour' WHERE id=$1`, node.NodeID); e != nil {
		t.Fatal(e)
	}
	modes := recordCommitModes(t, s)
	if e := s.EnableLivenessStartedAt(ctx, time.Now().Add(-time.Hour)); e != nil {
		t.Fatal(e)
	}
	if e := s.FlushLiveness(ctx); e != nil {
		t.Fatal(e)
	}
	if connectivity := readNode(t, s, node.NodeID).connectivity; connectivity != "offline" {
		t.Fatalf("connectivity %q", connectivity)
	}
	if got := modes(); len(got) == 0 || got[len(got)-1] != "on" {
		t.Fatalf("a connectivity change committed in modes %v", got)
	}
}

func TestFlushSkipsARowAnotherTransactionHolds(t *testing.T) {
	s, node := liveHeartbeats(t)
	ctx := context.Background()
	holder, e := s.DB.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer holder.Rollback(ctx)
	if _, e = holder.Exec(ctx, `SELECT 1 FROM managed_nodes WHERE id=$1 FOR UPDATE`, node.NodeID); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Heartbeat(ctx, node.NodeID, node.Credential, handshake()); e != nil {
		t.Fatal(e)
	}
	bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if e = s.FlushLiveness(bounded); e != nil {
		t.Fatal("the flush waited for a lock:", e)
	}
	if e = holder.Rollback(ctx); e != nil {
		t.Fatal(e)
	}
	out, e := s.Heartbeat(ctx, node.NodeID, node.Credential, handshake())
	if e != nil {
		t.Fatal(e)
	}
	if e = s.FlushLiveness(ctx); e != nil {
		t.Fatal(e)
	}
	if got := readNode(t, s, node.NodeID); !got.lastSeen.Equal(out.ServerTime) {
		t.Fatalf("last seen %v, heartbeat %v", got.lastSeen, out.ServerTime)
	}
}

func TestFailedFlushKeepsSightingsForTheNextPass(t *testing.T) {
	s, node := liveHeartbeats(t)
	out, e := s.Heartbeat(context.Background(), node.NodeID, node.Credential, handshake())
	if e != nil {
		t.Fatal(e)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if e = s.FlushLiveness(cancelled); e == nil {
		t.Fatal("a flush with no database reported success")
	}
	if e = s.FlushLiveness(context.Background()); e != nil {
		t.Fatal(e)
	}
	if got := readNode(t, s, node.NodeID); !got.lastSeen.Equal(out.ServerTime) {
		t.Fatalf("the sighting of the failed pass was lost: %v, heartbeat %v", got.lastSeen, out.ServerTime)
	}
}

func TestLeaseRenewalKeepsANodeSeenWithoutAWrite(t *testing.T) {
	s, a := setup(t)
	ctx := context.Background()
	node := enroll(t, s, a, "Renewing node")
	if e := s.EnableLiveness(ctx); e != nil {
		t.Fatal(e)
	}
	if _, e := s.CreateTask(ctx, a, node.NodeID, wire.CollectFacts, uuid.NewString(), uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	claim, e := s.Claim(ctx, node.NodeID, node.Credential, uuid.NewString())
	if e != nil || claim.Lease == nil {
		t.Fatal(claim, e)
	}
	before := readNode(t, s, node.NodeID)
	renewed, e := s.Renew(ctx, node.NodeID, node.Credential, claim.Lease.TaskID, claim.Lease.LeaseToken)
	if e != nil || renewed.ValidForMilliseconds <= 0 {
		t.Fatal(renewed, e)
	}
	if after := readNode(t, s, node.NodeID); after.xmin != before.xmin {
		t.Fatal("a renewal wrote the node row")
	}
	if e = s.FlushLiveness(ctx); e != nil {
		t.Fatal(e)
	}
	if after := readNode(t, s, node.NodeID); !after.lastSeen.After(before.lastSeen) {
		t.Fatal("a renewal did not advance last seen")
	}
}

func TestConnectivityChangesAreStored(t *testing.T) {
	s, a := setup(t)
	ctx := context.Background()
	node := enroll(t, s, a, "Silent node")
	// A node silent past the offline threshold goes offline once, with the time recorded.
	if _, e := s.DB.Exec(ctx, `UPDATE managed_nodes SET last_heartbeat_at=clock_timestamp()-interval '1 hour' WHERE id=$1`, node.NodeID); e != nil {
		t.Fatal(e)
	}
	if e := s.EnableLivenessStartedAt(ctx, time.Now().Add(-time.Hour)); e != nil {
		t.Fatal(e)
	}
	before := readNode(t, s, node.NodeID)
	if e := s.FlushLiveness(ctx); e != nil {
		t.Fatal(e)
	}
	offline := readNode(t, s, node.NodeID)
	if offline.connectivity != "offline" || !offline.changedAt.After(before.changedAt) {
		t.Fatalf("%+v then %+v", before, offline)
	}
	if e := s.FlushLiveness(ctx); e != nil {
		t.Fatal(e)
	}
	if again := readNode(t, s, node.NodeID); again.xmin != offline.xmin {
		t.Fatal("a stored change was written again")
	}
	got, e := s.Get(ctx, a, a.Me.Workspace.ID, node.NodeID)
	if e != nil || got.Connectivity != "offline" || got.ConnectivityChangedAt == nil || !got.ConnectivityChangedAt.Equal(offline.changedAt) {
		t.Fatal(got, e)
	}

	// The next heartbeat brings it back.
	if _, e = s.Heartbeat(ctx, node.NodeID, node.Credential, handshake()); e != nil {
		t.Fatal(e)
	}
	if e = s.FlushLiveness(ctx); e != nil {
		t.Fatal(e)
	}
	back := readNode(t, s, node.NodeID)
	if back.connectivity != "online" || !back.changedAt.After(offline.changedAt) {
		t.Fatalf("%+v then %+v", offline, back)
	}
}

func TestADatabaseWithANewerSightingKeepsANodeFromGettingWorse(t *testing.T) {
	s, a := setup(t)
	ctx := context.Background()
	node := enroll(t, s, a, "Heard elsewhere")
	if _, e := s.DB.Exec(ctx, `UPDATE managed_nodes SET last_heartbeat_at=clock_timestamp()-interval '1 hour' WHERE id=$1`, node.NodeID); e != nil {
		t.Fatal(e)
	}
	if e := s.EnableLivenessStartedAt(ctx, time.Now().Add(-time.Hour)); e != nil {
		t.Fatal(e)
	}
	// Another gateway wrote a newer last-seen time after this table loaded.
	if _, e := s.DB.Exec(ctx, `UPDATE managed_nodes SET last_heartbeat_at=clock_timestamp() WHERE id=$1`, node.NodeID); e != nil {
		t.Fatal(e)
	}
	if e := s.FlushLiveness(ctx); e != nil {
		t.Fatal(e)
	}
	if got := readNode(t, s, node.NodeID); got.connectivity != "online" {
		t.Fatalf("recorded %s over a newer sighting", got.connectivity)
	}
}

func TestRestartDoesNotMarkTheFleetOffline(t *testing.T) {
	s, a := setup(t)
	ctx := context.Background()
	node := enroll(t, s, a, "Node across a restart")
	// Control was down for an hour; agents kept running.
	if _, e := s.DB.Exec(ctx, `UPDATE managed_nodes SET last_heartbeat_at=clock_timestamp()-interval '1 hour' WHERE id=$1`, node.NodeID); e != nil {
		t.Fatal(e)
	}
	if e := s.EnableLiveness(ctx); e != nil {
		t.Fatal(e)
	}
	if e := s.FlushLiveness(ctx); e != nil {
		t.Fatal(e)
	}
	if got := readNode(t, s, node.NodeID); got.connectivity != "online" {
		t.Fatalf("a restart recorded %s", got.connectivity)
	}
}

func TestARemovedNodeLeavesTheTable(t *testing.T) {
	s, a := setup(t)
	ctx := context.Background()
	node := enroll(t, s, a, "Removed node")
	if _, e := s.DB.Exec(ctx, `UPDATE managed_nodes SET last_heartbeat_at=clock_timestamp()-interval '1 hour' WHERE id=$1`, node.NodeID); e != nil {
		t.Fatal(e)
	}
	if e := s.EnableLivenessStartedAt(ctx, time.Now().Add(-time.Hour)); e != nil {
		t.Fatal(e)
	}
	if tracked := trackedNodes(s); tracked != 1 {
		t.Fatalf("%v nodes tracked after the load", tracked)
	}
	if _, e := s.DB.Exec(ctx, `UPDATE managed_nodes SET status='removed',credential_hash=NULL WHERE id=$1`, node.NodeID); e != nil {
		t.Fatal(e)
	}
	if e := s.FlushLiveness(ctx); e != nil {
		t.Fatal(e)
	}
	if tracked := trackedNodes(s); tracked != 0 {
		t.Fatalf("%v nodes tracked", tracked)
	}
}

func trackedNodes(s *nodes.Service) float64 {
	for _, v := range s.LivenessSamples() {
		if v.Name == "ownsolid_node_liveness_tracked_nodes" {
			return v.Value
		}
	}
	return -1
}

type fakeReplica struct {
	published []liveness.Seen
	calls     int
	stall     bool
	fail      error
	watched   *liveness.Seen
	merged    chan struct{}
}

func (f *fakeReplica) Publish(ctx context.Context, seen []liveness.Seen) error {
	f.calls++
	if f.stall {
		<-ctx.Done()
		return ctx.Err()
	}
	f.published = append(f.published, seen...)
	return f.fail
}
func (f *fakeReplica) Watch(ctx context.Context, fn func(liveness.Seen)) error {
	if f.watched != nil {
		fn(*f.watched)
		close(f.merged)
	}
	<-ctx.Done()
	return ctx.Err()
}

func TestSightingsReachTheReplicaAndItsFailureStopsNothing(t *testing.T) {
	s, node := liveHeartbeats(t)
	ctx := context.Background()
	replica := &fakeReplica{fail: errors.New("LIVENESS_REPLICA_UNAVAILABLE")}
	s.LivenessReplica = replica
	out, e := s.Heartbeat(ctx, node.NodeID, node.Credential, handshake())
	if e != nil {
		t.Fatal(e)
	}
	if e = s.FlushLiveness(ctx); e != nil {
		t.Fatal(e)
	}
	if len(replica.published) != 1 || replica.published[0].Node != node.NodeID || !replica.published[0].At.Equal(out.ServerTime) {
		t.Fatalf("published %+v", replica.published)
	}
	if got := readNode(t, s, node.NodeID); !got.lastSeen.Equal(out.ServerTime) {
		t.Fatal("a replica failure stopped the flush")
	}
}

func TestAStalledReplicaDelaysAFlushByItsBoundOnly(t *testing.T) {
	s, node := liveHeartbeats(t)
	ctx := context.Background()
	replica := &fakeReplica{stall: true}
	s.LivenessReplica = replica
	out, e := s.Heartbeat(ctx, node.NodeID, node.Credential, handshake())
	if e != nil {
		t.Fatal(e)
	}
	started := time.Now()
	if e = s.FlushLiveness(ctx); e != nil {
		t.Fatal(e)
	}
	if took := time.Since(started); took > 3*time.Second {
		t.Fatalf("a stalled replica held the flush for %v", took)
	}
	if got := readNode(t, s, node.NodeID); !got.lastSeen.Equal(out.ServerTime) || replica.calls != 1 {
		t.Fatalf("last seen %v, heartbeat %v, %d publishes", got.lastSeen, out.ServerTime, replica.calls)
	}
}

func TestASightingFromAnotherGatewayKeepsANodeOnline(t *testing.T) {
	s, a := setup(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	node := enroll(t, s, a, "Heard by another gateway")
	if _, e := s.DB.Exec(ctx, `UPDATE managed_nodes SET last_heartbeat_at=clock_timestamp()-interval '1 hour' WHERE id=$1`, node.NodeID); e != nil {
		t.Fatal(e)
	}
	if e := s.EnableLivenessStartedAt(ctx, time.Now().Add(-time.Hour)); e != nil {
		t.Fatal(e)
	}
	replica := &fakeReplica{watched: &liveness.Seen{Node: node.NodeID, At: time.Now().UTC()}, merged: make(chan struct{})}
	s.LivenessReplica = replica
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		s.RunLivenessReplica(ctx)
	}()
	<-replica.merged
	if e := s.FlushLiveness(ctx); e != nil {
		t.Fatal(e)
	}
	if got := readNode(t, s, node.NodeID); got.connectivity != "online" {
		t.Fatalf("recorded %s over another gateway's sighting", got.connectivity)
	}
	cancel()
	<-finished
}

func TestHeartbeatDigestSeparatesStateFromVolatileFields(t *testing.T) {
	base := func() wire.Handshake {
		h := wire.Handshake{ProtocolVersion: wire.InferenceVersion, ExecutionProfile: wire.DockerInferenceProfile, Facts: facts()}
		h.Component = &wire.ComponentObservation{State: "running", Metrics: &wire.ComponentMetrics{CPUSecondsTotal: 1}}
		h.Inference = &wire.InferenceObservation{GPUs: []wire.NVIDIAGPU{{UUID: "gpu", MemoryFreeMiB: 10}}}
		return h
	}
	reference, ok := nodes.HeartbeatDigest(base())
	if !ok {
		t.Fatal("digest failed")
	}
	same := map[string]func(*wire.Handshake){
		"facts time":   func(h *wire.Handshake) { h.Facts.ObservedAt = h.Facts.ObservedAt.Add(time.Hour) },
		"free disk":    func(h *wire.Handshake) { h.Facts.DiskAvailableBytes++ },
		"metrics":      func(h *wire.Handshake) { h.Component.Metrics.CPUSecondsTotal = 2 },
		"component at": func(h *wire.Handshake) { h.Component.ObservedAt = time.Now() },
		"inference at": func(h *wire.Handshake) { h.Inference.ObservedAt = time.Now() },
		"gpu free":     func(h *wire.Handshake) { h.Inference.GPUs[0].MemoryFreeMiB = 5 },
		"gpu at":       func(h *wire.Handshake) { h.Inference.GPUs[0].ObservedAt = time.Now() },
	}
	for name, change := range same {
		h := base()
		change(&h)
		if got, _ := nodes.HeartbeatDigest(h); got != reference {
			t.Errorf("%s counted as state", name)
		}
	}
	different := map[string]func(*wire.Handshake){
		"hostname":        func(h *wire.Handshake) { h.Facts.Hostname = "other" },
		"disk size":       func(h *wire.Handshake) { h.Facts.DiskTotalBytes++ },
		"protocol":        func(h *wire.Handshake) { h.ProtocolVersion = "other" },
		"component state": func(h *wire.Handshake) { h.Component.State = "stopped" },
		"metrics gone":    func(h *wire.Handshake) { h.Component.Metrics = nil },
		"gpu added":       func(h *wire.Handshake) { h.Inference.GPUs = append(h.Inference.GPUs, wire.NVIDIAGPU{UUID: "b"}) },
		"gpu mode":        func(h *wire.Handshake) { h.Inference.GPUs[0].MIGMode = "enabled" },
		"probe error":     func(h *wire.Handshake) { h.Inference.ProbeErrorCode = "X" },
		"deployment":      func(h *wire.Handshake) { h.Inference.Deployment = &wire.DeploymentObservation{State: "running"} },
	}
	for name, change := range different {
		h := base()
		change(&h)
		if got, _ := nodes.HeartbeatDigest(h); got == reference {
			t.Errorf("%s counted as volatile", name)
		}
	}
	// Clearing must not touch what the worker later writes.
	h := base()
	nodes.HeartbeatDigest(h)
	if h.Component.Metrics.CPUSecondsTotal != 1 || h.Inference.GPUs[0].MemoryFreeMiB != 10 {
		t.Fatal("the digest changed the handshake")
	}
}
