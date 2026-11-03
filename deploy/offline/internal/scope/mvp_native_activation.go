package scope

import (
	"path"
	"reflect"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// MVP activation follows, rather than changes, the original readonly native
// SQL/database/stores qualifications. The original fixed PrepareRestored task
// is the only role/grant/audit writer. These hashes contain no credentials.
type mvpNativeRuntimeActivation struct {
	SQLProofSHA256          string `json:"sql_proof_sha256"`
	DatabaseProofSHA256     string `json:"database_proof_sha256"`
	StoresProofSHA256       string `json:"stores_proof_sha256"`
	ContextSHA256           string `json:"context_sha256"`
	RuntimeInputSHA256      string `json:"runtime_input_sha256"`
	PreparationGenerationID string `json:"preparation_generation_id,omitempty"`
	PreparationProofSHA256  string `json:"preparation_proof_sha256,omitempty"`
}

func liveDatabaseTaskKinds(native *mvpNativeRuntimeActivation) []string {
	kinds := []string{engine.DatabaseSchemaCheck, engine.DatabaseRuntimeCheck}
	if native != nil && native.PreparationProofSHA256 == "" {
		kinds = append([]string{engine.DatabaseRuntimeRestorePrepare}, kinds...)
	}
	return kinds
}

func validMVPNativeActivation(events []Event, n *mvpNativeRuntimeActivation) bool {
	if n == nil {
		return true
	}
	sql, sqlOK := restoreSQLProof(events)
	database, databaseOK := restoreDatabaseProof(events)
	stores, storesOK := restoreStoresProof(events)
	return sqlOK && databaseOK && storesOK && n.SQLProofSHA256 == sql && n.DatabaseProofSHA256 == database && n.StoresProofSHA256 == stores && wire.ValidSHA256(n.ContextSHA256) && wire.ValidSHA256(n.RuntimeInputSHA256) &&
		((n.PreparationGenerationID == "" && n.PreparationProofSHA256 == "") || (identityPattern.MatchString(n.PreparationGenerationID) && wire.ValidSHA256(n.PreparationProofSHA256)))
}

func mvpNativeActivationIdentity(n mvpNativeRuntimeActivation) mvpNativeRuntimeActivation {
	n.PreparationGenerationID, n.PreparationProofSHA256 = "", ""
	return n
}

// Bind both the exact typed runtime configuration and its actual protected DSN
// bytes. TLS-only file replacement may change the overall payload, never the
// original runtime principal or its credential/configuration identity.
func mvpNativeRuntimeInputDigest(files []engine.ConfigurationFile) (string, error) {
	envelope, e := restoreEnvelope(files, "runtime.yaml")
	if e != nil {
		return "", e
	}
	reference := envelope.Database.DSNFile
	if reference != engine.DatabaseTaskDestination+"/private/"+path.Base(reference) {
		return "", failure("OFFLINE_SCOPE_MVP_RUNTIME_INPUT_CHANGED")
	}
	index := []InputFile{}
	for _, name := range []string{"runtime.yaml", path.Base(reference)} {
		raw, e := restoreNamedFile(files, name)
		if e != nil {
			return "", e
		}
		index = append(index, InputFile{Name: name, Bytes: int64(len(raw)), SHA256: wire.Digest(raw)})
	}
	return wire.Digest(encode(index)), nil
}

// A successful original task result, its preceding durable start and exact
// created actor are the once-only proof. A reference/DTO, exit from another task,
// unknown start or later absence is not proof. Abort never erases this history.
func mvpNativePreparation(events []Event, expected mvpNativeRuntimeActivation) (generation, proof string, err error) {
	var create, start *Event
	for i := range events {
		event := events[i]
		r := event.RestoredLiveDatabase
		if r == nil || r.NativeRuntime == nil {
			continue
		}
		if !reflect.DeepEqual(mvpNativeActivationIdentity(*r.NativeRuntime), mvpNativeActivationIdentity(expected)) {
			return "", "", failure("OFFLINE_SCOPE_MVP_RUNTIME_PREPARATION_INPUT_CHANGED")
		}
		if r.NativeRuntime.PreparationProofSHA256 != "" {
			continue
		}
		steps := liveDatabaseSteps(r.NativeRuntime)
		if r.Step < 0 || r.Step >= len(steps) {
			return "", "", failure("OFFLINE_SCOPE_MVP_RUNTIME_PREPARATION_PROOF_INVALID")
		}
		step := steps[r.Step]
		if event.Action != "live_database_effect" && event.Action != "live_database_reconcile" {
			continue
		}
		if step.kind != engine.DatabaseRuntimeRestorePrepare {
			continue
		}
		if step.phase == "task-create" && event.Outcome == "completed" {
			copy := event
			create = &copy
		}
		if step.phase != "task-execute" {
			continue
		}
		if event.Action == "live_database_effect" && event.Outcome == "started" {
			if start != nil || proof != "" {
				return "", "", failure("OFFLINE_SCOPE_MVP_RUNTIME_PREPARATION_REPLAY_REFUSED")
			}
			copy := event
			start = &copy
		}
		if event.Outcome == "completed" {
			if start == nil || create == nil || create.RestoredLiveDatabase.GenerationID != r.GenerationID || start.RestoredLiveDatabase.GenerationID != r.GenerationID || !utilityIDPattern.MatchString(create.RestoredLiveDatabase.ObjectID) ||
				(event.Action == "live_database_effect" && event.OperationID != start.OperationID) || (event.Action == "live_database_reconcile" && event.ReferenceOperationID != start.OperationID) {
				return "", "", failure("OFFLINE_SCOPE_MVP_RUNTIME_PREPARATION_PROOF_INVALID")
			}
			generation, proof = r.GenerationID, wire.Digest(encode([]Event{*create, *start, event}))
		}
	}
	if start != nil && proof == "" {
		return "", "", failure("OFFLINE_SCOPE_MVP_RUNTIME_PREPARATION_UNRESOLVED")
	}
	return generation, proof, nil
}

func (l *locked) makeMVPNativeActivation(restore RestorePlan, restored wire.RestoredRuntimeContext, files []engine.ConfigurationFile, contextSHA string) (*mvpNativeRuntimeActivation, error) {
	native := requiresMVPNativeRestore(l.marker, restore) || restored.ApplicationProfile == wire.MVPPostgreSQLCPUApplicationProfile
	if !native {
		return nil, nil
	}
	if l.marker.Profile != wire.MVPPostgreSQLCPUProfile || restore.Source.Profile != wire.MVPPostgreSQLCPUProfile || restore.Source.NativePostgreSQL == nil || !restored.Valid() || restored.ApplicationProfile != wire.MVPPostgreSQLCPUApplicationProfile || restored.RestoreID != restore.AttemptID || restored.TargetScopeID != l.marker.ScopeID || restored.SourceScopeID != restore.Source.SourceScopeID {
		return nil, failure("OFFLINE_SCOPE_MVP_RUNTIME_NATIVE_CONTEXT_REQUIRED")
	}
	input, e := mvpNativeRuntimeInputDigest(files)
	if e != nil {
		return nil, e
	}
	return l.bindMVPNativeActivation(restore, restored, contextSHA, input)
}

func (l *locked) bindMVPNativeActivation(restore RestorePlan, restored wire.RestoredRuntimeContext, contextSHA, input string) (*mvpNativeRuntimeActivation, error) {
	native := requiresMVPNativeRestore(l.marker, restore) || restored.ApplicationProfile == wire.MVPPostgreSQLCPUApplicationProfile
	if !native {
		return nil, nil
	}
	if l.marker.Profile != wire.MVPPostgreSQLCPUProfile || restore.Source.Profile != wire.MVPPostgreSQLCPUProfile || restore.Source.NativePostgreSQL == nil || !restored.Valid() || restored.ApplicationProfile != wire.MVPPostgreSQLCPUApplicationProfile || restored.RestoreID != restore.AttemptID || restored.TargetScopeID != l.marker.ScopeID || restored.SourceScopeID != restore.Source.SourceScopeID {
		return nil, failure("OFFLINE_SCOPE_MVP_RUNTIME_NATIVE_CONTEXT_REQUIRED")
	}

	sql, _ := restoreSQLProof(l.events)
	database, _ := restoreDatabaseProof(l.events)
	stores, _ := restoreStoresProof(l.events)
	n := &mvpNativeRuntimeActivation{SQLProofSHA256: sql, DatabaseProofSHA256: database, StoresProofSHA256: stores, ContextSHA256: contextSHA, RuntimeInputSHA256: input}
	if !validMVPNativeActivation(l.events, n) {
		return nil, failure("OFFLINE_SCOPE_MVP_RUNTIME_READONLY_QUALIFICATIONS_REQUIRED")
	}
	var e error
	n.PreparationGenerationID, n.PreparationProofSHA256, e = mvpNativePreparation(l.events, *n)
	if e != nil {
		return nil, e
	}
	return n, nil
}

// Historical plan validation uses its exact bound file index, including current
// TLS projection overlays, without rereading credentials or requiring live peers.
func mvpNativeRuntimeInputDigestAtIndex(files []InputFile, original []engine.ConfigurationFile) (string, error) {
	envelope, e := restoreEnvelope(original, "runtime.yaml")
	if e != nil {
		return "", e
	}
	index := []InputFile{}
	for _, name := range []string{"runtime.yaml", path.Base(envelope.Database.DSNFile)} {
		found := false
		for _, file := range files {
			if file.Name == name {
				if found || file.Bytes <= 0 || !wire.ValidSHA256(file.SHA256) {
					return "", failure("OFFLINE_SCOPE_MVP_RUNTIME_INPUT_CHANGED")
				}
				found = true
				index = append(index, file)
			}
		}
		if !found {
			return "", failure("OFFLINE_SCOPE_MVP_RUNTIME_INPUT_CHANGED")
		}
	}
	return wire.Digest(encode(index)), nil
}
