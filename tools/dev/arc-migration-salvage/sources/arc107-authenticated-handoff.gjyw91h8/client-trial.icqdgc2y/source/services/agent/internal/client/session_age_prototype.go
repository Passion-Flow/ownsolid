package client

import (
	"context"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/agent/internal/settings"
	"github.com/Passion-Flow/ownsolid/services/agent/sessiontransport"
	"google.golang.org/grpc"
)

type ageClientConn = sessiontransport.Connection
type ageRetirement = sessiontransport.Retirement

func newAgeClientConn(target string, options ...grpc.DialOption) (*ageClientConn, error) {
	return sessiontransport.New(target, options...)
}

type ageHint struct {
	wire.SessionConnectionAge
	life      context.Context
	attempted bool
}

func (st *sessionStream) readAge(started time.Time) {
	md, err := st.stream.Header()
	if err != nil {
		return
	}
	age, ok := wire.ParseSessionConnectionAge(md.Get(wire.SessionDrainHeader), md.Get(wire.SessionCloseHeader), started)
	if !ok {
		return
	}
	life := st.connection.Lifetime(st.stream)
	if life == nil {
		return
	}
	st.age = ageHint{SessionConnectionAge: age, life: life}
}
func (s *Session) tryAgeHandoff(ctx context.Context, h wire.Handshake) *wire.Disposition {
	hint := &s.stream.age
	if hint.life == nil || hint.attempted {
		return nil
	}
	if s.retiring != nil {
		select {
		case <-s.retiring.Done():
			s.retiring = nil
		default:
			return nil
		}
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
	candidate, err := s.dial(address)
	if err != nil {
		return nil
	}
	st, err := s.openStream(probe, candidate)
	if err == nil {
		reply, exchangeErr := s.exchangeOn(probe, st, wire.AgentMessage{Heartbeat: &h})
		if exchangeErr == nil && reply.Disposition.AcceptsSessionRebalance() && probe.Err() == nil {
			retiring, retireErr := s.conn.Retire(hint.life, hint.CloseAt)
			if retireErr == nil {
				s.dropStream()
				s.conn, s.stream = candidate, st
				s.lastExchange = s.now()
				s.retiring = retiring
				return reply.Disposition
			}
		}
		st.cancel()
	}
	_ = candidate.Close()
	return nil
}
