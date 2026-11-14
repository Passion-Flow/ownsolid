package celladapter

// State reports a scheduler observation. Terminal is a scheduler fact and must
// be reconciled with the runtime owner before releasing resources.
type State string

const (
	// Pending means the scheduler has not confirmed readiness.
	Pending State = "pending"
	// Admitted means Kueue has admitted the workload, with pod readiness pending.
	Admitted State = "admitted"
	// Ready means Kueue reported all required pods ready.
	Ready State = "ready"
	// Active means Volcano reported its own minimum running threshold.
	Active State = "active"
	// Terminating means the observed object is being removed or stopped.
	Terminating State = "terminating"
	// Terminal means the scheduler reports an end state.
	Terminal State = "terminal"
	// Unknown means the reported phase could not be interpreted.
	Unknown State = "unknown"
)

// Blocker is a current, allowlisted scheduler reason.
type Blocker string

const (
	// WaitingForQuota means Kueue is waiting for unused quota.
	WaitingForQuota Blocker = "SCHEDULING_CELL_QUOTA_WAIT"
	// ExceedsMaxQuota means the Workload exceeds Kueue's quota ceiling.
	ExceedsMaxQuota Blocker = "SCHEDULING_CELL_QUOTA_EXCEEDED"
	// TopologyPlacementFailed means the current Kueue topology cannot place the Workload.
	TopologyPlacementFailed Blocker = "SCHEDULING_CELL_TOPOLOGY_PLACEMENT_FAILED"
	// QueueInadmissible means the Workload cannot be admitted to its cell queue.
	QueueInadmissible Blocker = "SCHEDULING_CELL_QUEUE_INADMISSIBLE"
	// PodsNotReady means the admitted Workload is waiting for its pods.
	PodsNotReady Blocker = "SCHEDULING_CELL_PODS_NOT_READY"
)

// Observation carries an opaque Kubernetes object version, scheduler state
// and an optional verified blocker.
type Observation struct {
	ResourceVersion string
	State           State
	Blocker         Blocker
}

type condition struct {
	Type, Status, Reason string
	ObservedGeneration   int64
}

type object struct {
	APIVersion string
	Kind       string
	Metadata   struct {
		Namespace, Name, UID, ResourceVersion string
		Generation                            int64
		DeletionTimestamp                     *string
	}
	Status struct {
		State      struct{ Phase string }
		Conditions []condition
	}
}

func (obj object) observation(scheduler Scheduler) Observation {
	out := Observation{ResourceVersion: obj.Metadata.ResourceVersion}
	switch {
	case obj.Metadata.DeletionTimestamp != nil:
		out.State = Terminating
	case scheduler == Kueue:
		out.State, out.Blocker = obj.kueueState()
	default:
		switch obj.Status.State.Phase {
		case "", "Pending", "Inqueue", "Restarting", "Completing":
			out.State = Pending
		case "Running":
			out.State = Active
		case "Aborting", "Terminating":
			out.State = Terminating
		case "Completed", "Failed", "Aborted", "Terminated":
			out.State = Terminal
		default:
			out.State = Unknown
		}
	}
	return out
}

func (obj object) kueueState() (State, Blocker) {
	var admitted, ready, notReady, evicted bool
	var quotaBlocker Blocker
	for _, condition := range obj.Status.Conditions {
		if condition.ObservedGeneration != obj.Metadata.Generation || obj.Metadata.Generation <= 0 {
			continue
		}
		switch condition.Type {
		case "Finished":
			if condition.Status == "True" {
				return Terminal, ""
			}
		case "Admitted":
			admitted = admitted || condition.Status == "True"
		case "PodsReady":
			ready = ready || condition.Status == "True"
			notReady = notReady || condition.Status == "False"
		case "Evicted":
			evicted = evicted || condition.Status == "True"
		case "QuotaReserved":
			if condition.Status == "False" {
				quotaBlocker = kueueQuotaBlocker(condition.Reason)
			}
		}
	}
	switch {
	case evicted:
		return Terminating, ""
	case admitted && ready:
		return Ready, ""
	case admitted && notReady:
		return Admitted, PodsNotReady
	case admitted:
		return Admitted, ""
	default:
		return Pending, quotaBlocker
	}
}

func kueueQuotaBlocker(reason string) Blocker {
	switch reason {
	case "WaitingForQuota":
		return WaitingForQuota
	case "ExceedsMaxQuota":
		return ExceedsMaxQuota
	case "TopologyPlacementFailed":
		return TopologyPlacementFailed
	case "Misconfigured", "Inadmissible":
		return QueueInadmissible
	default:
		return ""
	}
}
