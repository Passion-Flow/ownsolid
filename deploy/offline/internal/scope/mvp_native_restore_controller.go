package scope

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

func (l *locked) nativeRestoreStageState(stage string) (pending string, complete bool, completed int) {
	switch stage {
	case "sql":
		s := restoreSQLState(l.events)
		return s.pending, s.complete, len(s.completed)
	case "database":
		s := restoreDatabaseState(l.events)
		return s.pending, s.complete, len(s.completed)
	case "stores":
		s := restoreStoresState(l.events)
		return s.pending, s.complete, len(s.completed)
	}
	return "", false, 0
}
func (l *locked) nativeRestorePhysicalID(stage, role string) string {
	if stage == "sql" {
		for _, r := range restoreSQLState(l.events).completed {
			if r.Phase == "task-create" && r.Role == role {
				return r.ObjectID
			}
		}
	}
	if stage == "database" {
		for _, r := range restoreDatabaseState(l.events).completed {
			if r.Phase == "task-create" {
				return r.ObjectID
			}
		}
	}
	if stage == "stores" {
		for _, r := range restoreStoresState(l.events).completed {
			if r.Phase == "task-create" {
				return r.ObjectID
			}
		}
	}
	return ""
}
func (l *locked) nativeRestoreObservedSHA(stage string) string {
	if stage == "sql" {
		return restoreSQLState(l.events).completed[5].ResultSHA256
	}
	if stage == "database" {
		return restoreDatabaseState(l.events).completed[1].ResultSHA256
	}
	return restoreStoresState(l.events).completed[1].ResultSHA256
}

func (l *locked) nativeRestoreObservation(ctx context.Context, stage string) (engine.NativePostgreSQLRestoreObservation, error) {
	step := 1
	if stage == "sql" {
		step = 5
	}
	r, id, e := l.nativeRestoreReceipt(stage, step)
	if e != nil || r.Observation == nil {
		return engine.NativePostgreSQLRestoreObservation{}, failure("OFFLINE_SCOPE_MVP_NATIVE_RESTORE_OBSERVATION_REQUIRED")
	}
	plan, e := l.readBoundRestorePlan()
	if e != nil {
		return engine.NativePostgreSQLRestoreObservation{}, e
	}
	x, e := l.readMVPNativeRestoreExecution(ctx)
	if e != nil {
		return engine.NativePostgreSQLRestoreObservation{}, e
	}
	complete, e := l.readMVPNativeRestoreOriginal(ctx, plan)
	if e != nil {
		return engine.NativePostgreSQLRestoreObservation{}, e
	}
	o, e := readMVPNativeRestoreObservation(ctx, l.root, id, *r.Observation, x.NativeRequest.Policy.MaximumManifestBytes, plan.AttemptID, wire.Digest(encode(x.NativeRequest)), plan.Source, nil)
	if e != nil {
		return o, e
	}
	_, _, connection, e := l.nativeRestoreInputs(ctx)
	if e != nil {
		return o, e
	}
	o, e = bindMVPNativeRestoreCompletedObservation(plan, l.marker.ScopeID, x.NativeRequest, connection, x.SourceArchiveSHA256, complete, o)
	if e != nil {
		return o, e
	}
	if stage != "sql" {
		prior := "sql"
		if stage == "stores" {
			prior = "database"
		}
		p, e := l.nativeRestoreObservation(ctx, prior)
		if e != nil {
			return o, e
		}
		if !sameMVPNativeRestore(p.NativePostgreSQL, o.NativePostgreSQL) {
			return o, failure("OFFLINE_SCOPE_MVP_NATIVE_RESTORE_STATIC_IDENTITY_CHANGED")
		}
	}
	return o, nil
}

