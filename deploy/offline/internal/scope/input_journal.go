package scope

import (
	"context"
	"os"
	"path/filepath"

	"github.com/Passion-Flow/ownsolid/tools/release/offline/pkg/bundle"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/compose"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
)

func inputsState(events []Event) (*InputsRecord, string, string) {
	var record *InputsRecord
	outcome := ""
	operation := ""
	for _, e := range events {
		if e.Action == "inputs_prepare" {
			record = e.Inputs
			outcome = e.Outcome
			operation = e.OperationID
		}
		if e.Action == "inputs_reconcile" {
			record = e.Inputs
			outcome = "completed"
		}
	}
	return record, outcome, operation
}
func validateInputsEvent(events []Event, e Event) error {
	bad := func() error { return failure("OFFLINE_SCOPE_INPUT_JOURNAL_TRANSITION_INVALID") }
	if !validInputsRecord(e.Inputs) || e.Process != nil || e.Engine != nil || e.Image != nil || e.Runtime != nil || e.Volume != nil || len(events) < 4 {
		return bad()
	}
	state := imageState(events)
	image, admitted := state.completed[e.Inputs.ImageAssetID]
	if state.engine == nil || len(state.pending) > 0 || !admitted || image.ManifestDigest != e.Inputs.ImageID || image.ConfigDigest != e.Inputs.ImageConfigDigest {
		return bad()
	}
	prior, outcome, operation := inputsState(events)
	if e.Action == "inputs_prepare" {
		if e.ReferenceOperationID != "" {
			return bad()
		}
		if e.Outcome == "started" {
			if prior != nil || e.Code != "" {
				return bad()
			}
			for _, p := range events {
				if p.OperationID == e.OperationID {
					return bad()
				}
			}
			return nil
		}
		if prior == nil || outcome != "started" || operation != e.OperationID || *prior != *e.Inputs {
			return bad()
		}
		if e.Outcome == "completed" && e.Code == "" || e.Outcome == "uncertain" && codePattern.MatchString(e.Code) {
			return nil
		}
		return bad()
	}
	if e.Action == "inputs_reconcile" && prior != nil && outcome != "completed" && e.Outcome == "completed" && e.Code == "" && e.ReferenceOperationID == operation && *prior == *e.Inputs {
		for _, p := range events {
			if p.OperationID == e.OperationID {
				return bad()
			}
		}
		return nil
	}
	return bad()
}

type StorageInputsStatus struct {
	Status        string `json:"status"`
	ScopeID       string `json:"scope_id"`
	Profile       string `json:"profile"`
	Roles         int    `json:"roles"`
	Qualification string `json:"qualification"`
}

