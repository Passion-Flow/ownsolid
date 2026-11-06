package training_test

import (
	"strings"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"testing"
	"time"

	agentwire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	wire "github.com/Passion-Flow/ownsolid/api/training/v1"
	transport "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/outbox"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/agentcost"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/httpfixture"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	"github.com/google/uuid"
)

// The fleets of the agent cost test. Other nodes bring history of their own, and the node
// under test brings history as well, so neither may cost a call anything.
const (
	costSmallFleet = 50
	costLargeFleet = costSmallFleet * agentcost.FleetRatio * 2
	costTasksEach  = 3
	costAuditsEach = 5
)

// exchangeCost is one measured exchange of a call.
type exchangeCost struct {
	call agentwire.Call
	cost agentcost.Cost
}

// costRun drives one agent through every call against a fleet of a given size.
type costRun struct {
	t        *testing.T
	f        scenario
	recorder *agentcost.Recorder
	client   *httpfixture.Client
	post     func(path string, headers map[string]string, body []byte, status int) []byte
	measured map[string]exchangeCost
	// flush writes what the heartbeats of this run left in memory (ADR 0022).
	flush func() error
	// sweep looks for expired tasks across the fleet, and read is one read of the outbox by a
	// gateway. Neither is an agent call, but both run constantly whatever the fleet does.
	sweep func() error
	read  func() error
}

// measure records one exchange while another transaction holds a shared lock on every row
// the nodes of a fleet have in common. An agent call that wants one of them exclusively
// waits out the lock timeout and fails here, because at scale it would wait for the fleet.
func (r *costRun) measure(name string, call agentwire.Call, exchange func()) {
	r.t.Helper()
	ctx := context.Background()
	holder, e := r.f.s.DB.Begin(ctx)
	if e != nil {
		r.t.Fatal(e)
	}
	defer holder.Rollback(ctx)
	a := r.f.admin.Me
	for _, statement := range []struct {
		sql  string
		args []any
	}{
		{"SELECT 1 FROM installation_state WHERE id=1 FOR SHARE", nil},
		{"SELECT 1 FROM tenants WHERE id=$1 FOR SHARE", []any{a.Workspace.TenantID}},
		{"SELECT 1 FROM workspaces WHERE id=$1 FOR SHARE", []any{a.Workspace.ID}},
		{"SELECT 1 FROM users WHERE id=$1 FOR SHARE", []any{a.User.ID}},
		{"SELECT 1 FROM workspace_memberships WHERE workspace_id=$1 AND user_id=$2 FOR SHARE", []any{a.Workspace.ID, a.User.ID}},
	} {
		if _, e = holder.Exec(ctx, statement.sql, statement.args...); e != nil {
			r.t.Fatal(e)
		}
	}
	cost, e := r.recorder.Record(exchange)
	if e != nil {
		r.t.Fatal(e)
	}
	r.measured[name] = exchangeCost{call, cost}
}

// store is the repository of the fleet under test.
func store(t *testing.T, f scenario) transaction.Store {
	t.Helper()
	out, e := f.s.Identity.Repository()
	if e != nil {
		t.Fatal(e)
	}
	return out
}

// expectNoWrites fails unless an exchange only read, in at most one statement for itself and
// one for the pacing it may refresh.
func (r *costRun) expectNoWrites(name string) {
	r.t.Helper()
	statements := r.measured[name].cost.Statements
	if len(statements) > 2 {
		r.t.Errorf("%s ran %d statements", name, len(statements))
	}
	for _, statement := range statements {
		if !strings.HasPrefix(strings.ToUpper(strings.TrimSpace(statement.Query)), "SELECT") {
			r.t.Errorf("%s wrote: %s", name, statement.Query)
		}
	}
}

