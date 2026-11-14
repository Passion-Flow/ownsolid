package agentadmission

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
)

const (
	testNode       = "6f1c1d0e-6b1a-4d55-9a3c-2f8b5d1e7a10"
	testCredential = "credential-of-the-node"
)

// testPolicy has few seats and long waits, so tests decide when seats free up.
func testPolicy() config.AgentAdmissionPolicy {
	p := config.DefaultAgentAdmissionPolicy()
	p.Seats = 4
	p.Quarantine = config.AgentPriorityPolicy{MaxSeats: 4, QueueLength: 8, WaitMilliseconds: 5000, RetrySeconds: 1}
	p.Heartbeat = config.AgentPriorityPolicy{MaxSeats: 4, QueueLength: 8, WaitMilliseconds: 5000, RetrySeconds: 2}
	p.Telemetry = config.AgentPriorityPolicy{MaxSeats: 1, QueueLength: 8, WaitMilliseconds: 5000, RetrySeconds: 5}
	p.Bulk = config.AgentPriorityPolicy{MaxSeats: 2, QueueLength: 8, WaitMilliseconds: 5000, RetrySeconds: 10}
	return p
}

type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func newController(t *testing.T, p config.AgentAdmissionPolicy) (*Controller, *clock, *bytes.Buffer) {
	t.Helper()
	logs := &bytes.Buffer{}
	c, e := New(p, 0, slog.New(slog.NewJSONHandler(logs, nil)))
	if e != nil {
		t.Fatal(e)
	}
	fake := &clock{now: time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC)}
	c.now = fake.Now
	return c, fake, logs
}

func request(call wire.Call) Request {
	return Request{Call: call, Address: "192.0.2.10", Node: testNode, Credential: testCredential}
}

func refusal(t *testing.T, e error, code string, retry int) {
	t.Helper()
	var r *Refusal
	if !errors.As(e, &r) || r.Code != code || r.RetrySeconds() != retry || r.Error() != code {
		t.Fatalf("refusal %v, want %s retry %d", e, code, retry)
	}
}

func TestNewRefusesAnInvalidPolicyOrNoLogger(t *testing.T) {
	bad := testPolicy()
	bad.Seats = 0
	if _, e := New(bad, 0, slog.Default()); e == nil {
		t.Fatal("invalid policy accepted")
	}
	if _, e := New(testPolicy(), 0, nil); e == nil {
		t.Fatal("missing logger accepted")
	}
}

func TestEachNodeCallHasItsOwnLimit(t *testing.T) {
	p := testPolicy()
	p.Calls.Heartbeat = config.AgentCallLimit{PerMinute: 6, Burst: 2}
	c, fake, _ := newController(t, p)
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		release, e := c.Admit(ctx, request(wire.CallHeartbeat))
		if e != nil {
			t.Fatalf("heartbeat %d: %v", i, e)
		}
		release()
	}
	_, e := c.Admit(ctx, request(wire.CallHeartbeat))
	// Six a minute is one every ten seconds.
	refusal(t, e, "RATE_LIMITED", 10)
	// Another call of the same node has its own allowance.
	release, e := c.Admit(ctx, request(wire.CallResult))
	if e != nil {
		t.Fatalf("result after heartbeats: %v", e)
	}
	release()
	// A caller without the node's credential has its own bucket and cannot spend the
	// node's allowance.
	other := request(wire.CallHeartbeat)
	other.Credential = "guessed"
	release, e = c.Admit(ctx, other)
	if e != nil {
		t.Fatalf("other credential: %v", e)
	}
	release()
	fake.advance(4 * time.Second)
	_, e = c.Admit(ctx, request(wire.CallHeartbeat))
	refusal(t, e, "RATE_LIMITED", 6)
	fake.advance(6 * time.Second)
	if release, e = c.Admit(ctx, request(wire.CallHeartbeat)); e != nil {
		t.Fatalf("heartbeat after refill: %v", e)
	}
	release()
}

