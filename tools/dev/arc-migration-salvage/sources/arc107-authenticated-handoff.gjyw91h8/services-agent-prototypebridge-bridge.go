package prototypebridge

import (
	"github.com/Passion-Flow/ownsolid/services/agent/internal/client"
	"github.com/Passion-Flow/ownsolid/services/agent/internal/settings"
)

type Client = client.Client
type Session = client.Session

func New(origin, ca string) (*Client, error) {
	p := settings.Default()
	p.ControlRequestSeconds = 1
	p.ControlHeaderSeconds = 1
	p.TLSHandshakeSeconds = 1
	return client.New(origin, ca, p)
}
