package client

import (
	"errors"
	"io"
	"net/http"
	"testing"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type terminalRefusalStream struct {
	grpc.ClientStream
	code            string
	sends, receives int
}

func (s *terminalRefusalStream) SendMsg(any) error {
	s.sends++
	return io.EOF
}

func (s *terminalRefusalStream) RecvMsg(any) error {
	s.receives++
	return status.Error(codes.Unauthenticated, s.code)
}

func TestEarlyTerminalRefusalRetainsAuthenticationStatus(t *testing.T) {
	for _, code := range []string{"AGENT_UNAUTHORIZED", "AGENT_CERTIFICATE_REQUIRED"} {
		t.Run(code, func(t *testing.T) {
			transport := &terminalRefusalStream{code: code}
			s := &Session{client: &Client{}, node: sessionNode, now: time.Now,
				pacing: wire.DefaultPacing(), lastExchange: time.Now(),
				stream: &sessionStream{stream: transport, cancel: func() {}}}
			_, err := s.Heartbeat(t.Context(), wire.Handshake{ProtocolVersion: wire.Version})
			var problem *Problem
			if !errors.As(err, &problem) || problem.Status != http.StatusUnauthorized || problem.Code != code {
				t.Fatalf("terminal refusal lost its authentication status: %v", err)
			}
			if transport.sends != 1 || transport.receives != 1 || s.probeHTTPS || !s.fallbackUntil.IsZero() {
				t.Fatal("terminal refusal was replayed or treated as an HTTPS fallback")
			}
		})
	}
}
