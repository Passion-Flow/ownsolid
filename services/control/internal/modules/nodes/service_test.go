package nodes_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
)

// These wire facts are synthetic service-test data, not hardware qualification evidence.
func facts() wire.Facts {
	return wire.Facts{OSID: "ubuntu", OSVersion: "24.04", Architecture: "amd64", Hostname: "synthetic-node", LogicalCPUs: 2, MemoryTotalBytes: 1073741824, DiskPath: "/", DiskTotalBytes: 10737418240, DiskAvailableBytes: 5368709120, ObservedAt: time.Now().UTC(), AgentVersion: wire.AgentVersion}
}
func handshake() wire.Handshake {
	return wire.Handshake{ProtocolVersion: wire.Version, Capabilities: []string{wire.CollectFacts}, Facts: facts()}
}
func setup(t *testing.T) (*nodes.Service, identity.Auth) {
	t.Helper()
	return setupProfile(t, false)
}
func setupProfile(t *testing.T, minimal bool, component ...bool) (*nodes.Service, identity.Auth) {
	var s *identity.Service
	if len(component) > 0 && component[0] {
		s = fixture.NewM04(t)
	} else if minimal {
		s = fixture.NewM03(t)
	} else {
		s = fixture.New(t)
	}
	p := fixture.Bootstrap(t, s)
	a := fixture.Login(t, s, "admin@example.invalid", p, "admin")
	w := &workspaces.Service{DB: s.DB, Identity: s}
	return &nodes.Service{DB: s.DB, Identity: s, Workspaces: w}, a
}
func expected(t *testing.T, e error, code string) {
	t.Helper()
	if e == nil || e.Error() != code {
		t.Fatalf("expected %s observed %v", code, e)
	}
}
func enroll(t *testing.T, s *nodes.Service, a identity.Auth, name string) wire.Enrolled {
	t.Helper()
	token, e := s.Mint(context.Background(), a, a.Me.Workspace.ID, name, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	result, e := s.Enroll(context.Background(), wire.Enrollment{JoinToken: token.Token, Handshake: handshake()}, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	return result
}

func TestM03EnrollmentReplayAndCredentialBoundaries(t *testing.T) {
	s, a := setupProfile(t, true)
	ctx := context.Background()
	token, e := s.Mint(ctx, a, a.Me.Workspace.ID, "Node A", uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	start := make(chan struct{})
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, e := s.Enroll(ctx, wire.Enrollment{JoinToken: token.Token, Handshake: handshake()}, uuid.NewString())
			results <- e
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	wins := 0
	for e := range results {
		if e == nil {
			wins++
		} else {
			expected(t, e, "TOKEN_UNUSABLE")
		}
	}
	if wins != 1 {
		t.Fatal("join token not one-use")
	}
	one := enroll(t, s, a, "Node B")
	two := enroll(t, s, a, "Node C")
	_, e = s.Heartbeat(ctx, two.NodeID, one.Credential, handshake())
	expected(t, e, "AGENT_UNAUTHORIZED")
	d, e := s.Heartbeat(ctx, one.NodeID, one.Credential, handshake())
	if e != nil || d.Status != "active" {
		t.Fatal("heartbeat failed", e)
	}
	n, e := s.Get(ctx, a, "", one.NodeID)
	if e != nil || n.Facts.OSID != "ubuntu" || n.Connectivity != "online" {
		t.Fatal("facts not persisted", e)
	}
	if _, e = s.DB.Exec(ctx, "UPDATE managed_nodes SET last_heartbeat_at=clock_timestamp()-interval '60 seconds' WHERE id=$1", one.NodeID); e != nil {
		t.Fatal(e)
	}
	n, e = s.Get(ctx, a, "", one.NodeID)
	if e != nil || n.Connectivity != "unknown" {
		t.Fatal("unknown connectivity fabricated", e)
	}
	if _, e = s.DB.Exec(ctx, "UPDATE managed_nodes SET last_heartbeat_at=clock_timestamp()-interval '121 seconds' WHERE id=$1", one.NodeID); e != nil {
		t.Fatal(e)
	}
	n, e = s.Get(ctx, a, "", one.NodeID)
	if e != nil || n.Connectivity != "offline" {
		t.Fatal("offline connectivity wrong", e)
	}
	n, e = s.Update(ctx, a, n.ID, n.Name, "disabled", n.Version, "Synthetic disable", uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	_, e = s.Claim(ctx, one.NodeID, one.Credential, uuid.NewString())
	expected(t, e, "ACCESS_DENIED")
	n, e = s.Update(ctx, a, n.ID, n.Name, "active", n.Version, "Synthetic enable", uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	_, e = s.Update(ctx, a, n.ID, n.Name, "removed", n.Version, "Synthetic remove", uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	_, e = s.Heartbeat(ctx, one.NodeID, one.Credential, handshake())
	expected(t, e, "AGENT_UNAUTHORIZED")
	revoked, e := s.Mint(ctx, a, a.Me.Workspace.ID, "Revoked token", uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.RevokeToken(ctx, a, revoked.ID, "Synthetic revocation", uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	_, e = s.Enroll(ctx, wire.Enrollment{JoinToken: revoked.Token, Handshake: handshake()}, uuid.NewString())
	expected(t, e, "TOKEN_UNUSABLE")
	expired, e := s.Mint(ctx, a, a.Me.Workspace.ID, "Expired token", uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	if expired.ExpiresAt.Sub(expired.CreatedAt) < 590*time.Second || expired.ExpiresAt.Sub(expired.CreatedAt) > 610*time.Second {
		t.Fatal("join-token lifetime differs from contract")
	}
	if _, e = s.DB.Exec(ctx, "UPDATE node_join_tokens SET expires_at=clock_timestamp()-interval '1 second' WHERE id=$1", expired.ID); e != nil {
		t.Fatal(e)
	}
	_, e = s.Enroll(ctx, wire.Enrollment{JoinToken: expired.Token, Handshake: handshake()}, uuid.NewString())
	expected(t, e, "TOKEN_UNUSABLE")
}
func TestM03DurableTaskLeasesFencingAndIdempotentResults(t *testing.T) {
	s, a := setupProfile(t, true)
	ctx := context.Background()
	node := enroll(t, s, a, "Worker")
	key := uuid.NewString()
	task, e := s.CreateTask(ctx, a, node.NodeID, wire.CollectFacts, key, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	again, e := s.CreateTask(ctx, a, node.NodeID, wire.CollectFacts, key, uuid.NewString())
	if e != nil || again.ID != task.ID {
		t.Fatal("request key did not reconcile", e)
	}
	_, e = s.CreateTask(ctx, a, node.NodeID, "run_shell.v1", uuid.NewString(), uuid.NewString())
	expected(t, e, "AGENT_OPERATION_UNSUPPORTED")
	_, e = s.CreateTask(ctx, a, node.NodeID, wire.CollectFacts, uuid.NewString(), uuid.NewString())
	expected(t, e, "NODE_BUSY")
	claim, e := s.Claim(ctx, node.NodeID, node.Credential, uuid.NewString())
	if e != nil || claim.Lease == nil || claim.Lease.Attempt != 1 {
		t.Fatal("claim failed", e)
	}
	duplicate, e := s.Claim(ctx, node.NodeID, node.Credential, uuid.NewString())
	if e != nil || duplicate.Lease != nil {
		t.Fatal("duplicate concurrent lease", e)
	}
	if _, e = s.DB.Exec(ctx, "UPDATE node_tasks SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1", task.ID); e != nil {
		t.Fatal(e)
	}
	newLease, e := s.Claim(ctx, node.NodeID, node.Credential, uuid.NewString())
	if e != nil || newLease.Lease == nil || newLease.Lease.Attempt != 2 {
		t.Fatal("expired lease did not recover", e)
	}
	f := facts()
	input := wire.Result{LeaseToken: claim.Lease.LeaseToken, Outcome: "success", Facts: &f}
	_, e = s.Complete(ctx, node.NodeID, node.Credential, task.ID, input, uuid.NewString())
	expected(t, e, "AGENT_LEASE_STALE")
	input.LeaseToken = newLease.Lease.LeaseToken
	state, e := s.Complete(ctx, node.NodeID, node.Credential, task.ID, input, uuid.NewString())
	if e != nil || state != "succeeded" {
		t.Fatal("completion failed", e)
	}
	state, e = s.Complete(ctx, node.NodeID, node.Credential, task.ID, input, uuid.NewString())
	if e != nil || state != "succeeded" {
		t.Fatal("lost-response completion did not reconcile", e)
	}
	f.Hostname = "Changed duplicate"
	_, e = s.Complete(ctx, node.NodeID, node.Credential, task.ID, input, uuid.NewString())
	expected(t, e, "AGENT_LEASE_STALE")
	deadline, e := s.CreateTask(ctx, a, node.NodeID, wire.CollectFacts, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.DB.Exec(ctx, "UPDATE node_tasks SET deadline_at=clock_timestamp()-interval '1 second' WHERE id=$1", deadline.ID); e != nil {
		t.Fatal(e)
	}
	if e = s.Sweep(ctx); e != nil {
		t.Fatal(e)
	}
	var status, code string
	if e = s.DB.QueryRow(ctx, "SELECT state,error_code FROM node_tasks WHERE id=$1", deadline.ID).Scan(&status, &code); e != nil || status != "failed" || code != "TASK_DEADLINE_EXCEEDED" {
		t.Fatal("unattended deadline not expired", e)
	}
	exhausted, e := s.CreateTask(ctx, a, node.NodeID, wire.CollectFacts, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	for attempt := 1; attempt <= 3; attempt++ {
		lease, e := s.Claim(ctx, node.NodeID, node.Credential, uuid.NewString())
		if e != nil || lease.Lease == nil || lease.Lease.Attempt != attempt {
			t.Fatal("attempt budget not enforced", e)
		}
		if _, e = s.DB.Exec(ctx, "UPDATE node_tasks SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1", exhausted.ID); e != nil {
			t.Fatal(e)
		}
	}
	last, e := s.Claim(ctx, node.NodeID, node.Credential, uuid.NewString())
	if e != nil || last.Lease != nil {
		t.Fatal("exhausted lease was reissued", e)
	}
	if e = s.DB.QueryRow(ctx, "SELECT state,error_code FROM node_tasks WHERE id=$1", exhausted.ID).Scan(&status, &code); e != nil || status != "failed" || code != "TASK_ATTEMPTS_EXHAUSTED" {
		t.Fatal("attempt exhaustion not persisted", e)
	}
}

func TestM03AuditFailureRollsBackAuthorityAndTasks(t *testing.T) {
	s, a := setupProfile(t, true)
	ctx := context.Background()
	one := enroll(t, s, a, "Atomic running node")
	two := enroll(t, s, a, "Atomic pending node")
	three := enroll(t, s, a, "Atomic empty node")
	pending, e := s.Mint(ctx, a, a.Me.Workspace.ID, "Atomic pending join", uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	first, e := s.CreateTask(ctx, a, one.NodeID, wire.CollectFacts, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	second, e := s.CreateTask(ctx, a, two.NodeID, wire.CollectFacts, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	lease, e := s.Claim(ctx, one.NodeID, one.Credential, uuid.NewString())
	if e != nil || lease.Lease == nil {
		t.Fatal(e)
	}
	n, e := s.Get(ctx, a, "", one.NodeID)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.DB.Exec(ctx, `CREATE FUNCTION reject_node_audit() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RAISE EXCEPTION 'synthetic node audit failure'; END$$; CREATE TRIGGER reject_node_audit BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION reject_node_audit()`); e != nil {
		t.Fatal(e)
	}
	f := facts()
	for _, operation := range []struct {
		name string
		run  func() error
	}{
		{"mint", func() error { _, e := s.Mint(ctx, a, a.Me.Workspace.ID, "Must roll back", uuid.NewString()); return e }},
		{"revoke", func() error { _, e := s.RevokeToken(ctx, a, pending.ID, "Synthetic", uuid.NewString()); return e }},
		{"enroll", func() error {
			_, e := s.Enroll(ctx, wire.Enrollment{JoinToken: pending.Token, Handshake: handshake()}, uuid.NewString())
			return e
		}},
		{"node_status", func() error {
			_, e := s.Update(ctx, a, n.ID, n.Name, "disabled", n.Version, "Synthetic", uuid.NewString())
			return e
		}},
		{"task_create", func() error {
			_, e := s.CreateTask(ctx, a, three.NodeID, wire.CollectFacts, uuid.NewString(), uuid.NewString())
			return e
		}},
		{"task_claim", func() error { _, e := s.Claim(ctx, two.NodeID, two.Credential, uuid.NewString()); return e }},
		{"task_complete", func() error {
			_, e := s.Complete(ctx, one.NodeID, one.Credential, first.ID, wire.Result{LeaseToken: lease.Lease.LeaseToken, Outcome: "success", Facts: &f}, uuid.NewString())
			return e
		}},
	} {
		t.Run(operation.name, func(t *testing.T) {
			if operation.run() == nil {
				t.Fatal("audit failure falsely committed")
			}
		})
	}
	var nodesCount, taskCount int
	if e = s.DB.QueryRow(ctx, "SELECT count(*) FROM managed_nodes").Scan(&nodesCount); e != nil || nodesCount != 3 {
		t.Fatal("enrollment partially persisted", e)
	}
	if e = s.DB.QueryRow(ctx, "SELECT count(*) FROM node_tasks").Scan(&taskCount); e != nil || taskCount != 2 {
		t.Fatal("task partially persisted", e)
	}
	var consumed, revoked bool
	if e = s.DB.QueryRow(ctx, "SELECT consumed_at IS NOT NULL,revoked_at IS NOT NULL FROM node_join_tokens WHERE id=$1", pending.ID).Scan(&consumed, &revoked); e != nil || consumed || revoked {
		t.Fatal("token authority partially consumed", e)
	}
	var state string
	var attempt int
	if e = s.DB.QueryRow(ctx, "SELECT state,attempt FROM node_tasks WHERE id=$1", second.ID).Scan(&state, &attempt); e != nil || state != "pending" || attempt != 0 {
		t.Fatal("claim partially persisted", e)
	}
	fresh, e := s.Get(ctx, a, "", one.NodeID)
	if e != nil || fresh.Status != "active" || fresh.Version != n.Version {
		t.Fatal("node partially changed", e)
	}
	if _, e = s.DB.Exec(ctx, "DROP TRIGGER reject_node_audit ON audit_events"); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Complete(ctx, one.NodeID, one.Credential, first.ID, wire.Result{LeaseToken: lease.Lease.LeaseToken, Outcome: "success", Facts: &f}, uuid.NewString()); e != nil {
		t.Fatal("rolled-back completion could not recover", e)
	}
}

func TestM03WorkspaceAndForeignTenantIsolation(t *testing.T) {
	s, a := setupProfile(t, true)
	ctx := context.Background()
	password := fixture.Password()
	actors := []identity.Auth{}
	for _, email := range []string{"node-alice@example.invalid", "node-bob@example.invalid"} {
		invite, e := s.Identity.Invite(ctx, a, "", "createInvitation", uuid.NewString(), email, "en", 0, uuid.NewString())
		if e != nil {
			t.Fatal(e)
		}
		if e = s.Identity.Register(ctx, fixture.InvitationToken(t, s.Identity, invite.ID), "Synthetic node member", password, uuid.NewString()); e != nil {
			t.Fatal(e)
		}
		actors = append(actors, fixture.Login(t, s.Identity, email, password, "portal"))
	}
	alpha, e := s.Workspaces.Create(ctx, a, "Node Alpha", actors[0].Me.User.ID, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	beta, e := s.Workspaces.Create(ctx, a, "Node Beta", actors[1].Me.User.ID, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	join, e := s.Mint(ctx, a, alpha.ID, "Scoped node", uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	node, e := s.Enroll(ctx, wire.Enrollment{JoinToken: join.Token, Handshake: handshake()}, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Get(ctx, actors[0], alpha.ID, node.NodeID); e != nil {
		t.Fatal(e)
	}
	for _, check := range []func() error{func() error { _, e := s.Get(ctx, actors[1], alpha.ID, node.NodeID); return e }, func() error { _, e := s.Get(ctx, actors[1], beta.ID, node.NodeID); return e }, func() error { _, e := s.Tasks(ctx, actors[1], alpha.ID, node.NodeID, nil); return e }, func() error { _, e := s.List(ctx, actors[1], alpha.ID, nil); return e }} {
		expected(t, check(), "RESOURCE_NOT_FOUND")
	}
	tenant, workspace, foreignNode := uuid.NewString(), uuid.NewString(), uuid.NewString()
	raw := security.Token()
	encoded, _ := json.Marshal(facts())
	for _, q := range []struct {
		query string
		args  []any
	}{
		{"INSERT INTO tenants(id,name,status) VALUES($1,'Foreign node tenant','active')", []any{tenant}},
		{"INSERT INTO workspaces(id,tenant_id,name,status) VALUES($1,$2,'Foreign node workspace','active')", []any{workspace, tenant}},
		{"INSERT INTO managed_nodes(id,tenant_id,workspace_id,name,status,credential_hash,protocol_version,facts,last_heartbeat_at) VALUES($1,$2,$3,'Foreign node','active',$4,'1.0',$5,clock_timestamp())", []any{foreignNode, tenant, workspace, security.Digest("node-credential:v1:" + raw), encoded}},
	} {
		if _, e = s.DB.Exec(ctx, q.query, q.args...); e != nil {
			t.Fatal(e)
		}
	}
	_, e = s.Get(ctx, a, "", foreignNode)
	expected(t, e, "RESOURCE_NOT_FOUND")
	_, e = s.Heartbeat(ctx, foreignNode, raw, handshake())
	expected(t, e, "AGENT_UNAUTHORIZED")
	_, e = s.Mint(ctx, a, workspace, "Foreign token", uuid.NewString())
	expected(t, e, "RESOURCE_NOT_FOUND")
}

func TestM03BoundedEnrollmentAndSecretFreeAudit(t *testing.T) {
	s, a := setupProfile(t, true)
	ctx := context.Background()
	join, e := s.Mint(ctx, a, a.Me.Workspace.ID, "Capacity first", uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.DB.Exec(ctx, "INSERT INTO node_join_tokens(id,tenant_id,workspace_id,node_name,token_hash,created_by,expires_at) SELECT gen_random_uuid(),$1,$2,'Synthetic capacity token '||n,decode(md5(n::text)||md5((n+100)::text),'hex'),$3,clock_timestamp()+interval '10 minutes' FROM generate_series(1,99) n", a.Me.Workspace.TenantID, a.Me.Workspace.ID, a.Me.User.ID); e != nil {
		t.Fatal(e)
	}
	_, e = s.Mint(ctx, a, a.Me.Workspace.ID, "Too many pending tokens", uuid.NewString())
	expected(t, e, "RESOURCE_LIMIT_REACHED")
	node, e := s.Enroll(ctx, wire.Enrollment{JoinToken: join.Token, Handshake: handshake()}, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	extra, e := s.Mint(ctx, a, a.Me.Workspace.ID, "Over capacity node", uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	encoded, _ := json.Marshal(facts())
	if _, e = s.DB.Exec(ctx, "INSERT INTO managed_nodes(id,tenant_id,workspace_id,name,status,credential_hash,protocol_version,facts,last_heartbeat_at) SELECT gen_random_uuid(),$1,$2,'Synthetic capacity node '||n,'active',decode(md5(('credential'||n)::text)||md5((n+500)::text),'hex'),'1.0',$3,clock_timestamp() FROM generate_series(1,99) n", a.Me.Workspace.TenantID, a.Me.Workspace.ID, encoded); e != nil {
		t.Fatal(e)
	}
	_, e = s.Enroll(ctx, wire.Enrollment{JoinToken: extra.Token, Handshake: handshake()}, uuid.NewString())
	expected(t, e, "RESOURCE_LIMIT_REACHED")
	var consumed bool
	if e = s.DB.QueryRow(ctx, "SELECT consumed_at IS NOT NULL FROM node_join_tokens WHERE id=$1", extra.ID).Scan(&consumed); e != nil || consumed {
		t.Fatal("capacity failure consumed token", e)
	}
	var leaks int
	if e = s.DB.QueryRow(ctx, "SELECT count(*) FROM audit_events a WHERE row_to_json(a)::text LIKE '%'||$1||'%' OR row_to_json(a)::text LIKE '%'||$2||'%'", join.Token, node.Credential).Scan(&leaks); e != nil || leaks != 0 {
		t.Fatal("node secret leaked to audit", e)
	}
}

func TestM04EnrollmentProfileCannotUpgradeOrDowngradeAuthority(t *testing.T) {
	s, a := setupProfile(t, false, true)
	ctx := context.Background()
	management := wire.Handshake{ProtocolVersion: wire.ManagementVersion, ExecutionProfile: wire.SystemdComponentsProfile, Capabilities: wire.Capabilities(wire.SystemdComponentsProfile), Facts: facts()}
	readOnly, e := s.Mint(ctx, a, a.Me.Workspace.ID, "Read-only profile", uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	_, e = s.Enroll(ctx, wire.Enrollment{JoinToken: readOnly.Token, Handshake: management}, uuid.NewString())
	expected(t, e, "AGENT_PROFILE_REJECTED")
	legacy, e := s.Enroll(ctx, wire.Enrollment{JoinToken: readOnly.Token, Handshake: handshake()}, uuid.NewString())
	if e != nil || legacy.ProtocolVersion != wire.Version {
		t.Fatal("legacy profile changed", e)
	}
	_, e = s.Heartbeat(ctx, legacy.NodeID, legacy.Credential, management)
	expected(t, e, "AGENT_PROFILE_REJECTED")
	join, e := s.MintForProfile(ctx, a, a.Me.Workspace.ID, "Management profile", wire.SystemdComponentsProfile, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	_, e = s.Enroll(ctx, wire.Enrollment{JoinToken: join.Token, Handshake: handshake()}, uuid.NewString())
	expected(t, e, "AGENT_PROFILE_REJECTED")
	managed, e := s.Enroll(ctx, wire.Enrollment{JoinToken: join.Token, Handshake: management}, uuid.NewString())
	if e != nil || managed.ProtocolVersion != wire.ManagementVersion {
		t.Fatal("explicit management enrollment failed", e)
	}
	_, e = s.Heartbeat(ctx, managed.NodeID, managed.Credential, handshake())
	expected(t, e, "AGENT_PROFILE_REJECTED")
	if _, e = s.Heartbeat(ctx, managed.NodeID, managed.Credential, management); e != nil {
		t.Fatal(e)
	}
}
