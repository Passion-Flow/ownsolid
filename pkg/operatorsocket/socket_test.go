package operatorsocket

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeControl remembers the last request and blocks on hold while it is set.
type fakeControl struct {
	mu      sync.Mutex
	status  ShedStatus
	refusal error
	hold    chan struct{}
}

func (f *fakeControl) LocalShed() ShedStatus {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.status
}

func (f *fakeControl) SetLocalShed(ctx context.Context, r ShedRequest) (ShedStatus, error) {
	if f.hold != nil {
		select {
		case <-f.hold:
		case <-ctx.Done():
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.refusal != nil {
		return ShedStatus{}, f.refusal
	}
	f.status = ShedStatus{Level: r.Level, Stored: ctx.Err() == nil}
	return f.status, nil
}

// socketPath is short enough for a Unix socket address, which t.TempDir is not on every system.
func socketPath(t *testing.T) string {
	t.Helper()
	directory, e := os.MkdirTemp("/tmp", "ownsolid-socket-")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	return filepath.Join(directory, "control.sock")
}

func serve(t *testing.T, path string, control Control) {
	t.Helper()
	server, e := Listen(path, control)
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan error, 1)
	go func() { finished <- server.Serve(ctx) }()
	t.Cleanup(func() {
		cancel()
		if e := <-finished; e != nil {
			t.Errorf("serve ended with %v", e)
		}
	})
}

func TestAnOperatorSetsAndReadsTheLevelOverTheSocket(t *testing.T) {
	path := socketPath(t)
	serve(t, path, &fakeControl{status: ShedStatus{Level: "none", Stored: true}})
	ctx := context.Background()
	status, e := SetShed(ctx, path, ShedRequest{Level: "heartbeats", Reason: "Database saturated"})
	if e != nil || status != (ShedStatus{Level: "heartbeats", Stored: true}) {
		t.Fatal(status, e)
	}
	status, e = CurrentShed(ctx, path)
	if e != nil || status.Level != "heartbeats" {
		t.Fatal(status, e)
	}
}

func TestOnlyTheOwnerCanReachTheSocket(t *testing.T) {
	path := socketPath(t)
	serve(t, path, &fakeControl{})
	info, e := os.Stat(path)
	if e != nil || info.Mode().Perm() != 0o600 {
		t.Fatal(info.Mode(), e)
	}
}

func TestListenRefusesADirectoryOthersCanEnter(t *testing.T) {
	path := socketPath(t)
	if e := os.Chmod(filepath.Dir(path), 0o750); e != nil {
		t.Fatal(e)
	}
	if _, e := Listen(path, &fakeControl{}); e == nil || e.Error() != ReasonDirectoryInvalid {
		t.Fatal(e)
	}
	if _, e := Listen(filepath.Join(path, "missing", "control.sock"), &fakeControl{}); e == nil || e.Error() != ReasonDirectoryInvalid {
		t.Fatal(e)
	}
}

func TestListenReplacesWhatAKilledProcessLeftAndNothingElse(t *testing.T) {
	path := socketPath(t)
	if e := os.Chmod(filepath.Dir(path), 0o700); e != nil {
		t.Fatal(e)
	}
	left, e := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if e != nil {
		t.Fatal(e)
	}
	left.SetUnlinkOnClose(false)
	if e = left.Close(); e != nil {
		t.Fatal(e)
	}
	serve(t, path, &fakeControl{status: ShedStatus{Level: "bulk", Stored: true}})
	if status, e := CurrentShed(context.Background(), path); e != nil || status.Level != "bulk" {
		t.Fatal(status, e)
	}

	file := socketPath(t)
	if e = os.WriteFile(file, []byte("not a socket"), 0o600); e != nil {
		t.Fatal(e)
	}
	if _, e = Listen(file, &fakeControl{}); e == nil || e.Error() != ReasonListenFailed {
		t.Fatal(e)
	}
	if raw, e := os.ReadFile(file); e != nil || string(raw) != "not a socket" {
		t.Fatal("a refused start changed a file it does not own:", e)
	}
}

func TestStoppingLeavesTheNextProcessItsPath(t *testing.T) {
	path := socketPath(t)
	first, e := Listen(path, &fakeControl{})
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan error, 1)
	go func() { finished <- first.Serve(ctx) }()
	cancel()
	if e = <-finished; e != nil {
		t.Fatal(e)
	}
	if _, e = CurrentShed(context.Background(), path); e == nil || e.Error() != ReasonUnavailable {
		t.Fatal("a stopped control still answers:", e)
	}
	serve(t, path, &fakeControl{status: ShedStatus{Level: "none", Stored: true}})
	if _, e = CurrentShed(context.Background(), path); e != nil {
		t.Fatal(e)
	}
}

