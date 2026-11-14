package prototypebridge

import (
	"crypto/tls"
	"github.com/Passion-Flow/ownsolid/tools/dev/simulator/internal/link"
	"time"
)

type Dialer = link.Dialer
type Node = link.Node
type Stream = link.Stream

func New(origin, ca, certificate, key string) (*Dialer, error) {
	d, err := link.NewDialer(origin, ca, "", 2, time.Second)
	if err != nil {
		return nil, err
	}
	if certificate == "" {
		return d, nil
	}
	pair, err := tls.X509KeyPair([]byte(certificate), []byte(key))
	if err != nil {
		d.Close()
		return nil, err
	}
	return d.ForAgent(pair), nil
}
