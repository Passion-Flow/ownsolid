package scheduling

import (
	"context"
	"errors"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// ErrGangReleaseEvidence requires exactly one verified release for every replica.
var ErrGangReleaseEvidence = errors.New("SCHEDULING_GANG_RELEASE_EVIDENCE_REQUIRED")

// ReplicaReleaseOutcome identifies a positive release verified by the owner.
type ReplicaReleaseOutcome string

const (
	// ReplicaStopped means all physical incarnations of the slot have stopped.
	ReplicaStopped ReplicaReleaseOutcome = "stopped"
	// ReplicaNeverDelivered means no physical work was delivered for the slot.
	ReplicaNeverDelivered ReplicaReleaseOutcome = "never_delivered"
)

// ReplicaRelease identifies a verified release for one zero-based replica slot.
// The owner must verify the physical identity, delivery history and dispatch fence.
type ReplicaRelease struct {
	AttemptID string
	Group     string
	Index     int32
	Outcome   ReplicaReleaseOutcome
}

// ReleaseGangWithin releases the whole attempt after verifying coverage of its
// stored replica slots. The owner supplies positive evidence and commits its
// physical release and audit with this update. False with no error means an exact replay.
func ReleaseGangWithin(ctx context.Context, q transaction.Executor, a Attempt, evidence []ReplicaRelease) (bool, error) {
	if len(evidence) == 0 {
		return false, ErrGangReleaseEvidence
	}
	return releaseWithin(ctx, q, a, evidence)
}

func checkReplicaReleases(attemptID string, groups []ReplicaGroup, evidence []ReplicaRelease) error {
	if len(evidence) == 0 {
		if len(groups) == 1 && groups[0].Replicas == 1 {
			return nil
		}
		return ErrGangReleaseEvidence
	}

	counts := make(map[string]int32, len(groups))
	var total int64
	for _, group := range groups {
		counts[group.Name] = group.Replicas
		total += int64(group.Replicas)
	}
	if int64(len(evidence)) != total {
		return ErrGangReleaseEvidence
	}

	type slot struct {
		group string
		index int32
	}
	seen := make(map[slot]struct{}, len(evidence))
	for _, proof := range evidence {
		if proof.AttemptID != attemptID || proof.Index < 0 || proof.Index >= counts[proof.Group] ||
			(proof.Outcome != ReplicaStopped && proof.Outcome != ReplicaNeverDelivered) {
			return ErrGangReleaseEvidence
		}
		key := slot{proof.Group, proof.Index}
		if _, duplicate := seen[key]; duplicate {
			return ErrGangReleaseEvidence
		}
		seen[key] = struct{}{}
	}
	return nil
}
