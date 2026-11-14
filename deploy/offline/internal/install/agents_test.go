package install

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/pkg/operatorsocket"
	"go.yaml.in/yaml/v3"
)

// hostControl answers on the socket as control does.
type hostControl struct {
	status operatorsocket.ShedStatus
	last   operatorsocket.ShedRequest
}

func (h *hostControl) LocalShed() operatorsocket.ShedStatus { return h.status }

func (h *hostControl) SetLocalShed(_ context.Context, r operatorsocket.ShedRequest) (operatorsocket.ShedStatus, error) {
	if r.Level == "everything" {
		return operatorsocket.ShedStatus{}, errors.New("VALIDATION_FAILED")
	}
	h.last = r
	h.status.Level = r.Level
	return h.status, nil
}

// socketInstall is a configuration whose socket address fits a Unix socket on every system.
func socketInstall(t *testing.T, control operatorsocket.Control) Config {
	t.Helper()
	c := Config{InstallRoot: shortTempDir(t)}
	if control == nil {
		return c
	}
	if e := os.Mkdir(c.Layout().Socket, 0o700); e != nil {
		t.Fatal(e)
	}
	server, e := operatorsocket.Listen(c.SocketPath(), control)
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan error, 1)
	go func() { finished <- server.Serve(ctx) }()
	t.Cleanup(func() {
		cancel()
		if e := <-finished; e != nil {
			t.Error(e)
		}
	})
	return c
}

func TestShedAgentsSaysWhetherTheLevelWasStored(t *testing.T) {
	control := &hostControl{status: operatorsocket.ShedStatus{Level: "none", Stored: true}}
	c := socketInstall(t, control)
	var out bytes.Buffer
	if e := ShedAgents(context.Background(), c, "telemetry", "Fleet overload", &out); e != nil {
		t.Fatal(e)
	}
	if out.String() != "AGENT_SHED_LEVEL telemetry\n" || control.last.Reason != "Fleet overload" {
		t.Fatal(out.String(), control.last)
	}
	control.status.Stored = false
	out.Reset()
	if e := ShedAgents(context.Background(), c, "heartbeats", "Database down", &out); e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(out.String(), "AGENT_SHED_LEVEL heartbeats\nAGENT_SHED_NOT_STORED") {
		t.Fatal(out.String())
	}
	out.Reset()
	if e := AgentStatus(context.Background(), c, &out); e != nil || !strings.HasPrefix(out.String(), "AGENT_SHED_LEVEL heartbeats\n") {
		t.Fatal(out.String(), e)
	}
}

func TestShedAgentsExplainsWhatTheOperatorCanFix(t *testing.T) {
	c := socketInstall(t, &hostControl{status: operatorsocket.ShedStatus{Level: "none", Stored: true}})
	var out bytes.Buffer
	if e := ShedAgents(context.Background(), c, "everything", "Drill", &out); e == nil || !strings.Contains(e.Error(), "none, bulk, telemetry or heartbeats") {
		t.Fatal(e)
	}
	stopped := socketInstall(t, nil)
	out.Reset()
	e := AgentStatus(context.Background(), stopped, &out)
	if e == nil || !strings.HasPrefix(e.Error(), operatorsocket.ReasonUnavailable) || !strings.Contains(e.Error(), stopped.SocketPath()) {
		t.Fatal(e)
	}
	if out.Len() != 0 {
		t.Fatal("a failure printed a level:", out.String())
	}
}

func TestWithControlDownTheLevelWaitsOnTheHost(t *testing.T) {
	c := socketInstall(t, nil)
	if e := os.Mkdir(c.Layout().Socket, 0o700); e != nil {
		t.Fatal(e)
	}
	var out bytes.Buffer
	if e := ShedAgents(context.Background(), c, "heartbeats", "Database stopped", &out); e != nil {
		t.Fatal(e)
	}
	if !strings.HasPrefix(out.String(), "AGENT_SHED_HELD heartbeats") {
		t.Fatal(out.String())
	}
	held := operatorsocket.HeldPath(c.SocketPath())
	got, found, e := operatorsocket.ReadHeld(held)
	if !found || e != nil || got.Reason != "Database stopped" {
		t.Fatal(got, found, e)
	}
	if st, e := os.Stat(held); e != nil || st.Mode().Perm() != 0o600 {
		t.Fatal(st, e)
	}
	out.Reset()
	if e = AgentStatus(context.Background(), c, &out); e != nil || !strings.HasPrefix(out.String(), "AGENT_SHED_HELD heartbeats") {
		t.Fatal(out.String(), e)
	}
	if e = ShedAgents(context.Background(), c, "everything", "Drill", &out); e == nil || !strings.HasPrefix(e.Error(), "VALIDATION_FAILED") {
		t.Fatal("a level control would refuse was held:", e)
	}
}

func TestTheSocketReachesOnlyTheServingContainerThroughItsOwnDirectory(t *testing.T) {
	c := validConfig(t)
	if e := Materialize(c); e != nil {
		t.Fatal(e)
	}
	if st, e := os.Stat(c.Layout().Socket); e != nil || st.Mode().Perm() != 0o700 {
		t.Fatal(st, e)
	}
	raw, e := os.ReadFile(filepath.Join(c.Layout().Config, "control.yaml"))
	if e != nil {
		t.Fatal(e)
	}
	var document struct {
		Server struct {
			OperatorSocket string `yaml:"operator_socket"`
		} `yaml:"server"`
	}
	if e = yaml.Unmarshal(raw, &document); e != nil {
		t.Fatal(e)
	}
	if document.Server.OperatorSocket != "/run/projectone-socket/control.sock" {
		t.Fatal(document.Server.OperatorSocket)
	}
	operator, e := os.ReadFile(filepath.Join(c.Layout().Operator, "operator.yaml"))
	if e != nil || strings.Contains(string(operator), "operator_socket") {
		t.Fatal("the operator container, which never mounts the socket, was told to serve one:", e)
	}
	if c.ComposeVariables()["OWNSOLID_SOCKET_DIR"] != c.Layout().Socket {
		t.Fatal(c.ComposeVariables()["OWNSOLID_SOCKET_DIR"])
	}
	if c.Layout().Socket == c.Layout().Operator {
		t.Fatal("the serving container would receive the privileged identity")
	}
}

func TestAnInstallRootTooLongForTheSocketIsRefusedAtInstall(t *testing.T) {
	c := validConfig(t)
	c.InstallRoot = "/" + strings.Repeat("a", maximumSocketPath)
	if e := c.Validate(); e == nil || !strings.HasPrefix(e.Error(), "INSTALL_ROOT_TOO_LONG") {
		t.Fatal(e)
	}
}
