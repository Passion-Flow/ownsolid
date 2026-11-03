package monitoring

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/components"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/distribution"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/licensing"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/monitoringmetrics"
	release "github.com/Passion-Flow/ownsolid/api/release/v1"
)

type Service struct {
	identity     *identity.Service
	workspaces   *workspaces.Service
	license      *licensing.Service
	components   *components.Service
	distribution *distribution.Service
	metrics      *monitoringmetrics.Recorder
	config       Config
	slots        chan struct{}
}
type Snapshot struct {
	FormatVersion  int                                `json:"format_version"`
	WorkspaceID    string                             `json:"workspace_id"`
	ObservedAt     time.Time                          `json:"observed_at"`
	License        licensing.PortalState              `json:"license"`
	Process        monitoringmetrics.Snapshot         `json:"process"`
	Runtime        monitoringmetrics.RuntimeSnapshot  `json:"runtime"`
	Gateway        distribution.MonitoringWindow      `json:"gateway"`
	Nodes          []components.MonitoringObservation `json:"nodes"`
	NextNodeCursor *string                            `json:"next_node_cursor"`
}

func New(i *identity.Service, w *workspaces.Service, l *licensing.Service, c *components.Service, d *distribution.Service, m *monitoringmetrics.Recorder, policy Config) (*Service, error) {
	if i == nil || w == nil || w.Identity != i || l == nil || !l.UsesIdentity(i) || c == nil || c.Nodes == nil || c.Nodes.Identity != i || c.Nodes.Workspaces != w || d == nil || d.Identity != i || d.Workspaces != w || d.Nodes != c.Nodes || m == nil {
		return nil, errors.New("MONITORING_OWNER_REQUIRED")
	}
	if policy.Validate() != nil || !m.MatchesPolicy(policy.HTTP) {
		return nil, errors.New("MONITORING_CONFIGURATION_INVALID")
	}
	if _, e := i.Repository(); e != nil {
		return nil, e
	}
	return &Service{i, w, l, c, d, m, policy, make(chan struct{}, policy.ConcurrentSnapshots)}, nil
}

// RequestBudgetSeconds lets the original dispatcher use this module's validated
// deployment policy without a second timeout default or environment authority.
func (s *Service) RequestBudgetSeconds() int {
	if s == nil || s.config.Validate() != nil {
		return 0
	}
	return s.config.OperationSeconds
}

func statement(q transaction.Executor, pg, my string, args ...any) transaction.Statement {
	if q == nil {
		return transaction.Statement{}
	}
	switch q.Dialect() {
	case transaction.MySQL:
		return transaction.SQL(transaction.MySQL, my, args...)
	case transaction.PostgreSQL:
		return transaction.SQL(transaction.PostgreSQL, pg, args...)
	default:
		return transaction.SQL(q.Dialect(), "")
	}
}
func validID(s string) bool {
	u, e := uuid.Parse(s)
	return e == nil && u != uuid.Nil && u.String() == s
}
func safeError(e error) error {
	if e == nil {
		return nil
	}
	// The same transaction writes the audit and trusted license clock. A lost
	// COMMIT acknowledgement remains unknown even when shutdown also cancelled.
	if kind, ok := transaction.CommitOutcome(e); ok {
		return errors.New(string(kind))
	}
	if transaction.IsCancellationOnly(e) {
		if errors.Is(e, context.DeadlineExceeded) {
			return errors.New(string(transaction.Timeout))
		}
		return errors.New(string(transaction.Cancelled))
	}
	var tx *transaction.Error
	if errors.As(e, &tx) {
		if tx.Kind == transaction.Cancelled || tx.Kind == transaction.Timeout {
			return errors.New(string(transaction.Unavailable))
		}
		return errors.New(string(tx.Kind))
	}
	code := e.Error()
	if len(code) > 120 || strings.Trim(code, "ABCDEFGHIJKLMNOPQRSTUVWXYZ_0123456789") != "" {
		return errors.New("STORAGE_UNAVAILABLE")
	}
	for _, prefix := range []string{"MONITORING_", "AUTH_", "STORAGE_", "LICENSE_", "COMPONENT_", "NODE_", "REPOSITORY_"} {
		if strings.HasPrefix(code, prefix) {
			return errors.New(code)
		}
	}
	switch code {
	case "ACCESS_DENIED", "RESOURCE_NOT_FOUND", "VALIDATION_FAILED", "INVALID_CURSOR":
		return errors.New(code)
	}
	return errors.New("STORAGE_UNAVAILABLE")
}

type cursor struct {
	At string `json:"at"`
	ID string `json:"id"`
}

func decodeCursor(raw string) (time.Time, string, error) {
	if raw == "" {
		return time.Time{}, "", nil
	}
	if len(raw) > 256 {
		return time.Time{}, "", errors.New("INVALID_CURSOR")
	}
	b, e := base64.RawURLEncoding.Strict().DecodeString(raw)
	var c cursor
	if e != nil || release.StrictJSON(b, &c) != nil {
		return time.Time{}, "", errors.New("INVALID_CURSOR")
	}
	at, e := time.Parse(time.RFC3339Nano, c.At)
	canonical, _ := json.Marshal(c)
	if e != nil || at.Location() != time.UTC || at.IsZero() || at.Nanosecond()%1000 != 0 || at.Format(time.RFC3339Nano) != c.At || !validID(c.ID) || base64.RawURLEncoding.EncodeToString(canonical) != raw {
		return time.Time{}, "", errors.New("INVALID_CURSOR")
	}
	return at, c.ID, nil
}
func encodeCursor(at time.Time, id string) *string {
	raw, _ := json.Marshal(cursor{at.UTC().Format(time.RFC3339Nano), id})
	s := base64.RawURLEncoding.EncodeToString(raw)
	return &s
}

