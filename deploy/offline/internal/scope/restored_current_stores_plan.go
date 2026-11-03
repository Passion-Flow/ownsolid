package scope

import (
	"context"
	"reflect"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

func currentStoresPlanFile(generation string) string { return "current-stores-" + generation + ".json" }
func currentStoresPlanRecord(p RestoredCurrentStoresPlan) RestoredCurrentStoresRecord {
	count := 0
	if len(p.Tasks) == 4 {
		count = 4
	}
	return RestoredCurrentStoresRecord{StoreCount: count, GenerationID: p.GenerationID, PlanSHA256: wire.Digest(encode(p)), Admission: p.Admission, DatabaseProofSHA256: p.DatabaseProofSHA256, SelectedStoresProofSHA256: p.SelectedStoresProofSHA256}
}

// Historical store plans bind the SQL proof at their own generation prefix.
// A subsequent explicit SQL requalification never rewrites or invalidates that
// history; it must still be checked afresh before a new execution/activation.
func (l *locked) currentStoresPlanContext(generation string) *locked {
	view := *l
	for i, e := range l.events {
		if e.RestoredCurrentStores != nil && e.RestoredCurrentStores.GenerationID == generation {
			view.events = l.events[:i]
			break
		}
	}
	return &view
}
func (l *locked) validateCurrentStoresPlan(p RestoredCurrentStoresPlan) error {
	bad := func() error { return failure("OFFLINE_SCOPE_CURRENT_STORES_PLAN_CHANGED") }
	view := l.currentStoresPlanContext(p.GenerationID)
	databaseProof, ok := liveDatabaseProof(view.events)
	state := liveDatabaseState(view.events)
	storesProof, storesOK := restoreStoresProof(view.events)
	if !ok || !storesOK || state.record == nil || p.FormatVersion != 1 || p.ScopeID != l.marker.ScopeID || !identityPattern.MatchString(p.GenerationID) || p.DatabaseProofSHA256 != databaseProof || p.DatabasePlanSHA256 != state.record.PlanSHA256 || p.SelectedStoresProofSHA256 != storesProof {
		return bad()
	}
	database, e := view.readLiveDatabasePlan(*state.record)
	if e != nil {
		return e
	}
	stores, e := view.readRestoreStoresPlan()
	if e != nil {
		return e
	}
	if p.SelectedStoresPlanSHA256 != wire.Digest(encode(stores)) || !reflect.DeepEqual(p.Admission, liveDatabasePlanRecord(database).Admission) || p.PayloadSHA256 != database.PayloadSHA256 || p.ContextSHA256 != database.ContextSHA256 || p.PostgresPID != database.PostgresPID || p.PostgresStartedAt != database.PostgresStartedAt || p.Initializer != database.Initializer || !reflect.DeepEqual(p.Files, database.Files) {
		return bad()
	}
	inputs, model, e := view.replacementModels(context.Background())
	if e != nil {
		return e
	}
	if e = currentSelectedStoreProfile(inputs.files["control-config"], l.marker.Profile); e != nil {
		return e
	}
	database.GenerationID = p.GenerationID
	database.Tasks = append([]engine.DatabaseTask{}, database.Tasks...)
	for i := range database.Tasks {
		database.Tasks[i].Owner.OperationID = p.GenerationID
		database.Tasks[i].VolumeOwner = engine.Ownership{ScopeID: p.ScopeID, OperationID: p.GenerationID, Role: engine.StoreRestoreConfigRole + "-" + p.GenerationID}
	}
	expected, e := assembleCurrentStoreTasks(view, stores, database, model)
	if e != nil {
		return e
	}
	if !reflect.DeepEqual(expected, p.Tasks) {
		return bad()
	}
	return nil
}
func (l *locked) readCurrentStoresPlan(r RestoredCurrentStoresRecord) (RestoredCurrentStoresPlan, error) {
	var p RestoredCurrentStoresPlan
	raw, e := regular(l.root, currentStoresPlanFile(r.GenerationID), l.config.Verification.MaximumMetadataBytes)
	if e != nil || wire.Digest(raw) != r.PlanSHA256 || wire.StrictJSON(raw, &p) != nil || !currentStoresIdentity(currentStoresPlanRecord(p), r) {
		return p, failure("OFFLINE_SCOPE_CURRENT_STORES_PLAN_UNAVAILABLE_OR_CHANGED")
	}
	return p, l.validateCurrentStoresPlan(p)
}
func (l *locked) verifyCurrentStoresPlans() error {
	seen := map[string]bool{}
	for _, event := range l.events {
		r := event.RestoredCurrentStores
		if r == nil || seen[r.GenerationID] {
			continue
		}
		seen[r.GenerationID] = true
		if _, e := l.readCurrentStoresPlan(*r); e != nil {
			generationEvents := []Event{}
			for _, item := range l.events {
				if item.RestoredCurrentStores != nil && item.RestoredCurrentStores.GenerationID == r.GenerationID {
					generationEvents = append(generationEvents, item)
				}
			}
			s := currentStoresState(generationEvents)
			// Before publication completion no SDK intent is possible. Keep an
			// interrupted private metadata file for explicit metadata-only abort.
			if s.record != nil && s.record.Step == 0 && len(s.completed) == 0 {
				continue
			}
			return e
		}
	}
	return nil
}
