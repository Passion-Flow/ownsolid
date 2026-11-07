package install

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/Passion-Flow/ownsolid/pkg/operatorsocket"
)

// ShedAgents tells control on this host how much agent traffic to refuse (ADR 0043). The socket
// works with the connection limit full. With control not answering, for example while its database
// is stopped, the level waits on the host and control starts with it (ADR 0045).
func ShedAgents(ctx context.Context, c Config, level, reason string, out io.Writer) error {
	request := operatorsocket.ShedRequest{Level: level, Reason: reason}
	status, e := operatorsocket.SetShed(ctx, c.SocketPath(), request)
	if e != nil && e.Error() == operatorsocket.ReasonUnavailable {
		return holdShed(c, request, out)
	}
	if e != nil {
		return socketFailure(c, e)
	}
	return writeShed(out, status)
}

// holdShed leaves a level for control to find, owned so that control can clear it.
func holdShed(c Config, request operatorsocket.ShedRequest, out io.Writer) error {
	if !request.Valid() {
		return socketFailure(c, errors.New("VALIDATION_FAILED"))
	}
	raw, e := operatorsocket.EncodeHeld(request)
	if e != nil {
		return socketFailure(c, errors.New("VALIDATION_FAILED"))
	}
	if e = writeFile(operatorsocket.HeldPath(c.SocketPath()), raw, c, true); e != nil {
		return e
	}
	return writeHeld(out, request.Level)
}

// AgentStatus prints the level control enforces now, or the one waiting for it.
func AgentStatus(ctx context.Context, c Config, out io.Writer) error {
	status, e := operatorsocket.CurrentShed(ctx, c.SocketPath())
	if e == nil {
		return writeShed(out, status)
	}
	if e.Error() != operatorsocket.ReasonUnavailable {
		return socketFailure(c, e)
	}
	held, found, readError := operatorsocket.ReadHeld(operatorsocket.HeldPath(c.SocketPath()))
	if readError != nil || !found {
		return socketFailure(c, e)
	}
	return writeHeld(out, held.Level)
}

func writeShed(out io.Writer, status operatorsocket.ShedStatus) error {
	_, e := fmt.Fprintf(out, "AGENT_SHED_LEVEL %s\n", status.Level)
	if e == nil && !status.Stored {
		_, e = fmt.Fprintln(out, "AGENT_SHED_NOT_STORED: control acts on this level and keeps trying to store and audit it")
	}
	return e
}

func writeHeld(out io.Writer, level string) error {
	_, e := fmt.Fprintf(out, "AGENT_SHED_HELD %s: control is not answering. It starts with this level, then stores and audits it\n", level)
	return e
}

func socketFailure(c Config, e error) error {
	switch e.Error() {
	case operatorsocket.ReasonUnavailable:
		return fmt.Errorf("%s: nothing answered at %s. Check that control is running, and that you are root or its runtime user", e, c.SocketPath())
	case "VALIDATION_FAILED":
		return errors.New("VALIDATION_FAILED: the level must be none, bulk, telemetry or heartbeats, and the reason at most 500 characters")
	}
	return e
}
