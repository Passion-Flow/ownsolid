package fleet

import (
	"context"
	"errors"
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/tools/dev/simulator/internal/link"
	"github.com/Passion-Flow/ownsolid/tools/dev/simulator/internal/report"
)

// sessionIdleMarginSeconds replaces a stream this long before control would close it, as
// the agent does.
const sessionIdleMarginSeconds = 5

// agent is one simulated read-only node. Its cycle follows the real agent's run loop
// (services/agent/internal/runtime): heartbeat, claim held for most of the interval,
// acknowledgement before execution, then progress and the result; one retry layer, paced
// by the pacing control sends. Only its goroutine touches the fields above mu.
type agent struct {
	host     string
	node     link.Node
	longTask time.Duration
	cursor   uint64
	dialer   *link.Dialer
	conn     *link.Connection
	recorder *report.Recorder

	rebalanceInterval time.Duration
	pacing            wire.Pacing
	failures          int
	stream            *link.Stream
	lastExchange      time.Time
	fallbackUntil     time.Time
	probeHTTPS        bool
	probes            int

	// wake ends the wait between cycles early, for a restart.
	wake chan struct{}

	mu sync.Mutex
	// The faults the fleet set on this agent.
	partitionUntil   time.Time
	httpsOnlyUntil   time.Time
	loseRepliesUntil time.Time
	longTasksUntil   time.Time
	restart          bool
	loseCursor       bool
	pending          []recovery

	connected atomic.Bool
	onStream  atomic.Bool
	revoked   atomic.Bool
}

// recovery is a fault whose end awaits this agent's next good heartbeat.
type recovery struct {
	fault int
	since time.Time
}

func newAgent(identity Identity, dialer *link.Dialer, conn *link.Connection, recorder *report.Recorder, longTask time.Duration) *agent {
	return &agent{host: identity.Hostname, node: identity.node(), longTask: longTask, cursor: identity.AcknowledgedSequence, dialer: dialer, conn: conn, recorder: recorder, pacing: wire.DefaultPacing(), wake: make(chan struct{}, 1)}
}

// run cycles until ctx ends or control revokes the credential, starting after delay.
func (a *agent) run(ctx context.Context, delay time.Duration) {
	defer a.dropStream()
	for a.wait(ctx, delay) {
		started := time.Now()
		e := a.cycle(ctx)
		if ctx.Err() != nil {
			return
		}
		if e == nil {
			a.failures = 0
			delay = max(0, a.pacing.Interval(rand.Int64N)-time.Since(started))
			continue
		}
		if link.Revoked(e) {
			a.revoked.Store(true)
			a.connected.Store(false)
			return
		}
		delay = a.pacing.Backoff(rand.Int64N, a.failures)
		a.failures++
		if hint, ok := link.RetryHint(e); ok {
			delay = max(delay, wire.AfterHint(rand.Int64N, hint))
		}
	}
}

// wait waits for d, or less when the agent restarts, and reports whether to go on.
func (a *agent) wait(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
	case <-a.wake:
	}
	return true
}

// cycle reports the node, then claims and runs at most one task.
func (a *agent) cycle(ctx context.Context) error {
	a.applyFaults()
	disposition, e := a.heartbeat(ctx, handshake(a.host))
	if e != nil {
		// A heartbeat the end of the run cut short says nothing about the connection.
		if ctx.Err() == nil {
			a.connected.Store(false)
		}
		return e
	}
	a.connected.Store(true)
	a.onStream.Store(a.stream != nil)
	a.recovered(time.Now())
	if p := disposition.Pacing; p != nil && p.Validate() == nil {
		a.pacing = *p
	}
	switch disposition.Status {
	case "disabled":
		return nil
	case "active":
	default:
		return errors.New("AGENT_DISPOSITION_INVALID")
	}
	claim, e := a.claim(ctx, a.pacing.ClaimWaitSeconds(), a.cursor)
	if e != nil {
		return e
	}
	if claim.Resync != nil {
		a.recorder.Task(func(t *report.Tasks) { t.Resyncs++ })
		a.cursor = claim.Resync.AcknowledgedSequence
		return nil
	}
	if claim.Lease == nil {
		return nil
	}
	return a.execute(ctx, *claim.Lease)
}

