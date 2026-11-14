package cellclusters_test

import (
	"bytes"
	"context"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/cellclusters"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	"github.com/google/uuid"
)

// Every agent result here is a synthetic protocol fixture; the e2e test installs RKE2.
const (
	caHash = "2cd644cf7855ba56fe02245ba8c22a1f015f75ba25be5e89fc32502efc95f50d"
	token  = "K10" + caHash + "::awcyco.0123456789abcdef"
)

type scenario struct {
	ctx      context.Context
	account  *identity.Service
	nodes    *nodes.Service
	s        *cellclusters.Service
	admin    identity.Auth
	portal   identity.Auth
	enrolled []wire.Enrolled
}

func kubernetesHandshake(name string) wire.Handshake {
	return wire.Handshake{ProtocolVersion: wire.KubernetesVersion, ExecutionProfile: wire.KubernetesNodeProfile, Capabilities: wire.Capabilities(wire.KubernetesNodeProfile),
		Facts: wire.Facts{OSID: "ubuntu", OSVersion: "24.04", Architecture: "amd64", Hostname: name, LogicalCPUs: 8, MemoryTotalBytes: 16 << 30, DiskPath: "/", DiskTotalBytes: 200 << 30, DiskAvailableBytes: 150 << 30, ObservedAt: time.Now().UTC(), AgentVersion: wire.AgentVersion}}
}

func setup(t *testing.T, batch int, nodeCount int) scenario {
	t.Helper()
	account := fixture.New(t)
	directory := t.TempDir()
	key := filepath.Join(directory, "join-token.key")
	if e := os.WriteFile(key, bytes.Repeat([]byte{9}, 32), 0o600); e != nil {
		t.Fatal(e)
	}
	account.Config.KubernetesCells = &config.KubernetesCellsConfiguration{ArtifactDirectory: directory, JoinTokenKeyFile: key, JoinBatch: batch}
	password := fixture.Bootstrap(t, account)
	x := scenario{ctx: context.Background(), account: account}
	x.admin = fixture.Login(t, account, "admin@example.invalid", password, "admin")
	x.portal = fixture.Login(t, account, "admin@example.invalid", password, "portal")
	w := &workspaces.Service{DB: account.DB, Identity: account}
	x.nodes = &nodes.Service{DB: account.DB, Identity: account, Workspaces: w}
	s, e := cellclusters.New(account, w, x.nodes)
	if e != nil || s == nil {
		t.Fatal("cell clusters", e)
	}
	x.s, x.nodes.Kubernetes = s, s
	for i := range nodeCount {
		name := "synthetic-kubernetes-" + string(rune('a'+i))
		minted, e := x.nodes.MintForProfile(x.ctx, x.admin, x.admin.Me.Workspace.ID, name, wire.KubernetesNodeProfile, uuid.NewString())
		if e != nil {
			t.Fatal(e)
		}
		enrolled, e := x.nodes.EnrollFrom(x.ctx, wire.Enrollment{JoinToken: minted.Token, Handshake: kubernetesHandshake(name)}, uuid.NewString(), "10.40.0."+string(rune('1'+i)))
		if e != nil {
			t.Fatal(e)
		}
		x.enrolled = append(x.enrolled, enrolled)
	}
	return x
}

func want(t *testing.T, e error, code string) {
	t.Helper()
	if e == nil || e.Error() != code {
		t.Fatalf("wanted %s, observed %v", code, e)
	}
}

func (x scenario) node(i int) string { return x.enrolled[i].NodeID }

func (x scenario) create(t *testing.T, server int, workers ...int) cellclusters.Cluster {
	t.Helper()
	input := cellclusters.CreateInput{Name: "Synthetic cell cluster", ServerNodeID: x.node(server), WorkerNodeIDs: []string{}, Reason: "Synthetic cell cluster acceptance"}
	for _, w := range workers {
		input.WorkerNodeIDs = append(input.WorkerNodeIDs, x.node(w))
	}
	c, e := x.s.Create(x.ctx, x.admin, input, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal("create", e)
	}
	return c
}

// claim claims the node's next task and checks it is the expected Kubernetes operation.
func (x scenario) claim(t *testing.T, i int, operation string) wire.Lease {
	t.Helper()
	out, e := x.nodes.Claim(x.ctx, x.node(i), x.enrolled[i].Credential, uuid.NewString())
	if e != nil || out.Lease == nil || out.Lease.Operation != operation || out.Lease.Kubernetes == nil {
		t.Fatalf("claim %d: %+v %v", i, out.Lease, e)
	}
	return *out.Lease
}