// Historical reopen verifies custody only. Old post-image observations must not
// be rerun after activation's legitimate role/audit/serving writes.
func (l *locked) verifyMVPNativeRestoreHistory(ctx context.Context, stage string) error {
	p, e := l.readMVPNativeRestorePreparation()
	if e != nil {
		return e
	}
	_ = p
	if _, ok := restoreSQLState(l.events).completed[0]; !ok {
		return nil
	}
	if _, e = l.readMVPNativeRestoreExecution(ctx); e != nil {
		return e
	}
	if _, ok := restoreSQLState(l.events).completed[2]; ok {
		plan, e := l.readBoundRestorePlan()
		if e != nil {
			return e
		}
		if _, e = l.readMVPNativeRestoreOriginal(ctx, plan); e != nil {
			return e
		}
	}
	if stage == "sql" {
		if _, ok := restoreSQLState(l.events).completed[5]; !ok {
			return nil
		}
	} else if stage == "database" {
		if _, ok := restoreDatabaseState(l.events).completed[1]; !ok {
			return nil
		}
	} else {
		if _, ok := restoreStoresState(l.events).completed[1]; !ok {
			return nil
		}
	}
	_, e = l.nativeRestoreObservation(ctx, stage)
	return e
}

// Each original stage effect has its own durable intent. Reconcile checks only
// an already published receipt or the original actor's absence: no create,
// start, removal, publication or progression into a following mutation occurs.
func (l *locked) runMVPNativeRestoreStage(ctx context.Context, plan RestorePlan, stage, mode string, client any, worker runtimeOperator) error {
	if !validMVPNativeRestoreSource(plan.Source) {
		return mvpNativeRestoreRequired()
	}
	c, ok := client.(mvpNativeRestoreEngine)
	if !ok {
		return failure("OFFLINE_SCOPE_MVP_NATIVE_RESTORE_ENGINE_REQUIRED")
	}
	pending, complete, _ := l.nativeRestoreStageState(stage)
	aborted := false
	switch stage {
	case "sql":
		aborted = restoreSQLState(l.events).aborted
	case "database":
		aborted = restoreDatabaseState(l.events).aborted
	case "stores":
		aborted = restoreStoresState(l.events).aborted
	}
	if aborted {
		if strings.HasPrefix(mode, "abort-") {
			return nil
		}
		return failure("OFFLINE_SCOPE_MVP_NATIVE_RESTORE_ABORTED_NEW_TARGET_REQUIRED")
	}

	if complete {
		return l.verifyMVPNativeRestoreHistory(ctx, stage)
	}
	if strings.HasPrefix(mode, "abort-") {
		return l.abortMVPNativeRestoreStage(ctx, c, plan, stage)
	}
	reconcile := strings.HasPrefix(mode, "reconcile-")
	if pending != "" && !reconcile {
		return failure("OFFLINE_SCOPE_MVP_NATIVE_RESTORE_RECONCILIATION_REQUIRED")
	}
	if reconcile && pending == "" {
		return failure("OFFLINE_SCOPE_MVP_NATIVE_RESTORE_NOT_PENDING")
	}
	var prep mvpNativeRestorePreparation
	s := restoreSQLState(l.events)
	if s.record == nil {
		if stage != "sql" || reconcile {
			return failure("OFFLINE_SCOPE_MVP_NATIVE_RESTORE_SQL_REQUIRED")
		}
		// Current runtime/lineage admission occurs while no native actor exists.
		v, e := l.verifyRuntimeContext(ctx, c, worker, false)
		if e != nil {
			return e
		}
		if e = restoreTargetLineage(l.marker, l.events, v, plan.Source, plan.Compatibility); e != nil {
			return e
		}
		prep, e = l.makeMVPNativeRestorePreparation(ctx, plan)
		if e != nil {
			return e
		}
		if e = backupDiskAdmission(filepath.Join(l.config.ScopeRoot, restorePlanFile), plan.Request.MaximumBytes); e != nil {
			return e
		}
		if e = writeBackupFile(l.root, mvpNativeRestorePreparationFile, encode(prep)); e != nil {
			return e
		}
	} else {
		var e error
		prep, e = l.readMVPNativeRestorePreparation()
		if e != nil {
			return e
		}
	}
	native := &MVPNativeRestoreStageRecord{FormatVersion: 1, Stage: stage, ExecutionSHA256: wire.Digest(encode(prep)), OriginalRestoreID: plan.AttemptID}
	var sql RestoreSQLRecord
	var db RestoreDatabaseRecord
	var stores RestoreStoresRecord
	var phases, roles, kinds []string
	switch stage {
	case "sql":
		sql = RestoreSQLRecord{Native: native, PlanSHA256: wire.Digest(encode(plan)), Initializer: prep.Initializer}
		for _, v := range restoreSQLStepsForRecord(&sql) {
			phases = append(phases, v.phase)
			roles = append(roles, v.role)
			kinds = append(kinds, "")
		}
	case "database":
		if _, ok := restoreSQLProof(l.events); !ok {
			return failure("OFFLINE_SCOPE_RESTORE_QUALIFICATION_SQL_REQUIRED")
		}
		var e error
		db, _, e = l.makeRestoreDatabaseRecord(ctx, plan)
		if e != nil {
			return e
		}
		for _, v := range restoreDatabaseStepsForRecord(&db) {
			phases = append(phases, v.phase)
			roles = append(roles, "native-observer")
			kinds = append(kinds, v.kind)
		}
	case "stores":
		p, e := l.makeMVPNativeRestoreStorePlan(ctx, plan)
		if e != nil {
			return e
		}
		stores = restoreStoresPlanRecord(p)
		if restoreStoresState(l.events).record == nil {
			if e = writeBackupFile(l.root, restoreStoresPlanFile, encode(p)); e != nil {
				return e
			}
		}
		for _, v := range restoreStoresStepsForRecord(&stores) {
			phases = append(phases, v.phase)
			roles = append(roles, v.role)
			kinds = append(kinds, v.kind)
		}
	default:
		return failure("OFFLINE_SCOPE_MVP_NATIVE_RESTORE_STAGE_INVALID")
	}
	if e := admitProcessJournal(l, len(phases)*3+3); e != nil {
		return e
	}
	for step, phase := range phases {
		currentPending, _, done := l.nativeRestoreStageState(stage)
		if step < done {
			continue
		}
		id, e := nonce()
		if e != nil {
			return e
		}
		reference, action := "", "restore_"+stage+"_effect"
		if stage == "database" {
			action = "restore_database_effect"
		}
		if stage == "stores" {
			action = "restore_stores_effect"
		}
		if reconcile {
			reference = currentPending
			action = strings.TrimSuffix(action, "_effect") + "_reconcile"
			if reference == "" {
				return failure("OFFLINE_SCOPE_MVP_NATIVE_RESTORE_NOT_PENDING")
			}
		}
		event := Event{OperationID: id, ReferenceOperationID: reference, Action: action, Outcome: "started"}
		sql.Step, sql.Phase, sql.Role, sql.ObjectID, sql.ResultSHA256 = step, phase, roles[step], "", ""
		db.Step, db.Phase, db.Kind, db.ObjectID, db.ResultSHA256 = step, phase, kinds[step], "", ""
		stores.Step, stores.Phase, stores.Role, stores.Kind, stores.ObjectID, stores.ResultSHA256 = step, phase, roles[step], kinds[step], "", ""
		attach := func() {
			switch stage {
			case "sql":
				r := sql
				event.RestoreSQL = &r
			case "database":
				r := db
				event.RestoreDatabase = &r
			case "stores":
				r := stores
				event.RestoreStores = &r
			}
		}
		attach()
		if !reconcile {
			if e = l.appendEvent(event); e != nil {
				return e
			}
		}
		operation := id
		if reconcile {
			operation = reference
		}
		object, result, e := l.executeMVPNativeRestoreStageStep(ctx, c, plan, prep, stage, step, phase, roles[step], operation, reconcile)
		if _, binding := l.checkEngine(ctx, c); binding != nil {
			e = binding
		}
		sql.ObjectID, sql.ResultSHA256 = object, result
		db.ObjectID, db.ResultSHA256 = object, result
		stores.ObjectID, stores.ResultSHA256 = object, result
		attach()
		event.Outcome = "completed"
		if e != nil {
			event.Outcome = "uncertain"
			if reconcile {
				event.Outcome = "unresolved"
			}
			event.Code = "OFFLINE_SCOPE_MVP_NATIVE_RESTORE_EFFECT_UNRESOLVED"
			sql.ObjectID, sql.ResultSHA256 = "", ""
			db.ObjectID, db.ResultSHA256 = "", ""
			stores.ObjectID, stores.ResultSHA256 = "", ""
			attach()
		}
		if je := l.appendEvent(event); je != nil {
			return je
		}
		if e != nil {
			return e
		}
		if reconcile {
			return nil
		}
	}
	return nil
}

