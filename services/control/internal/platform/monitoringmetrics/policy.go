// Package monitoringmetrics records bounded process facts without a metric daemon.
package monitoringmetrics

import (
	"errors"
	"math"
	"slices"
)

const MaximumExactCounter uint64 = 9007199254740991

type Policy struct {
	LatencyMilliseconds  []float64 `json:"latency_milliseconds"`
	StructuredLogging    bool      `json:"structured_logging"`
	MaximumLogsPerSecond int       `json:"maximum_logs_per_second"`
}

func Defaults() Policy {
	return Policy{[]float64{1, 5, 10, 25, 50, 100, 250, 500, 1000, 5000, 30000}, false, 2}
}
func (p Policy) Validate() error {
	if len(p.LatencyMilliseconds) < 1 || len(p.LatencyMilliseconds) > 32 || p.MaximumLogsPerSecond < 1 || p.MaximumLogsPerSecond > 1000 {
		return errors.New("MONITORING_CONFIGURATION_INVALID")
	}
	previous := float64(0)
	for _, v := range p.LatencyMilliseconds {
		if math.IsNaN(v) || math.IsInf(v, 0) || v <= previous || v > 3600000 {
			return errors.New("MONITORING_CONFIGURATION_INVALID")
		}
		previous = v
	}
	return nil
}
func add(a, b uint64, overflow *bool) uint64 {
	if a > MaximumExactCounter || b > MaximumExactCounter-a {
		*overflow = true
		return MaximumExactCounter
	}
	return a + b
}

// MatchesPolicy prevents a separately constructed recorder from silently ignoring
// the owning typed deployment policy. The recorder keeps a private immutable copy.
func (r *Recorder) MatchesPolicy(p Policy) bool {
	return r != nil && r.policy.StructuredLogging == p.StructuredLogging && r.policy.MaximumLogsPerSecond == p.MaximumLogsPerSecond && slices.Equal(r.policy.LatencyMilliseconds, p.LatencyMilliseconds)
}