func (x scenario) complete(t *testing.T, i int, lease wire.Lease, observation wire.KubernetesObservation) {
	t.Helper()
	observation.ClusterID, observation.Version, observation.Role, observation.ObservedAt = lease.Kubernetes.ClusterID, lease.Kubernetes.Version, lease.Kubernetes.Role, time.Now().UTC()
	result := wire.Result{LeaseToken: lease.LeaseToken, Outcome: "success", Kubernetes: &observation}
	if observation.ErrorCode != "" {
		result.Outcome, result.ErrorCode = "failure", observation.ErrorCode
	}
	if _, e := x.nodes.Complete(x.ctx, x.node(i), x.enrolled[i].Credential, lease.TaskID, result, uuid.NewString()); e != nil {
		t.Fatal("complete", i, e)
	}
}

func (x scenario) member(t *testing.T, cluster string, i int) cellclusters.Member {
	t.Helper()
	page, e := x.s.Members(x.ctx, x.admin, cluster, url.Values{"limit": {"100"}})
	if e != nil {
		t.Fatal(e)
	}
	for _, m := range page.Items {
		if m.NodeID == x.node(i) {
			return m
		}
	}
	t.Fatal("member not found", i)
	return cellclusters.Member{}
}

func (x scenario) reconcile(t *testing.T) {
	t.Helper()
	if e := x.s.Reconcile(x.ctx); e != nil {
		t.Fatal("reconcile", e)
	}
}

func (x scenario) audits(t *testing.T, action string) int {
	t.Helper()
	var n int
	if e := x.account.DB.QueryRow(x.ctx, `SELECT count(*) FROM audit_events WHERE action=$1`, action).Scan(&n); e != nil {
		t.Fatal(e)
	}
	return n
}

// joinServer takes the server through its join and gives the cluster a join token.
func (x scenario) joinServer(t *testing.T, cluster string) {
	t.Helper()
	lease := x.claim(t, 0, wire.KubernetesJoinOperation)
	if lease.Kubernetes.Role != wire.KubernetesServerRole || lease.Kubernetes.NodeAddress != "10.40.0.1" || lease.Kubernetes.JoinToken != "" || lease.Kubernetes.NodeName != x.node(0) {
		t.Fatalf("server lease %+v", lease.Kubernetes)
	}
	x.complete(t, 0, lease, wire.KubernetesObservation{State: wire.KubernetesReady, CASHA256: caHash})
	if m := x.member(t, cluster, 0); m.State != cellclusters.MemberReady || m.TaskID != nil {
		t.Fatalf("server after join %+v", m)
	}
	x.reconcile(t)
	lease = x.claim(t, 0, wire.KubernetesTokenOperation)
	if lease.Kubernetes.TokenTTLSeconds != 3600 {
		t.Fatalf("token lease %+v", lease.Kubernetes)
	}
	x.complete(t, 0, lease, wire.KubernetesObservation{State: wire.KubernetesReady, CASHA256: caHash, JoinToken: token, JoinTokenExpiresAt: time.Now().UTC().Add(59 * time.Minute)})
}

