//go:build repository_integration && task_events_integration

package taskdeliveries

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/taskbus"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/repositoryfixture"
	strict "github.com/Passion-Flow/ownsolid/api/datasets/v1"
)

type eventPeerFixture struct {
	tenant        string
	configuration taskbus.Configuration
	canary        taskbus.Envelope
}

type eventStory struct {
	f               repositoryfixture.Fixture
	ctx             context.Context
	s               *Service
	owner           *identity.Service
	n               *nodes.Service
	admin, portal   identity.Auth
	enrolled        wire.Enrolled
	facts           wire.Facts
	peer            *eventPeerFixture
	restart         json.RawMessage
	nativeRestart   func()
	nativeInterrupt func(func())
}

// Every selected leaf owns a fresh actual full-schema database and an explicitly
// preprovisioned empty private stream/durable. No test creates a toy table, fake
// NATS result, widened controller permission or automatic stream purge.
func eventSetup(t *testing.T) *eventStory { return eventSetupWithTransport(t, nil) }

// Only the separately tagged real native fixture registers this test provider.
// The ordinary protected-map profile remains mandatory unless explicitly selected.
var ownedNativeTaskEventProfile func(*testing.T) (string, func(), func(func()))

func eventSetupWithTransport(t *testing.T, transform func(context.Context, taskbus.Configuration) taskbus.Configuration) *eventStory {
	t.Helper()
	path := os.Getenv("PROJECT_ONE_ACTUAL_TASK_EVENTS_FIXTURE")
	var nativeRestart func()
	var nativeInterrupt func(func())
	switch os.Getenv("PROJECT_ONE_ACTUAL_TASK_EVENTS_PROFILE_MODE") {
	case "":
	case "native_owned":
		if path != "" || ownedNativeTaskEventProfile == nil {
			t.Fatal("EXPLICIT_UNAMBIGUOUS_NATIVE_TASK_EVENT_PROFILE_REQUIRED")
		}
		path, nativeRestart, nativeInterrupt = ownedNativeTaskEventProfile(t)
	default:
		t.Fatal("UNKNOWN_TASK_EVENT_FIXTURE_PROFILE_MODE")
	}
	resolved, e := filepath.EvalSymlinks(path)
	if e != nil || resolved != path || !filepath.IsAbs(path) {
		t.Fatal("EXACT_PROTECTED_TASK_EVENT_FIXTURE_REQUIRED")
	}
	before, e := os.Lstat(path)
	if e != nil || !before.Mode().IsRegular() || before.Mode().Perm() != 0600 {
		t.Fatal("PRIVATE_TASK_EVENT_FIXTURE_REQUIRED")
	}
	parent, e := os.Stat(filepath.Dir(path))
	if e != nil || parent.Mode().Perm()&0077 != 0 {
		t.Fatal("PRIVATE_TASK_EVENT_FIXTURE_PARENT_REQUIRED")
	}
	st, ok := before.Sys().(*syscall.Stat_t)
	if !ok || st.Uid != uint32(os.Geteuid()) || st.Nlink != 1 {
		t.Fatal("OWNED_SINGLE_LINK_TASK_EVENT_FIXTURE_REQUIRED")
	}
	fd, e := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if e != nil {
		t.Fatal("PRIVATE_TASK_EVENT_FIXTURE_OPEN_FAILED")
	}
	file := os.NewFile(uintptr(fd), "task-event-fixture")
	raw, e := io.ReadAll(io.LimitReader(file, (1<<20)+1))
	after, se := file.Stat()
	file.Close()
	if e != nil || se != nil || len(raw) > 1<<20 || !os.SameFile(before, after) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		t.Fatal("TASK_EVENT_FIXTURE_CHANGED")
	}
	var outer map[string]json.RawMessage
	if strict.DecodeStrict(raw, &outer) != nil || len(outer) != 1 || outer["profiles"] == nil {
		t.Fatal("STRICT_TASK_EVENT_PROFILE_MAP_REQUIRED")
	}
	var profiles map[string]json.RawMessage
	if json.Unmarshal(outer["profiles"], &profiles) != nil || len(profiles) < 1 || len(profiles) > 128 {
		t.Fatal("BOUNDED_TASK_EVENT_PROFILE_MAP_REQUIRED")
	}
	var selected map[string]json.RawMessage
	if json.Unmarshal(profiles[os.Getenv("PROJECT_ONE_REPOSITORY_ENGINE")+"/"+t.Name()], &selected) != nil || len(selected) != 2 && len(selected) != 3 || len(selected) == 3 && selected["peer"] == nil && selected["restart"] == nil {
		t.Fatal("EXACT_ENGINE_LEAF_PROFILE_REQUIRED")
	}
	var tenant string
	if json.Unmarshal(selected["tenant_id"], &tenant) != nil || !taskbus.ValidID(tenant) {
		t.Fatal("EXACT_TASK_EVENT_TENANT_REQUIRED")
	}
	c, e := taskbus.DecodeConfiguration(selected["configuration"])
	duplicateBound := c.DuplicateWindowSeconds <= 2
	if strings.HasSuffix(t.Name(), "/reject_cross_event_msgid_alias") {
		duplicateBound = c.DuplicateWindowSeconds == 2*c.RequestSeconds+1
	}
	if e != nil || !duplicateBound || !c.Enabled || c.Stream != "M25Fixture_"+strings.ReplaceAll(c.NamespaceID, "-", "") || c.Consumer != "M25Receiver_"+strings.ReplaceAll(c.NamespaceID, "-", "") || (c.RetryLifetimeSeconds < 12 || c.RetryLifetimeSeconds > 30) || c.RequestSeconds > 5 || c.PullSeconds > 2 || c.MaximumDeliveries > 3 || c.RetrySeconds > 2 || c.HistoryRetentionSeconds > 100 {
		t.Fatal("EXACT_BOUNDED_DISPOSABLE_NATS_PROFILE_REQUIRED")
	}
	f := repositoryfixture.NewUnbootstrapped(t)
	f.Tenant = tenant
	ctx, cancel := context.WithTimeout(t.Context(), 120*time.Second)
	t.Cleanup(cancel)
	g := &eventStory{f: f, ctx: ctx, restart: selected["restart"], nativeRestart: nativeRestart, nativeInterrupt: nativeInterrupt}
	if selected["peer"] != nil {
		var entry map[string]json.RawMessage
		var peer eventPeerFixture
		if json.Unmarshal(selected["peer"], &entry) != nil || len(entry) != 3 || json.Unmarshal(entry["tenant_id"], &peer.tenant) != nil || !taskbus.ValidID(peer.tenant) || json.Unmarshal(entry["canary"], &peer.canary) != nil {
			t.Fatal("EXACT_SYNTHETIC_PEER_PROBE_REQUIRED")
		}
		var err error
		peer.configuration, err = taskbus.DecodeConfiguration(entry["configuration"])
		if err != nil {
			t.Fatal("STRICT_PEER_CONFIGURATION_REQUIRED")
		}
		if peer.configuration.Stream != "M25Fixture_"+strings.ReplaceAll(peer.configuration.NamespaceID, "-", "") || peer.configuration.Consumer != "M25Receiver_"+strings.ReplaceAll(peer.configuration.NamespaceID, "-", "") || peer.configuration.Endpoint != c.Endpoint || !reflect.DeepEqual(peer.configuration.DialAddresses, c.DialAddresses) || peer.configuration.CAFile != c.CAFile || peer.configuration.ClientCertificateFile != c.ClientCertificateFile || peer.configuration.ClientKeyFile != c.ClientKeyFile {
			t.Fatal("EXACT_SAME_PRIVATE_PEER_FIXTURE_REQUIRED")
		}
		var shape map[string]json.RawMessage
		var eventShape map[string]json.RawMessage
		if json.Unmarshal(entry["canary"], &shape) != nil || len(shape) != 2 || json.Unmarshal(shape["event"], &eventShape) != nil || len(eventShape) != 11 {
			t.Fatal("STRICT_PEER_CANARY_SHAPE_REQUIRED")
		}
		if _, err = peer.canary.Canonical(); err != nil || peer.canary.Event.TenantID != peer.tenant || peer.canary.Event.NamespaceID != peer.configuration.NamespaceID {
			t.Fatal("EXACT_EXISTING_PEER_CANARY_REQUIRED")
		}
		g.peer = &peer
	}
	for _, v := range []transaction.Statement{
		f.Statement("INSERT INTO tenants(id,name,status) VALUES($1,'Synthetic task event fixture','active')", "INSERT INTO tenants(id,name,status) VALUES(?,'Synthetic task event fixture','active')", tenant),
		f.Statement("INSERT INTO workspaces(id,tenant_id,name,status) VALUES($1,$2,'Synthetic event Workspace','active')", "INSERT INTO workspaces(id,tenant_id,name,status) VALUES(?,?,'Synthetic event Workspace','active')", f.Workspace, tenant),
		f.Statement("INSERT INTO users(id,email_canonical,email_display,display_name,locale,status,is_platform_admin) VALUES($1,'event-fixture@example.invalid','event-fixture@example.invalid','Synthetic event actor','en','active',true)", "INSERT INTO users(id,email_canonical,email_display,display_name,locale,status,is_platform_admin) VALUES(?,'event-fixture@example.invalid','event-fixture@example.invalid','Synthetic event actor','en','active',1)", f.User),
		f.Statement("UPDATE installation_state SET bootstrap_complete=true,tenant_id=$1,default_workspace_id=$2,primary_admin_user_id=$3,bootstrapped_at=clock_timestamp() WHERE id=1", "UPDATE installation_state SET bootstrap_complete=1,tenant_id=?,default_workspace_id=?,primary_admin_user_id=?,bootstrapped_at=UTC_TIMESTAMP(6) WHERE id=1", tenant, f.Workspace, f.User),
		f.Statement("INSERT INTO workspace_memberships(tenant_id,workspace_id,user_id,role) VALUES($1,$2,$3,'owner')", "INSERT INTO workspace_memberships(tenant_id,workspace_id,user_id,role) VALUES(?,?,?,'owner')", tenant, f.Workspace, f.User),
	} {
		if _, e = f.Store.Exec(ctx, v); e != nil {
			t.Fatal("actual product fixture seed", e)
		}
	}
	owner := &identity.Service{Storage: f.Store, HMACKey: make([]byte, 32)}
	if _, e = rand.Read(owner.HMACKey); e != nil {
		t.Fatal(e)
	}
	owner.Config.Server.AdminOrigin = "https://admin.example.invalid"
	owner.Config.Server.PortalOrigin = "https://portal.example.invalid"
	w := &workspaces.Service{Identity: owner}
	n := &nodes.Service{Identity: owner, Workspaces: w}
	g.owner, g.n = owner, n
	if transform != nil {
		c = transform(ctx, c)
	}
	bus, e := taskbus.NewJetStream(ctx, c, tenant)
	if e != nil {
		t.Fatal("ACTUAL_PRIVATE_NATS_AND_AUTH_PROFILE_REQUIRED", e)
	}
	stats, e := bus.Inspect(ctx)
	if e != nil || stats.Messages != 0 || stats.Pending != 0 || stats.AckPending != 0 {
		bus.Close()
		t.Fatal("FRESH_EMPTY_EXACT_BROKER_RESOURCES_REQUIRED", e)
	}
	s, e := NewService(ctx, owner, w, n, c, bus)
	if e != nil {
		bus.Close()
		t.Fatal("ACTUAL_FULL_PRODUCT_SCHEMA_GUARDS_REQUIRED", e)
	}
	n.TaskEvents = s
	g.s = s
	t.Cleanup(func() {
		stop, cancel := context.WithTimeout(context.Background(), time.Duration(c.ShutdownSeconds)*time.Second)
		defer cancel()
		if e := g.s.Close(stop); e != nil {
			t.Error("task event handle cleanup", e)
		}
		t.Logf("TASK_EVENT_RETAINED_FIXTURE namespace=%s tenant=%s stream=%s consumer=%s", c.NamespaceID, tenant, c.Stream, c.Consumer)
	})
	g.admin = g.session(t, "admin")
	g.portal = g.session(t, "portal")
	join, e := n.MintForProfile(ctx, g.admin, f.Workspace, "Synthetic event node", wire.ReadOnlyProfile, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	g.facts = wire.Facts{OSID: "ubuntu", OSVersion: "24.04", Architecture: "amd64", Hostname: "synthetic-event-node", LogicalCPUs: 1, MemoryTotalBytes: 1024, DiskPath: "/", DiskTotalBytes: 4096, DiskAvailableBytes: 2048, ObservedAt: time.Now().UTC(), AgentVersion: wire.AgentVersion}
	g.enrolled, e = n.Enroll(ctx, wire.Enrollment{JoinToken: join.Token, Handshake: wire.Handshake{ProtocolVersion: wire.Version, ExecutionProfile: wire.ReadOnlyProfile, Capabilities: wire.Capabilities(wire.ReadOnlyProfile), Facts: g.facts}}, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	return g
}
func (g *eventStory) session(t *testing.T, audience string) identity.Auth {
	t.Helper()
	var v identity.IssuedSession
	if e := g.f.Store.Within(g.ctx, false, func(ctx context.Context, q transaction.Executor) error {
		var e error
		v, e = g.owner.IssueSessionWithin(ctx, q, g.f.User, 1, audience, "", uuid.NewString(), nil)
		return e
	}); e != nil {
		t.Fatal(e)
	}
	a, e := g.owner.Authenticate(g.ctx, v.Token, audience)
	if e != nil {
		t.Fatal(e)
	}
	return a
}
func (g *eventStory) count(t *testing.T, pg, my string, args ...any) int {
	t.Helper()
	var n int
	if e := g.f.Store.QueryRow(g.ctx, g.f.Statement(pg, my, args...)).Scan(&n); e != nil {
		t.Fatal(e)
	}
	return n
}
func (g *eventStory) exec(t *testing.T, pg, my string, args ...any) {
	t.Helper()
	if _, e := g.f.Store.Exec(g.ctx, g.f.Statement(pg, my, args...)); e != nil {
		t.Fatal(e)
	}
}
func (g *eventStory) task(t *testing.T) (nodes.Task, storedDelivery) {
	t.Helper()
	task, e := g.n.CreateTask(g.ctx, g.admin, g.enrolled.NodeID, wire.CollectFacts, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	value, e := scanDelivery(g.f.Store.QueryRow(g.ctx, g.f.Statement("SELECT "+pgFields+" FROM task_event_outbox WHERE task_id=$1", "SELECT "+myFields+" FROM task_event_outbox WHERE task_id=?", task.ID)))
	if e != nil {
		t.Fatal("actual same-q node hook required", e)
	}
	return task, value
}
func (g *eventStory) fence(t *testing.T) {
	t.Helper()
	if e := g.f.Store.Within(g.ctx, false, func(ctx context.Context, q transaction.Executor) error {
		return FenceAfterRestoreWithin(ctx, q, g.n, g.f.Tenant)
	}); e != nil {
		t.Fatal(e)
	}
}
func (g *eventStory) publish(t *testing.T) {
	t.Helper()
	if worked, e := g.s.ProcessNext(g.ctx); e != nil || !worked {
		t.Fatal("actual publication not confirmed", worked, e)
	}
}
func (g *eventStory) consume(t *testing.T) {
	t.Helper()
	if worked, e := g.s.ConsumeNext(g.ctx); e != nil || !worked {
		t.Fatal("actual SQL receipt/broker ACK not confirmed", worked, e)
	}
}
func (g *eventStory) completeFacts(t *testing.T, task nodes.Task) {
	t.Helper()
	claim, e := g.n.Claim(g.ctx, g.enrolled.NodeID, g.enrolled.Credential, uuid.NewString())
	if e != nil || claim.Lease == nil || claim.Lease.TaskID != task.ID {
		t.Fatal("actual node claim required", e)
	}
	facts := g.facts
	facts.ObservedAt = time.Now().UTC()
	if _, e = g.n.Complete(g.ctx, g.enrolled.NodeID, g.enrolled.Credential, task.ID, wire.Result{LeaseToken: claim.Lease.LeaseToken, Outcome: "success", Facts: &facts}, uuid.NewString()); e != nil {
		t.Fatal(e)
	}
}

type eventOutcomeStore struct {
	transaction.Store
	unknown, rollback bool
}

func (s *eventOutcomeStore) Within(ctx context.Context, ro bool, fn func(context.Context, transaction.Executor) error) error {
	e := s.Store.Within(ctx, ro, func(ctx context.Context, q transaction.Executor) error {
		if e := fn(ctx, q); e != nil {
			return e
		}
		if !ro && s.rollback {
			return errors.New("EXPLICIT_EVENT_FIXTURE_ROLLBACK")
		}
		return nil
	})
	if e == nil && !ro && s.unknown {
		return &transaction.Error{Kind: transaction.CommitUnknown}
	}
	return e
}

func TestPairedTaskEvents(t *testing.T) {
	for _, leaf := range []string{"same_transaction_replay_utc", "outbox_audit_rollback", "producer_commit_unknown_zero", "publisher_claim_unknown_zero", "actual_publish_consume_duplicate", "persistent_receipt_reopen", "controlled_retry_replay_conflict", "retry_commit_unknown_zero", "retry_current_admin", "current_membership_and_cursor", "workspace_node_isolation", "immutable_event_receipt_retry", "restore_fence_without_physical_claim", "unchanged_physical_lease", "consume_unknown_no_ack", "consume_audit_rollback_no_ack", "application_delivery_dlq", "poison_quarantine_current_admin", "stale_retry_does_not_block_current", "published_expiry_retained_dlq", "inactive_workspace_expiry", "resolved_terminal_history_retention", "private_role_namespace_isolation", "reject_cross_event_msgid_alias"} {
		t.Run(leaf, func(t *testing.T) {
			g := eventSetup(t)
			if leaf == "outbox_audit_rollback" {
				baseline := g.count(t, "SELECT count(*) FROM audit_events", "SELECT count(*) FROM audit_events")
				if g.f.Store.Dialect() == transaction.PostgreSQL {
					g.exec(t, `CREATE FUNCTION m25_fixture_audit_refuse() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action LIKE 'task_event.%' THEN RAISE EXCEPTION 'EXPLICIT_EVENT_AUDIT_FAILURE'; END IF; RETURN NEW; END $$`, "")
					g.exec(t, `CREATE TRIGGER m25_fixture_audit_refuse BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION m25_fixture_audit_refuse()`, "")
				} else {
					g.exec(t, "", `CREATE TRIGGER m25_fixture_audit_refuse BEFORE INSERT ON audit_events FOR EACH ROW BEGIN IF NEW.action LIKE 'task_event.%' THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='EXPLICIT_EVENT_AUDIT_FAILURE'; END IF; END`)
				}
				out, e := g.n.CreateTask(g.ctx, g.admin, g.enrolled.NodeID, wire.CollectFacts, uuid.NewString(), uuid.NewString())
				if e == nil || !reflect.DeepEqual(out, nodes.Task{}) || g.count(t, "SELECT count(*) FROM node_tasks", "SELECT count(*) FROM node_tasks") != 0 || g.count(t, "SELECT count(*) FROM task_event_outbox", "SELECT count(*) FROM task_event_outbox") != 0 || g.count(t, "SELECT count(*) FROM audit_events", "SELECT count(*) FROM audit_events") != baseline {
					t.Fatal("actual audit failure exposed or committed task/event", e)
				}
				return
			}
			if leaf == "producer_commit_unknown_zero" {
				g.owner.Storage = &eventOutcomeStore{Store: g.f.Store, unknown: true}
				out, e := g.n.CreateTask(g.ctx, g.admin, g.enrolled.NodeID, wire.CollectFacts, uuid.NewString(), uuid.NewString())
				g.owner.Storage = g.f.Store
				if e == nil || !reflect.DeepEqual(out, nodes.Task{}) || g.count(t, "SELECT count(*) FROM node_tasks", "SELECT count(*) FROM node_tasks") != 1 || g.count(t, "SELECT count(*) FROM task_event_outbox", "SELECT count(*) FROM task_event_outbox") != 1 {
					t.Fatal("confirmed commit with hidden confirmation projected task", e)
				}
				stats, e := g.s.Transport.Inspect(g.ctx)
				if e != nil || stats.Messages != 0 {
					t.Fatal("producer contacted broker before returned commit", e)
				}
				return
			}
			task, value := g.task(t)
			switch leaf {
			case "reject_cross_event_msgid_alias":
				g.publish(t)
				aliased := envelope(value)
				aliased.Event.ID = uuid.NewString()
				publication, e := g.s.Transport.Publish(g.ctx, aliased)
				if e == nil || e.Error() != "TASK_BUS_READBACK_CONFLICT" || publication != (taskbus.Publication{}) {
					t.Fatal("stream-wide duplicate ACK aliased another immutable event", e)
				}
				g.consume(t)
			case "private_role_namespace_isolation":
				if g.peer == nil {
					t.Fatal("ACTUAL_PEER_PROFILE_CANARY_REQUIRED")
				}
				g.publish(t)
				peer, e := taskbus.NewJetStream(g.ctx, g.peer.configuration, g.peer.tenant)
				if e != nil {
					t.Fatal(e)
				}
				defer peer.Close()
				if e = g.s.Transport.VerifyPeerIsolation(g.ctx, envelope(value), peer, g.peer.canary); e != nil {
					t.Fatal("real role/cross-namespace denial unconfirmed", e)
				}
			case "consume_unknown_no_ack", "consume_audit_rollback_no_ack", "application_delivery_dlq", "poison_quarantine_current_admin", "stale_retry_does_not_block_current", "published_expiry_retained_dlq", "inactive_workspace_expiry", "resolved_terminal_history_retention":
				g.deliveryFailures(t, leaf, task, value)
			case "same_transaction_replay_utc":
				if value.State != "prepared" || value.Generation != 1 || value.OutcomeUnknown || value.Event.CreatedAt.Location() != time.UTC || !value.Event.TaskCreatedAt.Equal(task.CreatedAt) {
					t.Fatal("immutable queued event mismatch")
				}
				baseline := g.count(t, "SELECT count(*) FROM audit_events", "SELECT count(*) FROM audit_events")
				if e := g.f.Store.Within(g.ctx, false, func(ctx context.Context, q transaction.Executor) error {
					n, actual, e := g.n.TaskEventSourceWithin(ctx, q, g.f.Tenant, g.f.Workspace, g.enrolled.NodeID, task.ID)
					if e != nil {
						return e
					}
					return g.s.RecordTaskQueuedWithin(ctx, q, n, actual)
				}); e != nil {
					t.Fatal(e)
				}
				if g.count(t, "SELECT count(*) FROM task_event_outbox", "SELECT count(*) FROM task_event_outbox") != 1 || g.count(t, "SELECT count(*) FROM audit_events", "SELECT count(*) FROM audit_events") != baseline {
					t.Fatal("replay repeated event/audit")
				}
			case "publisher_claim_unknown_zero":
				g.owner.Storage = &eventOutcomeStore{Store: g.f.Store, unknown: true}
				worked, e := g.s.ProcessNext(g.ctx)
				g.owner.Storage = g.f.Store
				if e == nil || worked {
					t.Fatal("unknown publisher claim returned progress", e)
				}
				stats, e := g.s.Transport.Inspect(g.ctx)
				if e != nil || stats.Messages != 0 {
					t.Fatal("unconfirmed SQL claim sent broker message", e)
				}
			case "actual_publish_consume_duplicate":
				g.publish(t)
				g.consume(t)
				before := g.count(t, "SELECT count(*) FROM audit_events", "SELECT count(*) FROM audit_events")
				wait := time.NewTimer(time.Duration(g.s.Configuration.DuplicateWindowSeconds)*time.Second + time.Millisecond)
				select {
				case <-g.ctx.Done():
					t.Fatal(g.ctx.Err())
				case <-wait.C:
				}
				wait.Stop()
				if _, e := g.s.Transport.Publish(g.ctx, envelope(value)); e != nil {
					t.Fatal(e)
				}
				g.consume(t)
				if g.count(t, "SELECT count(*) FROM task_event_receipts", "SELECT count(*) FROM task_event_receipts") != 1 || g.count(t, "SELECT count(*) FROM audit_events", "SELECT count(*) FROM audit_events") != before {
					t.Fatal("durable receipt failed beyond broker duplicate window")
				}
			case "persistent_receipt_reopen":
				g.publish(t)
				g.consume(t)
				c := g.s.Configuration
				if e := g.s.Close(g.ctx); e != nil {
					t.Fatal(e)
				}
				transport, e := taskbus.NewJetStream(g.ctx, c, g.f.Tenant)
				if e != nil {
					t.Fatal(e)
				}
				reopened, e := NewService(g.ctx, g.owner, g.n.Workspaces, g.n, c, transport)
				if e != nil {
					transport.Close()
					t.Fatal(e)
				}
				g.s = reopened
				g.n.TaskEvents = reopened
				if e := g.s.VerifyStored(g.ctx); e != nil {
					t.Fatal(e)
				}
				if g.count(t, "SELECT count(*) FROM task_event_receipts", "SELECT count(*) FROM task_event_receipts") != 1 {
					t.Fatal("restart lost durable logical receipt")
				}
			case "controlled_retry_replay_conflict", "retry_commit_unknown_zero", "retry_current_admin":
				g.fence(t)
				input := RetryRequest{Generation: 1, Reason: "Review exact synthetic transport recovery"}
				key := uuid.NewString()
				if leaf == "retry_current_admin" {
					g.exec(t, "UPDATE users SET is_platform_admin=false WHERE id=$1", "UPDATE users SET is_platform_admin=0 WHERE id=?", g.f.User)
					if v, e := g.s.Retry(g.ctx, g.admin, g.f.Workspace, g.enrolled.NodeID, value.ID, input, key, uuid.NewString()); e == nil || v != (RetryReceipt{}) {
						t.Fatal("stale Admin retried task", e)
					}
					return
				}
				if leaf == "retry_commit_unknown_zero" {
					g.owner.Storage = &eventOutcomeStore{Store: g.f.Store, unknown: true}
					out, e := g.s.Retry(g.ctx, g.admin, g.f.Workspace, g.enrolled.NodeID, value.ID, input, key, uuid.NewString())
					g.owner.Storage = g.f.Store
					if e == nil || out != (RetryReceipt{}) {
						t.Fatal("unknown retry projected receipt", e)
					}
				}
				r, e := g.s.Retry(g.ctx, g.admin, g.f.Workspace, g.enrolled.NodeID, value.ID, input, key, uuid.NewString())
				if e != nil || r.Generation != 2 || r.EventID != value.ID {
					t.Fatal(e)
				}
				if e := g.s.VerifyStored(g.ctx); e != nil {
					t.Fatal("retained generation integrity", e)
				}
				r2, e := g.s.Retry(g.ctx, g.admin, g.f.Workspace, g.enrolled.NodeID, value.ID, input, key, uuid.NewString())
				if e != nil || r2 != r {
					t.Fatal("retry receipt not replayed", e)
				}
				input.Reason = "Changed recovery intent"
				if out, e := g.s.Retry(g.ctx, g.admin, g.f.Workspace, g.enrolled.NodeID, value.ID, input, key, uuid.NewString()); e == nil || e.Error() != "IDEMPOTENCY_CONFLICT" || out != (RetryReceipt{}) {
					t.Fatal("changed retry did not conflict", e)
				}
			case "current_membership_and_cursor":
				g.completeFacts(t, task)
				g.task(t)
				first, e := g.s.List(g.ctx, g.portal, g.f.Workspace, g.enrolled.NodeID, url.Values{"limit": {"1"}})
				if e != nil || first.NextCursor == nil {
					t.Fatal("actual cursor fixture missing", e)
				}
				g.exec(t, "DELETE FROM workspace_memberships WHERE tenant_id=$1 AND workspace_id=$2 AND user_id=$3", "DELETE FROM workspace_memberships WHERE tenant_id=? AND workspace_id=? AND user_id=?", g.f.Tenant, g.f.Workspace, g.f.User)
				if out, e := g.s.Get(g.ctx, g.portal, g.f.Workspace, g.enrolled.NodeID, value.ID); e == nil || !reflect.DeepEqual(out, Delivery{}) {
					t.Fatal("removed membership read history", e)
				}
				g.exec(t, "INSERT INTO workspace_memberships(tenant_id,workspace_id,user_id,role) VALUES($1,$2,$3,'owner')", "INSERT INTO workspace_memberships(tenant_id,workspace_id,user_id,role) VALUES(?,?,?,'owner')", g.f.Tenant, g.f.Workspace, g.f.User)
				if out, e := g.s.List(g.ctx, g.portal, g.f.Workspace, g.enrolled.NodeID, url.Values{"limit": {"1"}, "cursor": {*first.NextCursor}}); e == nil || !reflect.DeepEqual(out, Page[Delivery]{}) {
					t.Fatal("old membership cursor survived rejoin", e)
				}
			case "workspace_node_isolation":
				other := uuid.NewString()
				g.exec(t, "INSERT INTO workspaces(id,tenant_id,name,status) VALUES($1,$2,'Other synthetic scope','active')", "INSERT INTO workspaces(id,tenant_id,name,status) VALUES(?,?,'Other synthetic scope','active')", other, g.f.Tenant)
				if out, e := g.s.Get(g.ctx, g.admin, other, g.enrolled.NodeID, value.ID); e == nil || !reflect.DeepEqual(out, Delivery{}) {
					t.Fatal("Workspace override read delivery", e)
				}
				if out, e := g.s.Get(g.ctx, g.admin, g.f.Workspace, uuid.NewString(), value.ID); e == nil || !reflect.DeepEqual(out, Delivery{}) {
					t.Fatal("foreign node read delivery", e)
				}
			case "immutable_event_receipt_retry":
				if _, e := g.f.Store.Exec(g.ctx, g.f.Statement("UPDATE task_event_outbox SET expires_at=expires_at+interval '1 second' WHERE id=$1", "UPDATE task_event_outbox SET expires_at=TIMESTAMPADD(SECOND,1,expires_at) WHERE id=?", value.ID)); e == nil {
					t.Fatal("immutable event expiry changed")
				}
				g.publish(t)
				g.consume(t)
				if _, e := g.f.Store.Exec(g.ctx, g.f.Statement("UPDATE task_event_receipts SET consumer_name='foreign' WHERE event_id=$1", "UPDATE task_event_receipts SET consumer_name='foreign' WHERE event_id=?", value.ID)); e == nil {
					t.Fatal("durable receipt rebound")
				}
			case "restore_fence_without_physical_claim":
				g.fence(t)
				var state string
				var attempt int
				if e := g.f.Store.QueryRow(g.ctx, g.f.Statement("SELECT state,attempt FROM node_tasks WHERE id=$1", "SELECT state,attempt FROM node_tasks WHERE id=?", task.ID)).Scan(&state, &attempt); e != nil || state != "pending" || attempt != 0 {
					t.Fatal("transport restore changed physical task", e)
				}
				if worked, e := g.s.ProcessNext(g.ctx); e != nil || worked {
					t.Fatal("restore automatically resent event", e)
				}
			case "unchanged_physical_lease":
				claim, e := g.n.Claim(g.ctx, g.enrolled.NodeID, g.enrolled.Credential, uuid.NewString())
				if e != nil || claim.Lease == nil {
					t.Fatal(e)
				}
				g.publish(t)
				g.consume(t)
				if out, e := g.n.Renew(g.ctx, g.enrolled.NodeID, g.enrolled.Credential, task.ID, claim.Lease.LeaseToken); e != nil || out.LeaseExpiresAt.IsZero() {
					t.Fatal("event transport changed original lease", e)
				}
			}
		})
	}
}
