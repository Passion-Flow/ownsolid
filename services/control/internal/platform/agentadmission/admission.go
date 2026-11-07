// Package agentadmission decides, in memory and before any database work, whether this
// control process serves an agent call now (ADR 0018). Every call passes a limit on its
// client address and, when it names a node, a limit on that node's calls of its kind.
// Then it takes one of the process's serving seats. Seats go to the highest priority
// first, so under load control refuses bulk work before telemetry, telemetry before
// heartbeats, and heartbeats before task results and stop receipts. A refusal carries a
// retry hint that agents honor.
//
// Limits are per control process. With several gateway replicas (ARC-102) an agent that
// spreads its calls across them gets the limit of each.
package agentadmission

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/telemetrystore"
)

// reason is why a call was refused, as reported in metrics and logs.
type reason int

const (
	reasonAddress reason = iota // the client address is over its limit
	reasonNode                  // the node is over its limit for this call
	reasonSeat                  // no seat was free within the priority's wait
	reasonTable                 // the limit table is full of active callers
	reasonShed                  // an operator told control to refuse this priority
)

var reasonNames = [...]string{"address_rate", "node_rate", "overload", "tracked_keys", "shed"}

// refusalLogInterval spaces the log lines of one priority and reason; the lines in
// between are counted and reported with the next one.
const refusalLogInterval = 10 * time.Second

// Request is one agent call as admission sees it.
type Request struct {
	Call wire.Call
	// Address is the client address the HTTP layer resolved.
	Address string
	// Node and Credential identify the caller as it claims to be; they are not yet
	// verified. Credential keys the node's limits, so a caller without the node's
	// credential cannot spend the real node's allowance. Both are empty for enrollment.
	Node, Credential string
}

// Refusal is a call control will not serve now. Code is RATE_LIMITED for a call over its
// limit and AGENT_OVERLOADED for one that found no seat; RetryAfter is the whole seconds
// the caller should wait before it calls again.
type Refusal struct {
	Code       string
	RetryAfter int
}

func (r *Refusal) Error() string { return r.Code }

// RetrySeconds is the retry hint the HTTP and session layers send with the refusal.
func (r *Refusal) RetrySeconds() int { return r.RetryAfter }

// Controller admits the agent calls of one control process. It is safe for concurrent use.
type Controller struct {
	policy  config.AgentAdmissionPolicy
	calls   map[wire.Call]limit
	address limit
	limits  *buckets
	seats   *seatPool
	shed    atomic.Int32
	logger  *slog.Logger
	now     func() time.Time

	admitted [len(priorityOrder)]atomic.Uint64
	refused  [len(priorityOrder)][len(reasonNames)]atomic.Uint64

	logMu      sync.Mutex
	logged     [len(priorityOrder)][len(reasonNames)]time.Time
	suppressed [len(priorityOrder)][len(reasonNames)]uint64
}

func perSecond(l config.AgentCallLimit) limit {
	return limit{rate: float64(l.PerMinute) / 60, burst: float64(l.Burst)}
}

// New returns a controller for a validated policy. databaseSeats, when positive, bounds the seats
// in use so that agents leave the rest of the database pool to operators and background work
// (ADR 0043).
func New(policy config.AgentAdmissionPolicy, databaseSeats int, logger *slog.Logger) (*Controller, error) {
	if e := policy.Validate(); e != nil {
		return nil, e
	}
	if logger == nil {
		return nil, errors.New("AGENT_ADMISSION_LOGGER_REQUIRED")
	}
	c := &Controller{policy: policy, calls: map[wire.Call]limit{}, address: perSecond(policy.Address), limits: newBuckets(policy.TrackedKeys), logger: logger, now: time.Now}
	for _, call := range wire.Calls() {
		if l, ok := policy.Call(call); ok {
			c.calls[call] = perSecond(l)
		}
	}
	total := policy.Seats
	if databaseSeats > 0 {
		total = min(total, databaseSeats)
	}
	c.seats = &seatPool{total: total}
	for _, p := range priorityOrder {
		bounds := policy.Priority(p)
		seats := bounds.MaxSeats
		if total < policy.Seats {
			seats = seatCap(p, seats, total)
		}
		c.seats.classes[p] = seatClass{max: seats, queueLength: bounds.QueueLength, wait: time.Duration(bounds.WaitMilliseconds) * time.Millisecond}
	}
	return c, nil
}

// seatCap keeps the promise of the policy when the database bounds the seats: telemetry and bulk
// together never hold every seat, so a heartbeat always finds one.
func seatCap(p wire.Priority, configured, total int) int {
	if p == wire.PriorityTelemetry || p == wire.PriorityBulk {
		return min(configured, (total-1)/2)
	}
	return configured
}

