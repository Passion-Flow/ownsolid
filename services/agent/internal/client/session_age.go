package client

import (
	"context"
	"log/slog"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/agent/internal/settings"
	"github.com/Passion-Flow/ownsolid/services/agent/sessiontransport"
)

type ageHint struct {
	sessiontransport.Age
	attempted bool
}

func (st *sessionStream) readAge(started time.Time) {
	age, ok := st.connection.ReadAge(st.stream, started)
	if ok {
		st.age = ageHint{Age: age}
	}
}
func (s *Session) retirementPending() bool {
	if s.retiring == nil {
		return false
	}
	select {
	case <-s.retiring.Done():
		s.retiring = nil
		return false
	default:
		return true
	}
}
func (s *Session) tryAgeHandoff(ctx context.Context, h wire.Handshake) *wire.Disposition {
	hint := &s.stream.age
	if hint.Lifetime == nil || hint.attempted {
		return nil
	}
	if s.retirementPending() {
		return nil
	}
	interval := time.Duration(s.pacing.HeartbeatSeconds) * time.Second * time.Duration(100+s.pacing.JitterPercent) / 100
	if !hint.Due(s.now(), settings.Seconds(s.policy().ControlRequestSeconds), interval) {
		return nil
	}
	hint.attempted = true
	budget := hint.ProbeBudget(s.now(), settings.Seconds(s.policy().ControlRequestSeconds))
	idleRemaining := s.lastExchange.Add(settings.Seconds(s.pacing.IdleSeconds() - sessionIdleMarginSeconds)).Sub(s.now())
	budget = min(budget, idleRemaining)
	if budget <= 0 {
		return nil
	}
	probe, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	address := ""
	if len(s.gateways) > 0 {
		address = s.gateways[s.gateway]
	}
	adopted := false
	defer func() {
		if !adopted && ctx.Err() == nil {
			slog.Warn("agent_session_connection_handoff_failed", "node_id", s.node, "gateway", address)
		}
	}()
	candidate, err := s.dial(address)
	if err != nil {
		return nil
	}
	st, err := s.openStream(probe, candidate)
	if err == nil {
		reply, exchangeErr := s.exchangeOn(probe, st, wire.AgentMessage{Heartbeat: &h})
		if exchangeErr == nil && reply.Disposition.AcceptsSessionRebalance() && probe.Err() == nil {
			retiring, retireErr := s.conn.Retire(hint.Lifetime, hint.CloseAt)
			if retireErr == nil {
				s.dropStream()
				s.conn, s.stream = candidate, st
				s.lastExchange = s.now()
				s.retiring = retiring
				adopted = true
				slog.Info("agent_session_connection_handoff", "node_id", s.node, "gateway", address)
				return reply.Disposition
			}
		}
		st.cancel()
	}
	_ = candidate.Close()
	return nil
}
