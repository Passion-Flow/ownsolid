package nodes

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"log/slog"
	"math/rand/v2"
	"slices"
	"sync/atomic"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/liveness"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/telemetrystore"
)

// Liveness outside the database (ARC-010, ADR 0022). A heartbeat whose state did not change
// runs one read and goes to the table; this file's worker writes the table to PostgreSQL in
// batches. MySQL and Dameng keep the synchronous heartbeat until CMP-201 and CMP-202.

const (
	// livenessBatch bounds the rows of one batch statement.
	livenessBatch = 1000
	// livenessPublishTimeout bounds what a stalled replica can add to one flush, which the
	// online window has room for. A healthy store takes milliseconds for a full batch.
	livenessPublishTimeout = time.Second
)

// heartbeatReport is the part of a heartbeat that moves while the node's state does not.
type heartbeatReport struct {
	component *wire.ComponentObservation
	inference *wire.InferenceObservation
}

// ComponentSighting is the latest component observation of a heartbeat whose state did not
// change. Its validation rules passed on the synchronous write of the same state.
type ComponentSighting struct {
	NodeID      string
	At          time.Time
	Observation wire.ComponentObservation
}

// InferenceSighting is ComponentSighting for an inference observation.
type InferenceSighting struct {
	NodeID      string
	At          time.Time
	Observation wire.InferenceObservation
}

// ComponentSightingRecorder and InferenceSightingRecorder write sightings in the worker's
// transaction, for the module that owns the data. A row another transaction holds is skipped,
// because a newer report follows. An owner that does not implement them keeps its heartbeats
// on the synchronous path.
type ComponentSightingRecorder interface {
	RecordComponentSightingsWithin(context.Context, transaction.Executor, []ComponentSighting) error
}
type InferenceSightingRecorder interface {
	RecordInferenceSightingsWithin(context.Context, transaction.Executor, []InferenceSighting) error
}

type livenessCounters struct {
	unchanged, written, flushed, flushFailures, replicaFailures atomic.Uint64
	online, unknown, offline                                    atomic.Uint64
}

func (c *livenessCounters) transition(to liveness.Connectivity) {
	switch to {
	case liveness.Online:
		c.online.Add(1)
	case liveness.Unknown:
		c.unknown.Add(1)
	default:
		c.offline.Add(1)
	}
}

// heartbeatDigest hashes a handshake with its volatile fields cleared. Whatever is not named
// here counts as state, so a field added later takes the synchronous path until someone
// decides it is volatile. Clearing keeps whether metrics are present: that decides whether
// an observation counts as verified.
func heartbeatDigest(h wire.Handshake) ([32]byte, bool) {
	h.Facts.ObservedAt, h.Facts.DiskAvailableBytes = time.Time{}, 0
	if h.Component != nil {
		c := *h.Component
		c.ObservedAt = time.Time{}
		if c.Metrics != nil {
			c.Metrics = &wire.ComponentMetrics{}
		}
		h.Component = &c
	}
	if h.Inference != nil {
		o := *h.Inference
		o.ObservedAt = time.Time{}
		o.GPUs = slices.Clone(o.GPUs)
		for i := range o.GPUs {
			o.GPUs[i].ObservedAt, o.GPUs[i].MemoryFreeMiB = time.Time{}, 0
		}
		h.Inference = &o
	}
	raw, e := json.Marshal(h)
	if e != nil {
		return [32]byte{}, false
	}
	return sha256.Sum256(raw), true
}

// sightingsRecordable reports whether an owner can write what this heartbeat carries.
func (s *Service) sightingsRecordable(h wire.Handshake) bool {
	if h.Component != nil {
		if _, ok := s.Components.(ComponentSightingRecorder); !ok {
			return false
		}
	}
	if h.Inference != nil {
		if _, ok := s.Inference.(InferenceSightingRecorder); !ok {
			return false
		}
	}
	return true
}

func (s *Service) stateRefresh() time.Duration {
	return config.Seconds(s.Identity.Config.Policy().Nodes.StateRefreshSeconds)
}

