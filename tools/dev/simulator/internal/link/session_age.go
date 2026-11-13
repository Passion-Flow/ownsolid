package link

import (
	"context"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/agent/sessiontransport"
)

type connectionAge struct {
	sessiontransport.Age
	attempted bool
}

func (s *Stream) readAge(started time.Time) {
	age, ok := s.connection.ReadAge(s.stream, started)
	if ok {
		s.age = connectionAge{Age: age}
	}
}
func (c *Connection) retirementPending() bool {
	if c.retiring == nil {
		return false
	}
	select {
	case <-c.retiring.Done():
		c.retiring = nil
		return false
	default:
		return true
	}
}

// Handoff refreshes one certificate-bound Agent's physical connection between
// exchanges. Legacy multiplexed channels retain their original behavior.
func (c *Connection) Handoff(ctx context.Context, currentStream *Stream, node Node, h wire.Handshake, pacing wire.Pacing, idleDeadline time.Time) (*Stream, *wire.Disposition, error) {
	if currentStream == nil || len(c.dialer.tls.Certificates) != 1 || currentStream.age.Lifetime == nil || currentStream.age.attempted {
		return nil, nil, nil
	}
	c.mu.Lock()
	current := c.conn
	rank := c.gateway
	if current == nil || currentStream.connection != current || c.retirementPending() {
		c.mu.Unlock()
		return nil, nil, nil
	}
	now := time.Now()
	interval := time.Duration(pacing.HeartbeatSeconds) * time.Second * time.Duration(100+pacing.JitterPercent) / 100
	if !currentStream.age.Due(now, c.dialer.request, interval) {
		c.mu.Unlock()
		return nil, nil, nil
	}
	currentStream.age.attempted = true
	budget := min(currentStream.age.ProbeBudget(now, c.dialer.request), idleDeadline.Sub(now))
	c.mu.Unlock()
	if budget <= 0 {
		return nil, nil, nil
	}
	probe, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	candidate, err := c.dial(rank)
	if err != nil {
		return nil, nil, err
	}
	stream, err := c.open(probe, candidate, node)
	if err == nil {
		var reply wire.ControlMessage
		reply, err = stream.Exchange(probe, wire.AgentMessage{Heartbeat: &h})
		if err == nil && !reply.Disposition.AcceptsSessionRebalance() {
			err = ErrInvalid
		}
		if err == nil {
			c.mu.Lock()
			if c.conn == current && probe.Err() == nil && !c.retirementPending() {
				retirement, retireErr := current.Retire(currentStream.age.Lifetime, currentStream.age.CloseAt)
				if retireErr == nil {
					c.conn = candidate
					c.retiring = retirement
					c.mu.Unlock()
					return stream, reply.Disposition, nil
				}
				err = retireErr
			}
			c.mu.Unlock()
		}
		stream.Close()
	}
	_ = candidate.Close()
	if err == nil {
		err = ErrUnreachable
	}
	return nil, nil, err
}