func inputStatus(l *locked, p preparedInputs) StorageInputsStatus {
	return StorageInputsStatus{Status: "prepared_immutable_storage_inputs_only", ScopeID: l.marker.ScopeID, Profile: l.marker.Profile, Roles: len(p.descriptor.Roles), Qualification: "private_input_snapshot_not_initialized_volumes_namespaces_application_or_target"}
}
func PrepareStorageInputs(ctx context.Context, c configuration.Config, reconcile bool) (StorageInputsStatus, error) {
	if ctx.Err() != nil {
		return StorageInputsStatus{}, failure("OFFLINE_SCOPE_OPERATION_INTERRUPTED")
	}
	l, e := open(c, true)
	if e != nil {
		return StorageInputsStatus{}, e
	}
	defer l.close()
	if len(l.events) < 3 || l.events[2].Outcome != "completed" {
		return StorageInputsStatus{}, failure("OFFLINE_SCOPE_COMPLETED_PREPARATION_REQUIRED")
	}
	verified, e := bundle.VerifyPrepared(ctx, c.Verification, filepath.Join(c.ScopeRoot, "release"), c.ExpectedArchiveSHA256, c.ExpectedManifestSHA256)
	if e != nil {
		return StorageInputsStatus{}, e
	}
	if verified.Profile != l.marker.Profile || verified.ReleaseID != l.marker.ReleaseID {
		return StorageInputsStatus{}, failure("OFFLINE_SCOPE_RELEASE_CONTEXT_MISMATCH")
	}
	if e = validateImageAssets(l.events, verified.Manifest); e != nil {
		return StorageInputsStatus{}, e
	}
	if record, outcome, operation := inputsState(l.events); record != nil {
		if outcome != "completed" && !reconcile {
			return StorageInputsStatus{}, failure("OFFLINE_SCOPE_INPUT_RECONCILIATION_REQUIRED")
		}
		p, e := l.readInputs(ctx, *record)
		if e != nil {
			return StorageInputsStatus{}, e
		}
		if outcome == "completed" {
			return inputStatus(l, p), nil
		}
		if e = l.syncSnapshotFiles(p); e != nil {
			return StorageInputsStatus{}, e
		}
		id, e := nonce()
		if e != nil {
			return StorageInputsStatus{}, e
		}
		if e = l.appendEvent(Event{OperationID: id, Action: "inputs_reconcile", Outcome: "completed", ReferenceOperationID: operation, Inputs: record}); e != nil {
			return StorageInputsStatus{}, e
		}
		if ctx.Err() != nil {
			return StorageInputsStatus{}, failure("OFFLINE_SCOPE_COMMIT_UNCERTAIN")
		}
		return inputStatus(l, p), nil
	}
	if reconcile {
		return StorageInputsStatus{}, failure("OFFLINE_SCOPE_NO_INPUT_SNAPSHOT_TO_RECONCILE")
	}
	if c.Engine == nil || c.Compose == nil {
		return StorageInputsStatus{}, failure("OFFLINE_SCOPE_STORAGE_INPUT_CONFIGURATION_REQUIRED")
	}
	client, e := engine.New(*c.Engine)
	if e != nil {
		return StorageInputsStatus{}, e
	}
	defer client.Close()
	renderer, e := compose.New(c)
	if e != nil {
		return StorageInputsStatus{}, e
	}
	p, e := l.collectInputs(ctx, client, renderer)
	if e != nil {
		return StorageInputsStatus{}, e
	}
	return prepareInputs(ctx, l, p)
}
func prepareInputs(ctx context.Context, l *locked, p preparedInputs) (StorageInputsStatus, error) {
	if ctx.Err() != nil {
		return StorageInputsStatus{}, failure("OFFLINE_SCOPE_OPERATION_INTERRUPTED")
	}
	id, e := nonce()
	if e != nil {
		return StorageInputsStatus{}, e
	}
	if e = l.appendEvent(Event{OperationID: id, Action: "inputs_prepare", Outcome: "started", Inputs: &p.record}); e != nil {
		return StorageInputsStatus{}, e
	}
	e = l.snapshotInputs(ctx, p)
	if e == nil {
		_, e = l.readInputs(ctx, p.record)
	}
	if e != nil {
		if journalError := l.appendEvent(Event{OperationID: id, Action: "inputs_prepare", Outcome: "uncertain", Code: safeEffectCode(e), Inputs: &p.record}); journalError != nil {
			return StorageInputsStatus{}, journalError
		}
		return StorageInputsStatus{}, e
	}
	if e = l.appendEvent(Event{OperationID: id, Action: "inputs_prepare", Outcome: "completed", Inputs: &p.record}); e != nil {
		return StorageInputsStatus{}, e
	}
	if ctx.Err() != nil {
		return StorageInputsStatus{}, failure("OFFLINE_SCOPE_COMMIT_UNCERTAIN")
	}
	return inputStatus(l, p), nil
}
func (l *locked) syncSnapshotFiles(p preparedInputs) error {
	names := []string{"inputs/model.json", "inputs/catalog.json", "inputs/descriptor.json"}
	for _, role := range p.descriptor.Roles {
		for _, file := range role.Files {
			names = append(names, "inputs/payloads/"+role.Key+"/"+file.Name)
		}
	}
	for _, name := range names {
		file, e := l.root.OpenFile(name, os.O_RDWR, 0)
		if e != nil {
			return failure("OFFLINE_SCOPE_INPUT_SNAPSHOT_COMMIT_UNCERTAIN")
		}
		e = file.Sync()
		ce := file.Close()
		if e != nil || ce != nil {
			return failure("OFFLINE_SCOPE_INPUT_SNAPSHOT_COMMIT_UNCERTAIN")
		}
	}
	for _, role := range p.descriptor.Roles {
		if e := l.syncInputDirectory("inputs/payloads/" + role.Key); e != nil {
			return e
		}
	}
	for _, name := range []string{"inputs/payloads", inputsDirectory, "."} {
		if e := l.syncInputDirectory(name); e != nil {
			return e
		}
	}
	return nil
}
