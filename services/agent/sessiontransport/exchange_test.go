package sessiontransport

import (
	"context"
	"errors"
	"io"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type exchangeStream struct {
	grpc.ClientStream
	sendError, receiveError error
	sends, receives         int
}

func (s *exchangeStream) SendMsg(any) error {
	s.sends++
	return s.sendError
}

func (s *exchangeStream) RecvMsg(reply any) error {
	s.receives++
	if s.receiveError == nil {
		*reply.(*int) = 7
	}
	return s.receiveError
}

func TestExchangeReadsRemoteFinalStatusAfterSenderEOF(t *testing.T) {
	for _, code := range []codes.Code{codes.Unauthenticated, codes.ResourceExhausted, codes.Unavailable} {
		t.Run(code.String(), func(t *testing.T) {
			stream := &exchangeStream{sendError: io.EOF, receiveError: status.Error(code, "refused")}
			var reply int
			if err := Exchange(stream, new(int), &reply); status.Code(err) != code {
				t.Fatalf("final RPC status was lost: %v", err)
			}
			if stream.sends != 1 || stream.receives != 1 || reply != 0 {
				t.Fatal("terminal status was replayed or fabricated an application reply")
			}
		})
	}
}

func TestExchangeKeepsLocalSendFailureAndEmptyTermination(t *testing.T) {
	local := &exchangeStream{sendError: context.Canceled}
	var reply int
	if err := Exchange(local, new(int), &reply); !errors.Is(err, context.Canceled) || local.sends != 1 || local.receives != 0 {
		t.Fatal("local send failure initiated another I/O operation")
	}
	empty := &exchangeStream{sendError: io.EOF, receiveError: io.EOF}
	if err := Exchange(empty, new(int), &reply); !errors.Is(err, io.EOF) || empty.sends != 1 || empty.receives != 1 || reply != 0 {
		t.Fatal("empty stream termination became a successful application reply")
	}
}

func TestExchangeDeliversOneDecodedReplyWithoutRepeatingSend(t *testing.T) {
	for _, sendError := range []error{nil, io.EOF} {
		stream := &exchangeStream{sendError: sendError}
		var reply int
		if err := Exchange(stream, new(int), &reply); err != nil || reply != 7 || stream.sends != 1 || stream.receives != 1 {
			t.Fatalf("one response was not delivered once: %v", err)
		}
	}
}