// Get publishes only after the real selected store confirms commit, including
// current authority, trusted license clock and atomic diagnostic-read audit.
func (s *Service) Get(ctx context.Context, a identity.Auth, workspace, after, requestID string) (Snapshot, error) {
	if s == nil || ctx == nil || s.identity == nil || s.workspaces == nil || s.license == nil || s.components == nil || s.distribution == nil || s.metrics == nil || s.slots == nil || s.config.Validate() != nil {
		return Snapshot{}, errors.New("MONITORING_OWNER_REQUIRED")
	}
	if a.Me.Audience != "admin" || !a.Me.User.IsPlatformAdmin {
		return Snapshot{}, errors.New("ACCESS_DENIED")
	}
	if !validID(workspace) || !validID(requestID) {
		return Snapshot{}, errors.New("VALIDATION_FAILED")
	}
	at, id, e := decodeCursor(after)
	if e != nil {
		return Snapshot{}, e
	}
	select {
	case s.slots <- struct{}{}:
		defer func() { <-s.slots }()
	default:
		return Snapshot{}, errors.New("MONITORING_BUSY")
	}
	store, e := s.identity.Repository()
	if e != nil {
		return Snapshot{}, safeError(e)
	}
	operation, cancel := context.WithTimeout(ctx, time.Duration(s.config.OperationSeconds)*time.Second)
	defer cancel()
	var out Snapshot
	e = store.Within(operation, false, func(ctx context.Context, q transaction.Executor) error {
		if e := s.identity.RecheckOrderedWithin(ctx, q, a); e != nil {
			return e
		}
		// Hold the actual installed active-tenant fact until the same outer commit.
		// No client tenant or cached bootstrap flag is accepted as authority.
		var installedTenant string
		if e := q.QueryRow(ctx, installationStatement(q)).Scan(&installedTenant); e != nil {
			return e
		}
		w, e := s.workspaces.RecheckScopeWithin(ctx, q, a, workspace)
		if e != nil {
			return e
		}
		// The directory owner permits Admin inspection of disabled Workspaces.
		// Monitoring requires this locked scope to remain active until commit.
		if w.TenantID != installedTenant || w.Status != "active" {
			return errors.New("ACCESS_DENIED")
		}
		license, e := s.license.MonitoringWithin(ctx, q, a)
		if e != nil {
			return e
		}
		now, e := observationTimeWithin(ctx, q)
		if e != nil {
			return e
		}
		out = Snapshot{FormatVersion: 1, WorkspaceID: w.ID, ObservedAt: now, License: license, Nodes: []components.MonitoringObservation{}}
		from := now.Add(-time.Duration(s.config.LookbackSeconds) * time.Second)
		if out.Gateway, e = s.distribution.MonitoringWithin(ctx, q, a, w.ID, from, now, s.config.MaximumGatewayRows); e != nil {
			return e
		}
		var rows transaction.Rows
		if after == "" {
			rows, e = q.Query(ctx, nodePageStatement(q, w.TenantID, w.ID, s.config, time.Time{}, ""))
		} else {
			rows, e = q.Query(ctx, nodePageStatement(q, w.TenantID, w.ID, s.config, at, id))
		}
		if e != nil {
			return e
		}
		type key struct {
			id string
			at time.Time
		}
		keys := []key{}
		for rows.Next() {
			var k key
			if e = rows.Scan(&k.id, &k.at); e != nil {
				rows.Close()
				return e
			}
			if !validID(k.id) || k.at.IsZero() {
				rows.Close()
				return errors.New("STORAGE_UNAVAILABLE")
			}
			keys = append(keys, k)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
		if len(keys) > s.config.NodePageSize {
			keys = keys[:s.config.NodePageSize]
			last := keys[len(keys)-1]
			out.NextNodeCursor = encodeCursor(last.at, last.id)
		}
		// Lock node owners in canonical ID order; retain page presentation order.
		ordered := append([]key{}, keys...)
		sort.Slice(ordered, func(i, j int) bool { return ordered[i].id < ordered[j].id })
		facts := map[string]components.MonitoringObservation{}
		for _, k := range ordered {
			v, e := s.components.MonitoringWithin(ctx, q, a, w.ID, k.id)
			if e != nil {
				return e
			}
			facts[k.id] = v
		}
		for _, k := range keys {
			out.Nodes = append(out.Nodes, facts[k.id])
		}
		out.Process = s.metrics.Snapshot()
		out.Runtime = monitoringmetrics.Runtime()
		e = insertSnapshotAuditWithin(ctx, q, uuid.NewString(), a.Me.User.ID, w.ID, requestID, w.TenantID)
		return e
	})
	if e != nil {
		return Snapshot{}, safeError(e)
	}
	// A known commit may race caller cancellation. Its audit remains committed,
	// but a cancelled diagnostic request must not publish the assembled snapshot.
	// Classify the actual transaction error first so an unknown ACK stays unknown.
	if e := operation.Err(); e != nil {
		return Snapshot{}, safeError(e)
	}
	return out, nil
}
