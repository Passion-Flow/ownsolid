package scope

import (
	"context"
	"io"
	"path/filepath"
	"reflect"
	"slices"
	"strings"

	"github.com/Passion-Flow/ownsolid/tools/release/offline/pkg/bundle"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// ImageRecord binds both signed input and the independently measured tagless stream.
type ImageRecord struct {
	AssetID         string   `json:"asset_id"`
	Path            string   `json:"path"`
	SourceSHA256    string   `json:"source_sha256"`
	SourceBytes     int64    `json:"source_bytes"`
	ManifestDigest  string   `json:"manifest_digest"`
	ConfigDigest    string   `json:"config_digest"`
	DiffIDs         []string `json:"diff_ids"`
	ExpandedBytes   int64    `json:"expanded_bytes"`
	TransportSHA256 string   `json:"transport_sha256"`
	TransportBytes  int64    `json:"transport_bytes"`
	Mode            string   `json:"mode"`
}
type recordedImages struct {
	engine    *engine.Observation
	pending   map[string]Event
	completed map[string]ImageRecord
}

func imageState(events []Event) recordedImages {
	s := recordedImages{pending: map[string]Event{}, completed: map[string]ImageRecord{}}
	for _, event := range events {
		switch event.Action {
		case "engine_bind":
			s.engine = event.Engine
		case "image_import":
			if event.Outcome == "started" || event.Outcome == "uncertain" {
				s.pending[event.OperationID] = event
			}
			if event.Outcome == "completed" {
				delete(s.pending, event.OperationID)
				s.completed[event.Image.AssetID] = *event.Image
			}
		case "image_reconcile":
			if event.Outcome == "completed" {
				delete(s.pending, event.ReferenceOperationID)
				s.completed[event.Image.AssetID] = *event.Image
			}
		}
	}
	return s
}
func immutableDigest(value string) bool {
	return strings.HasPrefix(value, "sha256:") && wire.ValidSHA256(strings.TrimPrefix(value, "sha256:"))
}
func validImage(r *ImageRecord) bool {
	if r == nil || !wire.SafePath(r.AssetID) || strings.Contains(r.AssetID, "/") || !wire.SafePath(r.Path) || !wire.ValidSHA256(r.SourceSHA256) || !wire.ValidSHA256(r.TransportSHA256) || r.SourceBytes < 1 || r.TransportBytes < 1 || r.ExpandedBytes < 0 || !immutableDigest(r.ManifestDigest) || !immutableDigest(r.ConfigDigest) || r.DiffIDs == nil || len(r.DiffIDs) > 128 || (r.Mode != "load" && r.Mode != "reuse") {
		return false
	}
	for _, id := range r.DiffIDs {
		if !immutableDigest(id) {
			return false
		}
	}
	return true
}
func validEngine(o *engine.Observation) bool {
	if o == nil || !wire.ValidSHA256(o.EngineSHA256) || !wire.ValidSHA256(o.EndpointSHA256) {
		return false
	}
	var p engine.Policy
	if wire.StrictJSON(wire.EngineProfile, &p) != nil || o.APIVersion != p.APIVersion || o.DeclaredPlatform != "linux/amd64" {
		return false
	}
	found := false
	for _, v := range p.EngineVersions {
		if v == o.EngineVersion {
			found = true
		}
	}
	if !found {
		return false
	}
	if o.Profile == p.LocalProfile {
		return o.Status == engine.LocalStatus && o.Qualification == engine.LocalQualification && (o.HostArchitecture == "amd64" || o.HostArchitecture == "arm64")
	}
	return o.Status == engine.NativeStatus && o.Qualification == engine.NativeQualification && o.Profile == p.NativeProfile && o.HostArchitecture == "amd64"
}
func validateImageEvent(events []Event, e Event) error {
	if cumulativeUpgradeAction(e.Action) {
		return validateCumulativeUpgradeEvent(events, e)
	}
	if err := cumulativeUpgradeMutationGuard(events, e); err != nil {
		return err
	}
	if e.JournalCapacity != nil || e.Action == journalCapacityAction {
		return validateJournalCapacityEvent(events, e)
	}
	if currentStoresAction(e.Action) {
		return validateCurrentStoresEvent(events, e)
	}
	if e.RestoredCurrentStores != nil {
		return failure("OFFLINE_SCOPE_CURRENT_STORES_TRANSITION_INVALID")
	}
	stores := currentStoresState(events)
	if liveDatabaseAction(e.Action) && stores.record != nil && !stores.complete && !stores.aborted {
		return failure("OFFLINE_SCOPE_CURRENT_STORES_CLEANUP_REQUIRED")
	}
	if liveDatabaseAction(e.Action) {
		return validateLiveDatabaseEvent(events, e)
	}
	if e.RestoredLiveDatabase != nil {
		return failure("OFFLINE_SCOPE_LIVE_DATABASE_TRANSITION_INVALID")
	}
	if controlReplacementAction(e.Action) {
		return validateControlReplacementEvent(events, e)
	}
	if e.ControlReplacement != nil {
		return failure("OFFLINE_SCOPE_CONTROL_REPLACEMENT_TRANSITION_INVALID")
	}
	if replacement := controlReplacementState(events); replacement.record != nil && (tlsProjectionAction(e.Action) || restoreStoresAction(e.Action) || restoreDatabaseAction(e.Action) || restoreSQLAction(e.Action) || restoreReservationAction(e.Action)) {
		// Only a distinct TLS generation may follow a completed replacement.
		// The TLS validator still enforces original-prefix proof and transition
		// rules; restore/import/authority replay and consumed-generation abort stay closed.
		if !tlsProjectionAction(e.Action) || !replacement.complete || replacement.pending != "" || e.TLSProjection == nil || e.TLSProjection.GenerationID == replacement.record.GenerationID {
			return failure("OFFLINE_SCOPE_CONTROL_REPLACEMENT_ALREADY_STARTED")
		}
	}
	restoredActivation := e.Process != nil && e.Process.RestoredApplication != nil && validApplicationProcess(events, *e.Process)
	if controlReplacementState(events).record != nil && e.Action == "process_start" && (e.Process == nil || e.Process.RestoredDependency == nil && !restoredActivation || !validDependencyProcess(events, *e.Process)) {
		return failure("OFFLINE_SCOPE_RESTORED_ACTIVATION_REQUIRED")
	}

	if tlsProjectionAction(e.Action) {
		return validateTLSProjectionEvent(events, e)
	}
	if e.TLSProjection != nil {
		return failure("OFFLINE_SCOPE_TLS_PROJECTION_TRANSITION_INVALID")
	}
	if tlsProjectionState(events).record != nil && (restoreStoresAction(e.Action) || restoreDatabaseAction(e.Action) || restoreSQLAction(e.Action) || restoreReservationAction(e.Action)) {
		return failure("OFFLINE_SCOPE_TLS_PROJECTION_ALREADY_STARTED")
	}

	if restoreStoresAction(e.Action) {
		return validateRestoreStoresEvent(events, e)
	}
	if e.RestoreStores != nil {
		return failure("OFFLINE_SCOPE_RESTORE_STORES_TRANSITION_INVALID")
	}
	if restoreStoresState(events).record != nil && (restoreSQLAction(e.Action) || restoreDatabaseAction(e.Action)) {
		return failure("OFFLINE_SCOPE_RESTORE_STORES_ALREADY_STARTED")
	}

	if restoreDatabaseAction(e.Action) {
		return validateRestoreDatabaseEvent(events, e)
	}
	if e.RestoreDatabase != nil {
		return failure("OFFLINE_SCOPE_RESTORE_DATABASE_TRANSITION_INVALID")
	}
	if restoreDatabaseState(events).record != nil && restoreSQLAction(e.Action) {
		return failure("OFFLINE_SCOPE_RESTORE_DATABASE_ALREADY_STARTED")
	}
	if restoreSQLAction(e.Action) {
		return validateRestoreSQLEvent(events, e)
	}
	if restoreReservationAction(e.Action) {
		return validateRestoreReservationEvent(events, e)
	}
	if err := restoreMutationGuard(events, e); err != nil {
		return err
	}
	if e.Action == "backup_effect" || e.Action == "backup_reconcile" || e.Action == "backup_abort" {
		return validateBackupEvent(events, e)
	}
	if e.Backup != nil || backupState(events).record != nil && !backupState(events).terminal {
		return failure("OFFLINE_SCOPE_BACKUP_RECONCILIATION_REQUIRED")
	}
	if isDatabaseTaskAction(e.Action) {
		return validateDatabaseTaskActionEvent(events, e)
	}
	if e.DatabaseTaskAction != nil || databaseTaskActionState(events).pending != "" {
		return failure("OFFLINE_SCOPE_DATABASE_TASK_ACTION_RECONCILIATION_REQUIRED")
	}
	if e.Action == "database_task_create" || e.Action == "database_task_reconcile" {
		return validateDatabaseTaskEvent(events, e)
	}
	if e.DatabaseTask != nil || databaseTaskState(events).pending != "" {
		return failure("OFFLINE_SCOPE_DATABASE_TASK_RECONCILIATION_REQUIRED")
	}
	if databaseTaskState(events).record != nil && !restoredActivation {
		if !databaseTaskActionState(events).removed {
			return failure("OFFLINE_SCOPE_DATABASE_TASK_REMOVAL_REQUIRED")
		}
		if e.Action == "process_start" && (e.Process == nil || !slices.Equal(e.Process.SelectedServices, []string{"postgres"})) && !schemaQualified(events) {
			return failure("OFFLINE_SCOPE_INDEPENDENT_SCHEMA_QUALIFICATION_REQUIRED")
		}
		if e.Action == "process_start" && (e.Process == nil || !slices.Equal(e.Process.SelectedServices, []string{"postgres"})) && !runtimeAccessQualified(events) {
			return failure("OFFLINE_SCOPE_RUNTIME_ACCESS_QUALIFICATION_REQUIRED")
		}
		if _, declared := volumeState(events).roles[engine.BootstrapStateRole]; declared && e.Action == "process_start" && (e.Process == nil || !slices.Equal(e.Process.SelectedServices, []string{"postgres"})) && !bootstrapQualified(events) {
			return failure("OFFLINE_SCOPE_BOOTSTRAP_QUALIFICATION_REQUIRED")
		}

	}
	if e.Action == "process_start" && !restoredActivation && (e.Process == nil || !slices.Equal(e.Process.SelectedServices, []string{"postgres"})) {
		for _, role := range orderedStoreRoles {
			if _, declared := volumeState(events).roles[role]; declared && !storeQualified(events, role) {
				return failure("OFFLINE_SCOPE_STORE_QUALIFICATION_REQUIRED")
			}
		}
	}
	if isModelTaskAction(e.Action) {
		return validateModelTaskActionEvent(events, e)
	}
	if e.ModelTaskAction != nil || modelTaskActionState(events).pending != "" {
		return failure("OFFLINE_SCOPE_MODEL_TASK_ACTION_RECONCILIATION_REQUIRED")
	}
	if e.Action == "model_task_create" || e.Action == "model_task_reconcile" {
		return validateModelTaskEvent(events, e)
	}
	if e.ModelTask != nil || modelTaskState(events).pending != "" {
		return failure("OFFLINE_SCOPE_MODEL_TASK_RECONCILIATION_REQUIRED")
	}
	if modelTaskState(events).record != nil && !modelTaskActionState(events).removed {
		return failure("OFFLINE_SCOPE_MODEL_TASK_REMOVAL_REQUIRED")
	}
	if e.Action == "process_start" || e.Action == "process_stop" || e.Action == "process_reconcile" {
		return validateProcessEvent(events, e)
	}
	if e.Action == "runtime_create" || e.Action == "runtime_reconcile" || e.Action == runtimeAbsenceRecoveryAction {
		return validateRuntimeEvent(events, e)
	}
	if e.Action == "volume_prepare" || e.Action == "volume_reconcile" {
		return validateVolumeEvent(events, e)
	}
	if e.Action == "inputs_prepare" || e.Action == "inputs_reconcile" {
		return validateInputsEvent(events, e)
	}
	bad := func() error { return failure("OFFLINE_SCOPE_IMAGE_JOURNAL_TRANSITION_INVALID") }
	if e.Inputs != nil || e.Volume != nil || e.Runtime != nil || e.Process != nil {
		return bad()
	}
	if len(events) < 3 || events[2].Action != "release_prepare" || events[2].Outcome != "completed" {
		return bad()
	}
	s := imageState(events)
	for _, prior := range events {
		if prior.OperationID == e.OperationID && (e.Action != "image_import" || prior.Action != "image_import" || prior.Outcome != "started") {
			return bad()
		}
	}
	if e.Action == "engine_bind" {
		if s.engine != nil || len(events) != 3 || e.Outcome != "completed" || e.Code != "" || e.Image != nil || e.ReferenceOperationID != "" || !validEngine(e.Engine) {
			return bad()
		}
		return nil
	}
	if s.engine == nil || e.Engine != nil || !validImage(e.Image) {
		return bad()
	}
	switch e.Action {
	case "image_import":
		if e.ReferenceOperationID != "" {
			return bad()
		}
		if e.Outcome == "started" {
			if e.Code != "" || len(s.pending) > 0 {
				return bad()
			}
			if _, ok := s.completed[e.Image.AssetID]; ok {
				return bad()
			}
			return nil
		}
		prior, ok := s.pending[e.OperationID]
		if !ok || prior.Outcome != "started" || !reflect.DeepEqual(prior.Image, e.Image) {
			return bad()
		}
		if e.Outcome == "completed" && e.Code == "" || e.Outcome == "uncertain" && codePattern.MatchString(e.Code) {
			return nil
		}
	case "image_reconcile":
		prior, ok := s.pending[e.ReferenceOperationID]
		if !ok || !reflect.DeepEqual(prior.Image, e.Image) || e.OperationID == e.ReferenceOperationID {
			return bad()
		}
		if e.Outcome == "completed" && e.Code == "" || e.Outcome == "unresolved" && e.Code == "OFFLINE_SCOPE_IMAGE_NOT_OBSERVED" {
			return nil
		}
	}
	return bad()
}
func validateImageAssets(events []Event, manifest wire.Manifest) error {
	assets := map[string]wire.Asset{}
	for _, a := range manifest.Assets {
		assets[a.ID] = a
	}
	for _, event := range events {
		if r := event.Image; r != nil {
			a := assets[r.AssetID]
			if a.Kind != "image_archive" || a.Path != r.Path || a.SHA256 != r.SourceSHA256 || a.Size != r.SourceBytes || a.ImageID != r.ManifestDigest || a.ImageConfigDigest != r.ConfigDigest {
				return failure("OFFLINE_SCOPE_IMAGE_SIGNED_IDENTITY_MISMATCH")
			}
		}
	}
	return nil
}

type imageEngine interface {
	Preflight(context.Context) (engine.Observation, error)
	Inspect(context.Context, string) (engine.Image, bool, error)
	LoadTagless(context.Context, io.Reader) error
}

func Images(ctx context.Context, c configuration.Config, reconcile bool) (Status, error) {
	if c.Engine == nil {
		return Status{}, failure("OFFLINE_OPERATOR_ENGINE_CONFIGURATION_REQUIRED")
	}
	client, e := engine.New(*c.Engine)
	if e != nil {
		return Status{}, e
	}
	defer client.Close()
	return images(ctx, c, client, reconcile)
}

type admission struct {
	record ImageRecord
	asset  wire.Asset
	found  bool
}

func matchesImage(image engine.Image, r ImageRecord) bool {
	return image.ID == r.ManifestDigest && image.OS == "linux" && image.Architecture == "amd64" && image.RootFS.Type == "layers" && slices.Equal(image.RootFS.Layers, r.DiffIDs)
}
func (l *locked) checkEngine(ctx context.Context, client imageEngine) (engine.Observation, error) {
	o, e := client.Preflight(ctx)
	if e != nil {
		return o, e
	}
	if !validEngine(&o) || l.config.Engine == nil || o.Profile != l.config.Engine.Profile {
		return o, failure("OFFLINE_SCOPE_ENGINE_OBSERVATION_INVALID")
	}
	if bound := imageState(l.events).engine; bound != nil && *bound != o {
		return o, failure("OFFLINE_SCOPE_ENGINE_BINDING_CHANGED")
	}
	return o, nil
}
func (l *locked) admit(ctx context.Context, manifest wire.Manifest, client imageEngine) ([]admission, error) {
	all := []admission{}
	for _, a := range manifest.Assets {
		if a.Kind != "image_archive" {
			continue
		}
		source, e := bundle.OpenImageSource(ctx, l.config.Verification, filepath.Join(l.config.ScopeRoot, "release", filepath.FromSlash(a.Path)), a, l.config.Engine.MaximumExpandedImageBytes)
		if e != nil {
			return nil, e
		}
		inventory := source.Inventory()
		receipt, e := source.WriteTagless(ctx, io.Discard)
		ce := source.Close()
		if e != nil {
			return nil, e
		}
		if ce != nil {
			return nil, failure("OFFLINE_SCOPE_IMAGE_SOURCE_CLOSE_FAILED")
		}
		r := ImageRecord{AssetID: a.ID, Path: a.Path, SourceSHA256: a.SHA256, SourceBytes: a.Size, ManifestDigest: inventory.ManifestDigest, ConfigDigest: inventory.ConfigDigest, DiffIDs: append([]string{}, inventory.DiffIDs...), ExpandedBytes: inventory.ExpandedBytes, TransportSHA256: receipt.TransportSHA256, TransportBytes: receipt.TransportBytes, Mode: "load"}
		// The event envelope also consumes bounded journal space; reject before any mutation.
		if !validImage(&r) || !imageEventFits(r) {
			return nil, failure("OFFLINE_SCOPE_IMAGE_RECORD_LIMIT")
		}
		image, found, e := client.Inspect(ctx, r.ManifestDigest)
		if e != nil {
			return nil, e
		}
		if found && !matchesImage(image, r) {
			return nil, failure("OFFLINE_SCOPE_IMAGE_ROOTFS_MISMATCH")
		}
		if found {
			r.Mode = "reuse"
		}
		all = append(all, admission{r, a, found})
	}
	if len(all) == 0 {
		return nil, failure("OFFLINE_SCOPE_IMAGE_ASSETS_REQUIRED")
	}
	return all, nil
}

// Bound the complete largest image event, including reconciliation/failure
// metadata, before any engine mutation. Keep the actual4096byte journal ceiling.
func imageEventFits(record ImageRecord) bool {
	record.Mode = "reuse"
	value := Event{FormatVersion: scopeFormatVersion, Sequence: int(^uint(0) >> 1), PreviousSHA256: strings.Repeat("a", 64), ScopeID: strings.Repeat("a", 32), ConfigurationSHA256: strings.Repeat("a", 64), ArchiveSHA256: strings.Repeat("a", 64), ManifestSHA256: strings.Repeat("a", 64), OperationID: strings.Repeat("a", 32), ReferenceOperationID: strings.Repeat("a", 32), Action: "image_reconcile", Outcome: "unresolved", Code: strings.Repeat("A", 96), Time: "9999-12-31T23:59:59.999999999Z", Image: &record}
	return len(encode(value)) <= maximumEventBytes
}
func sameContent(a, b ImageRecord) bool { a.Mode = b.Mode; return reflect.DeepEqual(a, b) }
func images(ctx context.Context, c configuration.Config, client imageEngine, reconcile bool) (Status, error) {
	if c.Engine == nil {
		return Status{}, failure("OFFLINE_OPERATOR_ENGINE_CONFIGURATION_REQUIRED")
	}
	l, e := open(c, true)
	if e != nil {
		return Status{}, e
	}
	defer l.close()
	if len(l.events) < 3 || l.events[2].Outcome != "completed" {
		return Status{}, failure("OFFLINE_SCOPE_COMPLETED_PREPARATION_REQUIRED")
	}
	verified, e := bundle.VerifyPrepared(ctx, c.Verification, filepath.Join(c.ScopeRoot, "release"), c.ExpectedArchiveSHA256, c.ExpectedManifestSHA256)
	if e != nil {
		return Status{}, e
	}
	if verified.ReleaseID != l.marker.ReleaseID || verified.Profile != l.marker.Profile {
		return Status{}, failure("OFFLINE_SCOPE_RELEASE_CONTEXT_MISMATCH")
	}
	if e = validateImageAssets(l.events, verified.Manifest); e != nil {
		return Status{}, e
	}
	state := imageState(l.events)
	if processState(l.events).pending != "" {
		return Status{}, failure("OFFLINE_SCOPE_PROCESS_RECONCILIATION_REQUIRED")
	}
	if runtimeState(l.events).pending != "" {
		return Status{}, failure("OFFLINE_SCOPE_RUNTIME_RECONCILIATION_REQUIRED")
	}
	if len(volumeState(l.events).pending) > 0 {
		return Status{}, failure("OFFLINE_SCOPE_STORAGE_RECONCILIATION_REQUIRED")
	}
	if _, outcome, _ := inputsState(l.events); outcome != "" && outcome != "completed" {
		return Status{}, failure("OFFLINE_SCOPE_INPUT_RECONCILIATION_REQUIRED")
	}
	if !reconcile && len(state.pending) > 0 {
		return Status{}, failure("OFFLINE_SCOPE_IMAGE_RECONCILIATION_REQUIRED")
	}
	observation, e := l.checkEngine(ctx, client)
	if e != nil {
		return Status{}, e
	}
	all, e := l.admit(ctx, verified.Manifest, client)
	if e != nil {
		return Status{}, e
	}
	for _, a := range all {
		if prior, ok := state.completed[a.record.AssetID]; ok && (!sameContent(prior, a.record) || !a.found) {
			return Status{}, failure("OFFLINE_SCOPE_RECORDED_IMAGE_MISSING_OR_CHANGED")
		}
	}
	if ctx.Err() != nil {
		return Status{}, failure("OFFLINE_SCOPE_OPERATION_INTERRUPTED")
	}
	// Reserve enough space for this entire batch, including a terminal uncertain event.
	needed := 0
	if state.engine == nil {
		needed++
	}
	if reconcile {
		needed += len(state.pending)
	} else {
		for _, a := range all {
			if _, ok := state.completed[a.record.AssetID]; !ok {
				needed += 2
			}
		}
	}
	journal, e := l.root.Stat(journalName)
	if e != nil || journal.Size()+int64(needed)*maximumEventBytes > l.maximumJournalBytes() {
		return Status{}, failure("OFFLINE_SCOPE_JOURNAL_CAPACITY_REQUIRED")
	}
	if state.engine == nil {
		if reconcile {
			return Status{}, failure("OFFLINE_SCOPE_NO_IMAGE_ACTION_TO_RECONCILE")
		}
		id, e := nonce()
		if e != nil {
			return Status{}, e
		}
		if e = l.appendEvent(Event{OperationID: id, Action: "engine_bind", Outcome: "completed", Engine: &observation}); e != nil {
			return Status{}, e
		}
	}
	if reconcile {
		unresolved := false
		for _, a := range all {
			for reference, prior := range state.pending {
				if prior.Image.AssetID != a.record.AssetID {
					continue
				}
				if !sameContent(*prior.Image, a.record) {
					return Status{}, failure("OFFLINE_SCOPE_IMAGE_RECORD_CHANGED")
				}
				id, e := nonce()
				if e != nil {
					return Status{}, e
				}
				outcome, code := "completed", ""
				if !a.found {
					outcome = "unresolved"
					code = "OFFLINE_SCOPE_IMAGE_NOT_OBSERVED"
					unresolved = true
				}
				if _, e = l.checkEngine(ctx, client); e != nil {
					return Status{}, e
				}
				if e = l.appendEvent(Event{OperationID: id, Action: "image_reconcile", Outcome: outcome, Code: code, ReferenceOperationID: reference, Image: prior.Image}); e != nil {
					return Status{}, e
				}
			}
		}
		if unresolved {
			return l.status(), failure("OFFLINE_SCOPE_IMAGE_REMAINS_UNRESOLVED")
		}
		if ctx.Err() != nil {
			return Status{}, failure("OFFLINE_SCOPE_COMMIT_UNCERTAIN")
		}
		return l.status(), nil
	}
	for _, a := range all {
		if _, ok := state.completed[a.record.AssetID]; ok {
			continue
		}
		if _, e = l.checkEngine(ctx, client); e != nil {
			return Status{}, e
		}
		if ctx.Err() != nil {
			return Status{}, failure("OFFLINE_SCOPE_OPERATION_INTERRUPTED")
		}
		id, e := nonce()
		if e != nil {
			return Status{}, e
		}
		intent := Event{OperationID: id, Action: "image_import", Outcome: "started", Image: &a.record}
		if e = l.appendEvent(intent); e != nil {
			return Status{}, e
		}
		if !a.found {
			e = l.load(ctx, client, a)
		}
		if e == nil {
			var image engine.Image
			var found bool
			image, found, e = client.Inspect(ctx, a.record.ManifestDigest)
			if e == nil && (!found || !matchesImage(image, a.record)) {
				e = failure("OFFLINE_SCOPE_IMAGE_READBACK_FAILED")
			}
		}
		if e == nil {
			_, e = l.checkEngine(ctx, client)
		}
		if e == nil && ctx.Err() != nil {
			e = failure("OFFLINE_SCOPE_OPERATION_INTERRUPTED")
		}
		terminal := intent
		terminal.Outcome = "completed"
		if e != nil {
			terminal.Outcome = "uncertain"
			terminal.Code = safeEffectCode(e)
		}
		if ce := l.appendEvent(terminal); ce != nil {
			return Status{}, ce
		}
		if e != nil {
			return l.status(), failure("OFFLINE_SCOPE_IMAGE_IMPORT_UNCERTAIN")
		}
	}
	if ctx.Err() != nil {
		return Status{}, failure("OFFLINE_SCOPE_COMMIT_UNCERTAIN")
	}
	return l.status(), nil
}
func safeEffectCode(e error) string {
	if codePattern.MatchString(e.Error()) {
		return e.Error()
	}
	return "OFFLINE_SCOPE_IMAGE_EFFECT_FAILED_OR_UNCERTAIN"
}
func (l *locked) load(ctx context.Context, client imageEngine, a admission) error {
	source, e := bundle.OpenImageSource(ctx, l.config.Verification, filepath.Join(l.config.ScopeRoot, "release", filepath.FromSlash(a.asset.Path)), a.asset, l.config.Engine.MaximumExpandedImageBytes)
	if e != nil {
		return e
	}
	defer source.Close()
	reader, writer := io.Pipe()
	type result struct {
		receipt bundle.ImportReceipt
		err     error
	}
	done := make(chan result, 1)
	go func() { r, e := source.WriteTagless(ctx, writer); writer.CloseWithError(e); done <- result{r, e} }()
	loadError := client.LoadTagless(ctx, reader)
	reader.CloseWithError(loadError)
	produced := <-done
	if loadError != nil {
		return loadError
	}
	if produced.err != nil {
		return produced.err
	}
	r := produced.receipt
	if r.SourceSHA256 != a.record.SourceSHA256 || r.TransportSHA256 != a.record.TransportSHA256 || r.TransportBytes != a.record.TransportBytes || r.ExpandedBytes != a.record.ExpandedBytes {
		return failure("OFFLINE_SCOPE_IMAGE_TRANSPORT_CHANGED")
	}
	return nil
}