// applyFaults takes the one-time faults set since the last cycle.
func (a *agent) applyFaults() {
	a.mu.Lock()
	restart, loseCursor := a.restart, a.loseCursor
	a.restart, a.loseCursor = false, false
	a.mu.Unlock()
	if restart {
		// A new process: no stream, no fallback, default pacing, no backoff. The cursor is
		// on disk and survives.
		a.dropStream()
		a.fallbackUntil, a.probeHTTPS, a.probes, a.failures, a.pacing = time.Time{}, false, 0, 0, wire.DefaultPacing()
	}
	if loseCursor {
		a.cursor = 0
	}
}

// recovered reports a good heartbeat at now to the faults waiting for one.
func (a *agent) recovered(now time.Time) {
	a.mu.Lock()
	defer a.mu.Unlock()
	kept := a.pending[:0]
	for _, r := range a.pending {
		if now.Before(r.since) {
			kept = append(kept, r)
			continue
		}
		a.recorder.Recovered(r.fault, now.Sub(r.since))
	}
	a.pending = kept
}

func (a *agent) active(until *time.Time) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return time.Now().Before(*until)
}

func (a *agent) partitioned() bool { return a.active(&a.partitionUntil) }

// losesReply reports whether control's reply to the next acknowledgement or result is lost.
func (a *agent) losesReply() bool {
	if !a.active(&a.loseRepliesUntil) {
		return false
	}
	a.recorder.Task(func(t *report.Tasks) { t.LostReplies++ })
	return true
}

func (a *agent) dropStream() {
	if a.stream != nil {
		a.stream.Close()
		a.stream = nil
	}
}

// heartbeat reports the node on the stream, opening one when there is none, or over HTTPS
// while the agent has fallen back (ADR 0007). It mirrors the agent's Session.Heartbeat.
func (a *agent) heartbeat(ctx context.Context, h wire.Handshake) (wire.Disposition, error) {
	idleDeadline := a.lastExchange.Add(time.Duration(a.pacing.IdleSeconds()-sessionIdleMarginSeconds) * time.Second)
	if a.stream != nil && time.Until(idleDeadline) <= 0 {
		a.dropStream()
	}
	if a.stream == nil {
		if time.Now().Before(a.fallbackUntil) {
			return a.heartbeatHTTPS(ctx, h)
		}
		if a.probeHTTPS {
			// The stream failed last time. HTTPS working once may only mean control just came
			// back; working every time the stream fails means the path lacks HTTP/2.
			a.probeHTTPS = false
			out, e := a.heartbeatHTTPS(ctx, h)
			if e != nil {
				a.probes = 0
				return out, e
			}
			if a.probes++; a.probes >= wire.SessionFallbackProbes {
				a.probes = 0
				a.fallbackUntil = time.Now().Add(time.Duration(wire.SessionFallbackRetrySeconds) * time.Second)
			}
			return out, nil
		}
		return a.openWithHeartbeat(ctx, h)
	}

	if a.rebalanceInterval > 0 && !a.partitioned() && !a.active(&a.httpsOnlyUntil) {
		if disposition := a.tryRebalance(ctx, h, idleDeadline); disposition != nil {
			return *disposition, nil
		}
	}
	if !a.partitioned() && !a.active(&a.httpsOnlyUntil) {
		if disposition := a.tryConnectionHandoff(ctx, h, idleDeadline); disposition != nil {
			return *disposition, nil
		}
	}
	reply, e := a.exchange(ctx, report.CallHeartbeat, wire.AgentMessage{Heartbeat: &h})
	if e != nil {
		return wire.Disposition{}, e
	}
	return *reply.Disposition, nil
}

func (a *agent) tryRebalance(ctx context.Context, h wire.Handshake, idleDeadline time.Time) *wire.Disposition {
	started := time.Now()
	probe, cancel := context.WithDeadline(ctx, idleDeadline)
	defer cancel()
	stream, disposition, e := a.conn.Rebalance(probe, a.stream, a.rebalanceInterval, a.node, h)
	if e != nil || stream != nil {
		a.record(ctx, report.CallGatewayRebalance, started, e)
	}
	if stream == nil {
		return nil
	}
	a.dropStream()
	a.stream = stream
	a.lastExchange = time.Now()
	a.recorder.Succeeded(report.CallHeartbeat, time.Since(started))
	return disposition
}

