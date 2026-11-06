//go:build linux && static_stability_integration

package training

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"
	"time"

	agentwire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	wire "github.com/Passion-Flow/ownsolid/api/training/v1"
	"github.com/Passion-Flow/ownsolid/services/agent/internal/inference"
	"github.com/Passion-Flow/ownsolid/services/agent/internal/settings"
)

// worker counts into a file on the host until told to stop, so a test can see the process
// itself is alive, not only what Docker reports.
const wireLeaseSeconds = agentwire.TaskLeaseSeconds

const worker = `
import os, time
n = 0
while not os.path.exists("/proof/exit"):
    n += 1
    with open("/proof/ticks.tmp", "w") as f:
        f.write(str(n))
    os.replace("/proof/ticks.tmp", "/proof/ticks")
    time.sleep(0.2)
`

// liveEngine runs a real process in a real Docker container. It stands in for the training
// image and the GPU only: the process has no device and runs python from a plain image, so
// inspection overlays the product's expected configuration before the ownership check. The
// container's identity and state are Docker's own. This is lifecycle evidence, not training.
type liveEngine struct {
	*Docker
	config   Config
	image    string
	proof    string
	expected ContainerConfig
}

func (d *liveEngine) InspectImage(context.Context, Config) (Image, error) {
	return fixtureImage(d.config), nil
}
func (d *liveEngine) CheckEngine(context.Context) error { return nil }
func (d *liveEngine) Create(ctx context.Context, name string, expected ContainerConfig) (string, error) {
	d.expected = expected
	host := HostConfig{HostConfig: inference.HostConfig{NetworkMode: "none", Mounts: []inference.BindMount{{Type: "bind", Source: d.proof, Target: "/proof"}}}}
	host.RestartPolicy.Name = "no"
	return d.Docker.Create(ctx, name, ContainerConfig{Image: d.image, User: "0:0", Entrypoint: []string{"python", "-I", "-B", "-c", worker}, Labels: expected.Labels, HostConfig: host})
}
func (d *liveEngine) InspectContainer(ctx context.Context, reference string) (Container, error) {
	real, e := d.Docker.InspectContainer(ctx, reference)
	if e != nil {
		return real, e
	}
	expected := d.expected
	if expected.Image == "" {
		return real, errors.New("no expected configuration")
	}
	out := containerFrom(expected, fixtureImage(d.config), real.Name[1:])
	out.ID = real.ID
	out.State = real.State
	return out, nil
}

func (d *liveEngine) running(t *testing.T) bool {
	t.Helper()
	real, e := d.Docker.InspectContainer(t.Context(), wire.ContainerName(fixtureID))
	return e == nil && real.State.Running
}

// ticks reads the worker's counter, or zero before its first write.
func ticks(proof string) int {
	raw, e := os.ReadFile(filepath.Join(proof, "ticks"))
	if e != nil {
		return 0
	}
	n, _ := strconv.Atoi(string(bytes.TrimSpace(raw)))
	return n
}

func waitTicksAbove(t *testing.T, proof string, n int, why ...func() string) int {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if got := ticks(proof); got > n {
			return got
		}
		time.Sleep(100 * time.Millisecond)
	}
	reason := ""
	for _, f := range why {
		reason = f()
	}
	t.Fatalf("the worker stopped counting at %d; %s", n, reason)
	return 0
}

func liveManager(t *testing.T) (*Manager, *liveEngine, *transferFixture, string, func(Manager) *Manager, agentwire.Lease) {
	t.Helper()
	m, lease, _, transport := preparedManager(t)
	// The test server's disk is nearly full; the attempt writes only a counter.
	m.Config.Runtime.MinimumFreeBytes = 64 << 20
	lease.Training.Recipe.Resources.MaximumOutputBytes = 64 << 20
	policy := settings.Default()
	proof := t.TempDir()
	if e := os.Chmod(proof, 0777); e != nil {
		t.Fatal(e)
	}
	build := func(base Manager) *Manager {
		docker, e := NewDocker(os.Getenv("OWNSOLID_STABILITY_DOCKER_SOCKET"), policy)
		if e != nil {
			t.Fatal(e)
		}
		t.Cleanup(docker.Close)
		engine := &liveEngine{Docker: docker, config: base.Config, image: os.Getenv("OWNSOLID_STABILITY_IMAGE"), proof: proof}
		if previous, ok := base.Lifecycle.Engine.(*liveEngine); ok {
			engine.expected = previous.expected
		}
		base.Lifecycle.Engine = engine
		return &base
	}
	live := build(*m)
	engine := live.Lifecycle.Engine.(*liveEngine)
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), time.Minute)
		defer stop()
		current := live.Lifecycle.Engine.(*liveEngine)
		if value, e := current.Docker.InspectContainer(cleanup, wire.ContainerName(fixtureID)); e == nil {
			current.Docker.Kill(cleanup, value.ID)
			current.Docker.Remove(cleanup, value.ID)
		}
	})
	return live, engine, transport, proof, build, lease
}

