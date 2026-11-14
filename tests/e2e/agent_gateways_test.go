package e2e

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The proof of ARC-102 (docs/research/agent-gateway-tier.md): agent gateways beside control
// serve the session streams of a simulated fleet, tasks reach agents through whichever
// gateway holds them, and agents leave a gateway that stops for the next one in their order.
const (
	gatewayAgents   = 500
	gatewaySeconds  = 1200
	gatewayPort     = 9443
	gatewayInterval = 10
	// gatewayMove is how long a fleet may take to reach another gateway: a few backed-off
	// attempts at the default reconnect ceiling.
	gatewayMove = 3 * time.Minute
	// gatewayDispatch bounds a task's way from the API to its agent and back, which a held
	// claim woken through the outbox makes much shorter than one interval.
	gatewayDispatch = 2 * time.Minute
)

// agentGateway is one gateway container: its name and the host address agents reach it on.
type agentGateway struct {
	name, address string
}

// startGateway runs a gateway from the control image with control's configuration and the
// agent_gateway section added, on control's networks, published on hostPort.
func (e environment) startGateway(t *testing.T, name string, hostPort int) agentGateway {
	t.Helper()
	return e.startGatewayWithAge(t, name, hostPort, 0)
}

func (e environment) startGatewayWithAge(t *testing.T, name string, hostPort, maximumAgeSeconds int) agentGateway {
	t.Helper()
	inspect, err := run(t, "docker", "inspect", "--format", "{{json .NetworkSettings.Networks}}|{{.Config.User}}", "ownsolid-control-1")
	if err != nil {
		t.Fatalf("inspect control: %v %s", err, inspect)
	}
	parts := strings.SplitN(strings.TrimSpace(inspect), "|", 2)
	var networks map[string]any
	if err = json.Unmarshal([]byte(parts[0]), &networks); err != nil || len(networks) != 2 {
		t.Fatalf("control networks %s: %v", parts[0], err)
	}
	var edge, state string
	for network := range networks {
		if strings.HasSuffix(network, "_state") {
			state = network
		} else {
			edge = network
		}
	}
	control, err := os.ReadFile(filepath.Join(e.root, "config", "control.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(t.TempDir(), name)
	if err = os.Mkdir(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	gateway := string(control) + fmt.Sprintf("agent_gateway:\n  listen_address: 0.0.0.0:%d\n  tls_certificate_file: /run/projectone/certificate.pem\n  tls_private_key_file: /run/projectone/private-key.pem\n", gatewayPort)
	if maximumAgeSeconds > 0 {
		gateway += fmt.Sprintf("  connection_max_age_seconds: %d\n", maximumAgeSeconds)
	}
	if err = os.WriteFile(filepath.Join(directory, "gateway.yaml"), []byte(gateway), 0o644); err != nil {
		t.Fatal(err)
	}
	if output, err := run(t, "docker", "create", "--name", name, "--user", parts[1], "--read-only", "--cap-drop", "ALL",
		"--security-opt", "no-new-privileges:true", "--network", edge, "--publish", fmt.Sprintf("127.0.0.1:%d:%d", hostPort, gatewayPort),
		"--volume", filepath.Join(e.root, "config")+":/run/projectone:ro", "--volume", directory+":/run/ownsolid-gateway:ro",
		e.controlImage, "serve-agent-gateway", "--config", "/run/ownsolid-gateway/gateway.yaml"); err != nil {
		t.Fatalf("create %s: %v %s", name, err, output)
	}
	t.Cleanup(func() { run(t, "docker", "rm", "--force", name) })
	if output, err := run(t, "docker", "network", "connect", state, name); err != nil {
		t.Fatalf("connect %s: %v %s", name, err, output)
	}
	if output, err := run(t, "docker", "start", name); err != nil {
		t.Fatalf("start %s: %v %s", name, err, output)
	}
	g := agentGateway{name: name, address: "127.0.0.1:" + strconv.Itoa(hostPort)}
	g.waitReady(t, e, time.Minute)
	return g
}

// waitReady waits until the gateway's readiness probe answers 200, and shows what the gateway
// said when it does not.
func (g agentGateway) waitReady(t *testing.T, e environment, within time.Duration) {
	t.Helper()
	started := time.Now()
	status := 0
	for status = e.gatewayReady(t, g); status != http.StatusOK; status = e.gatewayReady(t, g) {
		if time.Since(started) > within {
			state, _ := run(t, "docker", "inspect", "--format", "{{.State.Status}} exit={{.State.ExitCode}} restarts={{.RestartCount}}", g.name)
			logs, _ := run(t, "docker", "logs", "--tail", "40", g.name)
			t.Fatalf("%s not ready within %s: last status %d, %s\n%s", g.name, within, status, strings.TrimSpace(state), logs)
		}
		time.Sleep(time.Second)
	}
}

// gatewayReady asks a gateway's readiness probe for the agent origin, as an agent reaches it,
// and returns the status, or 0 when it does not answer.
func (e environment) gatewayReady(t *testing.T, g agentGateway) int {
	t.Helper()
	authority, err := os.ReadFile(filepath.Join(e.root, "config", "ca.pem"))
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(authority)
	dialer := &net.Dialer{Timeout: 2 * time.Second}
	client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12},
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, network, g.address)
		},
	}}
	defer client.CloseIdleConnections()
	response, err := client.Get(adminOrigin + "/health/ready")
	if err != nil {
		return 0
	}
	response.Body.Close()
	return response.StatusCode
}