// openWithHeartbeat opens a stream whose first message is the heartbeat h. Control answers
// a stream only with that first reply, so opening counts as one call with it, and a
// stream that never answered makes the next attempt try HTTPS, as in the agent.
func (a *agent) openWithHeartbeat(ctx context.Context, h wire.Handshake) (wire.Disposition, error) {
	started := time.Now()
	var reply wire.ControlMessage
	e := link.ErrUnreachable
	if !a.partitioned() && !a.active(&a.httpsOnlyUntil) {
		a.stream, e = a.conn.Open(ctx, a.node)
	}
	if e == nil {
		reply, e = a.exchange(ctx, report.CallHeartbeat, wire.AgentMessage{Heartbeat: &h})
	}
	if e != nil && reply.Problem == nil {
		a.dropStream()
		a.fail(ctx, report.CallStreamOpen, e)
		unanswered := errors.Is(e, link.ErrUnreachable) && ctx.Err() == nil
		// As in the agent, another gateway gets the next attempt while some are untried.
		untried := (unanswered || errors.Is(e, link.ErrBusy)) && a.conn.Next()
		a.probeHTTPS = unanswered && !untried
		return wire.Disposition{}, e
	}
	a.conn.Opened()
	a.recorder.Succeeded(report.CallStreamOpen, time.Since(started))
	a.probes = 0
	if e != nil {
		return wire.Disposition{}, e
	}
	return *reply.Disposition, nil
}

func (a *agent) heartbeatHTTPS(ctx context.Context, h wire.Handshake) (wire.Disposition, error) {
	var out wire.Disposition
	e := a.post(ctx, report.CallHeartbeat, a.node.Base()+"/heartbeat", h, &out)
	return out, e
}

// claim asks for the next lease, letting control hold the claim for wait seconds.
func (a *agent) claim(ctx context.Context, wait int, acknowledged uint64) (wire.Claim, error) {
	request := wire.ClaimRequest{WaitSeconds: wait, AcknowledgedSequence: acknowledged}
	if a.stream != nil {
		reply, e := a.exchange(ctx, report.CallClaim, wire.AgentMessage{Claim: &request})
		if e != nil {
			return wire.Claim{}, e
		}
		return *reply.Claim, nil
	}
	if a.partitioned() {
		a.fail(ctx, report.CallClaim, link.ErrUnreachable)
		return wire.Claim{}, link.ErrUnreachable
	}
	var out wire.Claim
	started := time.Now()
	e := a.dialer.LongPoll(ctx, a.node.Base()+"/tasks/claims", a.node.Credential, wait, request, &out)
	if e == nil && out.Validate() != nil {
		e = link.ErrInvalid
	}
	a.record(ctx, report.CallClaim, started, e)
	return out, e
}

// execute acknowledges lease and, once control confirms, collects facts and posts them.
func (a *agent) execute(ctx context.Context, lease wire.Lease) error {
	a.recorder.Task(func(t *report.Tasks) { t.Leases++ })
	if lease.Operation != wire.CollectFacts || lease.Component != nil || lease.Inference != nil || lease.Training != nil || lease.Preparation != nil {
		return errors.New("AGENT_TASK_UNSUPPORTED")
	}
	if e := wire.AcceptLease(a.cursor, lease); e != nil {
		return e
	}
	// The cursor moves before the acknowledgement, so it is never behind control's record.
	a.cursor = max(a.cursor, lease.Sequence)
	if e := a.acknowledge(ctx, lease); e != nil {
		return e
	}
	a.recorder.Task(func(t *report.Tasks) { t.Acknowledged++ })
	task := a.node.Base() + "/tasks/" + lease.TaskID
	collected := facts(a.host)
	var accepted wire.TaskProgressAccepted
	// Progress is telemetry: the task goes on when a report is refused.
	_ = a.post(ctx, report.CallEvents, task+"/"+wire.TaskProgressPath, wire.TaskProgress{LeaseToken: lease.LeaseToken, Entries: progress(collected)}, &accepted)
	result := wire.Result{LeaseToken: lease.LeaseToken, Outcome: "success", Facts: &collected}
	var completed struct {
		State string `json:"state"`
	}
	if a.active(&a.longTasksUntil) {
		if e := a.work(ctx, task, lease.LeaseToken); e != nil {
			return e
		}
		if e := a.deliver(ctx, task+"/results", result, &completed); e != nil {
			return e
		}
		a.recorder.Task(func(t *report.Tasks) { t.Succeeded++ })
		return nil
	}
	if e := a.post(ctx, report.CallResult, task+"/results", result, &completed); e != nil {
		return e
	}
	if a.losesReply() {
		// The agent never saw the answer, so it delivers the result again, as the agent's
		// result delivery does. Control must accept it as the same result.
		if e := a.post(ctx, report.CallResult, task+"/results", result, &completed); e != nil {
			return e
		}
	}
	a.recorder.Task(func(t *report.Tasks) { t.Succeeded++ })
	return nil
}

