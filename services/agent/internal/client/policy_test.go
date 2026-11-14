package client

import (
	"net/http"
	"github.com/Passion-Flow/ownsolid/services/agent/internal/settings"
	"testing"
	"time"
)

func TestClientTransportUsesConfiguredLimits(t *testing.T) {
	p := settings.Default()
	p.ControlRequestSeconds = 8
	p.ControlHeaderSeconds = 6
	p.ControlConnections = 3
	p.IdleConnectionSeconds = 20
	c, err := New("https://control.example.invalid", "", p)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	tr := c.HTTP.Transport.(*http.Transport)
	if c.HTTP.Timeout != 8*time.Second || tr.ResponseHeaderTimeout != 6*time.Second || tr.MaxConnsPerHost != 3 || tr.IdleConnTimeout != 20*time.Second {
		t.Fatal("transport ignored policy")
	}
	p.ControlConnections = 0
	if _, err = New("https://control.example.invalid", "", p); err == nil {
		t.Fatal("invalid transport accepted")
	}
}