// heartbeatUnchanged serves a heartbeat that repeats the state last written, with one
// read-only statement. It reports false for anything else, including a refused credential,
// so that the synchronous path, which holds every rule and error, decides.
func (s *Service) heartbeatUnchanged(ctx context.Context, id, credential string, h wire.Handshake) (wire.Disposition, bool) {
	table := s.sightings.Load()
	if table == nil || ctx == nil || !nodeID(id) || !security.ValidToken(credential) || !s.sightingsRecordable(h) {
		return wire.Disposition{}, false
	}
	state, ok := heartbeatDigest(h)
	if !ok || !table.Unchanged(id, state, time.Now(), s.stateRefresh()) {
		return wire.Disposition{}, false
	}
	store, e := s.repository()
	if e != nil {
		return wire.Disposition{}, false
	}
	bounded, cancel := context.WithTimeout(ctx, config.Seconds(s.Identity.Config.Policy().HTTP.RequestSeconds))
	defer cancel()
	var status, protocol, profile string
	var capabilities []byte
	var active bool
	var at time.Time
	e = store.QueryRow(bounded, transaction.SQL(transaction.PostgreSQL, `SELECT n.status,w.status='active' AND t.status='active',n.protocol_version,n.execution_profile,n.capabilities,clock_timestamp() FROM managed_nodes n JOIN installation_state i ON i.tenant_id=n.tenant_id JOIN workspaces w ON w.tenant_id=n.tenant_id AND w.id=n.workspace_id JOIN tenants t ON t.id=n.tenant_id WHERE i.id=1 AND i.bootstrap_complete AND n.id=$1 AND n.credential_hash=$2`, id, digest("node-credential", credential))).Scan(&status, &active, &protocol, &profile, &capabilities, &at)
	if e != nil || status != "active" || !active || protocol != h.ProtocolVersion || profile != h.Profile() {
		return wire.Disposition{}, false
	}
	var stored []string
	if json.Unmarshal(capabilities, &stored) != nil || !slices.Equal(stored, h.Capabilities) {
		return wire.Disposition{}, false
	}
	at = at.UTC()
	table.Observe(id, at, heartbeatReport{component: h.Component, inference: h.Inference})
	s.livenessCounters.unchanged.Add(1)
	pacing := s.currentPacing(bounded)
	return wire.Disposition{Status: status, ServerTime: at, Pacing: &pacing}, true
}

// acceptHeartbeat notes a synchronous write, so the next heartbeat with the same state can
// skip it. Backdating by up to half the refresh spreads the next synchronous writes of nodes
// that were all written together, as after a restart.
func (s *Service) acceptHeartbeat(id string, h wire.Handshake, at time.Time) {
	s.livenessCounters.written.Add(1)
	table := s.sightings.Load()
	if table == nil {
		return
	}
	state, ok := heartbeatDigest(h)
	if !ok {
		return
	}
	table.Accept(id, state, at, time.Now().Add(-rand.N(s.stateRefresh()/2+1)))
}

// noteContactWithin records an authenticated contact that is not a heartbeat, such as a
// lease renewal. Without the table it writes the node row, as the other chains do.
func (s *Service) noteContactWithin(ctx context.Context, q transaction.Executor, id string) error {
	table := s.sightings.Load()
	if table == nil || q.Dialect() != transaction.PostgreSQL {
		_, e := q.Exec(ctx, nodeSQL(q, `UPDATE managed_nodes SET last_heartbeat_at=clock_timestamp(),updated_at=clock_timestamp() WHERE id=$1`, `UPDATE managed_nodes SET last_heartbeat_at=UTC_TIMESTAMP(6),updated_at=UTC_TIMESTAMP(6) WHERE id=?`, `UPDATE managed_nodes SET last_heartbeat_at=SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)),updated_at=SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)) WHERE id=?`, id))
		return e
	}
	var at time.Time
	if e := q.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, `SELECT clock_timestamp()`)).Scan(&at); e != nil {
		return e
	}
	table.Touch(id, at.UTC())
	return nil
}

// noteEnrolled makes a node that never reports again turn offline like any other.
func (s *Service) noteEnrolled(id string) {
	if table := s.sightings.Load(); table != nil {
		table.Load(id, time.Now().UTC(), liveness.Online)
	}
}

// EnableLiveness loads the table from PostgreSQL and turns the unchanged-heartbeat path on.
// Only a process that also runs FlushLiveness may call it. On another chain it does nothing.
func (s *Service) EnableLiveness(ctx context.Context) error {
	return s.enableLiveness(ctx, time.Now())
}