// TestARealContainerOutlivesAnOutageAndARestart holds control away for longer than the task
// lease, restarts the agent in that time, and then lets control return.
func TestARealContainerOutlivesAnOutageAndARestart(t *testing.T) {
	m, engine, transport, proof, rebuild, lease := liveManager(t)
	outage := make(chan struct{})
	transport.interrupt = func(string) error {
		select {
		case <-outage:
			return nil
		default:
			return connectionFailed
		}
	}
	first, shutdown := context.WithCancel(t.Context())
	defer shutdown()
	type ended struct {
		receipt wire.StopReceipt
		outcome []byte
		e       error
	}
	finished := make(chan ended, 1)
	go func() {
		receipt, outcome, e := m.Execute(first, fixtureID, lease, transport)
		finished <- ended{receipt, outcome, e}
	}()
	seen := waitTicksAbove(t, proof, 0, func() string {
		select {
		case end := <-finished:
			finished <- end
			return fmt.Sprintf("the attempt ended: %v, %+v", end.e, end.receipt)
		default:
			return "the attempt still runs"
		}
	})
	writeEvents(t, m, lease, 2)

	// Longer than the lease: this ended the attempt before ADR 0032.
	time.Sleep(time.Duration(wireLeaseSeconds+5) * time.Second)
	seen = waitTicksAbove(t, proof, seen)
	if !engine.running(t) || len(transport.events) != 0 {
		t.Fatalf("running %v, events %v", engine.running(t), transport.events)
	}

	// The agent stops while control is still away.
	shutdown()
	end := <-finished
	if !errors.Is(end.e, context.Canceled) || end.receipt.ReceiptID != "" {
		t.Fatalf("shutdown: %v, %+v", end.e, end.receipt)
	}
	seen = waitTicksAbove(t, proof, seen)
	time.Sleep(2 * time.Second)
	seen = waitTicksAbove(t, proof, seen)
	if !engine.running(t) {
		t.Fatal("the agent's shutdown stopped the container")
	}

	// A new process, with new connections, adopts it, still with control away.
	next := rebuild(*m)
	adopted, wall, ok, e := next.Resumable(fixtureID)
	if e != nil || !ok || adopted.LeaseToken != lease.LeaseToken || wall <= 0 {
		t.Fatalf("adoption: %v, %v, %v", ok, wall, e)
	}
	second, stop := context.WithTimeout(t.Context(), 2*time.Minute)
	defer stop()
	go func() {
		receipt, outcome, e := next.Resume(second, transport)
		finished <- ended{receipt, outcome, e}
	}()
	time.Sleep(3 * time.Second)
	seen = waitTicksAbove(t, proof, seen)

	// Control returns, the worker finishes, and everything written meanwhile arrives.
	close(outage)
	writeFailedResult(t, m, lease)
	if e = os.WriteFile(filepath.Join(proof, "exit"), nil, 0600); e != nil {
		t.Fatal(e)
	}
	end = <-finished
	if end.e != nil || end.receipt.Proof != "owned_container_removed" || end.receipt.AgentErrorCode != "" || len(end.outcome) == 0 {
		t.Fatalf("after the outage: %v, %+v", end.e, end.receipt)
	}
	if !reflect.DeepEqual(transport.events, []int64{1, 2}) {
		t.Fatalf("events %v", transport.events)
	}
	if _, e = engine.Docker.InspectContainer(t.Context(), wire.ContainerName(fixtureID)); !notFound(e) {
		t.Fatalf("the container was not removed: %v", e)
	}
	raw, _ := json.Marshal(end.receipt)
	t.Logf("worker counted to %d through the outage and the restart; receipt %s", seen, raw)
}

// TestARealContainerStopsWhenControlRefusesIt is the other half: an answer from control ends
// the same work at once.
func TestARealContainerStopsWhenControlRefusesIt(t *testing.T) {
	m, engine, transport, proof, _, lease := liveManager(t)
	ctx, revoke := context.WithCancelCause(t.Context())
	defer revoke(nil)
	finished := make(chan error, 1)
	var receipt wire.StopReceipt
	go func() {
		var e error
		receipt, _, e = m.Execute(ctx, fixtureID, lease, transport)
		finished <- e
	}()
	waitTicksAbove(t, proof, 0, func() string {
		select {
		case e := <-finished:
			finished <- e
			return fmt.Sprintf("the attempt ended: %v", e)
		default:
			return "the attempt still runs"
		}
	})
	if !engine.running(t) {
		t.Fatal("the worker is not running")
	}
	revoke(errors.New("AGENT_LEASE_STALE"))
	if e := <-finished; !errors.Is(e, context.Canceled) || receipt.Proof != "owned_container_removed" || receipt.AgentErrorCode != "TRAINING_AGENT_INTERRUPTED" {
		t.Fatalf("refusal: %v, %+v", e, receipt)
	}
	if _, e := engine.Docker.InspectContainer(t.Context(), wire.ContainerName(fixtureID)); !notFound(e) {
		t.Fatalf("the container was not removed: %v", e)
	}
}
