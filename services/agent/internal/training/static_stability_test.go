package training

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"
	"time"

	agentwire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	datawire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	wire "github.com/Passion-Flow/ownsolid/api/training/v1"
	"github.com/Passion-Flow/ownsolid/services/agent/internal/client"
)

var connectionFailed = errors.New("CONTROL_CONNECTION_FAILED")

// failTimes fails the named call n times with e, then lets it through.
func failTimes(call string, n int, e error) func(string) error {
	return func(name string) error {
		if name == call && n > 0 {
			n--
			return e
		}
		return nil
	}
}

func attemptRoot(m *Manager, lease agentwire.Lease) string {
	return filepath.Join(m.Config.RootDirectory, "jobs", lease.Training.Scope.JobID, lease.Training.AttemptID)
}

func writeEvents(t *testing.T, m *Manager, lease agentwire.Lease, sequences int) {
	t.Helper()
	body := []byte{}
	for i := 1; i <= sequences; i++ {
		raw, _ := json.Marshal(map[string]any{"sequence": i, "job_id": fixtureID, "attempt_id": fixtureID, "global_step": 0, "kind": "phase", "phase": "loading_model"})
		body = append(append(body, raw...), '\n')
	}
	if e := os.WriteFile(filepath.Join(attemptRoot(m, lease), "events.jsonl"), body, 0600); e != nil {
		t.Fatal(e)
	}
}

func writeFailedResult(t *testing.T, m *Manager, lease agentwire.Lease) {
	t.Helper()
	outcome, _ := json.Marshal(map[string]any{"schema_version": 1, "worker_version": wire.WorkerVersion, "status": "failed", "job_id": fixtureID, "attempt_id": fixtureID, "error_code": "TRAINING_PROCESSOR_TIMEOUT"})
	if e := os.WriteFile(filepath.Join(attemptRoot(m, lease), "output/result.json"), outcome, 0600); e != nil {
		t.Fatal(e)
	}
}

func exited(c *Container) {
	c.State.Running = false
	c.State.Status = "exited"
	c.State.ExitCode = 1
}

// stopCalled reports whether the engine was asked to end a container.
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
				writeEvents(t, m, lease, 2)
				writeFailedResult(t, m, lease)
				exited(c)
			}
			transport.interrupt = failTimes("events", 2, interruption)
			receipt, outcome, e := m.Execute(t.Context(), fixtureID, lease, transport)
			if e != nil || receipt.AgentErrorCode != "" || len(outcome) == 0 {
				t.Fatalf("the outage ended the attempt: %v, %q", e, receipt.AgentErrorCode)
			}
			if !reflect.DeepEqual(transport.events, []int64{1, 2}) {
				t.Fatalf("events after the outage %v", transport.events)
			}
		})
	}
}

