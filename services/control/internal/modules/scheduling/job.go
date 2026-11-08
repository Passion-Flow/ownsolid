// Package scheduling owns workload demand and admission policy independent of the
// cell scheduler and the controllers that own execution.
package scheduling

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"
)

var (
	// ErrInvalidJob indicates an unsupported workload intent.
	ErrInvalidJob = errors.New("SCHEDULING_INVALID_JOB")
	// ErrJobStateConflict indicates a stale or forbidden lifecycle transition.
	ErrJobStateConflict = errors.New("SCHEDULING_JOB_STATE_CONFLICT")
)

// Kind identifies the owner and execution shape of a job.
type Kind string

const (
	// Training represents a finite training or fine-tuning run.
	Training Kind = "training"
	// Inference represents a serving deployment.
	Inference Kind = "inference"
	// Notebook represents an interactive notebook session.
	Notebook Kind = "notebook"
	// Batch represents a finite batch run.
	Batch Kind = "batch"
)

// IntentVersion is the version of the adapter-neutral scheduling intent.
const IntentVersion = 1

// ReplicaGroup is a named set of identical replicas with per-replica demand.
type ReplicaGroup struct {
	Name     string    `json:"name"`
	Replicas int32     `json:"replicas"`
	Requests Resources `json:"requests"`
}

// JobIntent describes scheduling demand. The caller supplies authenticated scope
// separately, and the runtime owner retains executable configuration.
type JobIntent struct {
	Version  int            `json:"version"`
	Kind     Kind           `json:"kind"`
	SourceID string         `json:"source_id"`
	Groups   []ReplicaGroup `json:"groups"`
}

// Normalize validates the job intent and returns an independently owned,
// deterministically ordered copy and its exact aggregate demand.
func (j JobIntent) Normalize() (JobIntent, Resources, error) {
	switch j.Kind {
	case Training, Inference, Notebook, Batch:
	default:
		return JobIntent{}, nil, fmt.Errorf("%w: unknown workload kind", ErrInvalidJob)
	}
	if j.Version != IntentVersion || j.SourceID == "" || len(j.SourceID) > 128 ||
		!utf8.ValidString(j.SourceID) || strings.IndexFunc(j.SourceID, func(r rune) bool {
		return r < 33 || r > 126
	}) != -1 || len(j.Groups) == 0 || len(j.Groups) > 64 {
		return JobIntent{}, nil, fmt.Errorf("%w: check version, source identity and group count", ErrInvalidJob)
	}
	out := j
	out.Groups = make([]ReplicaGroup, 0, len(j.Groups))
	seen := make(map[string]bool, len(j.Groups))
	total := make(Resources)
	for _, group := range j.Groups {
		if !dnsLabel.MatchString(group.Name) || group.Replicas < 1 || seen[group.Name] {
			return JobIntent{}, nil, fmt.Errorf("%w: invalid or repeated replica group %q", ErrInvalidJob, group.Name)
		}
		resources, err := normalizeResources(group.Requests)
		if err != nil {
			return JobIntent{}, nil, err
		}
		if len(resources) == 0 {
			return JobIntent{}, nil, fmt.Errorf("%w: group %q needs a positive resource request", ErrInvalidResources, group.Name)
		}
		if err := total.addScaled(resources, int64(group.Replicas)); err != nil {
			return JobIntent{}, nil, err
		}
		seen[group.Name] = true
		group.Requests = resources
		out.Groups = append(out.Groups, group)
	}
	slices.SortFunc(out.Groups, func(a, b ReplicaGroup) int { return strings.Compare(a.Name, b.Name) })
	return out, total, nil
}

// JobState is the control plane's lifecycle view of a workload.
type JobState string

const (
	// Queued indicates an intent is waiting for admission.
	Queued JobState = "queued"
	// Reserved indicates the complete demand is reserved.
	Reserved JobState = "reserved"
	// Running indicates a confirmed running workload.
	Running JobState = "running"
	// Stopping indicates removal has been requested and quota is still held.
	Stopping JobState = "stopping"
	// Succeeded indicates confirmed successful completion.
	Succeeded JobState = "succeeded"
	// Failed indicates confirmed failure with no remaining runtime allocation.
	Failed JobState = "failed"
	// Cancelled indicates a withdrawn intent or confirmed runtime removal.
	Cancelled JobState = "cancelled"
)

// CheckTransition validates a lifecycle edge. A caller advancing into a
// runtime-derived state must first verify the observation and attempt identity.
func (state JobState) CheckTransition(next JobState) error {
	allowed := false
	switch state {
	case Queued:
		allowed = next == Reserved || next == Cancelled || next == Failed
	case Reserved:
		allowed = next == Running || next == Stopping || next == Succeeded || next == Failed
	case Running:
		allowed = next == Stopping || next == Succeeded || next == Failed
	case Stopping:
		allowed = next == Cancelled || next == Failed || next == Succeeded
	}
	if !allowed {
		return ErrJobStateConflict
	}
	return nil
}
