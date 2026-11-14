//go:build repository_integration

package commitfault

import "io"

type Mode uint32

const (
	Pass Mode = iota
	DropBeforeCommit
	DropCommitAcknowledgement
)

type Observation struct {
	Mode       Mode
	Forwarded  bool
	ServerOK   bool
	Suppressed bool
}

const maximumProxyConnections = 2
const proxyConnectionSeconds = 10

func writePacket(writer io.Writer, packet []byte) error {
	for len(packet) > 0 {
		n, e := writer.Write(packet)
		if e != nil {
			return e
		}
		if n <= 0 {
			return io.ErrShortWrite
		}
		packet = packet[n:]
	}
	return nil
}
