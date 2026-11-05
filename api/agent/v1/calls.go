package v1

import "strings"

// Priority orders agent calls when control is short of capacity (ADR 0017). Control sheds
// bulk work first, then telemetry, then heartbeats, and serves the reports that end or
// contain work for as long as it serves anything. A smaller value is served first.
type Priority int

const (
	// PriorityQuarantine carries the reports that end or contain work: task results and
	// stop receipts. Serving them frees leases and stops work that must stop.
	PriorityQuarantine Priority = iota
	// PriorityHeartbeat keeps nodes online and dispatch moving: heartbeats, claims,
	// acknowledgements and lease renewals.
	PriorityHeartbeat
	// PriorityTelemetry carries progress events of running work.
	PriorityTelemetry
	// PriorityBulk carries work that can wait: enrollment and file transfers.
	PriorityBulk
)

var priorityNames = [...]string{"quarantine", "heartbeat", "telemetry", "bulk"}

// Priorities lists every priority, highest first.
func Priorities() []Priority {
	return []Priority{PriorityQuarantine, PriorityHeartbeat, PriorityTelemetry, PriorityBulk}
}

// String is the priority's name in configuration, metrics and logs.
func (p Priority) String() string {
	if p < PriorityQuarantine || p > PriorityBulk {
		return "unknown"
	}
	return priorityNames[p]
}

// Call names one kind of agent request. Every HTTPS path and session exchange an agent
// uses is exactly one call, and control limits each call on its own.
type Call string

const (
	CallEnroll      Call = "enroll"
	CallHeartbeat   Call = "heartbeat"
	CallClaim       Call = "claim"
	CallAcknowledge Call = "acknowledge"
	CallRenew       Call = "renew"
	CallResult      Call = "result"
	CallStop        Call = "stop"
	CallEvents      Call = "events"
	CallDownload    Call = "download"
	CallUpload      Call = "upload"
)

var callPriorities = map[Call]Priority{
	CallResult:      PriorityQuarantine,
	CallStop:        PriorityQuarantine,
	CallHeartbeat:   PriorityHeartbeat,
	CallClaim:       PriorityHeartbeat,
	CallAcknowledge: PriorityHeartbeat,
	CallRenew:       PriorityHeartbeat,
	CallEvents:      PriorityTelemetry,
	CallEnroll:      PriorityBulk,
	CallDownload:    PriorityBulk,
	CallUpload:      PriorityBulk,
}

// Calls lists every call.
func Calls() []Call {
	return []Call{CallEnroll, CallHeartbeat, CallClaim, CallAcknowledge, CallRenew, CallResult, CallStop, CallEvents, CallDownload, CallUpload}
}

// Known reports whether c is a call of this protocol version.
func (c Call) Known() bool {
	_, ok := callPriorities[c]
	return ok
}

// Priority is the priority control serves c at.
func (c Call) Priority() Priority {
	if p, ok := callPriorities[c]; ok {
		return p
	}
	return PriorityBulk
}

// WaitsForWork reports whether control may hold c open until work arrives. Such a call
// holds a serving seat only while control looks for work, not while it waits.
func (c Call) WaitsForWork() bool { return c == CallClaim }

// MaxRetryAfterSeconds bounds the retry hint of a refused call, on the Retry-After header
// over HTTPS and in SessionProblem on the stream. An agent waits at least the hint before
// it repeats the call, so a hint can slow an agent no further than its reconnect backoff.
const MaxRetryAfterSeconds = MaxReconnectMaxSeconds

// taskCalls maps the leaf of a task path, /agent/v1/nodes/{node}/tasks/{task}/{leaf}, to
// its call. Upload sessions continue below their leaf.
var taskCalls = map[string]Call{
	TaskAcknowledgementsPath:        CallAcknowledge,
	"renewals":                      CallRenew,
	"results":                       CallResult,
	"training-stops":                CallStop,
	"model-preparation-stops":       CallStop,
	"training-events":               CallEvents,
	"model-preparation-events":      CallEvents,
	"artifact":                      CallDownload,
	"model-manifest":                CallDownload,
	"model-files":                   CallDownload,
	"training-input-files":          CallDownload,
	"model-preparation-input-files": CallDownload,
	"image-registry":                CallDownload,
	"training-uploads":              CallUpload,
	"model-preparation-uploads":     CallUpload,
}

// PathCall classifies a cell-relative agent HTTPS path. It returns the call and the node
// the path names, which is empty for enrollment. A path that is no agent call reports
// false; PathCall checks only the shape that decides the call, and the handler of the
// path still validates the rest.
func PathCall(path string) (Call, string, bool) {
	rest, ok := strings.CutPrefix(path, "/agent/v1/")
	if !ok {
		return "", "", false
	}
	if rest == "enroll" {
		return CallEnroll, "", true
	}
	parts := strings.Split(rest, "/")
	if len(parts) < 3 || parts[0] != "nodes" || parts[1] == "" {
		return "", "", false
	}
	node := parts[1]
	switch {
	case len(parts) == 3 && parts[2] == "heartbeat":
		return CallHeartbeat, node, true
	case len(parts) == 4 && parts[2] == "tasks" && parts[3] == "claims":
		return CallClaim, node, true
	case len(parts) >= 5 && parts[2] == "tasks" && parts[3] != "":
		call, known := taskCalls[parts[4]]
		if !known || len(parts) > 5 && call != CallUpload && call != CallDownload {
			return "", "", false
		}
		return call, node, true
	}
	return "", "", false
}