// gatewayNodes reads the nodes whose sessions a gateway's log shows open now. The log keeps
// earlier runs of the container, so counting starts again where the gateway last started.
func gatewayNodes(t *testing.T, name string) map[string]bool {
	t.Helper()
	open := map[string]bool{}
	for node := range gatewayConnections(t, name).active {
		open[node] = true
	}
	return open
}

// fleetOnline waits until every agent of the fleet was heard after moment.
func (e environment) fleetOnline(t *testing.T, f *simulatedRun, name string, moment time.Time, within time.Duration) time.Duration {
	t.Helper()
	took := waitFor(t, name, within, func() bool { return e.heardSince(t, f, moment) == len(f.nodes) })
	t.Logf("%s: every agent heard %s after it started", name, took.Round(100*time.Millisecond))
	return took
}

func TestAgentGateways(t *testing.T) {
	if os.Getenv("OWNSOLID_E2E_LOAD") == "" {
		t.Skip("needs OWNSOLID_E2E_LOAD, and OWNSOLID_E2E_RUN=TestAgentGateways in run-single-node.sh")
	}
	e := loadEnvironment(t)
	// The serving role holds control's pool of 10 and each gateway's.
	config := e.configurationWith(t, t.TempDir(), "single-node", e.controlImage, fleetLimits+"resources: {database_role_connections: 30}\n")
	e.apply(t, config)
	admin, csrf := e.adminSession(t, e.password(t))
	e.installLicense(t, admin, csrf)
	e.setPacing(t, admin, csrf, gatewayInterval, 10, 1, 30, "Agent gateway drill")
	first, second := e.startGateway(t, "ownsolid-gateway-a", 19443), e.startGateway(t, "ownsolid-gateway-b", 19444)
	fleet := e.enrollFleet(t, admin, csrf, gatewayAgents, map[string]any{"duration_seconds": gatewaySeconds, "window_seconds": 60, "session_addresses": []string{first.address, second.address}})
	fleet.start(t, 2*time.Minute)

	t.Run("the fleet spreads over the gateways and none of it reaches control's own", func(t *testing.T) {
		e.fleetOnline(t, fleet, "the fleet on the gateways", fleet.started, gatewayMove)
		onFirst, onSecond := gatewayNodes(t, first.name), gatewayNodes(t, second.name)
		t.Logf("sessions: %s %d, %s %d", first.name, len(onFirst), second.name, len(onSecond))
		if len(onFirst)+len(onSecond) != gatewayAgents || len(onFirst) < gatewayAgents/3 || len(onSecond) < gatewayAgents/3 {
			t.Fatalf("sessions %d and %d for %d agents", len(onFirst), len(onSecond), gatewayAgents)
		}
		logs, err := e.compose(t, "logs", "--no-color", "control")
		if err != nil {
			t.Fatal(err)
		}
		for _, node := range fleet.nodes {
			if strings.Contains(logs, "agent_session_opened node_id="+node.NodeID) {
				t.Fatalf("node %s opened its session on control", node.NodeID)
			}
		}
	})

	t.Run("tasks written on control reach agents through the gateways", func(t *testing.T) {
		e.waitForTasks(t, "tasks through two gateways", createTasks(t, admin, csrf, fleet.targets()), gatewayDispatch)
	})

	t.Run("agents leave a gateway that is killed for the next in their order", func(t *testing.T) {
		moved := gatewayNodes(t, first.name)
		if output, err := run(t, "docker", "kill", first.name); err != nil {
			t.Fatalf("kill: %v %s", err, output)
		}
		killed := time.Now()
		waitFor(t, "every agent of the killed gateway on the other", gatewayMove, func() bool {
			on := gatewayNodes(t, second.name)
			for node := range moved {
				if !on[node] {
					return false
				}
			}
			return true
		})
		e.fleetOnline(t, fleet, "after the kill", killed, gatewayMove)
		e.waitForTasks(t, "tasks through the remaining gateway", createTasks(t, admin, csrf, fleet.targets()), gatewayDispatch)
		if output, err := run(t, "docker", "start", first.name); err != nil {
			t.Fatalf("restart: %v %s", err, output)
		}
		first.waitReady(t, e, time.Minute)
	})

	t.Run("a gateway that stops drains its sessions to the next one", func(t *testing.T) {
		moved := gatewayNodes(t, second.name)
		stopped := time.Now()
		if output, err := run(t, "docker", "stop", "--time", "30", second.name); err != nil {
			t.Fatalf("stop: %v %s", err, output)
		}
		if took := time.Since(stopped); took > 20*time.Second {
			t.Fatalf("the gateway took %s to stop: its sessions did not drain", took)
		}
		output, _ := run(t, "docker", "logs", second.name)
		if !strings.Contains(output, "agent_session_closed") {
			t.Fatal("no session was closed by the drain")
		}
		waitFor(t, "every drained agent on the other gateway", gatewayMove, func() bool {
			on := gatewayNodes(t, first.name)
			for node := range moved {
				if !on[node] {
					return false
				}
			}
			return true
		})
		e.fleetOnline(t, fleet, "after the drain", stopped, gatewayMove)
		if output, err := run(t, "docker", "start", second.name); err != nil {
			t.Fatalf("restart: %v %s", err, output)
		}
		second.waitReady(t, e, time.Minute)
	})

	t.Run("gateways stop serving while control is stopped and serve again when it returns", func(t *testing.T) {
		if output, err := e.compose(t, "stop", "control"); err != nil {
			t.Fatalf("stop control: %v %s", err, output)
		}
		waitFor(t, "both gateways to fence", 30*time.Second, func() bool {
			return e.gatewayReady(t, first) == http.StatusServiceUnavailable && e.gatewayReady(t, second) == http.StatusServiceUnavailable
		})
		fenced := time.Now()
		time.Sleep(3 * gatewayInterval * time.Second)
		if heard := e.heardSince(t, fleet, fenced.Add(gatewayInterval*time.Second)); heard != 0 {
			t.Fatalf("%d agents were served while control was stopped", heard)
		}
		if output, err := e.compose(t, "start", "control"); err != nil {
			t.Fatalf("start control: %v %s", err, output)
		}
		returned := time.Now()
		waitFor(t, "both gateways to serve again", 2*time.Minute, func() bool {
			return e.gatewayReady(t, first) == http.StatusOK && e.gatewayReady(t, second) == http.StatusOK
		})
		e.fleetOnline(t, fleet, "after control returned", returned, gatewayMove)
		e.waitForTasks(t, "tasks after control returned", createTasks(t, admin, csrf, fleet.targets()), gatewayDispatch)
		for _, g := range []agentGateway{first, second} {
			output, _ := run(t, "docker", "logs", g.name)
			if !strings.Contains(output, "agent_gateway_fenced") || !strings.Contains(output, "agent_gateway_serving") {
				t.Fatalf("%s logged no fence and return:\n%s", g.name, output)
			}
		}
	})

	fleet.stop(t, time.Minute)
	report := fleet.report(t)
	if report.Revoked != 0 || report.OnStream != gatewayAgents {
		t.Fatalf("%d agents revoked, %d of %d on a stream at the end", report.Revoked, report.OnStream, gatewayAgents)
	}
}
