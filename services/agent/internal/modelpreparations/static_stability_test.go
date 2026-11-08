package modelpreparations

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	agentwire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	datawire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	wire "github.com/Passion-Flow/ownsolid/api/model-lifecycle/v1"
	"github.com/Passion-Flow/ownsolid/services/agent/internal/client"
)

var connectionFailed = errors.New("CONTROL_CONNECTION_FAILED")

func failTimes(call string, n int, e error) func(string) error {
	return func(name string) error {
		if name == call && n > 0 {
			n--
			return e
		}
		return nil
	}
}

func writeEvents(t *testing.T, m *Manager, lease agentwire.Lease, sequences int) {
	t.Helper()
	body := []byte{}
	for i := 1; i <= sequences; i++ {
		raw, _ := json.Marshal(map[string]any{"sequence": i, "preparation_id": fixtureID, "attempt_id": fixtureID, "kind": "phase", "phase": "evaluating_base"})
		body = append(append(body, raw...), '\n')
	}
	if e := os.WriteFile(filepath.Join(attemptPath(m.Config, Journal{Command: *lease.Preparation}), "events.jsonl"), body, 0600); e != nil {
		t.Fatal(e)
	}
}

func writeFailedResult(t *testing.T, m *Manager, lease agentwire.Lease) {
	t.Helper()
	outcome, _ := json.Marshal(map[string]any{"schema_version": 1, "worker_version": wire.WorkerVersion, "status": "failed", "preparation_id": fixtureID, "attempt_id": fixtureID, "error_code": "MODEL_PREPARATION_PROCESSOR_TIMEOUT"})
	if e := os.WriteFile(filepath.Join(attemptPath(m.Config, Journal{Command: *lease.Preparation}), "output/result.json"), outcome, 0600); e != nil {
		t.Fatal(e)
	}
}

func exited(c *Container) {
	c.State.Running = false
	c.State.Status = "exited"
	c.State.ExitCode = 1
}

func stopCalled(d *faultEngine) bool {
	for _, call := range d.calls {
		if call == "stop" || call == "kill" || call == "remove" {
			return true
		}
	}
	return false
}

func TestInterruptedTransfersWaitForControlAndLoseNothing(t *testing.T) {
	for _, interruption := range []error{connectionFailed, errors.New("CONTROL_RESPONSE_REJECTED"), &client.Problem{Code: "STORAGE_UNAVAILABLE", Status: 503}, &client.Problem{Code: "AGENT_OVERLOADED", Status: 429, RetryAfter: time.Second}} {
		t.Run(interruption.Error(), func(t *testing.T) {
			m, lease, d, transport := preparedManager(t)
			d.afterStart = func(c *Container) {
				writeEvents(t, m, lease, 1)
				writeFailedResult(t, m, lease)
				exited(c)
			}
			transport.interrupt = failTimes("events", 2, interruption)
			receipt, outcome, e := m.Execute(t.Context(), fixtureID, lease, transport)
			if e != nil || receipt.AgentErrorCode != "" || len(outcome) == 0 {
				t.Fatalf("the outage ended the attempt: %v, %q", e, receipt.AgentErrorCode)
			}
			if !reflect.DeepEqual(transport.events, []int64{1}) {
				t.Fatalf("events after the outage %v", transport.events)
			}
		})
	}
}

func TestARefusedTransferEndsTheAttempt(t *testing.T) {
	m, lease, d, transport := preparedManager(t)
	d.afterStart = func(*Container) { writeEvents(t, m, lease, 1) }
	transport.interrupt = failTimes("events", 1, &client.Problem{Code: "MODEL_PREPARATION_EVENT_ORDER_CONFLICT", Status: 409})
	receipt, _, e := m.Execute(t.Context(), fixtureID, lease, transport)
	if e == nil || e.Error() != "MODEL_PREPARATION_EVENT_TRANSFER_FAILED" || receipt.Proof != "owned_container_removed" || d.container != nil {
		t.Fatalf("refusal %v, proof %q", e, receipt.Proof)
	}
}

