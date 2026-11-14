package workflows

// Fixed durable protocol states. Deployment policies may bound execution and
// retention, but cannot redefine state-machine semantics.
const (
	NotStartedState  = "not_started"
	PendingState     = "pending"
	RunningState     = "running"
	SucceededState   = "succeeded"
	FailedState      = "failed"
	CancelledState   = "cancelled"
	InterruptedState = "interrupted"
)
