package sessiontransport

import (
	"errors"
	"io"

	"google.golang.org/grpc"
)

// Exchange sends once and reads one response or final RPC status. A remotely
// generated refusal can surface from SendMsg as EOF; RecvMsg owns that status.
// Callers retain cancellation, frame validation and all application retry policy.
func Exchange(stream grpc.ClientStream, request, reply any) error {
	if err := stream.SendMsg(request); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return stream.RecvMsg(reply)
}
