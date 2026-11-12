package scheduling

import (
	"cmp"
	"errors"
	"math/bits"
	"slices"
)

// ErrInvalidQueue indicates a missing queue, enqueue sequence or capacity allocation.
var ErrInvalidQueue = errors.New("SCHEDULING_INVALID_QUEUE")

// QueueShare describes the capacity allocation and currently reserved resources
// of a queue competing with other queues in the same policy domain.
type QueueShare struct {
	Capacity Resources
	Reserved Resources
}

// QueueEntry is a pending job after authority has resolved its queue and
// priority. Sequence is the unique order of durable enqueue keys in this batch.
type QueueEntry struct {
	JobID    string
	QueueID  string
	Priority int32
	Sequence uint64
}

type share struct {
	numerator   uint64
	denominator uint64
}

func (s share) compare(other share) int {
	aHigh, aLow := bits.Mul64(s.numerator, other.denominator)
	bHigh, bLow := bits.Mul64(other.numerator, s.denominator)
	if aHigh != bHigh {
		return cmp.Compare(aHigh, bHigh)
	}
	return cmp.Compare(aLow, bLow)
}

func (q QueueShare) dominant() (share, error) {
	capacity, err := normalizeResources(q.Capacity)
	if err != nil {
		return share{}, err
	}
	reserved, err := normalizeResources(q.Reserved)
	if err != nil {
		return share{}, err
	}
	if len(capacity) == 0 {
		return share{}, ErrInvalidQueue
	}
	dominant := share{denominator: 1}
	for resource, quantity := range reserved {
		limit := capacity[resource]
		if limit == 0 {
			return share{}, ErrInvalidQueue
		}
		ratio := share{numerator: uint64(quantity), denominator: uint64(limit)}
		if ratio.compare(dominant) > 0 {
			dominant = ratio
		}
	}
	return dominant, nil
}

// RankQueue orders pending jobs by priority, lowest current dominant share and
// then FIFO. Recompute using current shares after each successful reservation.
func RankQueue(entries []QueueEntry, queues map[string]QueueShare) ([]QueueEntry, error) {
	shares := make(map[string]share)
	sequences := make(map[uint64]bool, len(entries))
	jobs := make(map[string]bool, len(entries))
	for _, entry := range entries {
		if entry.JobID == "" || entry.QueueID == "" || entry.Sequence == 0 || sequences[entry.Sequence] || jobs[entry.JobID] {
			return nil, ErrInvalidQueue
		}
		sequences[entry.Sequence], jobs[entry.JobID] = true, true
		if _, ok := shares[entry.QueueID]; ok {
			continue
		}
		queue, ok := queues[entry.QueueID]
		if !ok {
			return nil, ErrInvalidQueue
		}
		value, err := queue.dominant()
		if err != nil {
			return nil, err
		}
		shares[entry.QueueID] = value
	}
	ranked := slices.Clone(entries)
	slices.SortFunc(ranked, func(a, b QueueEntry) int {
		if a.Priority != b.Priority {
			return cmp.Compare(b.Priority, a.Priority)
		}
		if order := shares[a.QueueID].compare(shares[b.QueueID]); order != 0 {
			return order
		}
		return cmp.Compare(a.Sequence, b.Sequence)
	})
	return ranked, nil
}

// QueueTurnReason explains an exact entry deferred by the dispatcher using
// this authorized RankQueue result. Call it only when rank was the reason
// for deferral. A head, absent or changed entry has no queue waiting reason.
func QueueTurnReason(ranked []QueueEntry, deferred QueueEntry) (WaitReason, bool) {
	if slices.Index(ranked, deferred) <= 0 {
		return WaitReason{}, false
	}
	return WaitReason{Code: ErrQueueTurnPending.Error(), Action: WaitForQueueTurn}, true
}
