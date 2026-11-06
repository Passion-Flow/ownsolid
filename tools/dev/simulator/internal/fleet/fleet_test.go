package fleet

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/tools/dev/simulator/internal/config"
	"github.com/Passion-Flow/ownsolid/tools/dev/simulator/internal/link"
	"github.com/Passion-Flow/ownsolid/tools/dev/simulator/internal/report"
)

// enrolled enrolls agents into f and returns their configuration, ready to run for
// seconds with faults.
func enrolled(t *testing.T, f *fakeControl, agents, seconds int, faults ...config.Fault) config.Config {
	t.Helper()
	c := f.files(agents)
	if e := Enroll(t.Context(), c); e != nil {
		t.Fatal(e)
	}
	c.DurationSeconds, c.Faults = seconds, faults
	if e := c.Validate(); e != nil {
		t.Fatal(e)
	}
	if e := c.ValidateRun(); e != nil {
		t.Fatal(e)
	}
	return c
}

// runAsync runs the fleet and returns the report when it ends.
func runAsync(t *testing.T, c config.Config) <-chan report.Report {
	t.Helper()
	done := make(chan report.Report, 1)
	go func() {
		out, e := Run(context.Background(), c)
		if e != nil {
			t.Error(e)
		}
		done <- out
	}()
	return done
}

// waitUntil polls condition, checked against f under its lock, for up to within.
func waitUntil(t *testing.T, f *fakeControl, within time.Duration, what string, condition func(*fakeControl) bool) {
	t.Helper()
	for deadline := time.Now().Add(within); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		f.mu.Lock()
		ok := condition(f)
		f.mu.Unlock()
		if ok {
			return
		}
	}
	t.Fatalf("not within %s: %s", within, what)
}

func everyNode(check func(*fakeNode) bool) func(*fakeControl) bool {
	return func(f *fakeControl) bool {
		for _, n := range f.nodes {
			if !check(n) {
				return false
			}
		}
		return len(f.nodes) > 0
	}
}

func TestEnrollWritesOneIdentityPerTokenAndRefusesToOverwrite(t *testing.T) {
	t.Parallel()
	f := newFakeControl(t)
	c := f.files(5)
	if e := Enroll(t.Context(), c); e != nil {
		t.Fatal(e)
	}
	agents, e := loadIdentities(c.IdentitiesFile, c.ControlOrigin)
	if e != nil || len(agents) != 5 {
		t.Fatalf("identities %d: %v", len(agents), e)
	}
	if info, _ := os.Stat(c.IdentitiesFile); info.Mode().Perm() != 0o600 {
		t.Fatalf("identities file mode %v", info.Mode())
	}
	if e := Enroll(t.Context(), c); e == nil || e.Error() != "SIMULATOR_IDENTITIES_EXIST" {
		t.Fatalf("second enroll: %v", e)
	}
	if _, e := loadIdentities(c.IdentitiesFile, "https://other.example:443"); e == nil {
		t.Fatal("identities of another control were accepted")
	}
}

func TestEnrollReportsTokensControlRefused(t *testing.T) {
	t.Parallel()
	f := newFakeControl(t)
	c := f.files(3)
	f.set(func(f *fakeControl) {
		for token := range f.tokens {
			delete(f.tokens, token)
			break
		}
	})
	if e := Enroll(t.Context(), c); e == nil || e.Error() != "SIMULATOR_ENROLLMENT_INCOMPLETE" {
		t.Fatalf("enroll with a spent token: %v", e)
	}
	if agents, e := loadIdentities(c.IdentitiesFile, c.ControlOrigin); e != nil || len(agents) != 2 {
		t.Fatalf("the enrolled agents were not kept: %d %v", len(agents), e)
	}
}

func TestFleetRunsTasksOverTheStream(t *testing.T) {
	t.Parallel()
	f := newFakeControl(t)
	c := enrolled(t, f, 4, 7)
	done := runAsync(t, c)
	waitUntil(t, f, 3*time.Second, "every node reported", everyNode(func(n *fakeNode) bool { return len(n.heartbeats) > 0 }))
	f.enqueue()
	out := <-done
	f.each(func(n *fakeNode) {
		if n.acknowledged != 1 || len(n.queue) != 0 {
			t.Errorf("node %s acknowledged %d, %d queued", n.id, n.acknowledged, len(n.queue))
		}
		for task, results := range n.results {
			if results != 1 || n.acks[task] != 1 || n.progress[task] != 1 {
				t.Errorf("task %s: %d results, %d acknowledgements, %d progress reports", task, results, n.acks[task], n.progress[task])
			}
		}
		for _, h := range n.heartbeats {
			if h.transport != "stream" {
				t.Errorf("heartbeat over %s", h.transport)
			}
		}
	})
	if out.Tasks.Succeeded != 4 || out.Tasks.Acknowledged != 4 || out.Connected != 4 || out.OnStream != 4 {
		t.Fatalf("report %+v", out)
	}
	if calls := out.Calls[report.CallHeartbeat]; calls == nil || len(calls.Failed) != 0 || calls.Succeeded < 4 {
		t.Fatalf("heartbeats %+v", calls)
	}
	agents, e := loadIdentities(c.IdentitiesFile, c.ControlOrigin)
	if e != nil {
		t.Fatal(e)
	}
	for _, a := range agents {
		if a.AcknowledgedSequence != 1 {
			t.Fatalf("cursor of %s not saved: %d", a.NodeID, a.AcknowledgedSequence)
		}
	}
	raw, e := os.ReadFile(c.ReportFile)
	var written report.Report
	if e != nil || json.Unmarshal(raw, &written) != nil || written.Tasks.Succeeded != 4 {
		t.Fatalf("report file: %v %s", e, raw)
	}
}

