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

// Observation carries an opaque Kubernetes object version and scheduler state.
type Observation struct {
	ResourceVersion string
	State           State
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
		Conditions []struct {
			Type, Status       string
			ObservedGeneration int64
		}
	}
}

func (obj object) observation(scheduler Scheduler) Observation {
	out := Observation{ResourceVersion: obj.Metadata.ResourceVersion}
	switch {
	case obj.Metadata.DeletionTimestamp != nil:
		out.State = Terminating
	case scheduler == Kueue:
		out.State = obj.kueueState()
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

func (obj object) kueueState() State {
	var admitted, ready, evicted bool
	for _, condition := range obj.Status.Conditions {
		if condition.Status != "True" || condition.ObservedGeneration != obj.Metadata.Generation || obj.Metadata.Generation <= 0 {
			continue
		}
		switch condition.Type {
		case "Finished":
			return Terminal
		case "Admitted":
			admitted = true
		case "PodsReady":
			ready = true
		case "Evicted":
			evicted = true
		}
	}
	if evicted {
		return Terminating
	}
	if admitted && ready {
		return Ready
	}
	if admitted {
		return Admitted
	}
	return Pending
}
