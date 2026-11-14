package nodes

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/Passion-Flow/ownsolid/pkg/operatorsocket"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/audit"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/agentadmission"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// Agent shedding (ARC-016, ADR 0043). An operator can tell control to refuse agent traffic,
// lowest priority first. The level acts in memory, because the database may be what is
// overloaded, and is stored so that it survives a restart and reaches other gateways.

// AgentShed is the stored shed level of this cell.
type AgentShed struct {
	Level     string    `json:"level"`
	Version   int64     `json:"version"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ShedSwitch is the admission controller of this process.
type ShedSwitch interface {
	Shed() agentadmission.ShedLevel
	SetShed(agentadmission.ShedLevel) error
}

// localShed is a level set on this host that the database has not stored yet. While it waits,
// the stored level must not overwrite it. heldFile keeps it across a restart (ADR 0045).
type localShed struct {
	mu       sync.Mutex
	pending  *agentadmission.ShedLevel
	reason   string
	heldFile string
}

// HoldShedAt names the file that keeps a host level until it is stored, and takes up a level
// already waiting there. Call it before agents are served, so that a fleet held back through a
// database outage is not let in when control returns.
func (s *Service) HoldShedAt(path string) {
	s.localShed.mu.Lock()
	s.localShed.heldFile = path
	s.localShed.mu.Unlock()
	s.adoptHeldShed()
}

// adoptHeldShed acts on a level ownsolid-install left while control was not answering.
func (s *Service) adoptHeldShed() {
	s.localShed.mu.Lock()
	defer s.localShed.mu.Unlock()
	if s.localShed.heldFile == "" || s.localShed.pending != nil {
		return
	}
	held, found, e := operatorsocket.ReadHeld(s.localShed.heldFile)
	if e != nil {
		slog.Error("agent_shed_held_unreadable", "code", "VALIDATION_FAILED")
		return
	}
	if !found {
		return
	}
	level, e := agentadmission.ParseShedLevel(held.Level)
	if e != nil {
		return
	}
	s.applyShed(level)
	s.localShed.pending, s.localShed.reason = &level, held.Reason
	slog.Warn("agent_shed_adopted_from_host", "level", level.String(), "reason", held.Reason)
}

func readShedWithin(ctx context.Context, q transaction.Executor, suffix string) (AgentShed, error) {
	if q.Dialect() != transaction.PostgreSQL {
		return AgentShed{Level: agentadmission.ShedNone.String(), Version: 1}, nil
	}
	var out AgentShed
	e := q.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, `SELECT shed_level,shed_version,shed_updated_at FROM agent_pacing WHERE site_id=local_site_id() AND cell_id=local_cell_id()`+suffix)).
		Scan(&out.Level, &out.Version, &out.UpdatedAt)
	out.UpdatedAt = out.UpdatedAt.UTC()
	return out, e
}

// AgentShed returns the stored level of this cell to a platform administrator.
func (s *Service) AgentShed(ctx context.Context, a identity.Auth) (AgentShed, error) {
	if !isAdmin(a) {
		return AgentShed{}, fail("ACCESS_DENIED")
	}
	var out AgentShed
	e := s.updateWithin(ctx, a, func(ctx context.Context, q transaction.Executor) error {
		var e error
		out, e = readShedWithin(ctx, q, "")
		return e
	})
	return out, e
}

// UpdateAgentShed stores a new level and applies it to this process at once; other gateways
// follow within the refresh interval.
func (s *Service) UpdateAgentShed(ctx context.Context, a identity.Auth, name string, version int64, reason, request string) (AgentShed, error) {
	if !isAdmin(a) {
		return AgentShed{}, fail("ACCESS_DENIED")
	}
	level, e := agentadmission.ParseShedLevel(name)
	if e != nil || ctx == nil || !nodeID(request) || !workspaces.ValidVersion(version) || !workspaces.ValidReason(reason) {
		return AgentShed{}, fail("VALIDATION_FAILED")
	}
	var out AgentShed
	e = s.updateWithin(ctx, a, func(ctx context.Context, q transaction.Executor) error {
		if q.Dialect() != transaction.PostgreSQL {
			return fail("AGENT_SHED_UNSUPPORTED")
		}
		current, e := readShedWithin(ctx, q, " FOR UPDATE")
		if e != nil {
			return e
		}
		if current.Version != version {
			return fail("VERSION_CONFLICT")
		}
		if current.Version == workspaces.MaximumVersion {
			return fail("RESOURCE_LIMIT_REACHED")
		}
		if out, e = writeShedWithin(ctx, q, level, current.Version+1); e != nil {
			return e
		}
		return audit.WriteWithin(ctx, q, a.Me.User.ID, "", a.Me.Audience, "node.agents_shed", "success", "agent_pacing", "", reason, request)
	})
	if e != nil {
		return AgentShed{}, e
	}
	s.applyShed(level)
	return out, nil
}

func writeShedWithin(ctx context.Context, q transaction.Executor, level agentadmission.ShedLevel, version int64) (AgentShed, error) {
	out := AgentShed{Level: level.String(), Version: version}
	e := q.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, `UPDATE agent_pacing SET shed_level=$1,shed_version=$2,shed_updated_at=clock_timestamp() WHERE site_id=local_site_id() AND cell_id=local_cell_id() RETURNING shed_updated_at`, out.Level, version)).Scan(&out.UpdatedAt)
	out.UpdatedAt = out.UpdatedAt.UTC()
	return out, e
}

func (s *Service) applyShed(level agentadmission.ShedLevel) {
	if s.Shed == nil {
		return
	}
	if e := s.Shed.SetShed(level); e != nil {
		slog.Error("agent_shed_not_applied", "level", level.String(), "code", e.Error())
	}
}

// LocalShed reports the level this process enforces, for the operator socket.
func (s *Service) LocalShed() operatorsocket.ShedStatus {
	s.localShed.mu.Lock()
	waiting := s.localShed.pending != nil
	s.localShed.mu.Unlock()
	return operatorsocket.ShedStatus{Level: s.Shed.Shed().String(), Stored: !waiting}
}

// SetLocalShed is the last resort of an operator on the host (ADR 0043). It acts at once without
// the database and reports whether the level was also stored; when it was not, RunAgentShed
// keeps trying, and the held file carries the level across a restart.
func (s *Service) SetLocalShed(ctx context.Context, r operatorsocket.ShedRequest) (operatorsocket.ShedStatus, error) {
	level, e := agentadmission.ParseShedLevel(r.Level)
	if e != nil || !workspaces.ValidReason(r.Reason) {
		return operatorsocket.ShedStatus{}, fail("VALIDATION_FAILED")
	}
	s.applyShed(level)
	slog.Warn("agent_shed_set_on_host", "level", level.String(), "reason", r.Reason)
	s.localShed.mu.Lock()
	s.localShed.pending, s.localShed.reason = &level, r.Reason
	if s.localShed.heldFile != "" {
		if e = operatorsocket.WriteHeld(s.localShed.heldFile, r); e != nil {
			slog.Error("agent_shed_not_held", "level", level.String())
		}
	}
	s.localShed.mu.Unlock()
	if e = s.storeLocalShed(ctx); e != nil {
		slog.Warn("agent_shed_not_stored", "code", e.Error())
	}
	return s.LocalShed(), nil
}

// storeLocalShed writes a pending host level with its audit record. Nobody signed in, so the
// record names no user.
func (s *Service) storeLocalShed(ctx context.Context) error {
	s.localShed.mu.Lock()
	pending, reason := s.localShed.pending, s.localShed.reason
	s.localShed.mu.Unlock()
	if pending == nil {
		return nil
	}
	store, e := s.repository()
	if e != nil {
		return e
	}
	bounded, cancel := context.WithTimeout(ctx, config.Seconds(s.Identity.Config.Policy().HTTP.RequestSeconds))
	defer cancel()
	e = store.Within(bounded, false, func(ctx context.Context, q transaction.Executor) error {
		if q.Dialect() != transaction.PostgreSQL {
			return nil
		}
		current, e := readShedWithin(ctx, q, " FOR UPDATE")
		if e != nil {
			return e
		}
		if current.Version == workspaces.MaximumVersion {
			return fail("RESOURCE_LIMIT_REACHED")
		}
		if _, e = writeShedWithin(ctx, q, *pending, current.Version+1); e != nil {
			return e
		}
		return audit.WriteWithin(ctx, q, "", "", "operator", "node.agents_shed", "success", "agent_pacing", "", reason, "")
	})
	if e != nil {
		return e
	}
	s.localShed.mu.Lock()
	defer s.localShed.mu.Unlock()
	if s.localShed.pending != pending {
		return nil
	}
	s.localShed.pending = nil
	if s.localShed.heldFile != "" {
		if e = os.Remove(s.localShed.heldFile); e != nil && !errors.Is(e, fs.ErrNotExist) {
			slog.Error("agent_shed_held_not_cleared")
		}
	}
	return nil
}

// RestoreShed applies the stored level, or a level held on the host, before agents are served, so
// that a restart never lets in a fleet an operator held back.
func (s *Service) RestoreShed(ctx context.Context) { s.syncShed(ctx) }

// RunAgentShed follows the stored level until ctx ends, so a restart restores it and every
// gateway applies what another one stored. It does not depend on agent traffic, which the
// strongest level stops.
func (s *Service) RunAgentShed(ctx context.Context) {
	ticker := time.NewTicker(PacingRefreshInterval)
	defer ticker.Stop()
	for {
		s.syncShed(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *Service) syncShed(ctx context.Context) {
	s.adoptHeldShed()
	if e := s.storeLocalShed(ctx); e != nil {
		slog.Warn("agent_shed_not_stored", "code", e.Error())
		return
	}
	s.localShed.mu.Lock()
	waiting := s.localShed.pending != nil
	s.localShed.mu.Unlock()
	if waiting {
		return
	}
	store, e := s.repository()
	if e != nil {
		return
	}
	bounded, cancel := context.WithTimeout(ctx, config.Seconds(s.Identity.Config.Policy().HTTP.RequestSeconds))
	defer cancel()
	var stored AgentShed
	if e = store.Within(bounded, true, func(ctx context.Context, q transaction.Executor) error {
		var e error
		stored, e = readShedWithin(ctx, q, "")
		return e
	}); e != nil {
		slog.Warn("agent_shed_refresh_failed", "code", e.Error())
		return
	}
	level, e := agentadmission.ParseShedLevel(stored.Level)
	if e != nil {
		slog.Error("agent_shed_level_unreadable", "code", e.Error())
		return
	}
	s.applyShed(level)
}