// enableLiveness is EnableLiveness for a process that began to listen at started.
func (s *Service) enableLiveness(ctx context.Context, started time.Time) error {
	if s.sightings.Load() != nil {
		return nil
	}
	store, e := s.repository()
	if e != nil {
		return e
	}
	if store.Dialect() != transaction.PostgreSQL {
		return nil
	}
	policy := s.Identity.Config.Policy().Nodes
	bounded, cancel := context.WithTimeout(ctx, config.Seconds(policy.ReconcileTimeoutSeconds))
	defer cancel()
	table, e := liveness.New[heartbeatReport](liveness.Thresholds{Online: config.Seconds(policy.OnlineSeconds), Offline: config.Seconds(policy.OfflineSeconds)}, started)
	if e != nil {
		return e
	}
	e = store.Within(bounded, true, func(ctx context.Context, q transaction.Executor) error {
		rows, e := q.Query(ctx, transaction.SQL(transaction.PostgreSQL, `SELECT id::text,last_heartbeat_at,connectivity FROM managed_nodes WHERE status<>'removed'`))
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var id, connectivity string
			var seen time.Time
			if e = rows.Scan(&id, &seen, &connectivity); e != nil {
				return e
			}
			table.Load(id, seen.UTC(), liveness.Connectivity(connectivity))
		}
		return rows.Err()
	})
	if e != nil {
		return e
	}
	s.sightings.Store(table)
	return nil
}

// RunLiveness writes the table to PostgreSQL until ctx ends, then once more, so a graceful
// stop loses nothing. The server runs it on PostgreSQL only.
func (s *Service) RunLiveness(ctx context.Context) {
	pass := func(ctx context.Context) {
		if e := s.EnableLiveness(ctx); e != nil {
			slog.Warn("node_liveness_load_failed", "code", "STORAGE_UNAVAILABLE")
			return
		}
		if e := s.FlushLiveness(ctx); e != nil {
			slog.Warn("node_liveness_flush_failed", "code", "STORAGE_UNAVAILABLE")
		}
	}
	pass(ctx)
	ticker := time.NewTicker(config.Seconds(s.Identity.Config.Policy().Nodes.LivenessFlushSeconds))
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			pass(context.WithoutCancel(ctx))
			return
		case <-ticker.C:
			pass(ctx)
		}
	}
}

// FlushLiveness writes what the table holds: batches of sightings, then connectivity changes.
// A failed batch goes back to the table for the next pass.
func (s *Service) FlushLiveness(ctx context.Context) error {
	table := s.sightings.Load()
	if table == nil {
		return nil
	}
	store, e := s.repository()
	if e != nil {
		return e
	}
	timeout := config.Seconds(s.Identity.Config.Policy().Nodes.ReconcileTimeoutSeconds)
	publishing := s.LivenessReplica != nil
	for {
		batch := table.Drain(livenessBatch)
		if len(batch) > 0 {
			bounded, cancel := context.WithTimeout(ctx, timeout)
			e = store.Within(bounded, false, func(ctx context.Context, q transaction.Executor) error { return s.writeSightingsWithin(ctx, q, batch) })
			cancel()
			if e != nil {
				table.Restore(batch)
				s.livenessCounters.flushFailures.Add(1)
				return e
			}
			s.livenessCounters.flushed.Add(uint64(len(batch)))
			publishing = publishing && s.publishSightings(ctx, batch)
		}
		if len(batch) < livenessBatch {
			break
		}
	}
	for {
		bounded, cancel := context.WithTimeout(ctx, timeout)
		more, e := s.recordTransitions(bounded, store, table)
		cancel()
		if e != nil {
			s.livenessCounters.flushFailures.Add(1)
			return e
		}
		if !more {
			return nil
		}
	}
}

func (s *Service) writeSightingsWithin(ctx context.Context, q transaction.Executor, batch []liveness.Sighting[heartbeatReport]) error {
	ids, times := make([]string, len(batch)), make([]time.Time, len(batch))
	var components []ComponentSighting
	var inference []InferenceSighting
	for i, v := range batch {
		ids[i], times[i] = v.Node, v.At
		if !v.HasReport {
			continue
		}
		if v.Report.component != nil {
			components = append(components, ComponentSighting{NodeID: v.Node, At: v.ReportAt, Observation: *v.Report.component})
		}
		if v.Report.inference != nil {
			inference = append(inference, InferenceSighting{NodeID: v.Node, At: v.ReportAt, Observation: *v.Report.inference})
		}
	}
	// SKIP LOCKED never waits for a long task transaction and never deadlocks with another
	// gateway; a skipped node is written with its next heartbeat. Readers measure this column
	// against the clock, and updated_at stays: nothing about the node changed.
	if _, e := q.Exec(ctx, transaction.SQL(transaction.PostgreSQL, `WITH t AS (SELECT n.id,v.at FROM managed_nodes n JOIN unnest($1::text[],$2::timestamptz[]) AS v(id,at) ON n.id=v.id::uuid WHERE n.status<>'removed' AND n.last_heartbeat_at<v.at ORDER BY n.id FOR NO KEY UPDATE OF n SKIP LOCKED) UPDATE managed_nodes n SET last_heartbeat_at=t.at FROM t WHERE n.id=t.id`, ids, times)); e != nil {
		return e
	}
	if len(components) > 0 {
		if e := s.Components.(ComponentSightingRecorder).RecordComponentSightingsWithin(ctx, q, components); e != nil {
			return e
		}
	}
	if len(inference) > 0 {
		return s.Inference.(InferenceSightingRecorder).RecordInferenceSightingsWithin(ctx, q, inference)
	}
	return nil
}