func TestAgentFallsBackToHTTPSWhenTheStreamCannotOpen(t *testing.T) {
	t.Parallel()
	f := newFakeControl(t)
	f.noStream = true
	c := enrolled(t, f, 1, 7)
	done := runAsync(t, c)
	waitUntil(t, f, 4*time.Second, "an HTTPS heartbeat", everyNode(func(n *fakeNode) bool {
		return len(n.heartbeats) > 0 && n.heartbeats[len(n.heartbeats)-1].transport == "https"
	}))
	f.enqueue()
	out := <-done
	if out.Tasks.Succeeded != 1 || out.Connected != 1 || out.OnStream != 0 {
		t.Fatalf("report %+v", out)
	}
	if opens := out.Calls[report.CallStreamOpen]; opens == nil || opens.Failed["CONTROL_CONNECTION_FAILED"] != 1 || opens.Succeeded != 0 {
		t.Fatalf("stream opens %+v: the fallback window should stop further attempts", opens)
	}
}

func TestRefusedHeartbeatsWaitForTheHint(t *testing.T) {
	t.Parallel()
	f := newFakeControl(t)
	f.refuse, f.retryAfter = 2, 2
	c := enrolled(t, f, 1, 8)
	out := <-runAsync(t, c)
	f.each(func(n *fakeNode) {
		if len(n.heartbeats) < 3 {
			t.Fatalf("%d heartbeats", len(n.heartbeats))
		}
		for i := 1; i < 3; i++ {
			// The reconnect backoff alone would come back within one second.
			if gap := n.heartbeats[i].at.Sub(n.heartbeats[i-1].at); gap < 2*time.Second {
				t.Fatalf("heartbeat %d came %s after a refusal with a 2 s hint", i, gap)
			}
		}
	})
	if out.Calls[report.CallHeartbeat].Failed["RATE_LIMITED"] != 2 {
		t.Fatalf("refusals %+v", out.Calls[report.CallHeartbeat])
	}
}

func TestLostRepliesAreSentAgainAndTheTaskRunsOnce(t *testing.T) {
	t.Parallel()
	f := newFakeControl(t)
	c := enrolled(t, f, 2, 7, config.Fault{Kind: config.FaultLoseReplies, AtSeconds: 0, Percent: 100, DurationSeconds: 6})
	done := runAsync(t, c)
	waitUntil(t, f, 3*time.Second, "every node reported", everyNode(func(n *fakeNode) bool { return len(n.heartbeats) > 0 }))
	f.enqueue()
	out := <-done
	f.each(func(n *fakeNode) {
		for task, results := range n.results {
			// The stream's acknowledgement is lost and sent again over HTTPS; the first
			// result's reply is lost and the result delivered again.
			if results != 2 || n.acks[task] != 2 {
				t.Errorf("task %s: %d results, %d acknowledgements", task, results, n.acks[task])
			}
		}
		if len(n.results) != 1 {
			t.Errorf("node %s ran %d tasks", n.id, len(n.results))
		}
	})
	if out.Tasks.Succeeded != 2 || out.Tasks.LostReplies != 4 {
		t.Fatalf("report %+v", out.Tasks)
	}
}

func TestLostCursorIsReplacedByControls(t *testing.T) {
	t.Parallel()
	f := newFakeControl(t)
	c := enrolled(t, f, 1, 8, config.Fault{Kind: config.FaultLoseCursor, AtSeconds: 2, Percent: 100})
	done := runAsync(t, c)
	waitUntil(t, f, 3*time.Second, "a heartbeat", everyNode(func(n *fakeNode) bool { return len(n.heartbeats) > 0 }))
	f.enqueue()
	waitUntil(t, f, 3*time.Second, "the task", everyNode(func(n *fakeNode) bool { return n.acknowledged == 1 }))
	out := <-done
	if out.Tasks.Resyncs != 1 {
		t.Fatalf("resyncs %d", out.Tasks.Resyncs)
	}
	agents, _ := loadIdentities(c.IdentitiesFile, c.ControlOrigin)
	if agents[0].AcknowledgedSequence != 1 {
		t.Fatalf("cursor after the resync %d", agents[0].AcknowledgedSequence)
	}
}