func (l *locked) executeMVPNativeRestoreStageStep(ctx context.Context, c mvpNativeRestoreEngine, plan RestorePlan, p mvpNativeRestorePreparation, stage string, step int, phase, role, operation string, reconcile bool) (string, string, error) {
	if ctx.Err() != nil {
		return "", "", failure("OFFLINE_SCOPE_OPERATION_INTERRUPTED")
	}
	receipt := mvpNativeRestoreReceipt{FormatVersion: 1, Stage: stage, Step: step}
	save := func() (string, string, error) {
		raw := encode(receipt)
		if e := writeBackupFile(l.root, nativeRestoreReceiptName(operation), raw); e != nil {
			return "", "", e
		}
		return "", wire.Digest(raw), nil
	}
	if phase == "native-prepare" {
		if reconcile {
			return "", "", failure("OFFLINE_SCOPE_MVP_NATIVE_RESTORE_PREPARATION_UNRESOLVED")
		}
		x, e := l.prepareMVPNativeRestore(ctx, c, plan, p)
		if e != nil {
			return "", "", e
		}
		file := InputFile{Name: mvpNativeRestoreExecutionFile, Bytes: int64(len(encode(x))), SHA256: wire.Digest(encode(x))}
		receipt.Execution = &file
		return save()
	}
	x, e := l.readMVPNativeRestoreExecution(ctx)
	if e != nil {
		return "", "", e
	}
	task := x.Writer
	if role != "native-writer" {
		task.Kind = engine.NativePostgreSQLRestoreVerify
		task.Owner.Role = "database-" + task.Kind
	}
	id := l.nativeRestorePhysicalID(stage, role)
	if reconcile {
		// Successful payload custody can be read back, never reconstructed. Missing
		// output remains unresolved even if another actor now has the same name.
		if phase == "task-execute" {
			raw, e := regular(l.root, nativeRestoreReceiptName(operation), l.config.Verification.MaximumMetadataBytes)
			if e != nil {
				return "", "", failure("OFFLINE_SCOPE_MVP_NATIVE_RESTORE_ORIGINAL_OUTPUT_UNRESOLVED")
			}
			var r mvpNativeRestoreReceipt
			if wire.StrictJSON(raw, &r) != nil || r.Stage != stage || r.Step != step || r.PhysicalID != id {
				return "", "", failure("OFFLINE_SCOPE_MVP_NATIVE_RESTORE_RECEIPT_CHANGED")
			}
			actual, found, e := c.InspectDatabaseTask(ctx, task)
			if e != nil || !found || !mvpNativeRestoreActorExited(actual, id) {
				return "", "", failure("OFFLINE_SCOPE_MVP_NATIVE_RESTORE_ACTOR_UNRESOLVED")
			}
			if role == "native-writer" {
				if _, e := l.readMVPNativeRestoreOriginalReceipt(ctx, plan, x, r, operation); e != nil {
					return "", "", e
				}
			} else {
				if r.Observation == nil || r.Completion != nil || r.Stdout != nil || r.Execution != nil {
					return "", "", failure("OFFLINE_SCOPE_MVP_NATIVE_RESTORE_RECEIPT_CHANGED")
				}
				complete, e := l.readMVPNativeRestoreOriginal(ctx, plan)
				if e != nil {
					return "", "", e
				}
				observed, e := readMVPNativeRestoreObservation(ctx, l.root, operation, *r.Observation, x.NativeRequest.Policy.MaximumManifestBytes, plan.AttemptID, wire.Digest(encode(x.NativeRequest)), plan.Source, nil)
				if e != nil {
					return "", "", e
				}
				_, _, connection, e := l.nativeRestoreInputs(ctx)
				if e != nil {
					return "", "", e
				}
				if _, e = bindMVPNativeRestoreCompletedObservation(plan, l.marker.ScopeID, x.NativeRequest, connection, x.SourceArchiveSHA256, complete, observed); e != nil {
					return "", "", e
				}
				if stage != "sql" {
					prior := "sql"
					if stage == "stores" {
						prior = "database"
					}
					previous, e := l.nativeRestoreObservation(ctx, prior)
					if e != nil {
						return "", "", e
					}
					if !sameMVPNativeRestore(previous.NativePostgreSQL, observed.NativePostgreSQL) {
						return "", "", failure("OFFLINE_SCOPE_MVP_NATIVE_RESTORE_STATIC_IDENTITY_CHANGED")
					}
				}
			}
			after, found, e := c.InspectDatabaseTask(ctx, task)
			if e != nil || !found || after.State != actual.State || after.ID != id {
				return "", "", failure("OFFLINE_SCOPE_MVP_NATIVE_RESTORE_ACTOR_CHANGED")
			}
			return "", wire.Digest(raw), nil
		}
		if phase == "complete" {
			if e := l.verifyMVPNativeRestoreHistory(ctx, stage); e != nil {
				return "", "", e
			}
			if stage == "stores" {
				for _, owner := range x.TemporaryOwners {
					if _, found, e := c.InspectVolume(ctx, owner); e != nil {
						return "", "", e
					} else if found {
						return "", "", failure("OFFLINE_SCOPE_MVP_NATIVE_RESTORE_VOLUME_REMOVAL_UNRESOLVED")
					}
				}
			}
			return "", l.nativeRestoreObservedSHA(stage), nil
		}
		if phase == "task-remove" {
			_, found, e := c.InspectDatabaseTask(ctx, task)
			if e != nil {
				return "", "", e
			}
			if !found && utilityIDPattern.MatchString(id) {
				return "", "", nil
			}
		}
		return "", "", failure("OFFLINE_SCOPE_MVP_NATIVE_RESTORE_EFFECT_UNRESOLVED")
	}
	switch phase {
	case "task-create":
		if _, found, e := c.InspectDatabaseTask(ctx, task); e != nil {
			return "", "", e
		} else if found {
			return "", "", failure("OFFLINE_SCOPE_MVP_NATIVE_RESTORE_FRESH_ACTOR_REQUIRED")
		}
		actor, e := c.CreateDatabaseTask(ctx, task)
		if e != nil {
			return "", "", e
		}
		if !utilityIDPattern.MatchString(actor.ID) {
			return "", "", failure("OFFLINE_SCOPE_MVP_NATIVE_RESTORE_ACTOR_CHANGED")
		}
		actual, found, e := c.InspectDatabaseTask(ctx, task)
		if e != nil || !found || actual.ID != actor.ID || actual.State.Status != "created" {
			return "", "", failure("OFFLINE_SCOPE_MVP_NATIVE_RESTORE_ACTOR_CHANGED")
		}
		return actor.ID, "", nil
	case "task-execute":
		if !utilityIDPattern.MatchString(id) {
			return "", "", failure("OFFLINE_SCOPE_MVP_NATIVE_RESTORE_ACTOR_ID_REQUIRED")
		}
		receipt.PhysicalID = id
		files, e := l.nativeRestoreTaskFiles(ctx, x)
		if e != nil {
			return "", "", e
		}
		_, _, connection, e := l.nativeRestoreInputs(ctx)
		if e != nil {
			return "", "", e
		}
		if role == "native-writer" {
			attach := func(ctx context.Context, t engine.DatabaseTask, id string) (mvpNativeRestoreOutput, error) {
				return c.ObserveDatabaseTaskOutput(ctx, t, id)
			}
			out, complete, e := executeMVPNativeRestoreOriginalTask(ctx, c, attach, *l.config.Compose, plan, l.marker.ScopeID, x.NativeRequest, connection, x.SourceArchiveSHA256, task, id, files)
			if e != nil {
				return "", "", e
			}
			file, _, e := retainMVPNativeRestoreCompletion(ctx, l.root, operation, plan, l.marker.ScopeID, x.NativeRequest, connection, x.SourceArchiveSHA256, *complete)
			if e != nil {
				return "", "", e
			}
			receipt.Completion = &file
			raw := out.NativePostgreSQLRestoreCompletion.Original
			stdout := InputFile{Name: "mvp-native-restore-stdout-" + operation + ".json", Bytes: int64(len(raw)), SHA256: wire.Digest(raw)}
			if e = writeBackupFile(l.root, stdout.Name, raw); e != nil {
				return "", "", e
			}
			receipt.Stdout = &stdout
			return save()
		}
		complete, e := l.readMVPNativeRestoreOriginal(ctx, plan)
		if e != nil {
			return "", "", e
		}
		out, e := l.observeNativeRestoreTask(ctx, c, task, id, files)
		if e != nil {
			return "", "", e
		}
		if out.NativePostgreSQLRestore == nil {
			return "", "", failure("OFFLINE_SCOPE_MVP_NATIVE_RESTORE_OBSERVATION_REQUIRED")
		}
		observed, e := bindMVPNativeRestoreCompletedObservation(plan, l.marker.ScopeID, x.NativeRequest, connection, x.SourceArchiveSHA256, complete, *out.NativePostgreSQLRestore)
		if e != nil {
			return "", "", e
		}
		var original *wire.NativePostgreSQLRestoreReference
		if stage != "sql" {
			prior := "sql"
			if stage == "stores" {
				prior = "database"
			}
			previous, e := l.nativeRestoreObservation(ctx, prior)
			if e != nil {
				return "", "", e
			}
			original = previous.NativePostgreSQL
		}
		file, _, e := retainMVPNativeRestoreObservation(ctx, l.root, operation, observed.Original, x.NativeRequest.Policy.MaximumManifestBytes, plan.AttemptID, wire.Digest(encode(x.NativeRequest)), plan.Source, original)
		if e != nil {
			return "", "", e
		}
		receipt.Observation = &file
		return save()
	case "task-remove":
		actor, found, e := c.InspectDatabaseTask(ctx, task)
		if e != nil {
			return "", "", e
		}
		if !found || !mvpNativeRestoreActorExited(actor, id) {
			return "", "", failure("OFFLINE_SCOPE_MVP_NATIVE_RESTORE_ACTOR_UNRESOLVED")
		}
		proofStep := step - 1
		if _, _, e = l.nativeRestoreReceipt(stage, proofStep); e != nil {
			return "", "", e
		}
		if e = l.verifyMVPNativeRestoreHistory(ctx, stage); e != nil {
			return "", "", e
		}
		if e = c.RemoveDatabaseTask(ctx, task, id); e != nil {
			return "", "", e
		}
		if _, found, e = c.InspectDatabaseTask(ctx, task); e != nil {
			return "", "", e
		} else if found {
			return "", "", failure("OFFLINE_SCOPE_MVP_NATIVE_RESTORE_ACTOR_REMOVAL_UNRESOLVED")
		}
		return "", "", nil
	case "complete":
		if e = l.verifyMVPNativeRestoreHistory(ctx, stage); e != nil {
			return "", "", e
		}
		if stage == "stores" {
			for _, owner := range x.TemporaryOwners {
				if e = c.RemoveNativePostgreSQLVolume(ctx, owner); e != nil {
					return "", "", e
				}
				if _, found, e := c.InspectVolume(ctx, owner); e != nil {
					return "", "", e
				} else if found {
					return "", "", failure("OFFLINE_SCOPE_MVP_NATIVE_RESTORE_VOLUME_REMOVAL_UNRESOLVED")
				}
			}
		}
		return "", l.nativeRestoreObservedSHA(stage), nil
	}
	return "", "", failure("OFFLINE_SCOPE_MVP_NATIVE_RESTORE_STEP_INVALID")
}

