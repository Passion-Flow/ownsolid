//go:build linux && docker_integration && egress_integration

package inference

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/nftables"
	"github.com/google/nftables/expr"
)

// The probe runs inside the containers: this test binary is mounted into them and started
// with -test.run=TestEgressProbe. It makes its connection attempts once, then serves the
// results on the container port, which the host reads through the published loopback port.
const (
	probeModeVariable    = "OWNSOLID_EGRESS_PROBE"
	probeTargetVariable  = "OWNSOLID_EGRESS_PROBE_TARGET"
	probeHostPortVar     = "OWNSOLID_EGRESS_PROBE_HOST_PORT"
	probeLookupVar       = "OWNSOLID_EGRESS_PROBE_LOOKUP"
	probeQueryName       = "egress-guard-probe.ownsolid.invalid"
	probeContainerPort   = "8000/tcp"
	probeAttemptDeadline = 5 * time.Second
	// promptRefusal is how fast a refused attempt must fail. A rejected attempt fails at
	// once; a dropped one would wait out the deadline.
	promptRefusal = 2 * time.Second
)

// probeAttempt is one connection attempt made from inside a container.
type probeAttempt struct {
	Connected bool          `json:"connected"`
	Error     string        `json:"error,omitempty"`
	Elapsed   time.Duration `json:"elapsed"`
}

// probeReport is everything one probe container tried. Only the guarded probe looks a
// name up: the control's lookup would really leave the host and fail the zero-egress run.
type probeReport struct {
	Target      probeAttempt  `json:"target"`
	HostService probeAttempt  `json:"host_service"`
	Lookup      *probeAttempt `json:"lookup,omitempty"`
}

func TestEgressProbe(t *testing.T) {
	if os.Getenv(probeModeVariable) != "serve" {
		t.Skip("runs only inside an egress probe container")
	}
	report := probeReport{
		Target:      dialAttempt(os.Getenv(probeTargetVariable)),
		HostService: dialAttempt(net.JoinHostPort(defaultGateway(t), os.Getenv(probeHostPortVar))),
	}
	if os.Getenv(probeLookupVar) == "1" {
		lookup := lookupAttempt(probeQueryName)
		report.Lookup = &lookup
	}
	body, _ := json.Marshal(report)
	server := &http.Server{ReadHeaderTimeout: time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(body)
	})}
	listener, e := net.Listen("tcp4", "0.0.0.0:8000")
	if e != nil {
		t.Fatal(e)
	}
	t.Fatal(server.Serve(listener))
}

func dialAttempt(address string) probeAttempt {
	started := time.Now()
	conn, e := net.DialTimeout("tcp4", address, probeAttemptDeadline)
	attempt := probeAttempt{Connected: e == nil, Elapsed: time.Since(started)}
	if e != nil {
		attempt.Error = e.Error()
	} else {
		conn.Close()
	}
	return attempt
}

func lookupAttempt(name string) probeAttempt {
	ctx, cancel := context.WithTimeout(context.Background(), probeAttemptDeadline)
	defer cancel()
	started := time.Now()
	_, e := (&net.Resolver{PreferGo: true}).LookupHost(ctx, name)
	attempt := probeAttempt{Elapsed: time.Since(started)}
	if e != nil {
		attempt.Error = e.Error()
	}
	// A lookup reached a resolver when it got an answer, including "no such host".
	attempt.Connected = e == nil || strings.Contains(attempt.Error, "no such host")
	return attempt
}

// defaultGateway reads the container's default gateway, the host's address on the bridge.
func defaultGateway(t *testing.T) string {
	raw, e := os.ReadFile("/proc/net/route")
	if e != nil {
		t.Fatal(e)
	}
	for _, line := range strings.Split(string(raw), "\n")[1:] {
		fields := strings.Fields(line)
		if len(fields) > 2 && fields[1] == "00000000" {
			value, e := strconv.ParseUint(fields[2], 16, 32)
			if e != nil {
				t.Fatal(e)
			}
			return net.IPv4(byte(value), byte(value>>8), byte(value>>16), byte(value>>24)).String()
		}
	}
	t.Fatal("no default route")
	return ""
}

