package progress

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
)

var errShed = errors.New("RATE_LIMITED")

// control records what reaches it and fails while refuse holds an error.
type control struct {
	mu       sync.Mutex
	received []wire.ProgressEntry
	reports  int
	refuse   error
}

func (c *control) send(_ context.Context, batch []wire.ProgressEntry) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.reports++
	if c.refuse != nil {
		return c.refuse
	}
	if e := (wire.TaskProgress{Entries: batch}).Validate(); e != nil {
		return e
	}
	encoded, e := json.Marshal(wire.TaskProgress{LeaseToken: strings.Repeat("x", 64), Entries: batch})
	if e != nil || len(encoded) > 16384 {
		return errors.New("AGENT_PAYLOAD_INVALID")
	}
	c.received = append(c.received, batch...)
	return nil
}

func (c *control) set(refusal error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.refuse = refusal
}

func (c *control) entries() []wire.ProgressEntry {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]wire.ProgressEntry(nil), c.received...)
}

func started(t *testing.T, c *control) (*Reporter, context.Context) {
	t.Helper()
	r := New("task", c.send, func(e error) bool { return errors.Is(e, errShed) })
	ctx := With(context.Background(), r)
	r.Start(ctx)
	return r, ctx
}

func TestStepsCloseEachOtherAndFinishClosesTheLast(t *testing.T) {
	c := &control{}
	r, ctx := started(t, c)
	Step(ctx, wire.ProgressStepDownload)
	Info(ctx, "Fetched %d files", 3)
	Step(ctx, wire.ProgressStepStart)
	r.Finish(ctx, false)

	got := []string{}
	for _, entry := range c.entries() {
		got = append(got, entry.Step+":"+entry.State+entry.Message)
	}
	want := []string{"download:running", ":Fetched 3 files", "download:succeeded", "start:running", "start:succeeded"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatal(got)
	}
}

func TestAFailedTaskFailsTheStepItStoppedOn(t *testing.T) {
	c := &control{}
	r, ctx := started(t, c)
	Step(ctx, wire.ProgressStepDownload)
	Error(ctx, "The download stopped")
	r.Finish(ctx, true)
	entries := c.entries()
	if last := entries[len(entries)-1]; last.Step != wire.ProgressStepDownload || last.State != wire.ProgressStepFailed {
		t.Fatal(last)
	}
}

func TestRepeatingAStepOpensItOnce(t *testing.T) {
	c := &control{}
	r, ctx := started(t, c)
	Step(ctx, wire.ProgressStepDownload)
	Step(ctx, wire.ProgressStepDownload)
	r.Finish(ctx, false)
	if entries := c.entries(); len(entries) != 2 {
		t.Fatal(entries)
	}
}

func TestReportsOnATimerWhileTheTaskRuns(t *testing.T) {
	interval := flushInterval
	flushInterval = 5 * time.Millisecond
	t.Cleanup(func() { flushInterval = interval })
	c := &control{}
	r, ctx := started(t, c)
	Step(ctx, wire.ProgressStepDownload)
	deadline := time.Now().Add(5 * time.Second)
	for len(c.entries()) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("nothing reported while the task ran")
		}
		time.Sleep(time.Millisecond)
	}
	r.Finish(ctx, false)
}

func TestAShedReportIsSentAgainInOrder(t *testing.T) {
	c := &control{}
	r, ctx := started(t, c)
	Info(ctx, "first")
	c.set(errShed)
	r.flush(ctx)
	if len(c.entries()) != 0 {
		t.Fatal("a refused report is not delivered")
	}
	Info(ctx, "second")
	c.set(nil)
	r.Finish(ctx, false)
	entries := c.entries()
	if len(entries) != 2 || entries[0].Message != "first" || entries[0].Sequence != 1 || entries[1].Sequence != 2 {
		t.Fatal(entries)
	}
}

