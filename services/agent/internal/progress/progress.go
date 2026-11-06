// Package progress reports what a running task is doing: the step it is on and a log of
// what happened. Work reaches its reporter through the context, so code below the runtime
// reports with one call and needs no reporter of its own.
//
// Progress never decides a task; the result does. A report that fails is tried again from
// a bounded buffer, and whatever cannot be delivered is dropped while the task carries on.
package progress

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
)

// maximumBuffered bounds what a reporter holds while control cannot be reached. Steps are
// kept past it, and there are at most wire.MaximumProgressSteps of them.
const maximumBuffered = 512

// closingTimeout bounds the last report, so a slow control cannot delay a task's result.
const closingTimeout = 5 * time.Second

// flushInterval is how often a running task reports; tests shorten it.
var flushInterval = time.Duration(wire.ProgressFlushSeconds) * time.Second

// Sender delivers one report. An error for which Retryable holds keeps the entries for
// the next attempt; any other error ends reporting for this task.
type Sender func(context.Context, []wire.ProgressEntry) error

// Retryable reports whether a send may succeed later.
type Retryable func(error) bool

// Reporter gathers one attempt's progress and sends it in the background.
type Reporter struct {
	send      Sender
	retryable Retryable
	task      string

	sending sync.Mutex

	mu       sync.Mutex
	buffer   []wire.ProgressEntry
	sequence int
	lines    int
	steps    int
	omitted  int
	open     string
	stopped  bool
	done     chan struct{}
	finished chan struct{}
}

// New returns a reporter for task. Nothing is sent until Start.
func New(task string, send Sender, retryable Retryable) *Reporter {
	return &Reporter{send: send, retryable: retryable, task: task, done: make(chan struct{}), finished: make(chan struct{})}
}

// Start reports on a timer until Finish or until ctx ends.
func (r *Reporter) Start(ctx context.Context) {
	go func() {
		defer close(r.finished)
		ticker := time.NewTicker(flushInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-r.done:
				return
			case <-ticker.C:
				r.flush(ctx)
			}
		}
	}()
}

// Finish closes the open step, says what was left out and sends everything that remains.
// It returns within closingTimeout. failed marks the open step as failed. Finish must
// follow Start, once.
func (r *Reporter) Finish(ctx context.Context, failed bool) {
	close(r.done)
	<-r.finished
	r.mu.Lock()
	state := wire.ProgressStepSucceeded
	if failed {
		state = wire.ProgressStepFailed
	}
	r.closeStep(state)
	if r.omitted > 0 {
		r.append(wire.ProgressEntry{Kind: wire.ProgressKindLog, Level: wire.ProgressLevelWarning, Message: fmt.Sprintf("%d log lines were left out", r.omitted)})
	}
	r.mu.Unlock()
	closing, cancel := context.WithTimeout(ctx, closingTimeout)
	defer cancel()
	r.flush(closing)
}

// step closes the open step as succeeded and opens key.
func (r *Reporter) step(key string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if key == r.open {
		return
	}
	r.closeStep(wire.ProgressStepSucceeded)
	if r.steps >= wire.MaximumProgressSteps {
		return
	}
	r.steps++
	r.open = key
	r.append(wire.ProgressEntry{Kind: wire.ProgressKindStep, Step: key, State: wire.ProgressStepRunning})
}

// closeStep ends the open step. The caller holds mu.
func (r *Reporter) closeStep(state string) {
	if r.open == "" {
		return
	}
	r.append(wire.ProgressEntry{Kind: wire.ProgressKindStep, Step: r.open, State: state})
	r.open = ""
}

// line adds a log line, or counts it when the log is full or control is out of reach.
func (r *Reporter) line(level, message string) {
	message = clean(message)
	if message == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.lines >= wire.MaximumProgressLogLines || len(r.buffer) >= maximumBuffered {
		r.omitted++
		return
	}
	r.lines++
	r.append(wire.ProgressEntry{Kind: wire.ProgressKindLog, Level: level, Message: message})
}

// append numbers and buffers an entry. The caller holds mu.
func (r *Reporter) append(entry wire.ProgressEntry) {
	if r.stopped {
		return
	}
	r.sequence++
	entry.Sequence = r.sequence
	entry.At = time.Now().UTC()
	r.buffer = append(r.buffer, entry)
}

// flush sends what is buffered, in batches that fit a report, until it runs out or a send
// fails. One flush runs at a time, so entries arrive in order.
func (r *Reporter) flush(ctx context.Context) {
	r.sending.Lock()
	defer r.sending.Unlock()
	for {
		batch := r.nextBatch()
		if len(batch) == 0 {
			return
		}
		e := r.send(ctx, batch)
		if e == nil {
			r.drop(len(batch))
			continue
		}
		if !r.retryable(e) {
			slog.Warn("agent_task_progress_stopped", "task_id", r.task, "code", e.Error())
			r.stop()
		}
		return
	}
}

// nextBatch copies the longest run of entries that fits one report.
func (r *Reporter) nextBatch() []wire.ProgressEntry {
	r.mu.Lock()
	defer r.mu.Unlock()
	size := 0
	for index, entry := range r.buffer {
		encoded, e := json.Marshal(entry)
		if e != nil {
			return nil
		}
		size += len(encoded) + 1
		if index == wire.MaximumProgressBatch || size > wire.MaximumProgressBatchBytes {
			return append([]wire.ProgressEntry(nil), r.buffer[:index]...)
		}
	}
	return append([]wire.ProgressEntry(nil), r.buffer...)
}

// drop forgets the first count entries, which control has.
func (r *Reporter) drop(count int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.buffer = append(r.buffer[:0], r.buffer[count:]...)
}

// stop ends reporting for this task.
func (r *Reporter) stop() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stopped = true
	r.buffer = nil
}

// clean keeps a line to what a report accepts: one line of printable text within the limit.
func clean(message string) string {
	out := make([]rune, 0, len(message))
	size := 0
	for _, value := range message {
		if !unicode.IsPrint(value) {
			value = ' '
		}
		size += utf8.RuneLen(value)
		if size > wire.MaximumProgressMessageBytes {
			break
		}
		out = append(out, value)
	}
	return string(out)
}

type contextKey struct{}

// With returns a context that carries r to the work below it.
func With(ctx context.Context, r *Reporter) context.Context {
	return context.WithValue(ctx, contextKey{}, r)
}

func from(ctx context.Context) *Reporter {
	r, _ := ctx.Value(contextKey{}).(*Reporter)
	return r
}

// Step marks the start of a step, which ends the one before it. Without a reporter in ctx
// it does nothing.
func Step(ctx context.Context, key string) {
	if r := from(ctx); r != nil {
		r.step(key)
	}
}

// Info adds a log line. Lines are written by agent code and never copy command output, so
// no secret or control sequence reaches the log.
func Info(ctx context.Context, format string, values ...any) {
	if r := from(ctx); r != nil {
		r.line(wire.ProgressLevelInfo, fmt.Sprintf(format, values...))
	}
}

// Error adds a log line about what stopped the task.
func Error(ctx context.Context, format string, values ...any) {
	if r := from(ctx); r != nil {
		r.line(wire.ProgressLevelError, fmt.Sprintf(format, values...))
	}
}
