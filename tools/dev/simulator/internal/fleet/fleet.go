// Package fleet runs simulated node agents against a real control plane: enrollment,
// then a fleet of agents with scripted faults, measured into a report (ADR 0033).
package fleet

import (
	"context"
	"errors"
	"log/slog"
	"math/rand/v2"
	"sort"
	"sync"
	"time"

	"github.com/Passion-Flow/ownsolid/tools/dev/simulator/internal/config"
	"github.com/Passion-Flow/ownsolid/tools/dev/simulator/internal/link"
	"github.com/Passion-Flow/ownsolid/tools/dev/simulator/internal/report"
)

// Run runs c.Agents agents from the identities file for c.DurationSeconds, or until ctx
// ends, applies the scripted faults, and writes the report and the agents' task cursors.
// It returns the report.
func Run(ctx context.Context, c config.Config) (report.Report, error) {
	all, e := loadIdentities(c.IdentitiesFile, c.ControlOrigin)
	if e != nil {
		return report.Report{}, e
	}
	if len(all) < c.Agents {
		return report.Report{}, errors.New("SIMULATOR_IDENTITIES_TOO_FEW")
	}
	dialer, e := link.NewDialer(c.ControlOrigin, c.CAFile, c.DialAddress, c.HTTPSConnections, time.Duration(c.RequestSeconds)*time.Second)
	if e != nil {
		return report.Report{}, e
	}
	defer dialer.Close()
	started := time.Now()
	recorder := report.New(c.Agents, started)
	connections := make([]*link.Connection, (c.Agents+c.AgentsPerConnection-1)/c.AgentsPerConnection)
	for i := range connections {
		connections[i] = dialer.Connection()
	}
	agents := make([]*agent, c.Agents)
	for i := range agents {
		agents[i] = newAgent(all[i], dialer, connections[i/c.AgentsPerConnection], recorder, time.Duration(c.LongTaskSeconds)*time.Second)
	}
	running, stop := context.WithTimeout(ctx, time.Duration(c.DurationSeconds)*time.Second)
	defer stop()
	var group sync.WaitGroup
	// Starts are spread evenly over the start window, the way a fleet comes up.
	spread := time.Duration(*c.StartSeconds) * time.Second
	for i, a := range agents {
		delay := spread * time.Duration(i) / time.Duration(len(agents))
		group.Go(func() { a.run(running, delay) })
	}
	group.Go(func() { inject(running, started, c.Faults, agents, recorder) })
	group.Go(func() { logEvery(running, time.Duration(c.LogSeconds)*time.Second, started, agents, recorder) })
	group.Go(func() { closeWindows(running, time.Duration(c.WindowSeconds)*time.Second, started, agents, recorder) })
	group.Go(func() { watchLag(running, recorder) })
	group.Wait()
	for _, conn := range connections {
		conn.Reset()
	}
	for i, a := range agents {
		all[i].AcknowledgedSequence = a.cursor
	}
	connected, onStream, revoked := counts(agents)
	out := recorder.Finish(time.Now(), connected, onStream, revoked)
	if e := saveIdentities(c.IdentitiesFile, c.ControlOrigin, all); e != nil {
		return out, errors.New("SIMULATOR_IDENTITIES_UNWRITABLE")
	}
	raw, e := report.Encode(out)
	if e == nil {
		e = writeFile(c.ReportFile, raw)
	}
	if e != nil {
		return out, errors.New("SIMULATOR_REPORT_UNWRITABLE")
	}
	return out, nil
}

// counts reports how many agents had a good last heartbeat, held a stream with it, and
// were revoked.
func counts(agents []*agent) (connected, onStream, revoked int) {
	for _, a := range agents {
		if a.connected.Load() {
			connected++
			if a.onStream.Load() {
				onStream++
			}
		}
		if a.revoked.Load() {
			revoked++
		}
	}
	return connected, onStream, revoked
}