func TestAddressLimitAppliesBeforeAnyCredential(t *testing.T) {
	p := testPolicy()
	p.Address = config.AgentCallLimit{PerMinute: 60, Burst: 3}
	c, _, _ := newController(t, p)
	for i := 0; i < 3; i++ {
		release, e := c.Admit(context.Background(), Request{Call: wire.CallEnroll, Address: "198.51.100.7"})
		if e != nil {
			t.Fatalf("call %d: %v", i, e)
		}
		release()
	}
	_, e := c.Admit(context.Background(), Request{Call: wire.CallHeartbeat, Address: "198.51.100.7", Node: testNode, Credential: testCredential})
	refusal(t, e, "RATE_LIMITED", 1)
	release, e := c.Admit(context.Background(), Request{Call: wire.CallEnroll, Address: "198.51.100.8"})
	if e != nil {
		t.Fatalf("another address: %v", e)
	}
	release()
}

func TestUnknownCallIsNotFound(t *testing.T) {
	c, _, _ := newController(t, testPolicy())
	if _, e := c.Admit(context.Background(), request("telemetry")); e == nil || e.Error() != "RESOURCE_NOT_FOUND" {
		t.Fatalf("unknown call: %v", e)
	}
	if _, e := c.Enter(context.Background(), wire.Priority(len(priorityOrder))); e == nil {
		t.Fatal("a seat of an unknown priority")
	}
}

func TestFullTableRefusesNewCallersUntilASweepMakesRoom(t *testing.T) {
	p := testPolicy()
	p.TrackedKeys = 1024
	c, fake, _ := newController(t, p)
	ctx := context.Background()
	// Each distinct address holds one bucket below full.
	for i := 0; i < p.TrackedKeys; i++ {
		release, e := c.Admit(ctx, Request{Call: wire.CallEnroll, Address: "addr-" + string(rune('a'+i%26)) + strings.Repeat("x", i/26)})
		if e != nil {
			t.Fatalf("address %d: %v", i, e)
		}
		release()
	}
	_, e := c.Admit(ctx, Request{Call: wire.CallEnroll, Address: "late"})
	refusal(t, e, "AGENT_OVERLOADED", p.Bulk.RetrySeconds)
	// Once the buckets are full again a sweep drops them.
	fake.advance(time.Minute)
	release, e := c.Admit(ctx, Request{Call: wire.CallEnroll, Address: "late"})
	if e != nil {
		t.Fatalf("after refill: %v", e)
	}
	release()
	if c.limits.size() != 1 {
		t.Fatalf("tracked %d keys after the sweep", c.limits.size())
	}
}

func TestBulkCannotTakeTheSeatsOfHeartbeats(t *testing.T) {
	p := testPolicy()
	p.Bulk.WaitMilliseconds = 0
	c, _, _ := newController(t, p)
	ctx := context.Background()
	var held []func()
	for i := 0; i < p.Bulk.MaxSeats; i++ {
		release, e := c.Admit(ctx, request(wire.CallDownload))
		if e != nil {
			t.Fatalf("download %d: %v", i, e)
		}
		held = append(held, release)
	}
	_, e := c.Admit(ctx, request(wire.CallDownload))
	refusal(t, e, "AGENT_OVERLOADED", p.Bulk.RetrySeconds)
	for i := 0; i < p.Seats-p.Bulk.MaxSeats; i++ {
		release, e := c.Admit(ctx, request(wire.CallHeartbeat))
		if e != nil {
			t.Fatalf("heartbeat %d with bulk at its cap: %v", i, e)
		}
		held = append(held, release)
	}
	for _, release := range held {
		release()
		release() // A second release frees nothing.
	}
	for _, p := range priorityOrder {
		if used, queued := c.seats.load(p); used != 0 || queued != 0 {
			t.Fatalf("%s: %d seats in use, %d queued after release", p, used, queued)
		}
	}
}

