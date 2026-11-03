package scope

import (
	"context"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
)

// This boundary is read-only. The caller must separately verify the immutable
// plan, current PKI/payload, engine and exact stopped runtime/peer observation.
// It proves nonapplication of one fixed pending effect, never general absence.
func (l *locked) confirmControlReplacementNotApplied(ctx context.Context, c controlReplacementEngine, p ControlReplacementPlan, tls RestoredTLSPlan, files []engine.ConfigurationFile, r ControlReplacementRecord) error {
	bad := func() error { return failure("OFFLINE_SCOPE_CONTROL_REPLACEMENT_NONAPPLICATION_NOT_PROVEN") }
	if ctx.Err() != nil {
		return failure("OFFLINE_SCOPE_OPERATION_INTERRUPTED")
	}
	s := controlReplacementState(l.events)
	if s.pending == "" || s.record == nil || !controlReplacementIdentity(*s.record, r) || r.Step != s.record.Step {
		return bad()
	}
	owner := tls.ConfigurationOwner
	switch r.Phase {
	case "inspector-create":
		if _, found, e := c.InspectUtility(ctx, owner, tls.Initializer, tls.UID, tls.GID); e != nil || found {
			return bad()
		}
		if e := c.VerifyVolumeConsumers(ctx, owner, nil); e != nil {
			return e
		}
	case "inspector-remove":
		u, found, e := c.InspectUtility(ctx, owner, tls.Initializer, tls.UID, tls.GID)
		expected, recorded := s.completed[1]
		if e != nil || !found || !recorded || u.ID != expected.ObjectID {
			return bad()
		}
		if e = c.VerifyVolumeConsumers(ctx, owner, []string{u.ID}); e != nil {
			return e
		}
		observed, e := c.InspectConfiguration(ctx, owner, tls.Initializer, tls.UID, tls.GID, files)
		if e != nil || observed.Status != engine.ConfigurationPayloadVerified {
			return bad()
		}
	case "control-remove":
		object, found, e := c.InspectRuntime(ctx, p.ScopeID, "control")
		if e != nil || !found {
			return bad()
		}
		fingerprint, e := engine.RuntimeContainerDigest(object)
		if e != nil || fingerprint != p.PreviousMetadataSHA256 {
			return bad()
		}
		if e = c.VerifyVolumeConsumers(ctx, owner, nil); e != nil {
			return e
		}
	case "control-create":
		if !controlReplacementCompleted(l.events, "control-remove") {
			return bad()
		}
		if _, found, e := c.InspectRuntime(ctx, p.ScopeID, "control"); e != nil || found {
			return bad()
		}
		if e := c.VerifyVolumeConsumers(ctx, owner, nil); e != nil {
			return e
		}
	default:
		return bad()
	}
	return nil
}