// Admit applies the call's limits and, unless the call waits for work, takes a seat at
// the call's priority. The caller runs release when the call ends. A call that waits for
// work takes a seat for each look through Enter instead, so a held claim holds no seat.
func (c *Controller) Admit(ctx context.Context, r Request) (release func(), err error) {
	if !r.Call.Known() {
		return nil, errors.New("RESOURCE_NOT_FOUND")
	}
	p := r.Call.Priority()
	if c.Shed().refuses(p) {
		return nil, c.overloaded(p, reasonShed)
	}
	if e := c.limit(p, r); e != nil {
		return nil, e
	}
	if r.Call.WaitsForWork() {
		return func() {}, nil
	}
	return c.Enter(ctx, p)
}

// Enter takes a seat at priority p for one step of a call Admit already let through.
func (c *Controller) Enter(ctx context.Context, p wire.Priority) (release func(), err error) {
	if p.String() == "unknown" {
		return nil, errors.New("AGENT_ADMISSION_PRIORITY_INVALID")
	}
	if c.Shed().refuses(p) {
		return nil, c.overloaded(p, reasonShed)
	}
	release, e := c.seats.enter(ctx, p)
	if errors.As(e, new(errNoSeat)) {
		return nil, c.overloaded(p, reasonSeat)
	}
	if e != nil {
		return nil, e
	}
	c.admitted[p].Add(1)
	return release, nil
}

func (c *Controller) limit(p wire.Priority, r Request) error {
	now := c.now()
	retry, full := c.limits.take("a\x00"+r.Address, c.address, now)
	if full {
		return c.overloaded(p, reasonTable)
	}
	if retry > 0 {
		return c.refuse(p, reasonAddress, &Refusal{Code: "RATE_LIMITED", RetryAfter: min(retry, wire.MaxRetryAfterSeconds)})
	}
	l, ok := c.calls[r.Call]
	if !ok || r.Node == "" {
		return nil
	}
	// The credential is hashed so the table holds no secret.
	digest := sha256.Sum256([]byte(r.Credential))
	retry, full = c.limits.take("n\x00"+r.Node+"\x00"+string(r.Call)+"\x00"+hex.EncodeToString(digest[:16]), l, now)
	if full {
		return c.overloaded(p, reasonTable)
	}
	if retry > 0 {
		return c.refuse(p, reasonNode, &Refusal{Code: "RATE_LIMITED", RetryAfter: min(retry, wire.MaxRetryAfterSeconds)})
	}
	return nil
}

// overloaded refuses a call for lack of capacity with its priority's retry hint.
func (c *Controller) overloaded(p wire.Priority, why reason) error {
	return c.refuse(p, why, &Refusal{Code: "AGENT_OVERLOADED", RetryAfter: c.policy.Priority(p).RetrySeconds})
}

// refuse counts a refusal and logs it, at most once per interval for each priority and
// reason.
func (c *Controller) refuse(p wire.Priority, why reason, r *Refusal) error {
	c.refused[p][why].Add(1)
	now := c.now()
	c.logMu.Lock()
	if now.Sub(c.logged[p][why]) < refusalLogInterval {
		c.suppressed[p][why]++
		c.logMu.Unlock()
		return r
	}
	suppressed := c.suppressed[p][why]
	c.logged[p][why], c.suppressed[p][why] = now, 0
	c.logMu.Unlock()
	c.logger.Warn("agent_call_refused", "priority", p.String(), "reason", reasonNames[why], "code", r.Code, "retry_after_seconds", r.RetryAfter, "suppressed", suppressed)
	return r
}

// Samples reports the controller's state for the metrics store.
func (c *Controller) Samples() []telemetrystore.Sample {
	out := []telemetrystore.Sample{{Name: "ownsolid_agent_admission_tracked_keys", Value: float64(c.limits.size())}}
	for _, p := range priorityOrder {
		priority := telemetrystore.Label{Name: "priority", Value: p.String()}
		used, queued := c.seats.load(p)
		out = append(out,
			telemetrystore.Sample{Name: "ownsolid_agent_admission_seats_in_use", Value: float64(used), Labels: []telemetrystore.Label{priority}},
			telemetrystore.Sample{Name: "ownsolid_agent_admission_queued_calls", Value: float64(queued), Labels: []telemetrystore.Label{priority}},
			telemetrystore.Sample{Name: "ownsolid_agent_admission_admitted_total", Value: float64(c.admitted[p].Load()), Labels: []telemetrystore.Label{priority}},
		)
		for why, name := range reasonNames {
			out = append(out, telemetrystore.Sample{Name: "ownsolid_agent_admission_refused_total", Value: float64(c.refused[p][why].Load()), Labels: []telemetrystore.Label{priority, {Name: "reason", Value: name}}})
		}
	}
	return out
}