// TestActualEgressGuardDeniesWorkloadNetworkAccess runs one probe on the owned, guarded
// network and one on an unguarded control bridge with the same options. The control
// proves that the attempts would succeed without the guard; the zero-egress harness
// reads its recorder log to prove the guarded attempts never left the host.
func TestActualEgressGuardDeniesWorkloadNetworkAccess(t *testing.T) {
	socket, image := os.Getenv("PROJECT_ONE_DOCKER_SOCKET"), os.Getenv("PROJECT_ONE_EGRESS_PROBE_IMAGE")
	if socket == "" || image == "" {
		t.Fatal("PROJECT_ONE_DOCKER_SOCKET and PROJECT_ONE_EGRESS_PROBE_IMAGE are required")
	}
	guardedTarget := environmentOr("PROJECT_ONE_EGRESS_GUARDED_TARGET", "192.0.2.20:9")
	controlTarget := environmentOr("PROJECT_ONE_EGRESS_CONTROL_TARGET", "192.0.2.10:9")
	binary, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	client, e := NewDocker(socket)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(client.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	node := randomNode(t)

	service, e := net.Listen("tcp4", "0.0.0.0:0")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { service.Close() })
	go func() {
		for {
			conn, e := service.Accept()
			if e != nil {
				return
			}
			conn.Close()
		}
	}()
	hostPort := strconv.Itoa(service.Addr().(*net.TCPAddr).Port)

	control := createControlNetwork(ctx, t, client, node)
	controlReport := runProbe(ctx, t, client, image, binary, control, node, false, controlTarget, hostPort)
	if !controlReport.HostService.Connected {
		t.Fatalf("control probe could not reach the host service, so the test proves nothing: %+v", controlReport)
	}

	network, e := client.EnsureNetwork(ctx, node)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		if e := client.RemoveUnusedNetwork(context.Background(), node, network); e != nil {
			t.Error(e)
		}
		if guardHolds(t, bridgeInterface(network)) {
			t.Error("guard rules outlived their network")
		}
	})
	report := runProbe(ctx, t, client, image, binary, network, node, true, guardedTarget, hostPort)
	if report.Lookup == nil {
		t.Fatal("guarded probe made no lookup")
	}
	for name, attempt := range map[string]probeAttempt{"routed egress": report.Target, "host service": report.HostService, "DNS lookup": *report.Lookup} {
		if attempt.Connected {
			t.Errorf("%s from the guarded container succeeded: %+v", name, attempt)
		}
		// The embedded resolver answers SERVFAIL only after its own upstream timeout, so
		// only connections must fail at once.
		if name != "DNS lookup" && attempt.Elapsed > promptRefusal {
			t.Errorf("%s was not refused promptly (%s): %s", name, attempt.Elapsed, attempt.Error)
		}
	}
	if !guardHolds(t, bridgeInterface(network)) {
		t.Fatal("guard rules missing while the network exists")
	}
	summary, _ := json.Marshal(map[string]any{"control": controlReport, "guarded": report, "guarded_target": guardedTarget, "control_target": controlTarget})
	t.Logf("egress probes: %s", summary)
}

// TestActualEgressGuardRepairsRemovedAndAlteredRules removes the guard table, then adds a
// rule the guard did not write, and expects the next endpoint check to restore the guard.
func TestActualEgressGuardRepairsRemovedAndAlteredRules(t *testing.T) {
	socket := os.Getenv("PROJECT_ONE_DOCKER_SOCKET")
	if socket == "" {
		t.Fatal("PROJECT_ONE_DOCKER_SOCKET is required")
	}
	client, e := NewDocker(socket)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(client.Close)
	ctx := context.Background()
	node := randomNode(t)
	network, e := client.EnsureNetwork(ctx, node)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		if e := client.RemoveUnusedNetwork(context.Background(), node, network); e != nil {
			t.Error(e)
		}
	})
	bridge := bridgeInterface(network)
	conn, e := nftables.New()
	if e != nil {
		t.Fatal(e)
	}
	table := &nftables.Table{Family: nftables.TableFamilyINet, Name: egressTable}
	conn.DelTable(table)
	if e = conn.Flush(); e != nil {
		t.Fatal(e)
	}
	if guardHolds(t, bridge) {
		t.Fatal("guard table still present after deletion")
	}
	if e = client.guard.Ensure(bridge); e != nil || !guardHolds(t, bridge) {
		t.Fatal("removed guard not restored", e)
	}
	chain := &nftables.Chain{Name: "input", Table: table}
	conn.InsertRule(&nftables.Rule{Table: table, Chain: chain, Exprs: append(matchInterface(expr.MetaKeyIIFNAME, bridge), &expr.Verdict{Kind: expr.VerdictAccept})})
	if e = conn.Flush(); e != nil {
		t.Fatal(e)
	}
	if guardHolds(t, bridge) {
		t.Fatal("altered guard reported sound")
	}
	if e = client.guard.Ensure(bridge); e != nil || !guardHolds(t, bridge) {
		t.Fatal("altered guard not rebuilt", e)
	}
	// The netlink library always writes table flags 0, so a dormant table needs the nft tool.
	t.Run("dormant table", func(t *testing.T) {
		tool, e := exec.LookPath("nft")
		if e != nil {
			t.Skip("nft is not installed on this host")
		}
		if output, e := exec.Command(tool, "add", "table", "inet", egressTable, "{ flags dormant; }").CombinedOutput(); e != nil {
			t.Fatal(e, string(output))
		}
		if guardHolds(t, bridge) {
			t.Fatal("dormant guard reported sound")
		}
		if e = client.VerifyNetworkEndpoint(ctx, node, network, strings.Repeat("0", 64)); e != nil || !guardHolds(t, bridge) {
			t.Fatal("dormant guard not rebuilt by the endpoint check", e)
		}
	})
}