// work stands for a long task: it renews the lease until the task is done, as the agent
// does. Failing to reach control ends nothing; only a refusal control wrote does (ADR 0032).
func (a *agent) work(ctx context.Context, task, token string) error {
	finished := time.NewTimer(a.longTask)
	defer finished.Stop()
	ticker := time.NewTicker(time.Duration(wire.LeaseRenewalSeconds) * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-finished.C:
			return nil
		case <-ticker.C:
			var renewed wire.RenewedLease
			if e := a.post(ctx, report.CallRenew, task+"/renewals", wire.LeaseRenewal{LeaseToken: token}, &renewed); e != nil && !comeBack(e) {
				return e
			}
		}
	}
}

// deliver posts the result of finished work again whenever control cannot be reached,
// until control answers or the run ends, as the agent's result delivery does.
func (a *agent) deliver(ctx context.Context, path string, result wire.Result, out any) error {
	for attempt := 0; ; attempt++ {
		e := a.post(ctx, report.CallResult, path, result, out)
		if e == nil || !comeBack(e) {
			return e
		}
		if !sleep(ctx, wire.DefaultPacing().Backoff(rand.Int64N, attempt)) {
			return ctx.Err()
		}
	}
}

// acknowledge confirms the lease on the stream and, when the stream fails or its reply is
// lost, once more over HTTPS, as the agent does.
func (a *agent) acknowledge(ctx context.Context, lease wire.Lease) error {
	ack := wire.TaskAcknowledgement{TaskID: lease.TaskID, Sequence: lease.Sequence, LeaseToken: lease.LeaseToken}
	if a.stream != nil {
		_, e := a.exchange(ctx, report.CallAcknowledge, wire.AgentMessage{Ack: &ack})
		var refused *link.Problem
		if errors.As(e, &refused) {
			return e
		}
		if e == nil && !a.losesReply() {
			return nil
		}
		a.dropStream()
	}
	var renewed wire.RenewedLease
	return a.post(ctx, report.CallAcknowledge, a.node.Base()+"/tasks/"+lease.TaskID+"/"+wire.TaskAcknowledgementsPath, wire.TaskAcknowledgementRequest{Sequence: ack.Sequence, LeaseToken: ack.LeaseToken}, &renewed)
}

// exchange makes one exchange on the stream. A broken stream is dropped; a refusal
// leaves it open.
func (a *agent) exchange(ctx context.Context, call string, m wire.AgentMessage) (wire.ControlMessage, error) {
	if a.partitioned() {
		a.dropStream()
		a.fail(ctx, call, link.ErrUnreachable)
		return wire.ControlMessage{}, link.ErrUnreachable
	}
	started := time.Now()
	reply, e := a.stream.Exchange(ctx, m)
	a.record(ctx, call, started, e)
	var refused *link.Problem
	if e != nil && !errors.As(e, &refused) {
		a.dropStream()
		return reply, e
	}
	a.lastExchange = time.Now()
	return reply, e
}

// post makes one HTTPS call, failing at once while partitioned.
func (a *agent) post(ctx context.Context, call, path string, value, out any) error {
	if a.partitioned() {
		a.fail(ctx, call, link.ErrUnreachable)
		return link.ErrUnreachable
	}
	started := time.Now()
	e := a.dialer.Post(ctx, path, a.node.Credential, value, out)
	a.record(ctx, call, started, e)
	return e
}

func (a *agent) record(ctx context.Context, call string, started time.Time, e error) {
	if e == nil {
		a.recorder.Succeeded(call, time.Since(started))
		return
	}
	a.fail(ctx, call, e)
}

// fail counts a failed call, except one the end of the run cut short.
func (a *agent) fail(ctx context.Context, call string, e error) {
	if ctx.Err() == nil {
		a.recorder.Failed(call, link.Code(e))
	}
}

func (a *agent) tryConnectionHandoff(ctx context.Context, h wire.Handshake, idleDeadline time.Time) *wire.Disposition {
	started := time.Now()
	stream, disposition, err := a.conn.Handoff(ctx, a.stream, a.node, h, a.pacing, idleDeadline)
	if err != nil || stream != nil {
		a.record(ctx, report.CallConnectionHandoff, started, err)
	}
	if stream == nil {
		return nil
	}
	a.dropStream()
	a.stream = stream
	a.lastExchange = time.Now()
	a.recorder.Succeeded(report.CallHeartbeat, time.Since(started))
	return disposition
}