// inject applies each fault at its time to its share of agents, chosen at random.
func inject(ctx context.Context, started time.Time, faults []config.Fault, agents []*agent, recorder *report.Recorder) {
	ordered := append([]config.Fault(nil), faults...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].AtSeconds < ordered[j].AtSeconds })
	for _, f := range ordered {
		if !sleep(ctx, time.Until(started.Add(time.Duration(f.AtSeconds)*time.Second))) {
			return
		}
		chosen := choose(len(agents), f.Percent)
		measured := f.Kind == config.FaultDisconnect || f.Kind == config.FaultRestart || f.Kind == config.FaultPartition
		index := recorder.AddFault(report.Fault{Kind: f.Kind, AtSeconds: f.AtSeconds, Percent: f.Percent, DurationSeconds: f.DurationSeconds, Agents: len(chosen), Measured: measured})
		now := time.Now()
		until := now.Add(time.Duration(f.DurationSeconds) * time.Second)
		for _, i := range chosen {
			agents[i].suffer(f.Kind, index, now, until)
		}
		slog.Info("simulator_fault", "kind", f.Kind, "agents", len(chosen), "duration_seconds", f.DurationSeconds)
	}
}

// choose picks percent of n indexes at random, at least one.
func choose(n, percent int) []int {
	k := max(1, (n*percent+99)/100)
	return rand.Perm(n)[:k]
}

// suffer applies the fault kind, the fleet's fault number index, from now until until.
func (a *agent) suffer(kind string, index int, now, until time.Time) {
	a.mu.Lock()
	switch kind {
	case config.FaultDisconnect:
		a.pending = append(a.pending, recovery{fault: index, since: now})
	case config.FaultRestart:
		a.restart = true
		a.pending = append(a.pending, recovery{fault: index, since: now})
	case config.FaultPartition:
		a.partitionUntil = until
		a.pending = append(a.pending, recovery{fault: index, since: until})
	case config.FaultHTTPSOnly:
		a.httpsOnlyUntil = until
	case config.FaultLoseReplies:
		a.loseRepliesUntil = until
	case config.FaultLoseCursor:
		a.loseCursor = true
	case config.FaultLongTasks:
		a.longTasksUntil = until
	}
	a.mu.Unlock()
	switch kind {
	case config.FaultDisconnect, config.FaultRestart, config.FaultPartition, config.FaultHTTPSOnly:
		// The connection goes now, so held claims and open streams end with it.
		a.conn.Reset()
	}
	if kind == config.FaultRestart {
		select {
		case a.wake <- struct{}{}:
		default:
		}
	}
}

// closeWindows ends a report window every interval.
func closeWindows(ctx context.Context, interval time.Duration, started time.Time, agents []*agent, recorder *report.Recorder) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			connected, onStream, _ := counts(agents)
			recorder.CloseWindow(int(time.Since(started).Round(time.Second).Seconds()), connected, onStream)
		}
	}
}

// lagProbeEvery is short enough to catch a stall that would show in a heartbeat's p99, and
// long enough to cost nothing beside thousands of agents.
const lagProbeEvery = 10 * time.Millisecond

// watchLag measures how late a timer fires, which tells a stall of this process or its host
// apart from a slow reply.
func watchLag(ctx context.Context, recorder *report.Recorder) {
	timer := time.NewTimer(lagProbeEvery)
	defer timer.Stop()
	for {
		asleep := time.Now()
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			recorder.ObserveLag(time.Since(asleep) - lagProbeEvery)
			timer.Reset(lagProbeEvery)
		}
	}
}

// logEvery logs a summary of the fleet every interval.
func logEvery(ctx context.Context, interval time.Duration, started time.Time, agents []*agent, recorder *report.Recorder) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	var previous report.Report
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		now := recorder.Snapshot()
		connected, onStream, revoked := counts(agents)
		heartbeats, failed := callDelta(now, previous, report.CallHeartbeat)
		slog.Info("simulator_fleet", "elapsed_seconds", int(time.Since(started).Seconds()), "agents", len(agents),
			"connected", connected, "on_stream", onStream, "revoked", revoked,
			"heartbeats", heartbeats, "heartbeats_failed", failed,
			"tasks_succeeded", now.Tasks.Succeeded, "resyncs", now.Tasks.Resyncs)
		previous = now
	}
}

// callDelta is how many calls succeeded and failed between two snapshots.
func callDelta(now, before report.Report, call string) (succeeded, failed int64) {
	total := func(r report.Report) (int64, int64) {
		c := r.Calls[call]
		if c == nil {
			return 0, 0
		}
		var f int64
		for _, n := range c.Failed {
			f += n
		}
		return c.Succeeded, f
	}
	s1, f1 := total(now)
	s0, f0 := total(before)
	return s1 - s0, f1 - f0
}
