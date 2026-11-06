package runtime

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/agent/internal/client"
	"github.com/Passion-Flow/ownsolid/services/agent/internal/state"
)

// deliveryControl is a stand-in control plane on HTTPS without HTTP/2, so the session
// uses its HTTPS fallback. It answers claims with claim and records every exchange.
type deliveryControl struct {
	mu       sync.Mutex
	events   []string
	cursors  []uint64
	claim    wire.Claim
	ackError bool
}

func (d *deliveryControl) record(event string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.events = append(d.events, event)
}

func (d *deliveryControl) log() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.events...)
}

func deliveryServer(t *testing.T, d *deliveryControl, identity state.Identity) *client.Client {
	t.Helper()
	base := "/agent/v1/sites/default/cells/default/nodes/" + identity.NodeID
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == base+"/heartbeat":
			d.record("heartbeat")
			json.NewEncoder(w).Encode(map[string]any{"data": wire.Disposition{Status: "active", ServerTime: time.Now().UTC()}})
		case r.URL.Path == base+"/tasks/claims":
			var request wire.ClaimRequest
			json.NewDecoder(r.Body).Decode(&request)
			d.mu.Lock()
			d.cursors = append(d.cursors, request.AcknowledgedSequence)
			claim := d.claim
			d.mu.Unlock()
			d.record("claim")
			json.NewEncoder(w).Encode(map[string]any{"data": claim})
		case strings.HasSuffix(r.URL.Path, "/"+wire.TaskAcknowledgementsPath):
			var request wire.TaskAcknowledgementRequest
			json.NewDecoder(r.Body).Decode(&request)
			d.record("ack")
			d.mu.Lock()
			refuse := d.ackError
			d.mu.Unlock()
			if refuse {
				w.Header().Set("Content-Type", "application/problem+json")
				w.WriteHeader(http.StatusConflict)
				json.NewEncoder(w).Encode(map[string]any{"code": "AGENT_LEASE_STALE"})
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"data": wire.RenewedLease{LeaseExpiresAt: time.Now().UTC().Add(30 * time.Second), ValidForMilliseconds: 30000}})
		case strings.HasSuffix(r.URL.Path, "/"+wire.TaskProgressPath):
			var report wire.TaskProgress
			json.NewDecoder(r.Body).Decode(&report)
			if e := report.Validate(); e != nil || report.LeaseToken == "" {
				t.Error("report", e)
			}
			d.record("progress")
			json.NewEncoder(w).Encode(map[string]any{"data": wire.TaskProgressAccepted{Accepted: len(report.Entries)}})
		case strings.HasSuffix(r.URL.Path, "/results"):
			d.record("result")
			json.NewEncoder(w).Encode(map[string]any{"data": map[string]string{"state": "succeeded"}})
		default:
			t.Error("unexpected endpoint", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	connection := &client.Client{Origin: server.URL, HTTP: server.Client(), Credential: identity.Credential, Scope: identity.Scope()}
	t.Cleanup(connection.Close)
	return connection
}

func TestAgentAcknowledgesBeforeExecutingAndKeepsItsCursor(t *testing.T) {
	identity := state.Identity{SiteID: "default", CellID: "default", NodeID: "00000000-0000-4000-8000-000000000001", Credential: strings.Repeat("B", 43)}
	lease := &wire.Lease{TaskID: "00000000-0000-4000-8000-000000000002", Sequence: 5, Operation: wire.CollectFacts, LeaseToken: strings.Repeat("L", 43), Attempt: 1, LeaseExpiresAt: time.Now().UTC().Add(30 * time.Second)}
	d := &deliveryControl{claim: wire.Claim{Lease: lease}}
	connection := deliveryServer(t, d, identity)
	dir := t.TempDir()
	if e := os.Chmod(dir, 0o700); e != nil {
		t.Fatal(e)
	}
	c := Config{DiskPath: dir, StateDirectory: dir}
	run := func() error {
		session := connection.Session(identity.NodeID)
		defer session.Close()
		return claimAndRun(t.Context(), c, connection, session, identity, wire.Facts{}, nil, nil, nil, nil, nil)
	}
	reset := func(claim wire.Claim, refuseAck bool) {
		d.mu.Lock()
		d.events, d.claim, d.ackError = nil, claim, refuseAck
		d.mu.Unlock()
	}

	// A new lease: the cursor is saved, the lease acknowledged, then executed.
	if e := run(); e != nil {
		t.Fatal(e)
	}
	if got := d.log(); !reflect.DeepEqual(got, []string{"heartbeat", "claim", "ack", "progress", "result"}) {
		t.Fatal("order", got)
	}
	if cursor, e := state.LoadCursor(dir); e != nil || cursor != 5 {
		t.Fatal("cursor", cursor, e)
	}

	// A refused acknowledgement means the lease is not ours: nothing executes.
	next := *lease
	next.Sequence = 6
	reset(wire.Claim{Lease: &next}, true)
	if e := run(); e == nil {
		t.Fatal("executed without an acknowledgement")
	}
	if got := d.log(); !reflect.DeepEqual(got, []string{"heartbeat", "claim", "ack"}) {
		t.Fatal("refused ack order", got)
	}

	// A lease older than the cursor is out of order: neither acknowledged nor executed.
	older := *lease
	older.Sequence = 2
	reset(wire.Claim{Lease: &older}, false)
	if e := run(); e == nil || e.Error() != "AGENT_TASK_SEQUENCE_STALE" {
		t.Fatal("older lease", e)
	}
	if got := d.log(); !reflect.DeepEqual(got, []string{"heartbeat", "claim"}) {
		t.Fatal("older lease order", got)
	}

	// A resync adopts control's cursor after recovery and runs nothing.
	reset(wire.Claim{Resync: &wire.Resync{AcknowledgedSequence: 3}}, false)
	if e := run(); e != nil {
		t.Fatal(e)
	}
	if got := d.log(); !reflect.DeepEqual(got, []string{"heartbeat", "claim"}) {
		t.Fatal("resync order", got)
	}
	if cursor, e := state.LoadCursor(dir); e != nil || cursor != 3 {
		t.Fatal("cursor after resync", cursor, e)
	}
	// Every claim carried the cursor the agent held at the time.
	d.mu.Lock()
	defer d.mu.Unlock()
	if !reflect.DeepEqual(d.cursors, []uint64{0, 5, 6, 6}) {
		t.Fatal("cursors", d.cursors)
	}
}