func TestFreedSeatGoesToTheHighestPriorityWaiter(t *testing.T) {
	c, _, _ := newController(t, testPolicy())
	ctx := context.Background()
	var held []func()
	for i := 0; i < 4; i++ {
		release, e := c.Enter(ctx, wire.PriorityHeartbeat)
		if e != nil {
			t.Fatal(e)
		}
		held = append(held, release)
	}
	order := make(chan wire.Priority, 3)
	var waiting sync.WaitGroup
	for _, p := range []wire.Priority{wire.PriorityBulk, wire.PriorityTelemetry, wire.PriorityQuarantine} {
		waiting.Add(1)
		go func(p wire.Priority) {
			defer waiting.Done()
			release, e := c.Enter(ctx, p)
			if e != nil {
				t.Errorf("%s: %v", p, e)
				return
			}
			order <- p
			release()
		}(p)
		// Queue them in this order, lowest priority first.
		for {
			if _, queued := c.seats.load(p); queued == 1 {
				break
			}
			time.Sleep(time.Millisecond)
		}
	}
	// One seat frees at a time; each goes to the best waiter, which returns it at once.
	held[0]()
	if got := <-order; got != wire.PriorityQuarantine {
		t.Fatalf("first seat went to %s", got)
	}
	if got := <-order; got != wire.PriorityTelemetry {
		t.Fatalf("second seat went to %s", got)
	}
	if got := <-order; got != wire.PriorityBulk {
		t.Fatalf("third seat went to %s", got)
	}
	waiting.Wait()
	for _, release := range held[1:] {
		release()
	}
}

