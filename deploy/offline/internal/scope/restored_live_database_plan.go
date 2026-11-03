package scope

import (
	"context"
	"reflect"
	"sort"
	"time"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

func liveDatabasePlanFile(generation string) string { return "live-database-" + generation + ".json" }
func liveDatabasePlanRecord(p RestoredLiveDatabasePlan) RestoredLiveDatabaseRecord {
	return RestoredLiveDatabaseRecord{GenerationID: p.GenerationID, NativeRuntime: p.NativeRuntime, PlanSHA256: wire.Digest(encode(p)), Admission: RestoredDependencyAdmission{ReplacementProofSHA256: p.ReplacementProofSHA256, TLSProofSHA256: p.TLSProofSHA256, EffectiveRuntimeSHA256: p.EffectiveRuntimeSHA256, ConfigurationPlanSHA256: p.TLSPlanSHA256}}
}

// Reopening/cleanup checks immutable metadata, without requiring current source
// credentials, a running dependency, or an unexpired certificate.
func (l *locked) validateLiveDatabasePlan(p RestoredLiveDatabasePlan) error {
	view := *l
	for i, event := range l.events {
		if event.RestoredLiveDatabase != nil && event.RestoredLiveDatabase.GenerationID == p.GenerationID {
			view.events = l.events[:i]
			break
		}
	}
	return view.validateLiveDatabasePlanAtAdmission(p)
}
func (l *locked) validateLiveDatabasePlanAtAdmission(p RestoredLiveDatabasePlan) error {
	bad := func() error { return failure("OFFLINE_SCOPE_LIVE_DATABASE_PLAN_CHANGED") }
	admission := dependencyAdmission(l.events)
	database, ok := restoreDatabaseProof(l.events)
	started, e := time.Parse(time.RFC3339Nano, p.PostgresStartedAt)
	if admission == nil || !ok || (p.FormatVersion != 1 && p.FormatVersion != 2) || (p.FormatVersion == 2) != (p.NativeRuntime != nil) || p.ScopeID != l.marker.ScopeID || !identityPattern.MatchString(p.GenerationID) || p.DatabaseProofSHA256 != database || !reflect.DeepEqual(liveDatabasePlanRecord(p).Admission, *admission) || p.PostgresPID <= 0 || e != nil || started.IsZero() || len(p.Tasks) != len(liveDatabaseTaskKinds(p.NativeRuntime)) {
		return bad()
	}
	restore, e := l.readBoundRestorePlan()
	if e != nil {
		return e
	}
	base, files, e := l.makeRestoreDatabaseRecord(context.Background(), restore)
	if e != nil {
		return e
	}
	if p.ContextSHA256 != base.ContextSHA256 || p.Initializer != base.Initializer {
		return bad()
	}
	tls := tlsProjectionState(l.events)
	if tls.record == nil {
		return bad()
	}
	projection, e := l.readTLSProjectionPlan(*tls.record)
	if e != nil {
		return e
	}
	index := map[string]InputFile{}
	for _, f := range files {
		index[f.Name] = InputFile{Name: f.Name, Bytes: int64(len(f.Contents)), SHA256: wire.Digest(f.Contents)}
	}
	for _, f := range projection.ConfigurationFiles {
		if projection.FormatVersion == 2 || f.Name == projection.CertificateName || f.Name == projection.PrivateKeyName {
			if _, ok := index[f.Name]; !ok {
				return bad()
			}
			index[f.Name] = f
		}
	}
	expectedFiles := []InputFile{}
	for _, f := range index {
		expectedFiles = append(expectedFiles, f)
	}
	sort.Slice(expectedFiles, func(i, j int) bool { return expectedFiles[i].Name < expectedFiles[j].Name })
	if !reflect.DeepEqual(expectedFiles, p.Files) || wire.Digest(encode(p.Files)) != p.PayloadSHA256 {
		return bad()
	}
	raw, e := restoreNamedFile(files, restoredRuntimeContextName)
	var restored wire.RestoredRuntimeContext
	if e != nil || wire.StrictJSON(raw, &restored) != nil {
		return bad()
	}
	input, e := mvpNativeRuntimeInputDigestAtIndex(expectedFiles, files)
	if e != nil {
		return bad()
	}
	native, e := l.bindMVPNativeActivation(restore, restored, base.ContextSHA256, input)
	if e != nil || !reflect.DeepEqual(native, p.NativeRuntime) {
		return bad()
	}
	runtime := effectiveRuntimeState(l.events)
	for i, kind := range liveDatabaseTaskKinds(p.NativeRuntime) {
		expected := base.task(kind)
		expected.Owner.OperationID = p.GenerationID
		expected.VolumeOwner = engine.Ownership{ScopeID: p.ScopeID, OperationID: p.GenerationID, Role: engine.StoreRestoreConfigRole + "-" + p.GenerationID}
		expected.NetworkID = runtime.record.NetworkID
		expected.Peers = map[string]string{}
		for role, service := range runtime.record.Services {
			expected.Peers[role] = service.ID
		}
		if !reflect.DeepEqual(expected, p.Tasks[i]) || expected.ValidateRestoredDatabaseTask() != nil {
			return bad()
		}
	}
	return nil
}
func (l *locked) readLiveDatabasePlan(r RestoredLiveDatabaseRecord) (RestoredLiveDatabasePlan, error) {
	var p RestoredLiveDatabasePlan
	raw, e := regular(l.root, liveDatabasePlanFile(r.GenerationID), l.config.Verification.MaximumMetadataBytes)
	if e != nil || wire.Digest(raw) != r.PlanSHA256 || wire.StrictJSON(raw, &p) != nil || !liveDatabaseIdentity(liveDatabasePlanRecord(p), r) {
		return p, failure("OFFLINE_SCOPE_LIVE_DATABASE_PLAN_UNAVAILABLE_OR_CHANGED")
	}
	return p, l.validateLiveDatabasePlan(p)
}
func (l *locked) verifyLiveDatabasePlans() error {
	seen := map[string]bool{}
	for _, event := range l.events {
		r := event.RestoredLiveDatabase
		if r == nil || seen[r.GenerationID] {
			continue
		}
		seen[r.GenerationID] = true
		if _, e := l.readLiveDatabasePlan(*r); e != nil {
			// An uncompleted first publication has no SDK effects. Its unusable
			// metadata cannot authorize execution, but must not block explicit
			// metadata-only abort or historical readback after a fresh generation.
			generationEvents := []Event{}
			for _, item := range l.events {
				if item.RestoredLiveDatabase != nil && item.RestoredLiveDatabase.GenerationID == r.GenerationID {
					generationEvents = append(generationEvents, item)
				}
			}
			g := liveDatabaseState(generationEvents)
			if g.record != nil && g.record.Step == 0 && len(g.completed) == 0 {
				continue
			}
			return e
		}
	}
	return nil
}
