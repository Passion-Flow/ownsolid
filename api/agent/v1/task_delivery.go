package v1

import (
	"errors"
	"regexp"
)

// Task delivery semantics (ARC-006, ADR 0011).
//
// Control numbers every task of a node with a sequence that grows by one per task. A
// lease names its sequence. The agent acknowledges a lease before it executes anything,
// and executes only after control confirms the acknowledgement. A lease that was never
// acknowledged was never delivered: control releases it on the node's next claim, or when
// it expires, without spending one of the task's attempts, and delivers it again.
//
// The agent keeps the highest sequence it acknowledged and sends it with every claim.
// When that cursor and control's record disagree, for example after control's database
// was restored or the agent's state directory was lost, control answers the claim with a
// resync instead of a lease. The agent then recovers all local work from its journals,
// reports a full heartbeat, and adopts the cursor control names. Control grants a resync
// to one node at most once per configured interval and to the fleet at a configured
// rate, so a mass reconnect cannot turn into a resync storm.

// MaxTaskSequence keeps sequences exact in every JSON implementation.
const MaxTaskSequence = 1<<53 - 1

// TaskAcknowledgementsPath is the HTTPS fallback path under a task, for agents without
// the session stream.
const TaskAcknowledgementsPath = "acknowledgements"

var deliveryUUID = regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`)
var deliveryToken = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)

// TaskAcknowledgement confirms that the agent holds a lease and is about to execute it.
// Control answers with the renewed lease. Acknowledging the same lease again is allowed and
// answers the same way.
type TaskAcknowledgement struct {
	TaskID     string `json:"task_id"`
	Sequence   uint64 `json:"sequence"`
	LeaseToken string `json:"lease_token"`
}

func (a TaskAcknowledgement) Validate() error {
	if !deliveryUUID.MatchString(a.TaskID) || a.Sequence > MaxTaskSequence || !deliveryToken.MatchString(a.LeaseToken) {
		return errors.New("VALIDATION_FAILED")
	}
	return nil
}

// TaskAcknowledgementRequest is the HTTPS body; the task ID is in the path.
type TaskAcknowledgementRequest struct {
	Sequence   uint64 `json:"sequence"`
	LeaseToken string `json:"lease_token"`
}

// Resync asks the agent to recover all local work and then use AcknowledgedSequence as
// its cursor.
type Resync struct {
	AcknowledgedSequence uint64 `json:"acknowledged_sequence"`
}

// AcceptLease reports whether an agent whose cursor is acknowledged may execute lease.
// A lease at the cursor is the same task delivered again; a lower one is out of order.
func AcceptLease(acknowledged uint64, lease Lease) error {
	if lease.Sequence > MaxTaskSequence || lease.Sequence < acknowledged {
		return errors.New("AGENT_TASK_SEQUENCE_STALE")
	}
	return nil
}

// Validate checks that a claim holds at most one of a lease or a resync.
func (c Claim) Validate() error {
	if c.Lease != nil && c.Resync != nil || c.Resync != nil && c.Resync.AcknowledgedSequence > MaxTaskSequence {
		return errors.New("CONTROL_RESPONSE_INVALID")
	}
	return nil
}