func TestARefusalKeepsItsCode(t *testing.T) {
	path := socketPath(t)
	serve(t, path, &fakeControl{refusal: errors.New("VALIDATION_FAILED")})
	if _, e := SetShed(context.Background(), path, ShedRequest{Level: "everything", Reason: "x"}); e == nil || e.Error() != "VALIDATION_FAILED" {
		t.Fatal(e)
	}
}

func TestNoControlIsReportedAsUnavailable(t *testing.T) {
	if _, e := CurrentShed(context.Background(), socketPath(t)); e == nil || e.Error() != ReasonUnavailable {
		t.Fatal(e)
	}
}

func TestAnOversizedRequestIsRefused(t *testing.T) {
	path := socketPath(t)
	serve(t, path, &fakeControl{status: ShedStatus{Level: "none", Stored: true}})
	_, e := SetShed(context.Background(), path, ShedRequest{Level: "bulk", Reason: strings.Repeat("x", maximumBodyBytes)})
	if e == nil || e.Error() != "VALIDATION_FAILED" {
		t.Fatal(e)
	}
}

func TestAStalledDatabaseCannotHoldTheReplyPastTheBudget(t *testing.T) {
	path := socketPath(t)
	serve(t, path, &fakeControl{status: ShedStatus{Level: "none"}, hold: make(chan struct{})})
	started := time.Now()
	status, e := SetShed(context.Background(), path, ShedRequest{Level: "bulk", Reason: "Stalled"})
	if e != nil || status.Stored {
		t.Fatal(status, e)
	}
	if waited := time.Since(started); waited < StoreBudget || waited > writeTimeout {
		t.Fatalf("the reply took %s", waited)
	}
}

func TestSomethingElseOnTheSocketIsNotBelieved(t *testing.T) {
	path := socketPath(t)
	if e := os.Chmod(filepath.Dir(path), 0o700); e != nil {
		t.Fatal(e)
	}
	listener, e := net.Listen("unix", path)
	if e != nil {
		t.Fatal(e)
	}
	server := &http.Server{ReadHeaderTimeout: time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })
	if _, e = CurrentShed(context.Background(), path); e == nil || e.Error() != ReasonInvalidReply {
		t.Fatal(e)
	}
}

func TestAHeldLevelSurvivesUntilItIsCleared(t *testing.T) {
	path := HeldPath(socketPath(t))
	if _, held, e := ReadHeld(path); held || e != nil {
		t.Fatal(held, e)
	}
	want := ShedRequest{Level: "heartbeats", Reason: "Database stopped"}
	if e := WriteHeld(path, want); e != nil {
		t.Fatal(e)
	}
	if info, e := os.Stat(path); e != nil || info.Mode().Perm() != 0o600 {
		t.Fatal(info, e)
	}
	if got, held, e := ReadHeld(path); !held || e != nil || got != want {
		t.Fatal(got, held, e)
	}
	entries, e := os.ReadDir(filepath.Dir(path))
	if e != nil || len(entries) != 1 {
		t.Fatal("a temporary file was left behind:", entries, e)
	}
}

func TestADamagedHeldLevelIsNeverActedOn(t *testing.T) {
	path := HeldPath(socketPath(t))
	for _, content := range []string{"not json", `{"level":"everything","reason":"x"}`, `{"level":"bulk","reason":""}`} {
		if e := os.WriteFile(path, []byte(content), 0o600); e != nil {
			t.Fatal(e)
		}
		if _, held, e := ReadHeld(path); held || e == nil {
			t.Fatal(content, held, e)
		}
	}
}

func TestValidMatchesWhatAnAuditRecordKeeps(t *testing.T) {
	for r, want := range map[ShedRequest]bool{
		{Level: "bulk", Reason: "Drill"}:                                      true,
		{Level: "bulk", Reason: strings.Repeat("\u6d4b", maximumReasonRunes)}: true,
		{Level: "bulk", Reason: strings.Repeat("x", maximumReasonRunes+1)}:    false,
		{Level: "bulk", Reason: "   "}:                                        false,
		{Level: "bulk", Reason: "a\x00b"}:                                     false,
		{Level: "everything", Reason: "Drill"}:                                false,
	} {
		if r.Valid() != want {
			t.Errorf("%q: want %v", r.Reason, want)
		}
	}
}