func TestShutdownLeavesTheContainerAndTheNextProcessAdoptsIt(t *testing.T) {
	m, lease, d, transport := preparedManager(t)
	ctx, shutdown := context.WithCancel(t.Context())
	defer shutdown()
	// The test needs a started container even when the host is slow to prepare it.
	d.afterStart = func(*Container) { shutdown() }
	receipt, _, e := m.Execute(ctx, fixtureID, lease, transport)
	if !errors.Is(e, context.Canceled) || receipt.ReceiptID != "" {
		t.Fatalf("shutdown returned %v with receipt %q", e, receipt.ReceiptID)
	}
	if stopCalled(d) || d.container == nil || !d.container.State.Running {
		t.Fatalf("shutdown touched the container: %v", d.calls)
	}
	adopted, wall, ok, e := m.Resumable(fixtureID)
	if e != nil || !ok {
		t.Fatalf("attempt not resumable: %v", e)
	}
	if adopted.LeaseToken != lease.LeaseToken || adopted.TaskID != lease.TaskID || adopted.Preparation == nil || wall <= 0 || wall > time.Duration(lease.Preparation.Recipe.Resources.WallSeconds)*time.Second {
		t.Fatalf("adopted %+v, wall %v", adopted, wall)
	}
	writeEvents(t, m, lease, 1)
	writeFailedResult(t, m, lease)
	exited(d.container)
	receipt, outcome, e := m.Resume(t.Context(), transport)
	if e != nil || receipt.AgentErrorCode != "" || receipt.Proof != "owned_container_removed" || len(outcome) == 0 {
		t.Fatalf("adopted attempt ended with %v, %+v", e, receipt)
	}
	if !reflect.DeepEqual(transport.events, []int64{1}) || d.container != nil {
		t.Fatalf("events %v, container left: %v", transport.events, d.container != nil)
	}
}

func TestShutdownDuringInputVerificationReturnsCancellation(t *testing.T) {
	m, lease, d, transport := preparedManager(t)
	ctx, shutdown := context.WithCancel(t.Context())
	defer shutdown()
	transport.downloaded = func(asset datawire.Asset) {
		if asset.Path == lease.Preparation.Inputs.Heldout.Path {
			shutdown()
		}
	}
	receipt, _, e := m.Execute(ctx, fixtureID, lease, transport)
	if !errors.Is(e, context.Canceled) || receipt.Proof != "never_created" ||
		receipt.AgentErrorCode != "MODEL_PREPARATION_AGENT_INTERRUPTED" || d.container != nil {
		t.Fatalf("pre-start shutdown returned %v with proof %q and code %q", e, receipt.Proof, receipt.AgentErrorCode)
	}
}

func TestOnlyAStartedAttemptIsResumable(t *testing.T) {
	m, lease, _, transport := preparedManager(t)
	if _, _, ok, e := m.Resumable(fixtureID); ok || e != nil {
		t.Fatalf("no journal: %v, %v", ok, e)
	}
	if _, e := m.Begin(fixtureID, lease); e != nil {
		t.Fatal(e)
	}
	if _, _, ok, e := m.Resumable(fixtureID); ok || e != nil {
		t.Fatalf("attempt never started: %v, %v", ok, e)
	}
	ctx, shutdown := context.WithCancel(t.Context())
	shutdown()
	receipt, _, e := m.Execute(ctx, fixtureID, lease, transport)
	if e == nil || receipt.Proof != "never_created" {
		t.Fatalf("shutdown before start: %v, %q", e, receipt.Proof)
	}
}

func TestTheWallLimitEndsAnAttemptDuringAnOutage(t *testing.T) {
	m, lease, d, transport := preparedManager(t)
	d.afterStart = func(*Container) { writeEvents(t, m, lease, 1) }
	transport.interrupt = func(string) error { return connectionFailed }
	ctx, stop := context.WithTimeout(t.Context(), 300*time.Millisecond)
	defer stop()
	receipt, _, e := m.Execute(ctx, fixtureID, lease, transport)
	if !errors.Is(e, context.DeadlineExceeded) || receipt.AgentErrorCode != "MODEL_PREPARATION_PROCESSOR_TIMEOUT" || d.container != nil {
		t.Fatalf("wall limit: %v, %+v", e, receipt)
	}
}

func TestARevokedAttemptStopsItsContainer(t *testing.T) {
	m, lease, d, transport := preparedManager(t)
	ctx, revoke := context.WithCancelCause(t.Context())
	// Revoke once the container exists: a timer could fire before it starts on a busy
	// machine, and then there is no container to remove.
	d.afterStart = func(*Container) { revoke(errors.New("AGENT_LEASE_STALE")) }
	receipt, _, e := m.Execute(ctx, fixtureID, lease, transport)
	if !errors.Is(e, context.Canceled) || receipt.Proof != "owned_container_removed" || receipt.AgentErrorCode != "MODEL_PREPARATION_AGENT_INTERRUPTED" || d.container != nil {
		t.Fatalf("revocation: %v, %+v", e, receipt)
	}
}
