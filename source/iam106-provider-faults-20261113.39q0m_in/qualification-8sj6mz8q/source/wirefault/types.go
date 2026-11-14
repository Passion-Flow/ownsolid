//go:build repository_integration

package commitfault

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