// publishSightings shares what this gateway heard and reports whether the store took it. It
// never fails a pass, and a failure ends publishing for that pass: a replica that is down must
// not delay the writes that keep nodes online. Newer sightings replace what was not shared.
func (s *Service) publishSightings(ctx context.Context, batch []liveness.Sighting[heartbeatReport]) bool {
	seen := make([]liveness.Seen, len(batch))
	for i, v := range batch {
		seen[i] = liveness.Seen{Node: v.Node, At: v.At}
	}
	bounded, cancel := context.WithTimeout(ctx, livenessPublishTimeout)
	defer cancel()
	if s.LivenessReplica.Publish(bounded, seen) != nil {
		s.livenessCounters.replicaFailures.Add(1)
		slog.Warn("node_liveness_publish_failed", "code", "LIVENESS_REPLICA_UNAVAILABLE")
		return false
	}
	return true
}

// RunLivenessReplica merges what other gateways heard until ctx ends, watching again after a
// failed store. While the store is down each gateway judges from its own sightings and the
// database refuses to turn a node worse when it holds a newer last-seen time. The server
// runs it only with a replica configured.
func (s *Service) RunLivenessReplica(ctx context.Context) {
	retry := config.Seconds(s.Identity.Config.Policy().Nodes.LivenessFlushSeconds)
	for ctx.Err() == nil {
		e := s.LivenessReplica.Watch(ctx, func(v liveness.Seen) {
			if table := s.sightings.Load(); table != nil {
				table.Merge(v.Node, v.At)
			}
		})
		if ctx.Err() != nil {
			return
		}
		if e != nil {
			s.livenessCounters.replicaFailures.Add(1)
			slog.Warn("node_liveness_watch_failed", "code", "LIVENESS_REPLICA_UNAVAILABLE")
		}
		select {
		case <-ctx.Done():
		case <-time.After(retry):
		}
	}
}

// recordTransitions stores one batch of connectivity changes and settles the ones it could
// not store. It reports whether it made progress on a full batch, so more may wait.
func (s *Service) recordTransitions(ctx context.Context, store transaction.Store, table *liveness.Table[heartbeatReport]) (bool, error) {
	var pending []liveness.Transition
	var recorded []liveness.Transition
	var forgotten []string
	var fresher []liveness.Seen
	e := store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
		pending, recorded, forgotten, fresher = nil, nil, nil, nil
		var now time.Time
		if e := q.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, `SELECT clock_timestamp()`)).Scan(&now); e != nil {
			return e
		}
		pending = table.Transitions(now, livenessBatch)
		if len(pending) == 0 {
			return nil
		}
		ids, states, seen := make([]string, len(pending)), make([]string, len(pending)), make([]time.Time, len(pending))
		for i, v := range pending {
			ids[i], states[i], seen[i] = v.Node, string(v.To), v.LastSeen
		}
		// A state gets worse only when this gateway has seen at least what the database has.
		rows, e := q.Query(ctx, transaction.SQL(transaction.PostgreSQL, `WITH t AS (SELECT n.id,v.state FROM managed_nodes n JOIN unnest($1::text[],$2::text[],$3::timestamptz[]) AS v(id,state,seen) ON n.id=v.id::uuid WHERE n.status<>'removed' AND n.connectivity<>v.state AND (v.state='online' OR n.last_heartbeat_at<=v.seen) ORDER BY n.id FOR NO KEY UPDATE OF n SKIP LOCKED) UPDATE managed_nodes n SET connectivity=t.state,connectivity_changed_at=clock_timestamp() FROM t WHERE n.id=t.id RETURNING n.id::text`, ids, states, seen))
		if e != nil {
			return e
		}
		stored := map[string]bool{}
		for rows.Next() {
			var id string
			if e = rows.Scan(&id); e != nil {
				rows.Close()
				return e
			}
			stored[id] = true
		}
		rows.Close()
		if e = rows.Err(); e != nil {
			return e
		}
		var open []string
		for _, v := range pending {
			if stored[v.Node] {
				recorded = append(recorded, v)
			} else {
				open = append(open, v.Node)
			}
		}
		if len(open) == 0 {
			return nil
		}
		return settleTransitionsWithin(ctx, q, pending, open, &recorded, &forgotten, &fresher)
	})
	if e != nil {
		return false, e
	}
	for _, id := range forgotten {
		table.Forget(id)
	}
	for _, v := range fresher {
		table.Merge(v.Node, v.At)
	}
	table.Recorded(recorded)
	for _, v := range recorded {
		s.livenessCounters.transition(v.To)
		slog.Info("node_connectivity_changed", "node", v.Node, "to", string(v.To), "last_seen", v.LastSeen)
	}
	return len(pending) == livenessBatch && len(recorded)+len(forgotten)+len(fresher) > 0, nil
}