func TestARefusalEndsReportingAndTheTaskCarriesOn(t *testing.T) {
	c := &control{}
	r, ctx := started(t, c)
	Info(ctx, "first")
	c.set(errors.New("CONTROL_RESPONSE_REJECTED"))
	r.flush(ctx)
	reports := c.reports
	Info(ctx, "second")
	Step(ctx, wire.ProgressStepDownload)
	r.Finish(ctx, false)
	if c.reports != reports {
		t.Fatal("reporting goes on after control refused it", c.reports, reports)
	}
}

func TestTheBufferStaysBoundedWhileControlIsOutOfReach(t *testing.T) {
	c := &control{}
	c.set(errShed)
	r, ctx := started(t, c)
	for range maximumBuffered * 3 {
		Info(ctx, "line")
	}
	Step(ctx, wire.ProgressStepDownload)
	if len(r.buffer) != maximumBuffered+1 {
		t.Fatal("lines stop at the bound and a step still gets in", len(r.buffer))
	}
	c.set(nil)
	r.Finish(ctx, false)
	entries := c.entries()
	if last := entries[len(entries)-1]; last.Message != "1024 log lines were left out" {
		t.Fatal(last)
	}
}

func TestAFullLogStopsAtTheLimitAndStepsContinue(t *testing.T) {
	c := &control{}
	r, ctx := started(t, c)
	for range wire.MaximumProgressLogLines + 5 {
		Info(ctx, "line")
		r.flush(ctx)
	}
	Step(ctx, wire.ProgressStepVerify)
	r.Finish(ctx, false)
	lines, steps := 0, 0
	for _, entry := range c.entries() {
		if entry.Kind == wire.ProgressKindStep {
			steps++
		} else {
			lines++
		}
	}
	if lines != wire.MaximumProgressLogLines+1 || steps != 2 {
		t.Fatal(lines, steps)
	}
}

func TestStepsStopAtTheLimit(t *testing.T) {
	c := &control{}
	r, ctx := started(t, c)
	keys := []string{wire.ProgressStepDownload, wire.ProgressStepStart}
	for index := range wire.MaximumProgressSteps * 2 {
		Step(ctx, keys[index%2])
		r.flush(ctx)
	}
	r.Finish(ctx, false)
	entries := c.entries()
	if len(entries) != 2*wire.MaximumProgressSteps {
		t.Fatal(len(entries))
	}
	if last := entries[len(entries)-1]; last.Sequence > wire.MaximumProgressSequence {
		t.Fatal(last)
	}
}

func TestLongLinesFillABatchWithoutPassingTheBodyLimit(t *testing.T) {
	c := &control{}
	r, ctx := started(t, c)
	for range 200 {
		Info(ctx, "%s", strings.Repeat("中", 400))
	}
	r.Finish(ctx, false)
	if entries := c.entries(); len(entries) != 200 {
		t.Fatal(len(entries))
	}
	if c.reports < 2 {
		t.Fatal("long lines take several reports", c.reports)
	}
}

func TestLinesAreCleanedOfWhatAReportRefuses(t *testing.T) {
	c := &control{}
	r, ctx := started(t, c)
	Info(ctx, "one\ntwo\x1b[2J")
	Info(ctx, "%s", strings.Repeat("x", wire.MaximumProgressMessageBytes*2))
	Info(ctx, "%s", "")
	r.Finish(ctx, false)
	entries := c.entries()
	if len(entries) != 2 || entries[0].Message != "one two [2J" || len(entries[1].Message) != wire.MaximumProgressMessageBytes {
		t.Fatal(entries)
	}
}

func TestWorkWithoutAReporterReportsNothing(t *testing.T) {
	ctx := context.Background()
	Step(ctx, wire.ProgressStepDownload)
	Info(ctx, "line")
	Warning(ctx, "line")
	Error(ctx, "line")
}

func TestASlowControlCannotHoldTheResultBack(t *testing.T) {
	r := New("task", func(ctx context.Context, _ []wire.ProgressEntry) error {
		<-ctx.Done()
		return ctx.Err()
	}, func(error) bool { return true })
	deadline, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	ctx := With(deadline, r)
	r.Start(ctx)
	Info(ctx, "line")
	started := time.Now()
	r.Finish(ctx, false)
	if time.Since(started) > closingTimeout {
		t.Fatal("the last report outlived its bound")
	}
}