func TestWaitEndsInARefusalWithTheRetryHint(t *testing.T) {
	p := testPolicy()
	p.Telemetry.WaitMilliseconds = 20
	p.Telemetry.QueueLength = 1
	c, _, logs := newController(t, p)
	ctx := context.Background()
	release, e := c.Admit(ctx, request(wire.CallEvents))
	if e != nil {
		t.Fatal(e)
	}
	defer release()
	started := time.Now()
	queued := make(chan error, 1)
	go func() {
		_, e := c.Admit(ctx, request(wire.CallEvents))
		queued <- e
	}()
	for {
		if _, n := c.seats.load(wire.PriorityTelemetry); n == 1 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	// The queue holds one, so a third call is refused without waiting.
	_, e = c.Admit(ctx, request(wire.CallEvents))
	refusal(t, e, "AGENT_OVERLOADED", 5)
	refusal(t, <-queued, "AGENT_OVERLOADED", 5)
	if waited := time.Since(started); waited < 20*time.Millisecond {
		t.Fatalf("queued call refused after %v", waited)
	}
	if _, n := c.seats.load(wire.PriorityTelemetry); n != 0 {
		t.Fatalf("%d calls left queued", n)
	}
	// Two refusals within the log interval make one line.
	if lines := strings.Count(logs.String(), "agent_call_refused"); lines != 1 {
		t.Fatalf("%d refusal log lines:\n%s", lines, logs)
	}
}

func TestCancelledWaiterLeavesTheQueue(t *testing.T) {
	p := testPolicy()
	p.Telemetry.MaxSeats = 1
	c, _, _ := newController(t, p)
	release, e := c.Enter(context.Background(), wire.PriorityTelemetry)
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, e := c.Enter(ctx, wire.PriorityTelemetry)
		done <- e
	}()
	for {
		if _, n := c.seats.load(wire.PriorityTelemetry); n == 1 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	if e := <-done; !errors.Is(e, context.Canceled) {
		t.Fatalf("cancelled waiter: %v", e)
	}
	release()
	if used, queued := c.seats.load(wire.PriorityTelemetry); used != 0 || queued != 0 {
		t.Fatalf("%d in use, %d queued", used, queued)
	}
}

func TestHeldClaimHoldsNoSeat(t *testing.T) {
	p := testPolicy()
	p.Heartbeat.WaitMilliseconds = 0
	c, _, _ := newController(t, p)
	ctx := context.Background()
	for i := 0; i < p.Seats+2; i++ {
		other := request(wire.CallClaim)
		other.Node = testNode[:35] + string(rune('a'+i))
		release, e := c.Admit(ctx, other)
		if e != nil {
			t.Fatalf("claim %d: %v", i, e)
		}
		defer release()
	}
	if used, _ := c.seats.load(wire.PriorityHeartbeat); used != 0 {
		t.Fatalf("held claims use %d seats", used)
	}
	// A look for work takes a seat of the claim's priority.
	release, e := c.Enter(ctx, wire.CallClaim.Priority())
	if e != nil {
		t.Fatal(e)
	}
	if used, _ := c.seats.load(wire.PriorityHeartbeat); used != 1 {
		t.Fatalf("a look uses %d seats", used)
	}
	release()
}

func TestSamplesReportSeatsAndRefusals(t *testing.T) {
	p := testPolicy()
	p.Calls.Upload = config.AgentCallLimit{PerMinute: 60, Burst: 1}
	c, _, _ := newController(t, p)
	release, e := c.Admit(context.Background(), request(wire.CallUpload))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = c.Admit(context.Background(), request(wire.CallUpload)); e == nil {
		t.Fatal("second upload admitted")
	}
	values := map[string]float64{}
	for _, s := range c.Samples() {
		key := s.Name
		for _, l := range s.Labels {
			key += "," + l.Name + "=" + l.Value
		}
		values[key] = s.Value
	}
	release()
	for key, want := range map[string]float64{
		"ownsolid_agent_admission_seats_in_use,priority=bulk":                       1,
		"ownsolid_agent_admission_admitted_total,priority=bulk":                     1,
		"ownsolid_agent_admission_refused_total,priority=bulk,reason=node_rate":     1,
		"ownsolid_agent_admission_refused_total,priority=heartbeat,reason=overload": 0,
		"ownsolid_agent_admission_queued_calls,priority=quarantine":                 0,
		"ownsolid_agent_admission_tracked_keys":                                     2,
	} {
		if got, ok := values[key]; !ok || got != want {
			t.Errorf("%s = %v (present %v), want %v", key, got, ok, want)
		}
	}
	// Four priorities, each with seats, queue, admitted and four refusal reasons.
	if len(values) != 2+4*(3+len(reasonNames)) {
		t.Fatalf("%d series", len(values))
	}
}

func TestConcurrentCallsNeverExceedTheSeats(t *testing.T) {
	p := testPolicy()
	p.Seats = 8
	p.Quarantine.MaxSeats, p.Heartbeat.MaxSeats, p.Telemetry.MaxSeats, p.Bulk.MaxSeats = 8, 8, 3, 3
	for _, priority := range []*config.AgentPriorityPolicy{&p.Quarantine, &p.Heartbeat, &p.Telemetry, &p.Bulk} {
		priority.QueueLength, priority.WaitMilliseconds = 64, 2000
	}
	c, _, _ := newController(t, p)
	var inUse, peak, bulk, bulkPeak atomic.Int64
	var group sync.WaitGroup
	for i := 0; i < 400; i++ {
		group.Add(1)
		go func(i int) {
			defer group.Done()
			priority := priorityOrder[i%len(priorityOrder)]
			release, e := c.Enter(context.Background(), priority)
			if e != nil {
				return
			}
			now := inUse.Add(1)
			for old := peak.Load(); now > old && !peak.CompareAndSwap(old, now); old = peak.Load() {
			}
			if priority == wire.PriorityBulk {
				b := bulk.Add(1)
				for old := bulkPeak.Load(); b > old && !bulkPeak.CompareAndSwap(old, b); old = bulkPeak.Load() {
				}
			}
			time.Sleep(time.Millisecond)
			// Count the seat as free before it is, never after.
			if priority == wire.PriorityBulk {
				bulk.Add(-1)
			}
			inUse.Add(-1)
			release()
		}(i)
	}
	group.Wait()
	if peak.Load() > int64(p.Seats) || bulkPeak.Load() > int64(p.Bulk.MaxSeats) {
		t.Fatalf("peak %d of %d seats, bulk %d of %d", peak.Load(), p.Seats, bulkPeak.Load(), p.Bulk.MaxSeats)
	}
	for _, priority := range priorityOrder {
		if used, queued := c.seats.load(priority); used != 0 || queued != 0 {
			t.Fatalf("%s: %d in use, %d queued", priority, used, queued)
		}
	}
}
