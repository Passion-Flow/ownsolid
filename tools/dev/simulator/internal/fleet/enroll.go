package fleet

import (
	"bufio"
	"context"
	"errors"
	"log/slog"
	"math/rand/v2"
	"os"
	"strings"
	"sync"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/tools/dev/simulator/internal/config"
	"github.com/Passion-Flow/ownsolid/tools/dev/simulator/internal/link"
	"github.com/Passion-Flow/ownsolid/tools/dev/simulator/internal/report"
)

// enrollAttempts bounds how often one enrollment is tried while control asks the agent to
// come back or cannot be reached.
const enrollAttempts = 20

// commitUnknown is control's answer when it cannot tell whether an enrollment committed.
// The token may be spent, so the enrollment is not repeated.
const commitUnknown = "STORAGE_COMMIT_OUTCOME_UNKNOWN"

// Enroll enrolls one simulated agent per join token, up to c.Agents, and writes the
// identities file. It refuses to overwrite one. Agents that could not enroll are left out
// and reported in the error; the others are written.
func Enroll(ctx context.Context, c config.Config) error {
	if _, e := os.Stat(c.IdentitiesFile); !errors.Is(e, os.ErrNotExist) {
		return errors.New("SIMULATOR_IDENTITIES_EXIST")
	}
	tokens, e := readTokens(c.JoinTokensFile)
	if e != nil {
		return e
	}
	if len(tokens) < c.Agents {
		return errors.New("SIMULATOR_JOIN_TOKENS_TOO_FEW")
	}
	dialer, e := link.NewDialer(c.ControlOrigin, c.CAFile, c.DialAddress, c.HTTPSConnections, time.Duration(c.RequestSeconds)*time.Second)
	if e != nil {
		return e
	}
	defer dialer.Close()
	recorder := report.New(c.Agents, time.Now())
	enrolled := make([]Identity, c.Agents)
	jobs := make(chan int)
	var workers sync.WaitGroup
	for range c.EnrollConcurrency {
		workers.Go(func() {
			for index := range jobs {
				identity, e := enroll(ctx, dialer, recorder, tokens[index], hostname(index))
				if e != nil {
					slog.Warn("simulator_enroll_failed", "hostname", hostname(index), "code", link.Code(e))
					continue
				}
				enrolled[index] = identity
			}
		})
	}
	for index := range c.Agents {
		select {
		case jobs <- index:
		case <-ctx.Done():
		}
	}
	close(jobs)
	workers.Wait()
	var out []Identity
	for _, identity := range enrolled {
		if identity.NodeID != "" {
			out = append(out, identity)
		}
	}
	calls := recorder.Snapshot().Calls[report.CallEnroll]
	slog.Info("simulator_enrolled", "agents", len(out), "of", c.Agents, "latency_p99_ms", calls.LatencyMilliseconds.P99)
	if e := saveIdentities(c.IdentitiesFile, c.ControlOrigin, out); e != nil {
		return errors.New("SIMULATOR_IDENTITIES_UNWRITABLE")
	}
	if len(out) < c.Agents {
		return errors.New("SIMULATOR_ENROLLMENT_INCOMPLETE")
	}
	return nil
}

// readTokens reads one join token per line. Every line must hold a token.
func readTokens(path string) ([]string, error) {
	f, e := os.Open(path)
	if e != nil {
		return nil, errors.New("SIMULATOR_JOIN_TOKENS_UNREADABLE")
	}
	defer f.Close()
	var tokens []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		token := strings.TrimSpace(scanner.Text())
		if token == "" {
			continue
		}
		if !credential.MatchString(token) {
			return nil, errors.New("SIMULATOR_JOIN_TOKEN_INVALID")
		}
		tokens = append(tokens, token)
	}
	if scanner.Err() != nil {
		return nil, errors.New("SIMULATOR_JOIN_TOKENS_UNREADABLE")
	}
	return tokens, nil
}

// enroll enrolls one agent, coming back while control asks it to or cannot be reached.
func enroll(ctx context.Context, dialer *link.Dialer, recorder *report.Recorder, token, host string) (Identity, error) {
	pacing := wire.DefaultPacing()
	var e error
	for attempt := range enrollAttempts {
		started := time.Now()
		var out wire.Enrolled
		e = dialer.Post(ctx, "/agent/v1/enroll", "", wire.Enrollment{JoinToken: token, Handshake: handshake(host)}, &out)
		if e == nil {
			if !nodeID.MatchString(out.NodeID) || !credential.MatchString(out.Credential) || out.ProtocolVersion != wire.ProfileVersion(wire.ReadOnlyProfile) || !(wire.Scope{SiteID: out.SiteID, CellID: out.CellID}).Valid() {
				recorder.Failed(report.CallEnroll, "ENROLLMENT_RESPONSE_INVALID")
				return Identity{}, errors.New("ENROLLMENT_RESPONSE_INVALID")
			}
			recorder.Succeeded(report.CallEnroll, time.Since(started))
			return Identity{Hostname: host, NodeID: out.NodeID, SiteID: out.SiteID, CellID: out.CellID, Credential: out.Credential}, nil
		}
		recorder.Failed(report.CallEnroll, link.Code(e))
		if !comeBack(e) || link.Code(e) == commitUnknown {
			return Identity{}, e
		}
		delay := pacing.Backoff(rand.Int64N, attempt)
		if hint, ok := link.RetryHint(e); ok {
			delay = max(delay, wire.AfterHint(rand.Int64N, hint))
		}
		if !sleep(ctx, delay) {
			return Identity{}, ctx.Err()
		}
	}
	return Identity{}, e
}

// comeBack reports whether the failure e asks the agent to try again: control was not
// reached, or refused with a retry hint or a server error.
func comeBack(e error) bool {
	if _, hinted := link.RetryHint(e); hinted || errors.Is(e, link.ErrUnreachable) {
		return true
	}
	var p *link.Problem
	return errors.As(e, &p) && p.Status >= 500
}

// sleep waits for d or until ctx ends, and reports whether d passed.
func sleep(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