// settleTransitionsWithin explains the changes the update did not store. A node that is gone
// leaves the table, one already in that state counts as recorded, and a newer last-seen time
// in the database replaces this gateway's. What remains was locked and stays pending.
func settleTransitionsWithin(ctx context.Context, q transaction.Executor, pending []liveness.Transition, open []string, recorded *[]liveness.Transition, forgotten *[]string, fresher *[]liveness.Seen) error {
	rows, e := q.Query(ctx, transaction.SQL(transaction.PostgreSQL, `SELECT v.id,COALESCE(n.status,'removed'),COALESCE(n.connectivity,''),COALESCE(n.last_heartbeat_at,'epoch'::timestamptz) FROM unnest($1::text[]) AS v(id) LEFT JOIN managed_nodes n ON n.id=v.id::uuid`, open))
	if e != nil {
		return e
	}
	defer rows.Close()
	wanted := make(map[string]liveness.Transition, len(pending))
	for _, v := range pending {
		wanted[v.Node] = v
	}
	for rows.Next() {
		var id, status, connectivity string
		var seen time.Time
		if e = rows.Scan(&id, &status, &connectivity, &seen); e != nil {
			return e
		}
		v := wanted[id]
		switch {
		case status == "removed":
			*forgotten = append(*forgotten, id)
		case connectivity == string(v.To):
			*recorded = append(*recorded, v)
		case seen.After(v.LastSeen):
			*fresher = append(*fresher, liveness.Seen{Node: id, At: seen.UTC()})
		}
	}
	return rows.Err()
}

// LivenessSamples reports the liveness path for the metrics store.
func (s *Service) LivenessSamples() []telemetrystore.Sample {
	c := &s.livenessCounters
	tracked := 0
	if table := s.sightings.Load(); table != nil {
		tracked = table.Size()
	}
	sample := func(name string, value uint64, labels ...telemetrystore.Label) telemetrystore.Sample {
		return telemetrystore.Sample{Name: name, Value: float64(value), Labels: labels}
	}
	path := func(v string) telemetrystore.Label { return telemetrystore.Label{Name: "path", Value: v} }
	to := func(v liveness.Connectivity) telemetrystore.Label {
		return telemetrystore.Label{Name: "to", Value: string(v)}
	}
	return []telemetrystore.Sample{
		{Name: "ownsolid_node_liveness_tracked_nodes", Value: float64(tracked)},
		sample("ownsolid_node_heartbeats_total", c.unchanged.Load(), path("unchanged")),
		sample("ownsolid_node_heartbeats_total", c.written.Load(), path("written")),
		sample("ownsolid_node_liveness_flushed_sightings_total", c.flushed.Load()),
		sample("ownsolid_node_liveness_flush_failures_total", c.flushFailures.Load()),
		sample("ownsolid_node_liveness_replica_failures_total", c.replicaFailures.Load()),
		sample("ownsolid_node_connectivity_transitions_total", c.online.Load(), to(liveness.Online)),
		sample("ownsolid_node_connectivity_transitions_total", c.unknown.Load(), to(liveness.Unknown)),
		sample("ownsolid_node_connectivity_transitions_total", c.offline.Load(), to(liveness.Offline)),
	}
}