func TestClusterInstallsServerThenWorkersAndRetriesFailures(t *testing.T) {
	x := setup(t, 50, 3)
	c := x.create(t, 0, 1, 2)
	if c.State != cellclusters.ClusterInstalling || c.Members.Total() != 3 || c.Members.Joining != 1 || c.Members.Pending != 2 || c.ServerNode != x.node(0) || c.CASHA256 != nil || c.Version != wire.ApprovedKubernetesRelease().Version {
		t.Fatalf("created %+v", c)
	}
	// Workers wait for the server: nothing is written for them yet.
	x.reconcile(t)
	if m := x.member(t, c.ID, 1); m.State != cellclusters.MemberPending || m.TaskID != nil {
		t.Fatalf("worker before server %+v", m)
	}
	x.joinServer(t, c.ID)
	var sealed []byte
	if e := x.account.DB.QueryRow(x.ctx, `SELECT join_token_sealed FROM cell_clusters WHERE id=$1`, c.ID).Scan(&sealed); e != nil || len(sealed) == 0 || bytes.Contains(sealed, []byte("0123456789abcdef")) {
		t.Fatal("the join token is not sealed at rest", e)
	}
	x.reconcile(t)
	worker := x.claim(t, 1, wire.KubernetesJoinOperation)
	if worker.Kubernetes.JoinToken != token || worker.Kubernetes.ServerAddress != "10.40.0.1" || worker.Kubernetes.NodeAddress != "10.40.0.2" || worker.Kubernetes.Role != wire.KubernetesWorkerRole {
		t.Fatalf("worker lease %+v", worker.Kubernetes)
	}
	var payload string
	if e := x.account.DB.QueryRow(x.ctx, `SELECT payload::text FROM node_tasks WHERE id=$1`, worker.TaskID).Scan(&payload); e != nil || strings.Contains(payload, "0123456789abcdef") || strings.Contains(payload, "join_token") {
		t.Fatal("the task row holds the join token", payload, e)
	}
	x.complete(t, 1, worker, wire.KubernetesObservation{State: wire.KubernetesReady})
	failing := x.claim(t, 2, wire.KubernetesJoinOperation)
	x.complete(t, 2, failing, wire.KubernetesObservation{State: wire.KubernetesNotReady, ErrorCode: "KUBERNETES_NOT_READY"})
	m := x.member(t, c.ID, 2)
	if m.State != cellclusters.MemberFailed || m.ErrorCode == nil || *m.ErrorCode != "KUBERNETES_NOT_READY" || m.JoinAttempts != 1 {
		t.Fatalf("failed worker %+v", m)
	}
	c, e := x.s.Get(x.ctx, x.admin, c.ID)
	if e != nil || c.State != cellclusters.ClusterDegraded || c.Members.Ready != 2 || c.Members.Failed != 1 || c.CASHA256 == nil || *c.CASHA256 != caHash {
		t.Fatalf("degraded %+v %v", c, e)
	}
	// Only a failed member can be retried.
	_, e = x.s.Retry(x.ctx, x.admin, c.ID, x.node(1), cellclusters.ReasonInput{Reason: "Synthetic retry"}, uuid.NewString())
	want(t, e, "KUBERNETES_MEMBER_STATE_CONFLICT")
	retried, e := x.s.Retry(x.ctx, x.admin, c.ID, x.node(2), cellclusters.ReasonInput{Reason: "Synthetic retry"}, uuid.NewString())
	if e != nil || retried.State != cellclusters.MemberJoining || retried.ErrorCode != nil {
		t.Fatalf("retried %+v %v", retried, e)
	}
	again := x.claim(t, 2, wire.KubernetesJoinOperation)
	x.complete(t, 2, again, wire.KubernetesObservation{State: wire.KubernetesReady})
	if c, e = x.s.Get(x.ctx, x.admin, c.ID); e != nil || c.State != cellclusters.ClusterReady || c.Members.Ready != 3 {
		t.Fatalf("ready %+v %v", c, e)
	}
	if m = x.member(t, c.ID, 2); m.JoinAttempts != 2 || m.ObservedAt == nil {
		t.Fatalf("retried member %+v", m)
	}
	for action, count := range map[string]int{"cell_cluster.created": 1, "cell_cluster.member_join_requested": 4, "cell_cluster.member_joined": 3, "cell_cluster.member_failed": 1, "cell_cluster.member_retried": 1} {
		if got := x.audits(t, action); got != count {
			t.Errorf("%s audited %d times, want %d", action, got, count)
		}
	}
	// An observation reports a node that stopped being Ready.
	if _, e = x.s.Observe(x.ctx, x.admin, c.ID, cellclusters.ReasonInput{Reason: "Synthetic observation"}, uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	observed := x.claim(t, 1, wire.KubernetesObserveOperation)
	x.complete(t, 1, observed, wire.KubernetesObservation{State: wire.KubernetesNotReady})
	if m = x.member(t, c.ID, 1); m.State != cellclusters.MemberNotReady || m.TaskID != nil {
		t.Fatalf("observed member %+v", m)
	}
	if c, _ = x.s.Get(x.ctx, x.admin, c.ID); c.State != cellclusters.ClusterDegraded {
		t.Fatalf("after observation %+v", c)
	}
}

func TestClusterCreateRefusals(t *testing.T) {
	x := setup(t, 50, 3)
	other, e := x.nodes.MintForProfile(x.ctx, x.admin, x.admin.Me.Workspace.ID, "synthetic-read-only", wire.ReadOnlyProfile, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	h := kubernetesHandshake("synthetic-read-only")
	h.ProtocolVersion, h.ExecutionProfile, h.Capabilities = wire.Version, wire.ReadOnlyProfile, wire.Capabilities(wire.ReadOnlyProfile)
	readOnly, e := x.nodes.Enroll(x.ctx, wire.Enrollment{JoinToken: other.Token, Handshake: h}, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	base := cellclusters.CreateInput{Name: "Synthetic cell cluster", ServerNodeID: x.node(0), WorkerNodeIDs: []string{x.node(1)}, Reason: "Synthetic refusal"}
	for name, c := range map[string]struct {
		change func(*cellclusters.CreateInput)
		code   string
	}{
		"another profile":  {func(i *cellclusters.CreateInput) { i.WorkerNodeIDs = []string{readOnly.NodeID} }, "AGENT_PROFILE_REJECTED"},
		"unknown node":     {func(i *cellclusters.CreateInput) { i.WorkerNodeIDs = []string{uuid.NewString()} }, "RESOURCE_NOT_FOUND"},
		"server as worker": {func(i *cellclusters.CreateInput) { i.WorkerNodeIDs = []string{x.node(0)} }, "VALIDATION_FAILED"},
		"repeated worker":  {func(i *cellclusters.CreateInput) { i.WorkerNodeIDs = []string{x.node(1), x.node(1)} }, "VALIDATION_FAILED"},
		"no reason":        {func(i *cellclusters.CreateInput) { i.Reason = " " }, "VALIDATION_FAILED"},
		"no name":          {func(i *cellclusters.CreateInput) { i.Name = "" }, "VALIDATION_FAILED"},
		"non-canonical id": {func(i *cellclusters.CreateInput) { i.ServerNodeID = strings.ToUpper(x.node(0)) }, "VALIDATION_FAILED"},
	} {
		input := base
		c.change(&input)
		_, e := x.s.Create(x.ctx, x.admin, input, uuid.NewString(), uuid.NewString())
		if e == nil || e.Error() != c.code {
			t.Errorf("%s: wanted %s, observed %v", name, c.code, e)
		}
	}
	_, e = x.s.Create(x.ctx, x.portal, base, uuid.NewString(), uuid.NewString())
	want(t, e, "ACCESS_DENIED")
	_, e = x.s.List(x.ctx, x.portal)
	want(t, e, "ACCESS_DENIED")
	if x.audits(t, "cell_cluster.created") != 0 {
		t.Fatal("a refused create was audited")
	}
	key := uuid.NewString()
	first, e := x.s.Create(x.ctx, x.admin, base, key, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	replay, e := x.s.Create(x.ctx, x.admin, base, key, uuid.NewString())
	if e != nil || replay.ID != first.ID {
		t.Fatal("replay", replay.ID, first.ID, e)
	}
	changed := base
	changed.Name = "Another synthetic cell cluster"
	_, e = x.s.Create(x.ctx, x.admin, changed, key, uuid.NewString())
	want(t, e, "IDEMPOTENCY_CONFLICT")
	_, e = x.s.Create(x.ctx, x.admin, cellclusters.CreateInput{Name: "Second", ServerNodeID: x.node(2), WorkerNodeIDs: []string{}, Reason: "Synthetic second"}, uuid.NewString(), uuid.NewString())
	want(t, e, "KUBERNETES_CELL_CLUSTER_EXISTS")
	_, e = x.s.AddWorkers(x.ctx, x.admin, first.ID, cellclusters.AddWorkersInput{WorkerNodeIDs: []string{readOnly.NodeID}, Reason: "Synthetic add"}, uuid.NewString())
	want(t, e, "AGENT_PROFILE_REJECTED")
	added, e := x.s.AddWorkers(x.ctx, x.admin, first.ID, cellclusters.AddWorkersInput{WorkerNodeIDs: []string{x.node(1), x.node(2)}, Reason: "Synthetic add"}, uuid.NewString())
	if e != nil || added.Members.Total() != 3 {
		t.Fatal("add", added, e)
	}
	// Adding the same nodes again changes nothing.
	if again, e := x.s.AddWorkers(x.ctx, x.admin, first.ID, cellclusters.AddWorkersInput{WorkerNodeIDs: []string{x.node(2)}, Reason: "Synthetic add"}, uuid.NewString()); e != nil || again.Members.Total() != 3 || x.audits(t, "cell_cluster.workers_added") != 1 {
		t.Fatal("repeated add", again, e)
	}
	_, e = x.s.Get(x.ctx, x.admin, uuid.NewString())
	want(t, e, "RESOURCE_NOT_FOUND")
}

func TestKubernetesNodeEnrollsOnlyWithItsAddress(t *testing.T) {
	x := setup(t, 50, 0)
	for _, address := range []string{"", "127.0.0.1", "0.0.0.0", "fe80::1", "::ffff:10.0.0.1"} {
		minted, e := x.nodes.MintForProfile(x.ctx, x.admin, x.admin.Me.Workspace.ID, "synthetic-addressless", wire.KubernetesNodeProfile, uuid.NewString())
		if e != nil {
			t.Fatal(e)
		}
		_, e = x.nodes.EnrollFrom(x.ctx, wire.Enrollment{JoinToken: minted.Token, Handshake: kubernetesHandshake("synthetic-addressless")}, uuid.NewString(), address)
		want(t, e, "KUBERNETES_NODE_ADDRESS_INVALID")
		if _, e = x.nodes.RevokeToken(x.ctx, x.admin, minted.ID, "Synthetic cleanup", uuid.NewString()); e != nil {
			t.Fatal(e)
		}
	}
}

func TestClusterJoinsWorkersInBatchesAndWaitsForBusyNodes(t *testing.T) {
	x := setup(t, 1, 4)
	busy, e := x.nodes.CreateTask(x.ctx, x.admin, x.node(1), wire.CollectFacts, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	c := x.create(t, 0, 1, 2, 3)
	x.joinServer(t, c.ID)
	x.reconcile(t)
	joining := func() int {
		n := 0
		for i := 1; i <= 3; i++ {
			if x.member(t, c.ID, i).State == cellclusters.MemberJoining {
				n++
			}
		}
		return n
	}
	// The busy worker waits; one other worker joins, the batch being one.
	if joining() != 1 || x.member(t, c.ID, 1).State != cellclusters.MemberPending {
		t.Fatalf("joining %d, busy worker %+v", joining(), x.member(t, c.ID, 1))
	}
	x.reconcile(t)
	if joining() != 1 {
		t.Fatal("the batch grew past its bound")
	}
	// Once the busy node is idle, it joins in its turn.
	facts, e := x.nodes.Claim(x.ctx, x.node(1), x.enrolled[1].Credential, uuid.NewString())
	if e != nil || facts.Lease == nil || facts.Lease.TaskID != busy.ID {
		t.Fatal("collect facts claim", e)
	}
	if _, e = x.nodes.Complete(x.ctx, x.node(1), x.enrolled[1].Credential, busy.ID, wire.Result{LeaseToken: facts.Lease.LeaseToken, Outcome: "failure", ErrorCode: "DISCOVERY_FAILED"}, uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	for i := 2; i <= 3; i++ {
		if m := x.member(t, c.ID, i); m.State == cellclusters.MemberJoining {
			lease := x.claim(t, i, wire.KubernetesJoinOperation)
			x.complete(t, i, lease, wire.KubernetesObservation{State: wire.KubernetesReady})
		}
	}
	x.reconcile(t)
	if joining() != 1 {
		t.Fatal("the next worker did not join")
	}
	page, e := x.s.Members(x.ctx, x.admin, c.ID, url.Values{"limit": {"3"}})
	if e != nil || len(page.Items) != 3 || page.NextCursor == nil {
		t.Fatal("first page", page, e)
	}
	next, e := x.s.Members(x.ctx, x.admin, c.ID, url.Values{"limit": {"3"}, "cursor": {*page.NextCursor}})
	if e != nil || len(next.Items) != 1 || next.NextCursor != nil {
		t.Fatal("second page", next, e)
	}
	seen := map[string]bool{next.Items[0].NodeID: true}
	for _, m := range page.Items {
		seen[m.NodeID] = true
	}
	if len(seen) != 4 {
		t.Fatal("pages overlap", seen)
	}
	ready, e := x.s.Members(x.ctx, x.admin, c.ID, url.Values{"state": {"ready"}})
	if e != nil || len(ready.Items) != 2 {
		t.Fatal("ready members", ready, e)
	}
	_, e = x.s.Members(x.ctx, x.admin, c.ID, url.Values{"state": {"joined"}})
	want(t, e, "VALIDATION_FAILED")
	_, e = x.s.Members(x.ctx, x.admin, c.ID, url.Values{"state": {"ready"}, "cursor": {*page.NextCursor}})
	want(t, e, "INVALID_CURSOR")
}

func TestClusterSettlesTasksThatEndWithoutAResult(t *testing.T) {
	x := setup(t, 50, 2)
	c := x.create(t, 0, 1)
	server := x.member(t, c.ID, 0)
	if _, e := x.account.DB.Exec(x.ctx, `UPDATE node_tasks SET deadline_at=clock_timestamp()-interval '1 second' WHERE id=$1`, *server.TaskID); e != nil {
		t.Fatal(e)
	}
	if e := x.nodes.Sweep(x.ctx); e != nil {
		t.Fatal(e)
	}
	x.reconcile(t)
	server = x.member(t, c.ID, 0)
	if server.State != cellclusters.MemberFailed || server.ErrorCode == nil || *server.ErrorCode != "TASK_DEADLINE_EXCEEDED" {
		t.Fatalf("expired server %+v", server)
	}
	if got, _ := x.s.Get(x.ctx, x.admin, c.ID); got.State != cellclusters.ClusterFailed {
		t.Fatalf("cluster with a failed server %+v", got)
	}
	// A token request that fails fails the workers waiting for it.
	if _, e := x.s.Retry(x.ctx, x.admin, c.ID, x.node(0), cellclusters.ReasonInput{Reason: "Synthetic retry"}, uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	lease := x.claim(t, 0, wire.KubernetesJoinOperation)
	x.complete(t, 0, lease, wire.KubernetesObservation{State: wire.KubernetesReady, CASHA256: caHash})
	x.reconcile(t)
	lease = x.claim(t, 0, wire.KubernetesTokenOperation)
	x.complete(t, 0, lease, wire.KubernetesObservation{State: wire.KubernetesReady, ErrorCode: "KUBERNETES_TOKEN_FAILED"})
	worker := x.member(t, c.ID, 1)
	if worker.State != cellclusters.MemberFailed || worker.ErrorCode == nil || *worker.ErrorCode != "KUBERNETES_TOKEN_FAILED" {
		t.Fatalf("worker after a failed token %+v", worker)
	}
	// A server that comes back with another CA is not the cluster's server any more.
	if _, e := x.s.Retry(x.ctx, x.admin, c.ID, x.node(1), cellclusters.ReasonInput{Reason: "Synthetic retry"}, uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	x.reconcile(t)
	lease = x.claim(t, 0, wire.KubernetesTokenOperation)
	x.complete(t, 0, lease, wire.KubernetesObservation{State: wire.KubernetesReady, CASHA256: strings.Repeat("e", 64), JoinToken: "K10" + strings.Repeat("e", 64) + "::awcyco.0123456789abcdef", JoinTokenExpiresAt: time.Now().UTC().Add(59 * time.Minute)})
	if worker = x.member(t, c.ID, 1); worker.ErrorCode == nil || *worker.ErrorCode != "KUBERNETES_CA_CHANGED" {
		t.Fatalf("worker after a changed CA %+v", worker)
	}
}

func TestWorkerClaimWithoutUsableTokenIsRefused(t *testing.T) {
	x := setup(t, 50, 2)
	c := x.create(t, 0, 1)
	x.joinServer(t, c.ID)
	x.reconcile(t)
	// The token is replaced by one sealed for another cluster, as a copied row would be.
	if _, e := x.account.DB.Exec(x.ctx, `UPDATE cell_clusters SET join_token_sealed=decode(repeat('00',64),'hex') WHERE id=$1`, c.ID); e != nil {
		t.Fatal(e)
	}
	_, e := x.nodes.Claim(x.ctx, x.node(1), x.enrolled[1].Credential, uuid.NewString())
	want(t, e, "KUBERNETES_JOIN_TOKEN_UNAVAILABLE")
}

func TestConcurrentCreatesMakeOneCluster(t *testing.T) {
	x := setup(t, 50, 2)
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = x.s.Create(x.ctx, x.admin, cellclusters.CreateInput{Name: "Synthetic concurrent", ServerNodeID: x.node(i), WorkerNodeIDs: []string{}, Reason: "Synthetic concurrent create"}, uuid.NewString(), uuid.NewString())
		}()
	}
	wg.Wait()
	created, refused := 0, 0
	for _, e := range errs {
		switch {
		case e == nil:
			created++
		case e.Error() == "KUBERNETES_CELL_CLUSTER_EXISTS":
			refused++
		default:
			t.Fatal(e)
		}
	}
	if created != 1 || refused != 1 || x.audits(t, "cell_cluster.created") != 1 {
		t.Fatalf("created %d, refused %d", created, refused)
	}
}