// Explicit abort only removes exact terminal recorded actors. It preserves all
// database/domain/registry data and original recovery custody. A running or
// unknown actor is never stopped or replaced automatically.
func (l *locked) abortMVPNativeRestoreStage(ctx context.Context, c mvpNativeRestoreEngine, plan RestorePlan, stage string) error {
	x, e := l.readMVPNativeRestoreExecution(ctx)
	if e != nil {
		return e
	}
	var event Event
	id, e := nonce()
	if e != nil {
		return e
	}
	event = Event{OperationID: id, Action: "restore_" + stage + "_abort", Outcome: "started"}
	aborting := false
	switch stage {
	case "sql":
		state := restoreSQLState(l.events)
		if state.record == nil {
			return failure("OFFLINE_SCOPE_RESTORE_SQL_NOT_RECORDED")
		}
		if state.aborted {
			return nil
		}
		record := *state.record
		record.ObjectID, record.ResultSHA256 = "", ""
		event.RestoreSQL = &record
		event.ReferenceOperationID = state.pending
		aborting = state.aborting
		if aborting {
			event.OperationID = state.pending
			event.ReferenceOperationID = state.abortReference
		}
	case "database":
		state := restoreDatabaseState(l.events)
		if state.record == nil {
			return failure("OFFLINE_SCOPE_RESTORE_DATABASE_NOT_RECORDED")
		}
		if state.aborted {
			return nil
		}
		record := *state.record
		record.ObjectID, record.ResultSHA256 = "", ""
		event.RestoreDatabase = &record
		event.ReferenceOperationID = state.pending
		aborting = state.aborting
		if aborting {
			event.OperationID = state.pending
			event.ReferenceOperationID = state.abortReference
		}
	case "stores":
		state := restoreStoresState(l.events)
		if state.record == nil {
			return failure("OFFLINE_SCOPE_RESTORE_STORES_NOT_RECORDED")
		}
		if state.aborted {
			return nil
		}
		record := *state.record
		record.ObjectID, record.ResultSHA256 = "", ""
		event.RestoreStores = &record
		event.ReferenceOperationID = state.pending
		aborting = state.aborting
		if aborting {
			event.OperationID = state.pending
			event.ReferenceOperationID = state.abortReference
		}
	default:
		return failure("OFFLINE_SCOPE_MVP_NATIVE_RESTORE_STAGE_INVALID")
	}
	if !aborting {
		if e = l.appendEvent(event); e != nil {
			return e
		}
	}
	actors := []mvpNativeRestoreAbortActor{}
	roles := []string{"native-observer"}
	if stage == "sql" {
		roles = []string{"native-writer", "native-observer"}
	}
	for _, role := range roles {
		physicalID := l.nativeRestorePhysicalID(stage, role)
		if physicalID == "" {
			continue
		}
		task := x.Writer
		removed := false
		if stage == "sql" {
			index := 6
			if role == "native-writer" {
				index = 3
			}
			_, removed = restoreSQLState(l.events).completed[index]
		} else if stage == "database" {
			_, removed = restoreDatabaseState(l.events).completed[2]
		} else {
			_, removed = restoreStoresState(l.events).completed[2]
		}
		if role != "native-writer" {
			task.Kind = engine.NativePostgreSQLRestoreVerify
			task.Owner.Role = "database-" + task.Kind
		}
		actors = append(actors, mvpNativeRestoreAbortActor{Role: role, Task: task, PhysicalID: physicalID, Removed: removed})
	}
	cleanup := func() error {
		return cleanupMVPNativeRestoreAbort(ctx, c, actors, x.TemporaryOwners, aborting,
			func() error {
				if _, e := l.readMVPNativeRestoreOriginal(ctx, plan); e != nil {
					return e
				}
				return l.verifyMVPNativeRestoreHistory(ctx, stage)
			},
			func(role string) error {
				if role == "native-writer" {
					_, e := l.readMVPNativeRestoreOriginal(ctx, plan)
					return e
				}
				_, e := l.nativeRestoreObservation(ctx, stage)
				return e
			})
	}
	e = cleanup()
	event.Outcome = "completed"
	if e != nil {
		event.Outcome = "uncertain"
		event.Code = "OFFLINE_SCOPE_MVP_NATIVE_RESTORE_ABORT_UNRESOLVED"
	}
	if je := l.appendEvent(event); je != nil {
		return je
	}
	return e
}

