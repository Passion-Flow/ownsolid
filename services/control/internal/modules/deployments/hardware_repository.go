package deployments

import (
	"context"
	"database/sql"
	"errors"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	datawire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	trainwire "github.com/Passion-Flow/ownsolid/api/training/v1"
)

// HardwareWithin borrows current actor/Workspace/node fences. It grants neither
// a lease nor execution authority and never commits or opens a machine connection.
func (s *Service) HardwareWithin(ctx context.Context, q transaction.Executor, n nodes.Node) (wire.InferenceObservation, error) {
	if ctx == nil || q == nil || s == nil || s.Nodes == nil || s.Nodes.Identity == nil {
		return wire.InferenceObservation{}, fail("STORAGE_UNAVAILABLE")
	}
	if e := ctx.Err(); e != nil {
		if errors.Is(e, context.Canceled) {
			return wire.InferenceObservation{}, fail(string(transaction.Cancelled))
		}
		return wire.InferenceObservation{}, fail(string(transaction.Timeout))
	}
	for _, id := range []string{n.TenantID, n.WorkspaceID, n.ID} {
		if !datawire.ValidID(id) {
			return wire.InferenceObservation{}, fail("TRAINING_PREFLIGHT_REQUIRED")
		}
	}
	store, e := s.Nodes.Identity.Repository()
	if e != nil {
		return wire.InferenceObservation{}, fail("STORAGE_UNAVAILABLE")
	}
	if store.Dialect() != q.Dialect() || s.Nodes.DB != nil && s.Nodes.DB != s.Nodes.Identity.DB {
		return wire.InferenceObservation{}, fail("TRAINING_STORAGE_MISMATCH")
	}
	seconds := s.Nodes.Identity.Config.Policy().Nodes.OnlineSeconds
	statement := transaction.SQL(transaction.PostgreSQL, `SELECT observation,received_at>clock_timestamp()-make_interval(secs=>$4) FROM inference_nodes WHERE tenant_id=$1 AND workspace_id=$2 AND node_id=$3 AND EXISTS(SELECT 1 FROM installation_state WHERE id=1 AND bootstrap_complete AND tenant_id=$1)`, n.TenantID, n.WorkspaceID, n.ID, seconds)
	if q.Dialect() == transaction.MySQL {
		statement = transaction.SQL(transaction.MySQL, `SELECT observation,received_at>TIMESTAMPADD(SECOND,?,UTC_TIMESTAMP(6)) FROM inference_nodes WHERE tenant_id=? AND workspace_id=? AND node_id=? AND EXISTS(SELECT 1 FROM installation_state WHERE id=1 AND bootstrap_complete AND tenant_id=?)`, -seconds, n.TenantID, n.WorkspaceID, n.ID, n.TenantID)
	}
	if q.Dialect() == transaction.Dameng {
		if !nativeRuntimeOwnsExecutor(store, q) {
			return wire.InferenceObservation{}, fail("TRAINING_STORAGE_MISMATCH")
		}
		statement = nativeRuntimeHardware(n.TenantID, n.WorkspaceID, n.ID, seconds, true)
	}
	var raw []byte
	var fresh bool
	e = q.QueryRow(ctx, statement).Scan(&raw, &fresh)
	if errors.Is(e, sql.ErrNoRows) || e == nil && !fresh {
		return wire.InferenceObservation{}, fail("TRAINING_PREFLIGHT_REQUIRED")
	}
	if e != nil {
		if errors.Is(e, context.Canceled) {
			return wire.InferenceObservation{}, fail(string(transaction.Cancelled))
		}
		if errors.Is(e, context.DeadlineExceeded) {
			return wire.InferenceObservation{}, fail(string(transaction.Timeout))
		}
		return wire.InferenceObservation{}, fail("STORAGE_UNAVAILABLE")
	}
	var out wire.InferenceObservation
	if trainwire.DecodeStrict(raw, &out) != nil || out.Validate() != nil {
		return wire.InferenceObservation{}, fail("TRAINING_DATA_INVALID")
	}
	out.ObservedAt = out.ObservedAt.UTC()
	for i := range out.GPUs {
		out.GPUs[i].ObservedAt = out.GPUs[i].ObservedAt.UTC()
	}
	return out, nil
}
