package runtime

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"log/slog"
	mathrand "math/rand/v2"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/agent/internal/client"
	"github.com/Passion-Flow/ownsolid/services/agent/internal/state"
)

const (
	certificateRetryBase = 5 * time.Second
	certificateRetryMax  = 15 * time.Minute
)

func renewAt(leaf *x509.Certificate) time.Time {
	return wire.RenewAt(mathrand.Int64N, leaf.NotBefore, leaf.NotAfter)
}

// certificateAgent keeps a node's certificate current while the agent runs.
type certificateAgent struct {
	directory  string
	node       string
	connection *client.Client
	now        func() time.Time
}

// errStopRotating ends the loop: the agent is revoked, or control cannot issue certificates.
var errStopRotating = errors.New("CERTIFICATE_ROTATION_STOPPED")

// run rotates the certificate until ctx ends. A failure never stops the agent's work, which
// outlives control; it is retried, and an expired certificate is replaced by the same path.
func (a certificateAgent) run(ctx context.Context) {
	failures := 0
	for {
		wait, e := a.step(ctx)
		if ctx.Err() != nil {
			return
		}
		if errors.Is(e, errStopRotating) {
			return
		}
		if e != nil {
			slog.Warn("agent_certificate_failed", "code", e.Error())
			failures++
			wait = wire.FullJitter(mathrand.Int64N, certificateRetryBase, certificateRetryMax, failures)
			if hint, ok := client.RetryHint(e); ok {
				wait = max(wait, client.AfterHint(hint))
			}
		} else if wait == 0 {
			failures = 0
			continue
		} else {
			failures = 0
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

// step does the next thing the certificate needs and says how long until the one after.
func (a certificateAgent) step(ctx context.Context) (time.Duration, error) {
	current, found, e := state.LoadCertificate(a.directory)
	if e != nil {
		return 0, e
	}
	switch {
	case !found || current.Certificate == "":
		if !found {
			key, e := wire.NewCertificateKey()
			if e != nil {
				return 0, e
			}
			current = state.Certificate{PrivateKey: key}
			if e = state.SaveCertificate(a.directory, current); e != nil {
				return 0, e
			}
		}
		return 0, a.renew(ctx, current.PrivateKey)
	case a.expired(current):
		// With no certificate to present, only a key control has pinned is accepted, and a
		// pending key may never have reached it. The old key is pinned either way.
		return 0, a.renew(ctx, current.PrivateKey)
	case current.PendingKey != "":
		return 0, a.renew(ctx, current.PendingKey)
	case !current.Confirmed:
		return 0, a.confirm(ctx, current)
	}
	if wait := current.RenewAt.Sub(a.now()); wait > 0 {
		return wait, nil
	}
	key, e := wire.NewCertificateKey()
	if e != nil {
		return 0, e
	}
	current.PendingKey = key
	if e = state.SaveCertificate(a.directory, current); e != nil {
		return 0, e
	}
	return 0, a.renew(ctx, key)
}

// expired reports a certificate this agent no longer presents.
func (a certificateAgent) expired(current state.Certificate) bool {
	block, _ := pem.Decode([]byte(current.Certificate))
	if block == nil {
		return true
	}
	leaf, e := x509.ParseCertificate(block.Bytes)
	return e != nil || !a.now().Add(wire.ExpirySafety).Before(leaf.NotAfter)
}

// renew gets a certificate for key and starts using it.
func (a certificateAgent) renew(ctx context.Context, key string) error {
	request, e := wire.NewCertificateRequest(key)
	if e != nil {
		return e
	}
	issued, e := a.connection.RenewCertificate(ctx, a.node, request)
	if e != nil {
		return a.classify(e)
	}
	leaf, e := wire.CheckIssuedCertificate(issued.Certificate, key, a.node)
	if e != nil {
		return e
	}
	next := state.Certificate{PrivateKey: key, Certificate: issued.Certificate, RenewAt: renewAt(leaf)}
	if e = state.SaveCertificate(a.directory, next); e != nil {
		return e
	}
	if e = a.connection.UseCertificate(next.Certificate, next.PrivateKey); e != nil {
		return e
	}
	slog.Info("agent_certificate_renewed", "not_after", leaf.NotAfter)
	return nil
}

// confirm ends the rotation. The new certificate is in use by now.
func (a certificateAgent) confirm(ctx context.Context, current state.Certificate) error {
	if e := a.connection.ConfirmCertificate(ctx, a.node); e != nil {
		return a.classify(e)
	}
	current.Confirmed = true
	return state.SaveCertificate(a.directory, current)
}

// classify decides what a refusal means here. A revoked node stops rotating, and the run loop
// ends the agent; an engine that keeps no pins cannot issue a certificate at all.
func (a certificateAgent) classify(e error) error {
	var p *client.Problem
	if errors.As(e, &p) && !client.CertificateRefused(e) && (p.Status == 401 || p.Code == "AGENT_IDENTITY_UNAVAILABLE") {
		slog.Info("agent_certificate_rotation_stopped", "code", p.Code)
		return errStopRotating
	}
	return e
}
