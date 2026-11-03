package scope

import (
	"context"
	"reflect"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/compose"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

func (l *locked) verifyRecordedRestoreSQL(plan RestorePlan) error {
	s := restoreSQLState(l.events)
	if s.record == nil {
		return nil
	}
	if requiresMVPNativeRestore(l.marker, plan) {
		return l.verifyMVPNativeRestoreHistory(context.Background(), "sql")
	}
	r := *s.record
	if !validRestoreSQLRecord(&r, restoreState(l.events).record, l.marker.ScopeID) || r.Task.Restore.ExpectedSource != plan.Source.Source || r.Task.Restore.ArchiveBytes > plan.Request.MaximumBytes {
		return failure("OFFLINE_SCOPE_RESTORE_SQL_SOURCE_CONTEXT_CHANGED")
	}
	input, outcome, _ := inputsState(l.events)
	if input == nil || outcome != "completed" {
		return failure("OFFLINE_SCOPE_RESTORE_SQL_INPUTS_REQUIRED")
	}
	p, e := l.readInputs(context.Background(), *input)
	if e != nil {
		return e
	}
	model, e := compose.DecodeModel(p.model, l.config.Verification.MaximumMetadataBytes)
	if e != nil {
		return e
	}
	connection, e := restoreConnections(p, model)
	if e != nil {
		return e
	}
	runtime := runtimeState(l.events)
	uid, gid, e := roleUser(model, "postgres")
	if e != nil || runtime.record == nil || r.Initializer != p.descriptor.ImageID || r.Task.Image != model.Services["postgres"].Image || r.Task.UID != uid || r.Task.GID != gid || r.Task.NetworkID != runtime.record.NetworkID || r.Task.Database != connection.database || r.Task.User != connection.operator || r.Task.Port != connection.port {
		return failure("OFFLINE_SCOPE_RESTORE_SQL_TARGET_CONTEXT_CHANGED")
	}
	peers := map[string]string{}
	for role, service := range runtime.record.Services {
		peers[role] = service.ID
	}
	if !reflect.DeepEqual(peers, r.Task.Peers) || plan.Target.DescriptorSHA256 != wire.Digest(encode(p.descriptor)) {
		return failure("OFFLINE_SCOPE_RESTORE_SQL_PEER_CONTEXT_CHANGED")
	}
	return nil
}