func guardHolds(t *testing.T, bridge string) bool {
	t.Helper()
	conn, e := nftables.New()
	if e != nil {
		t.Fatal(e)
	}
	state, e := readGuard(conn)
	if e != nil {
		t.Fatal(e)
	}
	return state.protects(bridge)
}

func environmentOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func randomNode(t *testing.T) string {
	var random [16]byte
	if _, e := rand.Read(random[:]); e != nil {
		t.Fatal(e)
	}
	return fmt.Sprintf("%x-%x-%x-%x-%x", random[0:4], random[4:6], random[6:8], random[8:10], random[10:16])
}

// createControlNetwork creates an unguarded bridge with the owned network's options.
func createControlNetwork(ctx context.Context, t *testing.T, client *Docker, node string) string {
	t.Helper()
	input := map[string]any{"Name": "ownsolid-egress-control-" + node, "Driver": "bridge", "CheckDuplicate": true, "Options": networkOptions(), "Labels": map[string]string{"ownsolid.egress_fixture": node}}
	var created struct {
		ID string `json:"Id"`
	}
	if e := client.request(ctx, "POST", "/networks/create", input, &created, 201); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		if e := client.request(context.Background(), "DELETE", "/networks/"+created.ID, nil, nil, 204); e != nil {
			t.Error(e)
		}
	})
	return created.ID
}

// runProbe starts a probe container on network and returns its report, read through the
// container's published loopback port. A guarded probe gets the DNS settings of an owned
// workload container and also tries a lookup.
func runProbe(ctx context.Context, t *testing.T, client *Docker, image, binary, network, node string, guarded bool, target, hostPort string) probeReport {
	t.Helper()
	role, lookup, resolvers := "control", "0", []string(nil)
	if guarded {
		role, lookup, resolvers = "guarded", "1", workloadResolvers()
	}
	port := freeLoopbackPort(t)
	spec := ContainerConfig{Image: image, User: "65534:65534", WorkingDir: "/", Entrypoint: []string{"/egress-probe"}, Cmd: []string{"-test.run=^TestEgressProbe$"},
		Env:          []string{probeModeVariable + "=serve", probeTargetVariable + "=" + target, probeHostPortVar + "=" + hostPort, probeLookupVar + "=" + lookup},
		Labels:       map[string]string{"ownsolid.egress_fixture": node},
		ExposedPorts: map[string]struct{}{probeContainerPort: {}},
		HostConfig: HostConfig{NetworkMode: network, Dns: resolvers, ReadonlyRootfs: true, CapDrop: []string{"ALL"}, SecurityOpt: []string{"no-new-privileges:true"},
			Memory: 64 << 20, MemorySwap: 64 << 20, NanoCpus: 1e9, PidsLimit: 64,
			Mounts:       []BindMount{{Type: "bind", Source: binary, Target: "/egress-probe", ReadOnly: true}},
			PortBindings: map[string][]PortBinding{probeContainerPort: {{HostIP: "127.0.0.1", HostPort: port}}}}}
	var created struct {
		ID string `json:"Id"`
	}
	if e := client.request(ctx, "POST", "/containers/create?name=ownsolid-egress-"+role+"-"+node, spec, &created, 201); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		if value, e := client.InspectContainer(ctx, created.ID); e == nil && value.State.Running {
			client.StopContainer(ctx, created.ID)
		}
		if e := client.RemoveContainer(ctx, created.ID); e != nil {
			t.Error(e)
		}
	})
	if e := client.StartContainer(ctx, created.ID); e != nil {
		t.Fatal(e)
	}
	if guarded {
		if e := client.VerifyNetworkEndpoint(ctx, node, network, created.ID); e != nil {
			t.Fatal(e)
		}
	}
	deadline := time.Now().Add(45 * time.Second)
	for {
		report, e := fetchReport("http://127.0.0.1:" + port + "/")
		if e == nil {
			return report
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s probe report unreachable through the loopback port: %v", role, e)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func fetchReport(url string) (probeReport, error) {
	var report probeReport
	response, e := (&http.Client{Timeout: 2 * time.Second}).Get(url)
	if e != nil {
		return report, e
	}
	defer response.Body.Close()
	body, e := io.ReadAll(io.LimitReader(response.Body, 1<<16))
	if e != nil {
		return report, e
	}
	return report, json.Unmarshal(body, &report)
}

func freeLoopbackPort(t *testing.T) string {
	listener, e := net.Listen("tcp4", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer listener.Close()
	return strconv.Itoa(listener.Addr().(*net.TCPAddr).Port)
}
