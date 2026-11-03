package distribution

import (
	"context"
	"sort"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	wire "github.com/Passion-Flow/ownsolid/api/gateway/v1"
)

// MonitoringWindow describes a finite newest-first sample by admission time.
// No returned held count releases capacity or claims total platform capacity.
type MonitoringWindow struct {
	From             time.Time      `json:"from"`
	Until            time.Time      `json:"until"`
	Maximum          int            `json:"maximum"`
	Sampled          int            `json:"sampled"`
	Truncated        bool           `json:"truncated"`
	OldestAdmission  *time.Time     `json:"oldest_admission"`
	NewestAdmission  *time.Time     `json:"newest_admission"`
	Admitted         int            `json:"admitted"`
	Reported         int            `json:"reported"`
	Reconciled       int            `json:"reconciled"`
	HeldInSample     int            `json:"held_in_sample"`
	Outcomes         map[string]int `json:"outcomes"`
	UsageKnown       int            `json:"usage_known"`
	UsageUnknown     int            `json:"usage_unknown"`
	PromptTokens     uint64         `json:"prompt_tokens"`
	CompletionTokens uint64         `json:"completion_tokens"`
	TotalTokens      uint64         `json:"total_tokens"`
	P50Milliseconds  *uint64        `json:"p50_milliseconds"`
	P95Milliseconds  *uint64        `json:"p95_milliseconds"`
	P99Milliseconds  *uint64        `json:"p99_milliseconds"`
	ErrorRate        *float64       `json:"error_rate"`
}

func sampleQuantile(values []uint64, percent int) *uint64 {
	if len(values) == 0 {
		return nil
	}
	rank := (len(values)*percent + 99) / 100
	v := values[rank-1]
	return &v
}
func (s *Service) MonitoringWithin(ctx context.Context, q transaction.Executor, a identity.Auth, workspace string, from, until time.Time, maximum int) (MonitoringWindow, error) {
	if a.Me.Audience != "admin" || !a.Me.User.IsPlatformAdmin {
		return MonitoringWindow{}, fail("ACCESS_DENIED")
	}
	if e := s.executor(ctx, q); e != nil {
		return MonitoringWindow{}, e
	}
	if !wire.ValidID(workspace) || maximum < 1 || maximum > monitoringMaximumRows || from.IsZero() || until.IsZero() || !from.Before(until) || until.Sub(from) > 24*time.Hour || from.Location() != time.UTC || until.Location() != time.UTC {
		return MonitoringWindow{}, fail("VALIDATION_FAILED")
	}
	if e := s.Identity.RecheckOrderedWithin(ctx, q, a); e != nil {
		return MonitoringWindow{}, e
	}
	w, e := s.Workspaces.RecheckScopeWithin(ctx, q, a, workspace)
	if e != nil {
		return MonitoringWindow{}, e
	}
	out := MonitoringWindow{From: from, Until: until, Maximum: maximum, Outcomes: map[string]int{}}
	rows, e := q.Query(ctx, monitoringWindowStatement(q, w.TenantID, w.ID, from, until, maximum))
	if e != nil {
		return MonitoringWindow{}, distributionError(e)
	}
	defer rows.Close()
	latencies := make([]uint64, 0, maximum)
	failed := 0
	for rows.Next() {
		if out.Sampled == maximum {
			out.Truncated = true
			break
		}
		var id, state string
		var held bool
		var admitted, deadline time.Time
		var resolved, reported *time.Time
		var raw []byte
		var sha *string
		var maxOutput, contextTokens, seconds int
		if e = rows.Scan(&id, &state, &held, &admitted, &deadline, &resolved, &reported, &raw, &sha, &maxOutput, &contextTokens, &seconds); e != nil {
			return MonitoringWindow{}, distributionError(e)
		}
		if !wire.ValidID(id) || admitted.IsZero() || deadline.IsZero() || !deadline.After(admitted) || admitted.Before(from) || !admitted.Before(until) || maxOutput < 1 || maxOutput > int(wire.MaxOutputTokens) || contextTokens < 1 || contextTokens > int(wire.MaxContextTokens) || seconds < 1 || seconds > 300 {
			return MonitoringWindow{}, fail("STORAGE_UNAVAILABLE")
		}
		admitted = admitted.UTC()
		if out.NewestAdmission == nil {
			stamp := admitted
			out.NewestAdmission = &stamp
		}
		stamp := admitted
		out.OldestAdmission = &stamp
		switch state {
		case "admitted":
			if !held || resolved != nil || reported != nil || len(raw) != 0 || sha != nil {
				return MonitoringWindow{}, fail("STORAGE_UNAVAILABLE")
			}
			out.Admitted++
			out.UsageUnknown++
		case "reconciled":
			if held || resolved == nil || reported != nil || len(raw) != 0 || sha != nil {
				return MonitoringWindow{}, fail("STORAGE_UNAVAILABLE")
			}
			out.Reconciled++
			out.UsageUnknown++
		case "reported":
			value, decode := wire.DecodeOutcome(raw)
			if decode != nil || value.RequestID != id || reported == nil || resolved == nil || sha == nil {
				return MonitoringWindow{}, fail("STORAGE_UNAVAILABLE")
			}
			digest, e := value.Digest()
			if e != nil || digest != *sha || value.Usage != nil && (value.Usage.CompletionTokens > uint32(maxOutput) || value.Usage.TotalTokens > uint32(contextTokens)) {
				return MonitoringWindow{}, fail("STORAGE_UNAVAILABLE")
			}
			out.Reported++
			out.Outcomes[value.Status]++
			latencies = append(latencies, value.ElapsedMilliseconds)
			if value.Status != "succeeded" {
				failed++
			}
			if value.Usage == nil {
				out.UsageUnknown++
			} else {
				out.UsageKnown++
				out.PromptTokens += uint64(value.Usage.PromptTokens)
				out.CompletionTokens += uint64(value.Usage.CompletionTokens)
				out.TotalTokens += uint64(value.Usage.TotalTokens)
			}
		default:
			return MonitoringWindow{}, fail("STORAGE_UNAVAILABLE")
		}
		if held {
			out.HeldInSample++
		}
		out.Sampled++
	}
	if e = rows.Err(); e != nil {
		return MonitoringWindow{}, distributionError(e)
	}
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	out.P50Milliseconds = sampleQuantile(latencies, 50)
	out.P95Milliseconds = sampleQuantile(latencies, 95)
	out.P99Milliseconds = sampleQuantile(latencies, 99)
	if out.Reported > 0 {
		v := float64(failed) / float64(out.Reported)
		out.ErrorRate = &v
	}
	return out, nil
}
