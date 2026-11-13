package link

import (
	"io"
	"testing"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type terminalRefusalStream struct {
	grpc.ClientStream
	sends, receives int
}

func (s *terminalRefusalStream) SendMsg(any) error {
	s.sends++
	return io.EOF
}

func (s *terminalRefusalStream) RecvMsg(any) error {
	s.receives++
	return status.Error(codes.Unauthenticated, "AGENT_UNAUTHORIZED")
}

func TestEarlyTerminalRefusalRetainsRevocationWithoutReplay(t *testing.T) {
	transport := new(terminalRefusalStream)
	stream := &Stream{stream: transport, cancel: func() {}, budget: time.Second}
	_, err := stream.Exchange(t.Context(), wire.AgentMessage{Heartbeat: &wire.Handshake{ProtocolVersion: wire.Version}})
	if !Revoked(err) || Code(err) != "AGENT_UNAUTHORIZED" || transport.sends != 1 || transport.receives != 1 {
		t.Fatalf("early terminal refusal changed status or repeated a frame: %v", err)
	}
}
