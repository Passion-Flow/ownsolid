package v1

import (
	"errors"
	"time"
	"unicode"
	"unicode/utf8"
)

// TaskProgressPath is the leaf of the task path an agent reports progress on.
const TaskProgressPath = "task-progress"

const (
	// ProgressFlushSeconds is how often a running task reports what it has gathered.
	ProgressFlushSeconds = 5
	// MaximumProgressBatch bounds the entries of one report.
	MaximumProgressBatch = 100
	// MaximumProgressBatchBytes bounds the encoded entries of one report, below the
	// 16 KiB an agent may post.
	MaximumProgressBatchBytes = 12288
	// MaximumProgressMessageBytes bounds one log line.
	MaximumProgressMessageBytes = 512
	// MaximumProgressLogLines bounds the log of one attempt. Rows of an append table are
	// not deleted, so the agent stops sending lines here and reports how many it left out.
	MaximumProgressLogLines = 4000
	// MaximumProgressSteps bounds the steps of one attempt.
	MaximumProgressSteps = 64
	// MaximumProgressSequence bounds an entry's sequence: every line, two entries per
	// step, and the closing line that counts what was left out.
	MaximumProgressSequence = MaximumProgressLogLines + 2*MaximumProgressSteps + 1
)

// The kinds of progress entry.
const (
	ProgressKindStep = "step"
	ProgressKindLog  = "log"
)

// The states of a step.
const (
	ProgressStepRunning   = "running"
	ProgressStepSucceeded = "succeeded"
	ProgressStepFailed    = "failed"
)

// The levels of a log line.
const (
	ProgressLevelInfo    = "info"
	ProgressLevelWarning = "warning"
	ProgressLevelError   = "error"
)

// The steps tasks report. Steps are stable keys the console translates, so a new one is
// added here, in the nodes contract and in both locales.
const (
	ProgressStepReadSystem = "read_system"
	ProgressStepReadMemory = "read_memory"
	ProgressStepReadDisk   = "read_disk"
	ProgressStepPreflight  = "preflight"
	ProgressStepDownload   = "download"
	ProgressStepInstall    = "install"
	ProgressStepStart      = "start"
	ProgressStepStop       = "stop"
	ProgressStepRemove     = "remove"
	ProgressStepVerify     = "verify"
)

var progressSteps = map[string]bool{
	ProgressStepReadSystem: true,
	ProgressStepReadMemory: true,
	ProgressStepReadDisk:   true,
	ProgressStepPreflight:  true,
	ProgressStepDownload:   true,
	ProgressStepInstall:    true,
	ProgressStepStart:      true,
	ProgressStepStop:       true,
	ProgressStepRemove:     true,
	ProgressStepVerify:     true,
}

// ProgressEntry is one thing a running task reports: a step changing state, or a log line.
// The time is the agent's, and is compared only with other times of the same attempt.
type ProgressEntry struct {
	Sequence int       `json:"sequence"`
	Kind     string    `json:"kind"`
	At       time.Time `json:"at"`
	Step     string    `json:"step,omitempty"`
	State    string    `json:"state,omitempty"`
	Level    string    `json:"level,omitempty"`
	Message  string    `json:"message,omitempty"`
}

// TaskProgress is one report, proved by the task's lease. Reports are idempotent: an entry
// is identified by the attempt and its sequence, so an agent resends a report it has no
// answer for.
type TaskProgress struct {
	LeaseToken string          `json:"lease_token"`
	Entries    []ProgressEntry `json:"entries"`
}

// TaskProgressAccepted says how many entries of a report were new.
type TaskProgressAccepted struct {
	Accepted int `json:"accepted"`
}

var errProgressInvalid = errors.New("TASK_PROGRESS_INVALID")

// Validate checks a report's shape and bounds. It does not read the lease.
func (p TaskProgress) Validate() error {
	if len(p.Entries) < 1 || len(p.Entries) > MaximumProgressBatch {
		return errProgressInvalid
	}
	last := 0
	for _, entry := range p.Entries {
		if entry.Sequence <= last {
			return errProgressInvalid
		}
		last = entry.Sequence
		if e := entry.Validate(); e != nil {
			return e
		}
	}
	return nil
}

// Validate checks one entry.
func (e ProgressEntry) Validate() error {
	if e.Sequence < 1 || e.Sequence > MaximumProgressSequence || e.At.IsZero() {
		return errProgressInvalid
	}
	switch e.Kind {
	case ProgressKindStep:
		state := e.State == ProgressStepRunning || e.State == ProgressStepSucceeded || e.State == ProgressStepFailed
		if !progressSteps[e.Step] || !state || e.Level != "" || e.Message != "" {
			return errProgressInvalid
		}
	case ProgressKindLog:
		level := e.Level == ProgressLevelInfo || e.Level == ProgressLevelWarning || e.Level == ProgressLevelError
		if !level || e.Step != "" || e.State != "" || !progressMessage(e.Message) {
			return errProgressInvalid
		}
	default:
		return errProgressInvalid
	}
	return nil
}

// progressMessage keeps a line to one line of printable text, so a log cannot carry
// terminal sequences or forge a line of its own.
func progressMessage(value string) bool {
	if value == "" || len(value) > MaximumProgressMessageBytes || !utf8.ValidString(value) {
		return false
	}
	for _, r := range value {
		if !unicode.IsPrint(r) {
			return false
		}
	}
	return true
}
