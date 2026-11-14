package client

import (
	"context"
	"net"
	"strconv"
	"sync"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/agent/internal/settings"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/stats"
)

const prototypeDrainHint = "ownsolid-connection-drain-ns"
const prototypeCloseHint = "ownsolid-connection-close-ns"

type ageTransport struct {
	ended context.Context
	end   context.CancelFunc
}
type ageTransportContext struct{}
type ageTracker struct {
	mu   sync.Mutex
	live map[string]*ageTransport
}
type ageClientConn struct {
	*grpc.ClientConn
	tracker *ageTracker
}

func agePair(local, remote net.Addr) string {
	return local.Network() + "|" + local.String() + "|" + remote.Network() + "|" + remote.String()
}
func (a *ageTracker) TagConn(ctx context.Context, info *stats.ConnTagInfo) context.Context {
	done, end := context.WithCancel(context.Background())
	life := &ageTransport{done, end}
	a.mu.Lock()
	if a.live == nil {
		a.live = make(map[string]*ageTransport)
	}
	a.live[agePair(info.LocalAddr, info.RemoteAddr)] = life
	a.mu.Unlock()
	return context.WithValue(ctx, ageTransportContext{}, life)
}
func (a *ageTracker) HandleConn(ctx context.Context, event stats.ConnStats) {
	if _, ok := event.(*stats.ConnEnd); !ok {
		return
	}
	life, ok := ctx.Value(ageTransportContext{}).(*ageTransport)
	if !ok {
		return
	}
	a.mu.Lock()
	for key, current := range a.live {
		if current == life {
			delete(a.live, key)
		}
	}
	a.mu.Unlock()
	life.end()
}
func (*ageTracker) TagRPC(ctx context.Context, _ *stats.RPCTagInfo) context.Context { return ctx }
func (*ageTracker) HandleRPC(context.Context, stats.RPCStats)                       {}
func (a *ageTracker) bind(stream grpc.ClientStream) *ageTransport {
	p, ok := peer.FromContext(stream.Context())
	if !ok || p.LocalAddr == nil || p.Addr == nil {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.live[agePair(p.LocalAddr, p.Addr)]
}

type ageHint struct {
	drain, close time.Time
	duration     time.Duration
	life         *ageTransport
	attempted    bool
}

func durationHint(md metadata.MD, key string) (time.Duration, bool) {
	values := md.Get(key)
	if len(values) != 1 || len(values[0]) > 20 {
		return 0, false
	}
	value, err := strconv.ParseInt(values[0], 10, 64)
	if err != nil || value <= 0 || value > int64(48*time.Hour) || strconv.FormatInt(value, 10) != values[0] {
		return 0, false
	}
	return time.Duration(value), true
}
func (st *sessionStream) readAge(started time.Time) {
	md, err := st.stream.Header()
	if err != nil {
		return
	}
	drain, ok := durationHint(md, prototypeDrainHint)
	if !ok {
		return
	}
	closing, ok := durationHint(md, prototypeCloseHint)
	if !ok || closing < drain {
		return
	}
	life := st.connection.tracker.bind(st.stream)
	if life == nil {
		return
	}
	st.age = ageHint{drain: started.Add(drain), close: started.Add(closing), duration: drain, life: life}
}

type ageRetirement struct {
	stop chan struct{}
	done chan struct{}
	once sync.Once
}

func retireAgeConnection(conn *ageClientConn, hint ageHint) *ageRetirement {
	r := &ageRetirement{stop: make(chan struct{}), done: make(chan struct{})}
	go func() {
		timer := time.NewTimer(max(time.Until(hint.close), 0))
		defer timer.Stop()
		select {
		case <-hint.life.ended.Done():
		case <-timer.C:
		case <-r.stop:
		}
		_ = conn.Close()
		close(r.done)
	}()
	return r
}
func (r *ageRetirement) close() { r.once.Do(func() { close(r.stop) }); <-r.done }
func (s *Session) tryAgeHandoff(ctx context.Context, h wire.Handshake) *wire.Disposition {
	hint := &s.stream.age
	if hint.life == nil || hint.attempted {
		return nil
	}
	if s.retiring != nil {
		select {
		case <-s.retiring.done:
			s.retiring = nil
		default:
			return nil
		}
	}
	interval := time.Duration(s.pacing.HeartbeatSeconds) * time.Second * time.Duration(100+s.pacing.JitterPercent) / 100
	lead := min(hint.duration/2, settings.Seconds(s.policy().ControlRequestSeconds)+2*interval)
	if s.now().Before(hint.drain.Add(-lead)) {
		return nil
	}
	hint.attempted = true
	// This probe never retries an outstanding claim or changes the capacity policy.
	budget := settings.Seconds(s.policy().ControlRequestSeconds)
	remaining := hint.drain.Sub(s.now())
	if remaining > 0 {
		budget = min(budget, remaining/2)
	}
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
			old, oldHint := s.conn, s.stream.age
			s.dropStream()
			s.conn, s.stream = candidate, st
			s.lastExchange = s.now()
			s.retiring = retireAgeConnection(old, oldHint)
			return reply.Disposition
		}
		st.cancel()
	}
	_ = candidate.Close()
	return nil
}