// seedFleet adds nodes with finished tasks and audit history beside the node under test,
// and history to that node, then refreshes the planner's statistics.
func seedFleet(t *testing.T, f scenario, nodes int) {
	t.Helper()
	ctx := context.Background()
	for _, seed := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO managed_nodes(id,tenant_id,workspace_id,name,status,protocol_version,facts,execution_profile,capabilities,credential_hash,last_heartbeat_at)
			SELECT gen_random_uuid(),m.tenant_id,m.workspace_id,'cost-fleet-'||g,m.status,m.protocol_version,m.facts,m.execution_profile,m.capabilities,sha256(g::text::bytea),clock_timestamp()
			FROM managed_nodes m,generate_series(1,$1::integer) g WHERE m.id=$2`, []any{nodes, f.input.NodeID}},
		{`INSERT INTO node_tasks(id,tenant_id,workspace_id,node_id,operation,state,idempotency_key,requested_by,deadline_at,payload,completed_at)
			SELECT gen_random_uuid(),n.tenant_id,n.workspace_id,n.id,$1,'succeeded',gen_random_uuid(),$2,clock_timestamp(),'{}',clock_timestamp()
			FROM managed_nodes n,generate_series(1,$3::integer) WHERE n.name LIKE 'cost-fleet-%'`, []any{agentwire.CollectFacts, f.admin.Me.User.ID, costTasksEach}},
		{`INSERT INTO node_tasks(id,tenant_id,workspace_id,node_id,operation,state,idempotency_key,requested_by,deadline_at,payload,completed_at)
			SELECT gen_random_uuid(),n.tenant_id,n.workspace_id,n.id,$1,'succeeded',gen_random_uuid(),$2,clock_timestamp(),'{}',clock_timestamp()
			FROM managed_nodes n,generate_series(1,$3::integer) WHERE n.id=$4`, []any{agentwire.CollectFacts, f.admin.Me.User.ID, nodes / 10, f.input.NodeID}},
		{`INSERT INTO audit_events(id,occurred_at,tenant_id,workspace_id,audience,action,outcome,target_type,target_id,request_id)
			SELECT gen_random_uuid(),clock_timestamp(),n.tenant_id,n.workspace_id,'system','node.task_claimed','success','node_task',gen_random_uuid(),gen_random_uuid()
			FROM managed_nodes n,generate_series(1,$1::integer) WHERE n.name LIKE 'cost-fleet-%'`, []any{costAuditsEach}},
		{`ANALYZE`, nil},
	} {
		if _, e := f.s.DB.Exec(ctx, seed.sql, seed.args...); e != nil {
			t.Fatal(e)
		}
	}
}

// measureFleet measures every exchange on a fleet of the given size, in its own schema.
func measureFleet(t *testing.T, nodes int) map[string]exchangeCost {
	t.Helper()
	var recorder agentcost.Recorder
	f := setupOn(t, fixture.NewConfigured(t, recorder.Configure))
	attachStore(t, f)
	policy := config.DefaultRuntimePolicy()
	policy.Nodes.MaximumLiveNodes = costLargeFleet * 2
	f.s.Identity.Config.Runtime = &policy
	seedFleet(t, f, nodes)
	handler := transport.New(f.s.Identity)
	handler.Training.Store = f.s.Store
	handler.Datasets.Service.Store = f.s.Datasets.Store
	handler.Datasets.Service.Processor = f.s.Datasets.Processor
	if e := handler.Nodes.Service.EnableLiveness(context.Background()); e != nil {
		t.Fatal(e)
	}
	server := httpfixture.NewTLSServer(t, handler)
	t.Cleanup(server.Close)
	client := httpfixture.New(t, server, "admin", httpfixture.Schemas(t, "../../../../../api/openapi/v1/nodes/openapi.json"))
	client.Headers = map[string]string{"Authorization": "Bearer " + f.credential}
	base := "/agent/v1/sites/default/cells/default/nodes/" + f.input.NodeID
	r := &costRun{t: t, f: f, recorder: &recorder, client: client, measured: map[string]exchangeCost{}, flush: func() error { return handler.Nodes.Service.FlushLiveness(context.Background()) }}
	r.sweep = func() error { return handler.Nodes.Service.Sweep(context.Background()) }
	reader, e := outbox.NewReader(store(t, f), 1000)
	if e != nil {
		t.Fatal(e)
	}
	r.read = func() error {
		bounded, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if e := reader.Start(bounded); e != nil {
			return e
		}
		_, e := reader.Next(bounded)
		return e
	}
	r.post = func(path string, headers map[string]string, body []byte, status int) []byte {
		t.Helper()
		request, e := http.NewRequest("POST", httpfixture.AdminOrigin+base+path, bytes.NewReader(body))
		if e != nil {
			t.Fatal(e)
		}
		request.Host = "admin.example.invalid"
		request.Header.Set("Origin", "https://admin.example.invalid")
		request.Header.Set("Authorization", "Bearer "+f.credential)
		for name, value := range headers {
			request.Header.Set(name, value)
		}
		response, e := server.Client().Do(request)
		if e != nil {
			t.Fatal(e)
		}
		defer response.Body.Close()
		var out bytes.Buffer
		if _, e = out.ReadFrom(response.Body); e != nil || response.StatusCode != status {
			t.Fatal(path, response.StatusCode, e, out.String())
		}
		return out.Bytes()
	}
	r.exchanges(base)
	return r.measured
}

// claim asks for work and decodes the lease.
func (r *costRun) claim(base string, acknowledged uint64) agentwire.Lease {
	r.t.Helper()
	raw, e := json.Marshal(r.client.Call("POST", base+"/tasks/claims", agentwire.ClaimRequest{AcknowledgedSequence: acknowledged}, 200, ""))
	if e != nil {
		r.t.Fatal(e)
	}
	var claim struct {
		Lease *agentwire.Lease `json:"lease"`
	}
	if e = json.Unmarshal(raw, &claim); e != nil {
		r.t.Fatal(e)
	}
	if claim.Lease == nil {
		return agentwire.Lease{}
	}
	return *claim.Lease
}

// exchanges runs the agent's life: it enrolls, reports, runs a facts task and a training
// run. Every call of the protocol appears at least once.
func (r *costRun) exchanges(base string) {
	t, f, ctx := r.t, r.f, context.Background()
	var facts agentwire.Facts
	var stored []byte
	if e := f.s.DB.QueryRow(ctx, "SELECT facts FROM managed_nodes WHERE id=$1", f.input.NodeID).Scan(&stored); e != nil || json.Unmarshal(stored, &facts) != nil {
		t.Fatal("stored facts", e)
	}
	handshake := agentwire.Handshake{ProtocolVersion: agentwire.AIVersion, ExecutionProfile: agentwire.DockerAIProfile, Capabilities: agentwire.Capabilities(agentwire.DockerAIProfile), Facts: facts}
	join, e := f.s.Nodes.MintForProfile(ctx, f.admin, f.admin.Me.Workspace.ID, "Agent cost node", agentwire.DockerAIProfile, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	r.measure("enroll", agentwire.CallEnroll, func() {
		r.client.Call("POST", "/agent/v1/enroll", agentwire.Enrollment{JoinToken: join.Token, Handshake: handshake}, 201, "")
	})
	r.measure("heartbeat", agentwire.CallHeartbeat, func() { r.client.Call("POST", base+"/heartbeat", handshake, 200, "") })
	r.measure("heartbeat unchanged", agentwire.CallHeartbeat, func() { r.client.Call("POST", base+"/heartbeat", handshake, 200, "") })
	r.expectNoWrites("heartbeat unchanged")
	for name, background := range map[string]func() error{"task sweep": r.sweep, "outbox read": r.read} {
		r.measure(name, agentwire.CallHeartbeat, func() {
			if e := background(); e != nil {
				t.Fatal(e)
			}
		})
	}
	r.measure("liveness flush", agentwire.CallHeartbeat, func() {
		if e := r.flush(); e != nil {
			t.Fatal(e)
		}
	})
	var sequence uint64
	if e = f.s.DB.QueryRow(ctx, "SELECT task_sequence FROM managed_nodes WHERE id=$1", f.input.NodeID).Scan(&sequence); e != nil {
		t.Fatal(e)
	}
	if _, e = f.s.DB.Exec(ctx, "UPDATE managed_nodes SET task_acknowledged_sequence=task_sequence WHERE id=$1", f.input.NodeID); e != nil {
		t.Fatal(e)
	}
	r.measure("idle claim", agentwire.CallClaim, func() {
		if l := r.claim(base, sequence); l.TaskID != "" {
			t.Fatal("idle claim leased", l.TaskID)
		}
	})
	task, e := f.s.Nodes.CreateTask(ctx, f.admin, f.input.NodeID, agentwire.CollectFacts, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	var l agentwire.Lease
	r.measure("facts claim", agentwire.CallClaim, func() { l = r.claim(base, sequence) })
	if l.TaskID != task.ID {
		t.Fatal("facts claim leased", l.TaskID)
	}
	path := base + "/tasks/" + l.TaskID
	acknowledge := func(l agentwire.Lease) {
		r.client.Call("POST", base+"/tasks/"+l.TaskID+"/"+agentwire.TaskAcknowledgementsPath, agentwire.TaskAcknowledgementRequest{Sequence: l.Sequence, LeaseToken: l.LeaseToken}, 200, "")
	}
	renew := func(l agentwire.Lease) {
		r.client.Call("POST", base+"/tasks/"+l.TaskID+"/renewals", map[string]any{"lease_token": l.LeaseToken}, 200, "")
	}
	r.measure("facts acknowledge", agentwire.CallAcknowledge, func() { acknowledge(l) })
	r.measure("facts renew", agentwire.CallRenew, func() { renew(l) })
	r.measure("facts result", agentwire.CallResult, func() {
		r.client.Call("POST", path+"/results", agentwire.Result{LeaseToken: l.LeaseToken, Outcome: "success", Facts: &facts}, 200, "")
	})

	dispatched(t, f)
	r.measure("training claim", agentwire.CallClaim, func() { l = r.claim(base, l.Sequence) })
	if l.Training == nil {
		t.Fatal("training claim leased no run")
	}
	path = base + "/tasks/" + l.TaskID
	r.measure("training acknowledge", agentwire.CallAcknowledge, func() { acknowledge(l) })
	r.measure("training renew", agentwire.CallRenew, func() { renew(l) })
	var receipt wire.CheckpointReceipt
	if e = json.Unmarshal(checkpointMetadata(t, f, *l.Training), &receipt); e != nil {
		t.Fatal(e)
	}
	for i := range receipt.Files {
		receipt.Files[i].SHA256 = digest([]byte("x"))
	}
	metadata, e := json.Marshal(receipt)
	if e != nil {
		t.Fatal(e)
	}
	events := []json.RawMessage{}
	for _, event := range append(prelude(*l.Training), eventFixture(*l.Training, 3, 1, "checkpoint", map[string]any{"path": "checkpoint-1", "receipt_sha256": digest(metadata)})) {
		events = append(events, event)
	}
	if len(events) != agentwire.CostBatchItems {
		t.Fatal("the events batch is not the declared one")
	}
	r.measure("training events", agentwire.CallEvents, func() {
		r.client.Call("POST", path+"/training-events", map[string]any{"lease_token": l.LeaseToken, "events": events}, 200, "")
	})
	asset := l.Training.Inputs.Train
	request, e := json.Marshal(wire.InputFileRequest{LeaseToken: l.LeaseToken, Kind: "dataset", Asset: asset.Path, Length: asset.SizeBytes})
	if e != nil {
		t.Fatal(e)
	}
	jsonBody := map[string]string{"Content-Type": "application/json"}
	r.measure("training input", agentwire.CallDownload, func() {
		r.post("/tasks/"+l.TaskID+"/training-input-files", jsonBody, request, 200)
	})
	begin, e := json.Marshal(map[string]any{"lease_token": l.LeaseToken, "kind": "checkpoint", "metadata": json.RawMessage(metadata)})
	if e != nil {
		t.Fatal(e)
	}
	var upload struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	r.measure("training upload", agentwire.CallUpload, func() {
		if e := json.Unmarshal(r.post("/tasks/"+l.TaskID+"/training-uploads", jsonBody, begin, 201), &upload); e != nil {
			t.Fatal(e)
		}
	})
	r.measure("training chunk", agentwire.CallUpload, func() {
		r.post("/tasks/"+l.TaskID+"/training-uploads/"+upload.Data.ID+"/chunks", map[string]string{"Content-Type": "application/octet-stream", "X-Training-Lease-Token": l.LeaseToken, "X-Training-Asset": "optimizer.pt", "X-Training-SHA256": digest([]byte("x")), "X-Training-Offset": "0"}, []byte("x"), 200)
	})
	stop := stopFixture(t, f, l)
	container := digest([]byte("container"))
	stop.Proof, stop.ContainerID = "owned_container_removed", &container
	r.measure("training stop", agentwire.CallStop, func() {
		r.client.Call("POST", path+"/training-stops", wire.StopSubmission{LeaseToken: l.LeaseToken, Receipt: stop, Outcome: json.RawMessage("null")}, 200, "")
	})
}

// TestAgentCallCostDoesNotGrowWithTheFleet is the cost rule of ADR 0020: every agent call
// stays within its declared cost, and a fleet many times larger costs it nothing more.
func TestAgentCallCostDoesNotGrowWithTheFleet(t *testing.T) {
	small := measureFleet(t, costSmallFleet)
	large := measureFleet(t, costLargeFleet)
	names := make([]string, 0, len(large))
	covered := map[agentwire.Call]bool{}
	for name, exchange := range large {
		names = append(names, name)
		covered[exchange.call] = true
	}
	sort.Strings(names)
	for _, name := range names {
		exchange := large[name]
		t.Logf("%-22s statements %3d  blocks %4d/%-4d  rows %4d/%-4d", name, len(exchange.cost.Statements), small[name].cost.Blocks(), exchange.cost.Blocks(), small[name].cost.Rows(), exchange.cost.Rows())
		for _, violation := range agentcost.Violations(exchange.call.Cost(), small[name].cost, exchange.cost) {
			t.Errorf("%s (%s) %s", name, exchange.call, violation)
		}
	}
	for _, call := range agentwire.Calls() {
		if !covered[call] {
			t.Errorf("no exchange measures %s", call)
		}
	}
}
