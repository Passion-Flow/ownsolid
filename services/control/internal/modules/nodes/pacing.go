package nodes

import (
	"context"
	"log/slog"
	"sync"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/audit"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// Agent pacing (ARC-007, ADR 0013). One row per cell says how often its agents report and
// how they back off. Every heartbeat reply carries it, so heartbeats read a cached copy
// and the cost of pacing does not grow with the fleet.

// PacingRefreshInterval bounds how long a control process serves pacing another process
// changed. A failed refresh waits as long, so a struggling database gets no extra reads.
const PacingRefreshInterval = 5 * time.Second

// AgentPacing is the stored pacing of this cell.
type AgentPacing struct {
	wire.Pacing
	Version   int64     `json:"version"`
	UpdatedAt time.Time `json:"updated_at"`
}

type pacingCache struct {
	refreshing sync.Mutex
	mu         sync.RWMutex
	value      wire.Pacing
	loadedAt   time.Time
}

// read returns the cached pacing, the default before the first load, and whether it is
// still fresh at now.
func (c *pacingCache) read(now time.Time) (wire.Pacing, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.loadedAt.IsZero() {
		return wire.DefaultPacing(), false
	}
	return c.value, now.Sub(c.loadedAt) < PacingRefreshInterval
}

func (c *pacingCache) put(value wire.Pacing, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.value, c.loadedAt = value, now
}

// currentPacing is what agents are told. It never fails a heartbeat: when the row cannot
// be read, agents keep the last known pacing. One caller refreshes an expired copy and
// the others answer from it meanwhile.
func (s *Service) currentPacing(ctx context.Context) wire.Pacing {
	now := time.Now()
	value, fresh := s.pacing.read(now)
	if fresh || !s.pacing.refreshing.TryLock() {
		return value
	}
	defer s.pacing.refreshing.Unlock()
	stored, e := s.loadPacing(ctx)
	if e != nil {
		slog.Warn("agent_pacing_refresh_failed", "code", e.Error())
		s.pacing.put(value, now)
		return value
	}
	value = s.effectivePacing(stored.Pacing)
	s.pacing.put(value, now)
	return value
}

// effectivePacing guards against an online window lowered after the row was written: such
// a row would mark healthy nodes offline and fence their inference.
func (s *Service) effectivePacing(p wire.Pacing) wire.Pacing {
	if p.Validate() != nil || !p.FitsOnlineWindow(s.Identity.Config.Policy().Nodes.PacingWindowSeconds()) {
		slog.Error("agent_pacing_outside_online_window", "heartbeat_seconds", p.HeartbeatSeconds, "jitter_percent", p.JitterPercent, "window_seconds", s.Identity.Config.Policy().Nodes.PacingWindowSeconds())
		return wire.DefaultPacing()
	}
	return p
}

func (s *Service) loadPacing(ctx context.Context) (AgentPacing, error) {
	store, e := s.repository()
	if e != nil {
		return AgentPacing{}, e
	}
	bounded, cancel := context.WithTimeout(ctx, config.Seconds(s.Identity.Config.Policy().HTTP.RequestSeconds))
	defer cancel()
	var out AgentPacing
	e = store.Within(bounded, true, func(ctx context.Context, q transaction.Executor) error {
		var e error
		out, e = readPacingWithin(ctx, q, "")
		return e
	})
	return out, e
}

// readPacingWithin reads the row of this cell. Chains without the table (CMP-201, CMP-202)
// run on the default pacing.
//
// suffix locks the row for a writer.
func readPacingWithin(ctx context.Context, q transaction.Executor, suffix string) (AgentPacing, error) {
	if q.Dialect() != transaction.PostgreSQL {
		return AgentPacing{Pacing: wire.DefaultPacing(), Version: 1}, nil
	}
	var out AgentPacing
	e := q.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, `SELECT heartbeat_seconds,jitter_percent,reconnect_base_seconds,reconnect_max_seconds,version,updated_at FROM agent_pacing WHERE site_id=local_site_id() AND cell_id=local_cell_id()`+suffix)).
		Scan(&out.HeartbeatSeconds, &out.JitterPercent, &out.ReconnectBaseSeconds, &out.ReconnectMaxSeconds, &out.Version, &out.UpdatedAt)
	out.UpdatedAt = out.UpdatedAt.UTC()
	return out, e
}

// AgentPacing returns the stored pacing of this cell to a platform administrator.
func (s *Service) AgentPacing(ctx context.Context, a identity.Auth) (AgentPacing, error) {
	if !isAdmin(a) {
		return AgentPacing{}, fail("ACCESS_DENIED")
	}
	var out AgentPacing
	e := s.updateWithin(ctx, a, func(ctx context.Context, q transaction.Executor) error {
		var e error
		out, e = readPacingWithin(ctx, q, "")
		return e
	})
	if e != nil {
		return AgentPacing{}, e
	}
	return out, nil
}

// UpdateAgentPacing changes the pacing of this cell. The change reaches agents with their
// next heartbeat reply; the refresh interval bounds it across control processes.
func (s *Service) UpdateAgentPacing(ctx context.Context, a identity.Auth, next wire.Pacing, version int64, reason, request string) (AgentPacing, error) {
	if !isAdmin(a) {
		return AgentPacing{}, fail("ACCESS_DENIED")
	}
	if ctx == nil || !nodeID(request) || !workspaces.ValidVersion(version) || !workspaces.ValidReason(reason) || next.Validate() != nil {
		return AgentPacing{}, fail("VALIDATION_FAILED")
	}
	if !next.FitsOnlineWindow(s.Identity.Config.Policy().Nodes.PacingWindowSeconds()) {
		return AgentPacing{}, fail("AGENT_PACING_EXCEEDS_ONLINE_WINDOW")
	}
	var out AgentPacing
	e := s.updateWithin(ctx, a, func(ctx context.Context, q transaction.Executor) error {
		if q.Dialect() != transaction.PostgreSQL {
			return fail("AGENT_PACING_UNSUPPORTED")
		}
		current, e := readPacingWithin(ctx, q, " FOR UPDATE")
		if e != nil {
			return e
		}
		if current.Version != version {
			return fail("VERSION_CONFLICT")
		}
		if current.Version == workspaces.MaximumVersion {
			return fail("RESOURCE_LIMIT_REACHED")
		}
		out = AgentPacing{Pacing: next, Version: current.Version + 1}
		if e = q.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, `UPDATE agent_pacing SET heartbeat_seconds=$1,jitter_percent=$2,reconnect_base_seconds=$3,reconnect_max_seconds=$4,version=$5,updated_at=clock_timestamp() WHERE site_id=local_site_id() AND cell_id=local_cell_id() RETURNING updated_at`,
			next.HeartbeatSeconds, next.JitterPercent, next.ReconnectBaseSeconds, next.ReconnectMaxSeconds, out.Version)).Scan(&out.UpdatedAt); e != nil {
			return e
		}
		out.UpdatedAt = out.UpdatedAt.UTC()
		return audit.WriteWithin(ctx, q, a.Me.User.ID, "", a.Me.Audience, "node.pacing_updated", "success", "agent_pacing", "", reason, request)
	})
	if e != nil {
		return AgentPacing{}, e
	}
	s.pacing.put(next, time.Now())
	return out, nil
}