// Abort cleanup has one dedicated, testable destructive boundary. Original
// custody and every active actor are verified before any removal. Failed,
// missing or unknown actors preserve all actors and original records volumes.
type mvpNativeRestoreAbortActor struct {
	Role       string
	Task       engine.DatabaseTask
	PhysicalID string
	Removed    bool
}
type mvpNativeRestoreAbortEngine interface {
	InspectDatabaseTask(context.Context, engine.DatabaseTask) (engine.RuntimeContainer, bool, error)
	RemoveDatabaseTask(context.Context, engine.DatabaseTask, string) error
	InspectVolume(context.Context, engine.Ownership) (engine.Volume, bool, error)
	RemoveNativePostgreSQLVolume(context.Context, engine.Ownership) error
}

func cleanupMVPNativeRestoreAbort(ctx context.Context, c mvpNativeRestoreAbortEngine, actors []mvpNativeRestoreAbortActor, owners []engine.Ownership, reconcile bool, verifyOriginal func() error, verifyOutput func(string) error) error {
	bad := failure("OFFLINE_SCOPE_MVP_NATIVE_RESTORE_ABORT_UNRESOLVED")
	if ctx == nil || ctx.Err() != nil || c == nil || verifyOriginal == nil || verifyOutput == nil {
		return bad
	}
	if e := verifyOriginal(); e != nil {
		return e
	}
	if reconcile {
		for _, a := range actors {
			if _, found, e := c.InspectDatabaseTask(ctx, a.Task); e != nil {
				return e
			} else if found {
				return bad
			}
		}
		for _, owner := range owners {
			if _, found, e := c.InspectVolume(ctx, owner); e != nil {
				return e
			} else if found {
				return bad
			}
		}
		return nil
	}
	snapshots := make([]engine.RuntimeContainer, len(actors))
	for i, a := range actors {
		if a.Removed {
			continue
		}
		actual, found, e := c.InspectDatabaseTask(ctx, a.Task)
		if e != nil {
			return e
		}
		if !found || !mvpNativeRestoreActorExited(actual, a.PhysicalID) {
			return bad
		}
		if e = verifyOutput(a.Role); e != nil {
			return e
		}
		snapshots[i] = actual
	}
	for i, a := range actors {
		if a.Removed {
			continue
		}
		actual, found, e := c.InspectDatabaseTask(ctx, a.Task)
		if e != nil {
			return e
		}
		if !found || !mvpNativeRestoreActorExited(actual, a.PhysicalID) || actual.State != snapshots[i].State {
			return bad
		}
		if e = c.RemoveDatabaseTask(ctx, a.Task, a.PhysicalID); e != nil {
			return e
		}
		if _, found, e = c.InspectDatabaseTask(ctx, a.Task); e != nil {
			return e
		} else if found {
			return bad
		}
	}
	for _, owner := range owners {
		if e := c.RemoveNativePostgreSQLVolume(ctx, owner); e != nil {
			return e
		}
		if _, found, e := c.InspectVolume(ctx, owner); e != nil {
			return e
		} else if found {
			return bad
		}
	}
	return nil
}