func TestARefusedTransferEndsTheAttempt(t *testing.T) {
	m, lease, d, transport := preparedManager(t)
	d.afterStart = func(*Container) { writeEvents(t, m, lease, 1) }
	transport.interrupt = failTimes("events", 1, &client.Problem{Code: "TRAINING_EVENT_ORDER_CONFLICT", Status: 409})
	receipt, _, e := m.Execute(t.Context(), fixtureID, lease, transport)
	if e == nil || e.Error() != "TRAINING_EVENT_TRANSFER_FAILED" || receipt.Proof != "owned_container_removed" || d.container != nil {
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
	if adopted.LeaseToken != lease.LeaseToken || adopted.TaskID != lease.TaskID || adopted.Training == nil || wall <= 0 || wall > time.Duration(lease.Training.Recipe.Resources.WallSeconds)*time.Second {
		t.Fatalf("adopted %+v, wall %v", adopted, wall)
	}

	// Interrupt the adopted process as it sends an event. The next process must
	// still be able to deliver that event and collect the result.
	writeEvents(t, m, lease, 1)
	resumeContext, interrupt := context.WithCancel(t.Context())
	defer interrupt()
	transport.interrupt = func(call string) error {
		if call == "events" {
			interrupt()
			return resumeContext.Err()
		}
		return nil
	}
	if receipt, _, e = m.Resume(resumeContext, transport); !errors.Is(e, context.Canceled) || receipt.ReceiptID != "" || stopCalled(d) {
		t.Fatalf("restart ended the attempt: %v, %v", e, d.calls)
	}
	transport.interrupt = nil

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
	if _, _, ok, e := m.Resumable("00000000-0000-4000-8000-000000000009"); ok || e == nil {
		t.Fatal("a journal of another node was accepted")
	}
	// A shutdown before the container started leaves nothing to adopt: it is closed.
	ctx, shutdown := context.WithCancel(t.Context())
	shutdown()
	receipt, _, e := m.Execute(ctx, fixtureID, lease, transport)
	if e == nil || receipt.Proof != "never_created" {
		t.Fatalf("shutdown before start: %v, %q", e, receipt.Proof)
	}
	if _, _, ok, e := m.Resumable(fixtureID); ok || e != nil {
		t.Fatalf("closed attempt: %v, %v", ok, e)
	}
}

func TestTheWallLimitEndsAnAttemptDuringAnOutage(t *testing.T) {
	m, lease, d, transport := preparedManager(t)
	d.afterStart = func(*Container) { writeEvents(t, m, lease, 1) }
	transport.interrupt = func(string) error { return connectionFailed }
	ctx, stop := context.WithTimeout(t.Context(), 300*time.Millisecond)
	defer stop()
	receipt, _, e := m.Execute(ctx, fixtureID, lease, transport)
	if !errors.Is(e, context.DeadlineExceeded) || receipt.AgentErrorCode != "TRAINING_PROCESSOR_TIMEOUT" || d.container != nil {
		t.Fatalf("wall limit: %v, %+v", e, receipt)
	}
}

func TestARevokedAttemptStopsItsContainer(t *testing.T) {
	m, lease, d, transport := preparedManager(t)
	ctx, revoke := context.WithCancelCause(t.Context())
	d.afterStart = func(*Container) { revoke(errors.New("AGENT_LEASE_STALE")) }
	receipt, _, e := m.Execute(ctx, fixtureID, lease, transport)
	if !errors.Is(e, context.Canceled) || receipt.Proof != "owned_container_removed" || receipt.AgentErrorCode != "TRAINING_AGENT_INTERRUPTED" || d.container != nil {
		t.Fatalf("revocation: %v, %+v", e, receipt)
	}
}

// checkpointFiles writes a real checkpoint directory and the event naming it.
func checkpointFiles(t *testing.T, m *Manager, lease agentwire.Lease, step int64) wire.Upload {
	t.Helper()
	root := attemptRoot(m, lease)
	directory := filepath.Join(root, "output/checkpoints", "checkpoint-"+strconv.FormatInt(step, 10))
	if e := os.MkdirAll(directory, 0700); e != nil {
		t.Fatal(e)
	}
	files := []datawire.Asset{}
	for _, name := range []string{"adapter_model.safetensors", "adapter_config.json", "optimizer.pt", "scheduler.pt", "trainer_state.json", "rng_state.pth"} {
		body := []byte("synthetic " + name)
		if e := os.WriteFile(filepath.Join(directory, name), body, 0600); e != nil {
			t.Fatal(e)
		}
		files = append(files, datawire.Asset{Path: name, SizeBytes: int64(len(body)), SHA256: digest(body)})
	}
	request, e := lease.Training.ContainerRequest()
	if e != nil {
		t.Fatal(e)
	}
	metadata, _ := json.Marshal(wire.CheckpointReceipt{SchemaVersion: 1, Bindings: wire.ExpectedBindings(request, agentwire.ApprovedModel().Repository), GlobalStep: step, Files: files})
	if e = os.WriteFile(filepath.Join(directory, "projectone-checkpoint.json"), metadata, 0600); e != nil {
		t.Fatal(e)
	}
	sum := digest(metadata)
	event, _ := json.Marshal(map[string]any{"sequence": 1, "job_id": fixtureID, "attempt_id": fixtureID, "global_step": step, "kind": "checkpoint", "path": "checkpoint-" + strconv.FormatInt(step, 10), "receipt_sha256": sum})
	if e = os.WriteFile(filepath.Join(root, "events.jsonl"), append(event, '\n'), 0600); e != nil {
		t.Fatal(e)
	}
	all := append(files, datawire.Asset{Path: "projectone-checkpoint.json", SizeBytes: int64(len(metadata)), SHA256: sum})
	return wire.Upload{ID: fixtureID, JobID: fixtureID, AttemptID: fixtureID, Kind: "checkpoint", GlobalStep: step, State: "uploading", Files: all}
}

func TestAnInterruptedCheckpointUploadRunsAgainAfterItsEvent(t *testing.T) {
	for _, call := range []string{"begin", "chunk", "finish"} {
		t.Run(call, func(t *testing.T) {
			m, lease, d, transport := preparedManager(t)
			d.afterStart = func(c *Container) {
				transport.upload = checkpointFiles(t, m, lease, 1)
				writeFailedResult(t, m, lease)
				exited(c)
			}
			finishes := 0
			inner := failTimes(call, 1, connectionFailed)
			transport.interrupt = func(name string) error {
				e := inner(name)
				if name == "finish" && e == nil {
					finishes++
				}
				return e
			}
			if _, _, e := m.Execute(t.Context(), fixtureID, lease, transport); e != nil {
				t.Fatal(e)
			}
			if finishes != 1 {
				t.Fatalf("checkpoint finished %d times", finishes)
			}
			// The event is sent again with the retry; control answers a duplicate.
			if len(transport.events) != 2 || transport.events[0] != 1 || transport.events[1] != 1 {
				t.Fatalf("events %v", transport.events)
			}
		})
	}
}

func TestAnUploadAlreadyAcceptedSendsNoChunk(t *testing.T) {
	m, lease, d, transport := preparedManager(t)
	d.afterStart = func(c *Container) {
		transport.upload = checkpointFiles(t, m, lease, 1)
		transport.upload.State = "accepted"
		writeFailedResult(t, m, lease)
		exited(c)
	}
	if _, _, e := m.Execute(t.Context(), fixtureID, lease, transport); e != nil {
		t.Fatal(e)
	}
	if transport.chunks != 0 {
		t.Fatalf("%d chunks sent for an accepted upload", transport.chunks)
	}
}