func TestDisconnectIsMeasuredUntilEveryAgentReconnects(t *testing.T) {
	t.Parallel()
	f := newFakeControl(t)
	c := enrolled(t, f, 6, 8, config.Fault{Kind: config.FaultDisconnect, AtSeconds: 2, Percent: 100})
	out := <-runAsync(t, c)
	if len(out.Faults) != 1 {
		t.Fatalf("faults %+v", out.Faults)
	}
	fault := out.Faults[0]
	// A held claim fails with the connection, then the agent backs off at most the
	// two-second cap before it opens a new stream.
	if fault.Agents != 6 || fault.RecoveredAgents != 6 || fault.FullRecovery == nil || *fault.FullRecovery > 3 {
		t.Fatalf("fault %+v", fault)
	}
	if f.streams < 12 || out.OnStream != 6 {
		t.Fatalf("%d streams opened, %d on the stream at the end", f.streams, out.OnStream)
	}
}

func TestRestartComesBackWithoutBackoff(t *testing.T) {
	t.Parallel()
	f := newFakeControl(t)
	f.pacing.ReconnectBaseSeconds, f.pacing.ReconnectMaxSeconds = 30, 30
	c := enrolled(t, f, 3, 7, config.Fault{Kind: config.FaultRestart, AtSeconds: 2, Percent: 100})
	out := <-runAsync(t, c)
	if fault := out.Faults[0]; fault.RecoveredAgents != 3 || fault.FullRecovery == nil || *fault.FullRecovery > 1 {
		t.Fatalf("a restart waited for the 30 s backoff: %+v", fault)
	}
}

func TestPartitionReachesNothingUntilItHeals(t *testing.T) {
	t.Parallel()
	f := newFakeControl(t)
	c := enrolled(t, f, 2, 9, config.Fault{Kind: config.FaultPartition, AtSeconds: 1, Percent: 100, DurationSeconds: 3})
	started := time.Now()
	out := <-runAsync(t, c)
	f.each(func(n *fakeNode) {
		for _, h := range n.heartbeats {
			if at := h.at.Sub(started); at > 1200*time.Millisecond && at < 4*time.Second {
				t.Errorf("a partitioned agent reached control %s into the run", at)
			}
		}
	})
	fault := out.Faults[0]
	if fault.Agents != 2 || fault.RecoveredAgents != 2 || fault.FullRecovery == nil || *fault.FullRecovery > 3 {
		t.Fatalf("fault %+v", fault)
	}
	if out.Calls[report.CallHeartbeat].Failed["CONTROL_CONNECTION_FAILED"] == 0 {
		t.Fatalf("no failed heartbeat during the partition: %+v", out.Calls[report.CallHeartbeat])
	}
}

func TestRevokedAgentStops(t *testing.T) {
	t.Parallel()
	f := newFakeControl(t)
	c := enrolled(t, f, 2, 7)
	f.each(func(n *fakeNode) { f.revoked[n.id] = true })
	out := <-runAsync(t, c)
	if out.Revoked != 2 || out.Connected != 0 {
		t.Fatalf("report %+v", out)
	}
	f.each(func(n *fakeNode) {
		if len(n.heartbeats) != 0 {
			t.Fatalf("revoked node %s was served", n.id)
		}
	})
	if f.streams != 0 || out.Calls[report.CallStreamOpen].Failed["AGENT_UNAUTHORIZED"] != 2 {
		t.Fatalf("a revoked agent tried again: %+v", out.Calls[report.CallStreamOpen])
	}
}

func TestAHeartbeatCutShortByTheEndOfTheRunKeepsTheAgentConnected(t *testing.T) {
	t.Parallel()
	f := newFakeControl(t)
	c := enrolled(t, f, 1, 7)
	all, e := loadIdentities(c.IdentitiesFile, c.ControlOrigin)
	if e != nil {
		t.Fatal(e)
	}
	dialer, e := link.NewDialer(c.ControlOrigin, c.CAFile, c.DialAddress, 2, time.Second)
	if e != nil {
		t.Fatal(e)
	}
	defer dialer.Close()
	a := newAgent(all[0], dialer, dialer.Connection(), report.New(1, time.Now()))
	a.connected.Store(true)
	ended, cancel := context.WithCancel(t.Context())
	cancel()
	if a.cycle(ended) == nil {
		t.Fatal("a cycle ran after the end of the run")
	}
	if !a.connected.Load() {
		t.Fatal("the end of the run counted as a lost connection")
	}
}

func TestManyAgentsLongPollAtOnceOverASmallHTTPSPool(t *testing.T) {
	t.Parallel()
	f := newFakeControl(t)
	f.noStream = true
	c := enrolled(t, f, 12, 12)
	// Twelve claims held for four seconds each would queue behind two pooled connections.
	c.HTTPSConnections = 2
	out := <-runAsync(t, c)
	if calls := out.Calls[report.CallClaim]; calls == nil || len(calls.Failed) != 0 || calls.Succeeded < 12 {
		t.Fatalf("claims %+v", calls)
	}
	if out.Connected != 12 || out.OnStream != 0 {
		t.Fatalf("report %+v", out)
	}
}
