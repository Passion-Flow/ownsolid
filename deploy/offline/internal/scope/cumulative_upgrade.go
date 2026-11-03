package scope

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/Passion-Flow/ownsolid/tools/release/offline/pkg/bundle"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/compose"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
	view "github.com/Passion-Flow/ownsolid/api/release/view"
)

type cumulativeUpgradeEngine interface {
	databaseTaskActionEngine
	storageEngine
	ValidateConfigurationFiles([]engine.ConfigurationFile, int, int) error
	RemoveStoppedControl(context.Context, string, engine.RuntimeContainer) error
	InspectStoppedControlConfiguration(context.Context, engine.Ownership, string, string, int, int, []engine.ConfigurationFile) (engine.ConfigurationObservation, error)
	InspectCurrentControlConfiguration(context.Context, engine.Ownership, string, string, int, int, []engine.ConfigurationFile) (engine.ConfigurationObservation, error)
	InspectFailedControlConfiguration(context.Context, engine.Ownership, string, string, int, int, []engine.ConfigurationFile) (engine.ConfigurationObservation, error)
}
type cumulativeUpgradeOperator interface {
	controlReplacementOperator
	processOperator
	ProbeControlReadiness(context.Context, compose.RuntimeReader, compose.RenderContext, compose.Model, compose.RuntimeStatus, compose.ControlReadinessInput) (compose.ControlReadiness, error)
}

var _ cumulativeUpgradeEngine = (*engine.Client)(nil)
var _ cumulativeUpgradeOperator = (*compose.Client)(nil)

type CumulativeUpgradeResult struct {
	ScopeID        string                 `json:"scope_id"`
	GenerationID   string                 `json:"generation_id,omitempty"`
	Status         string                 `json:"status"`
	CompletedSteps int                    `json:"completed_steps"`
	TotalSteps     int                    `json:"total_steps"`
	Pending        bool                   `json:"pending"`
	Installed      *view.InstalledRelease `json:"installed_release,omitempty"`
	Qualification  string                 `json:"qualification"`
}

func (l *locked) cumulativeUpgradeResult() CumulativeUpgradeResult {
	s := cumulativeUpgradeState(l.events)
	out := CumulativeUpgradeResult{ScopeID: l.marker.ScopeID, Status: "no_cumulative_upgrade_recorded", TotalSteps: len(cumulativeUpgradeStepsFor(cumulativeRecordProfile(func() string {
		if l.marker.Profile == wire.CumulativeCPUProfile {
			return wire.MVPPostgreSQLCPUProfile
		}
		return ""
	}()))), Qualification: "recorded_cumulative_upgrade_only_not_live_health_cold_install_browser_or_target"}
	if s.record == nil {
		return out
	}
	out.GenerationID = s.record.GenerationID
	out.CompletedSteps = len(s.completed)
	out.Pending = true
	out.Status = "cumulative_upgrade_incomplete"
	if s.pending != "" {
		out.Status = "cumulative_upgrade_reconciliation_required"
	}
	if s.notApplied {
		out.Status = "cumulative_upgrade_not_applied_explicit_retry_required"
	}
	if s.replaced {
		out.Status = "cumulative_control_replaced_start_and_activation_pending"
	}
	if s.activated {
		out.Status = "cumulative_upgrade_activation_recorded"
		out.Pending = false
		copy := *s.record.Installed
		out.Installed = &copy
	}
	return out
}
func CumulativeUpgradeCommand(mode string) bool {
	switch mode {
	case "upgrade-cumulative", "reconcile-cumulative-upgrade", "recover-cumulative-upgrade", "inspect-cumulative-upgrade", "activate-cumulative-upgrade":
		return true
	}
	return false
}
func RunCumulativeUpgrade(ctx context.Context, c configuration.Config, r configuration.CumulativeUpgradeRequest, mode string) (CumulativeUpgradeResult, error) {
	if ctx == nil || ctx.Err() != nil || r.Validate() != nil || !CumulativeUpgradeCommand(mode) {
		return CumulativeUpgradeResult{}, failure("OFFLINE_SCOPE_CUMULATIVE_UPGRADE_CONFIGURATION_REQUIRED")
	}
	l, e := open(c, mode != "inspect-cumulative-upgrade")
	if e != nil {
		return CumulativeUpgradeResult{}, e
	}
	defer l.close()
	result := func() CumulativeUpgradeResult { return l.cumulativeUpgradeResult() }
	if mode == "inspect-cumulative-upgrade" {
		s := cumulativeUpgradeState(l.events)
		if s.record == nil {
			return result(), nil
		}
		p, e := l.readCumulativeUpgradePlan()
		if e != nil || !reflect.DeepEqual(p.Request, r) {
			return result(), failure("OFFLINE_SCOPE_CUMULATIVE_UPGRADE_REQUEST_CHANGED")
		}
		return result(), nil
	}
	if c.Engine == nil || c.Engine.Utility == nil || c.Compose == nil || c.Compose.Process == nil || r.ReadinessTimeoutSeconds >= c.Compose.CommandSeconds {
		return result(), failure("OFFLINE_SCOPE_CUMULATIVE_UPGRADE_CONFIGURATION_REQUIRED")
	}
	client, e := engine.New(*c.Engine)
	if e != nil {
		return result(), e
	}
	defer client.Close()
	worker, e := compose.New(c)
	if e != nil {
		return result(), e
	}
	return l.runCumulativeUpgradeLocked(ctx, r, mode, client, worker)
}
func (l *locked) runCumulativeUpgradeLocked(ctx context.Context, r configuration.CumulativeUpgradeRequest, mode string, client cumulativeUpgradeEngine, worker cumulativeUpgradeOperator) (CumulativeUpgradeResult, error) {
	result := func() CumulativeUpgradeResult { return l.cumulativeUpgradeResult() }
	var e error
	if _, e = l.checkEngine(ctx, client); e != nil {
		return result(), e
	}
	state := cumulativeUpgradeState(l.events)
	if state.activated {
		p, e := l.readCumulativeUpgradePlan()
		if e != nil || !reflect.DeepEqual(p.Request, r) {
			return result(), failure("OFFLINE_SCOPE_CUMULATIVE_UPGRADE_REQUEST_CHANGED")
		}
		return result(), nil
	}
	if mode == "reconcile-cumulative-upgrade" && state.pending == "" {
		return result(), nil
	}
	if mode == "activate-cumulative-upgrade" {
		return l.activateCumulativeUpgrade(ctx, client, worker, r)
	}
	if state.replaced {
		return result(), nil
	}
	if state.pending != "" && mode == "upgrade-cumulative" {
		return result(), failure("OFFLINE_SCOPE_CUMULATIVE_UPGRADE_RECONCILIATION_REQUIRED")
	}
	if state.record == nil && mode != "upgrade-cumulative" {
		return result(), failure("OFFLINE_SCOPE_CUMULATIVE_UPGRADE_NOT_RECORDED")
	}
	var p CumulativeUpgradePlan
	if state.record == nil {
		generation, e := nonce()
		if e != nil {
			return result(), e
		}
		p, e = l.makeCumulativeUpgradePlan(ctx, client, worker, r, generation)
		if e != nil {
			return result(), e
		}
	} else {
		p, e = l.readCumulativeUpgradePlan()
		if e != nil {
			if state.record.Step != 0 {
				return result(), e
			}
			if _, statError := l.root.Lstat(cumulativeUpgradePlanFile); !os.IsNotExist(statError) {
				return result(), e
			}
			p, e = l.makeCumulativeUpgradePlan(ctx, client, worker, r, state.record.GenerationID)
			if e != nil || wire.Digest(encode(p)) != state.record.PlanSHA256 {
				return result(), failure("OFFLINE_SCOPE_CUMULATIVE_UPGRADE_PLAN_RECONSTRUCTION_REFUSED")
			}
		}
		if !reflect.DeepEqual(p.Request, r) {
			return result(), failure("OFFLINE_SCOPE_CUMULATIVE_UPGRADE_REQUEST_CHANGED")
		}
	}
	target, e := l.cumulativeTarget(ctx, r)
	if e != nil || !reflect.DeepEqual(target.Manifest, p.Target) {
		return result(), failure("OFFLINE_SCOPE_CUMULATIVE_UPGRADE_TARGET_CHANGED")
	}
	files, model, e := l.cumulativePayloads(ctx, p)
	if e != nil {
		return result(), e
	}
	for _, role := range []string{"control-config", "operator-config"} {
		if e = client.ValidateConfigurationFiles(files[role], p.UID, p.GID); e != nil {
			return result(), e
		}
	}
	if e = admitProcessJournal(l, 3*(len(cumulativeUpgradeStepsFor(p.Target.Profile))-len(state.completed))+3); e != nil {
		return result(), e
	}
	for step, phase := range cumulativeUpgradeStepsFor(p.Target.Profile) {
		s := cumulativeUpgradeState(l.events)
		if _, done := s.completed[step]; done {
			continue
		}
		record := CumulativeUpgradeRecord{TargetProfile: cumulativeRecordProfile(p.Target.Profile), GenerationID: p.GenerationID, PlanSHA256: wire.Digest(encode(p)), Step: step, Phase: phase}
		if ctx.Err() != nil {
			return result(), failure("OFFLINE_SCOPE_OPERATION_INTERRUPTED")
		}
		if e = l.verifyCumulativeRuntime(ctx, client, worker, p, model); e != nil {
			return result(), e
		}
		if mode == "recover-cumulative-upgrade" && s.pending != "" {
			if e = l.confirmCumulativeNotApplied(ctx, client, worker, p, model, files, record); e != nil {
				return result(), e
			}
			id, e := nonce()
			if e != nil {
				return result(), e
			}
			e = l.appendEvent(Event{OperationID: id, ReferenceOperationID: s.pending, Action: "upgrade_recover", Outcome: "not_applied", CumulativeUpgrade: &record})
			return result(), e
		}
		observe := s.pending != ""
		action, reference := "upgrade_effect", ""
		if observe {
			if mode != "reconcile-cumulative-upgrade" {
				return result(), failure("OFFLINE_SCOPE_CUMULATIVE_UPGRADE_RECONCILIATION_REQUIRED")
			}
			action = "upgrade_reconcile"
			reference = s.pending
		} else if s.notApplied {
			if mode != "upgrade-cumulative" {
				return result(), failure("OFFLINE_SCOPE_CUMULATIVE_UPGRADE_EXPLICIT_RETRY_REQUIRED")
			}
			action = "upgrade_retry"
			reference = s.recoveryReference
		}
		if !observe && mode == "recover-cumulative-upgrade" {
			return result(), failure("OFFLINE_SCOPE_CUMULATIVE_UPGRADE_NO_PENDING_EFFECT")
		}
		id, e := nonce()
		if e != nil {
			return result(), e
		}
		if !observe {
			if e = l.appendEvent(Event{OperationID: id, ReferenceOperationID: reference, Action: action, Outcome: "started", CumulativeUpgrade: &record}); e != nil {
				return result(), e
			}
		}
		completed, e := l.executeCumulativeStep(ctx, client, worker, p, model, files, record, observe)
		if _, binding := l.checkEngine(ctx, client); binding != nil {
			e = binding
		}
		outcome, code := "completed", ""
		if e != nil {
			completed = record
			outcome = "uncertain"
			if observe {
				outcome = "unresolved"
			}
			code = "OFFLINE_SCOPE_CUMULATIVE_UPGRADE_EFFECT_UNRESOLVED"
		}
		if err := l.appendEvent(Event{OperationID: id, ReferenceOperationID: reference, Action: action, Outcome: outcome, Code: code, CumulativeUpgrade: &completed}); err != nil {
			return result(), err
		}
		if e != nil {
			return result(), e
		}
		if observe {
			return result(), nil
		}
	}
	return result(), nil
}
func (l *locked) makeCumulativeUpgradePlan(ctx context.Context, c cumulativeUpgradeEngine, w cumulativeUpgradeOperator, r configuration.CumulativeUpgradeRequest, generation string) (CumulativeUpgradePlan, error) {
	var p CumulativeUpgradePlan
	original := l.cumulativeOriginal()
	if original.marker.Profile != wire.CumulativePredecessorCPUProfile && original.marker.Profile != wire.CumulativeCPUProfile || controlReplacementState(original.events).record != nil || restoreState(original.events).reserved || tlsProjectionState(original.events).record != nil {
		return p, failure("OFFLINE_SCOPE_CUMULATIVE_UPGRADE_ORIGINAL_M16_SCOPE_REQUIRED")
	}
	backup, e := InspectRestoreSource(ctx, l.config, r.BackupSource)
	if e != nil || backup.SourceScopeID != l.marker.ScopeID || backup.ReleaseID != l.marker.ReleaseID || backup.Profile != l.marker.Profile || !slices.Equal(backup.Stores, []string{"datasets", "models", "preparations", "training"}) {
		return p, failure("OFFLINE_SCOPE_CUMULATIVE_UPGRADE_COMPLETE_ORIGINAL_BACKUP_REQUIRED")
	}
	v, e := original.verifyRuntimeContext(ctx, c, w, false)
	if e != nil {
		return p, e
	}
	if !modelTaskActionState(original.events).initialized || !modelTaskActionState(original.events).removed || !schemaQualified(original.events) || !runtimeAccessQualified(original.events) || !bootstrapQualified(original.events) {
		return p, failure("OFFLINE_SCOPE_CUMULATIVE_UPGRADE_ORIGINAL_QUALIFICATION_REQUIRED")
	}
	for _, role := range orderedStoreRoles {
		if !storeQualified(original.events, role) {
			return p, failure("OFFLINE_SCOPE_CUMULATIVE_UPGRADE_ALL_STORES_REQUIRED")
		}
	}
	target, e := l.cumulativeTarget(ctx, r)
	if e != nil {
		return p, e
	}
	predecessor, e := bundle.VerifyPrepared(ctx, l.config.Verification, filepath.Join(l.config.ScopeRoot, "release"), l.marker.ArchiveSHA256, l.marker.ManifestSHA256)
	if e != nil {
		return p, e
	}
	if e = cumulativeSelectionCompatible(predecessor.Manifest, target.Manifest); e != nil {
		return p, e
	}
	image, e := l.cumulativeImage(ctx, target.Manifest, r.PreparedRoot)
	if e != nil {
		return p, e
	}
	before, found, e := c.InspectRuntime(ctx, l.marker.ScopeID, "control")
	if e != nil || !found || before.State.Running {
		return p, failure("OFFLINE_SCOPE_CUMULATIVE_UPGRADE_STOPPED_CONTROL_REQUIRED")
	}
	pg, found, e := c.InspectRuntime(ctx, l.marker.ScopeID, "postgres")
	if e != nil || !found || !pg.State.Running || pg.State.PID < 1 {
		return p, failure("OFFLINE_SCOPE_CUMULATIVE_UPGRADE_ORIGINAL_POSTGRES_REQUIRED")
	}
	raw, _ := json.Marshal(before)
	uid, gid, e := roleUser(v.model, "control-config")
	if e != nil {
		return p, e
	}
	b := backupState(original.events)
	if b.record == nil {
		return p, failure("OFFLINE_SCOPE_CUMULATIVE_UPGRADE_BACKUP_REQUIRED")
	}
	p = CumulativeUpgradePlan{FormatVersion: 1, ScopeID: l.marker.ScopeID, GenerationID: generation, Request: r, Target: target.Manifest, ControlImage: image, RuntimeSHA256: wire.Digest(encode(*v.creation.record)), BackupProofSHA256: wire.Digest(encode(*b.record)), PreviousControlJSON: string(raw), Before: v.observed, UID: uid, GID: gid, PostgresPID: pg.State.PID, PostgresStartedAt: pg.State.StartedAt}
	if p.Target.Profile == wire.MVPPostgreSQLCPUProfile {
		raw, e := protectedInput(l.config, r.MVPConfigurationFiles, l.config.Verification.MaximumMetadataBytes)
		if e != nil {
			return p, e
		}
		p.MVPConfigurationSHA256 = wire.Digest(raw)
		var catalog cumulativeMVPCatalog
		if wire.StrictJSON(raw, &catalog) != nil {
			return p, failure("OFFLINE_SCOPE_MVP_CONFIGURATION_INVALID")
		}
		p.RegistryMountTarget = catalog.RegistryMountTarget
		p.RegistryReleaseMountTarget = catalog.RegistryReleaseMountTarget
		profile, e := l.registryReleaseDescription(ctx, r.PreparedRoot, r.ArchiveSHA256, r.ManifestSHA256)
		if e != nil {
			return p, e
		}
		p.RegistryReleaseProfile = &profile
		p.RegistryReleaseMetadataSHA256 = wire.Digest(encode(profile))
	} else if r.MVPConfigurationFiles != "" {
		return p, failure("OFFLINE_SCOPE_MVP_CONFIGURATION_UNEXPECTED")
	}
	files, model, e := l.cumulativePayloads(ctx, p)
	if e != nil {
		return p, e
	}
	p.ModelSHA256 = wire.Digest(encode(model))
	p.ControlFiles = cumulativeIndex(files["control-config"])
	p.OperatorFiles = cumulativeIndex(files["operator-config"])
	return p, l.validateCumulativeUpgradePlan(ctx, p)
}
func cumulativeProfileTransition(before, after string) bool {
	return before == wire.CumulativePredecessorCPUProfile && after == wire.CumulativeCPUProfile || before == wire.CumulativeCPUProfile && after == wire.MVPPostgreSQLCPUProfile
}

func cumulativeSelectionCompatible(before, after wire.Manifest) error {
	bad := failure("OFFLINE_SCOPE_CUMULATIVE_UPGRADE_SELECTED_ASSET_CONFLICT")
	if !cumulativeProfileTransition(before.Profile, after.Profile) || before.Cumulative == nil || after.Cumulative == nil {
		return bad
	}
	a, b := *before.Cumulative, *after.Cumulative
	for owner, version := range a.ComponentVersions {
		if owner != "control" && owner != "operator" && b.ComponentVersions[owner] != version {
			return bad
		}
	}
	if a.LocaleSourceSHA256 != b.LocaleSourceSHA256 || !slices.Equal(a.InstalledLocales, b.InstalledLocales) || a.HumanApprovals != b.HumanApprovals || a.EmbeddedSeed != b.EmbeddedSeed {
		return bad
	}
	for _, old := range before.Assets {
		if old.Kind != "image_archive" && old.Kind != "model" && old.Kind != "locale" && old.Kind != "runtime_inputs" && old.Kind != "plugin" {
			continue
		}
		if old.ID == "control-image" {
			continue
		}
		found := false
		for _, next := range after.Assets {
			if old.ID == next.ID {
				found = reflect.DeepEqual(old, next)
				break
			}
		}
		if !found {
			return bad
		}
	}
	return nil
}
func cumulativeOwner(p CumulativeUpgradePlan, operator bool) engine.Ownership {
	role := engine.CumulativeControlRole
	if operator {
		role = engine.CumulativeOperatorRole
	}
	return engine.Ownership{ScopeID: p.ScopeID, OperationID: p.GenerationID, Role: role + "-" + p.GenerationID}
}
func cumulativeTask(p CumulativeUpgradePlan) engine.DatabaseTask {
	peers := map[string]string{}
	for role, s := range p.Before.Services {
		peers[role] = s.ID
	}
	kind := engine.DatabaseCumulativeReleases
	if p.Target.Profile == wire.MVPPostgreSQLCPUProfile {
		kind = engine.DatabaseCumulativeMVP
	}
	return engine.DatabaseTask{Owner: engine.Ownership{ScopeID: p.ScopeID, OperationID: p.GenerationID, Role: "database-" + kind}, VolumeOwner: cumulativeOwner(p, true), Kind: kind, Image: p.ControlImage.ManifestDigest, UID: p.UID, GID: p.GID, NetworkID: p.Before.NetworkID, Peers: peers}
}
func cumulativeStepIntended(events []Event, phase string) bool {
	for _, e := range events {
		if e.CumulativeUpgrade != nil && e.CumulativeUpgrade.Phase == phase && e.Outcome == "started" {
			return true
		}
	}
	return false
}
func cumulativeStepDone(events []Event, phase string) bool {
	for _, r := range cumulativeUpgradeState(events).completed {
		if r.Phase == phase {
			return true
		}
	}
	return false
}
func (l *locked) cumulativeReader(ctx context.Context, c cumulativeUpgradeEngine, p CumulativeUpgradePlan) (runtimeEngine, error) {
	var reader runtimeEngine = c
	owners := []struct {
		prefix string
		owner  engine.Ownership
	}{{"control", cumulativeOwner(p, false)}, {"operator", cumulativeOwner(p, true)}}
	if p.Target.Profile == wire.MVPPostgreSQLCPUProfile {
		owners = append(owners, struct {
			prefix string
			owner  engine.Ownership
		}{"registry", cumulativeRegistryOwner(p)})
	}
	if p.Target.Profile == wire.MVPPostgreSQLCPUProfile {
		owners = append(owners, struct {
			prefix string
			owner  engine.Ownership
		}{"registry-release", cumulativeRegistryReleaseOwner(p)})
	}
	for _, entry := range owners {
		if !cumulativeStepIntended(l.events, entry.prefix+"-utility-create") || cumulativeStepDone(l.events, entry.prefix+"-utility-remove") {
			continue
		}
		u, found, e := c.InspectUtility(ctx, entry.owner, p.ControlImage.ManifestDigest, p.UID, p.GID)
		if e != nil {
			return nil, e
		}
		if found {
			reader = restoredInputRuntimeView{runtimeEngine: reader, scopeID: p.ScopeID, utilityID: u.ID}
		}
	}
	if cumulativeStepIntended(l.events, "database-create") && !cumulativeStepDone(l.events, "database-remove") {
		t := cumulativeTask(p)
		object, found, e := c.InspectDatabaseTask(ctx, t)
		if e != nil {
			return nil, e
		}
		if found {
			if r := cumulativePhaseRecord(cumulativeUpgradeState(l.events).completed, "database-create"); r.ObjectID != "" && r.ObjectID != object.ID {
				return nil, failure("OFFLINE_SCOPE_CUMULATIVE_UPGRADE_TASK_CHANGED")
			}
			reader = databaseTaskRuntimeView{runtimeEngine: reader, task: t, object: object}
		}
	}
	return reader, nil
}
func (l *locked) cumulativeRenderContext(p CumulativeUpgradePlan) compose.RenderContext {
	allowed := map[string]bool{p.ControlImage.ManifestDigest: true}
	for _, r := range imageState(l.cumulativeOriginal().events).completed {
		allowed[r.ManifestDigest] = true
	}
	return compose.RenderContext{ScopeID: p.ScopeID, ScopeRoot: l.config.ScopeRoot, AllowedImages: allowed}
}
func (l *locked) verifyCumulativeRuntime(ctx context.Context, c cumulativeUpgradeEngine, w cumulativeUpgradeOperator, p CumulativeUpgradePlan, next compose.Model) error {
	if e := l.validateCumulativeUpgradePlan(ctx, p); e != nil {
		return e
	}
	if _, e := l.checkEngine(ctx, c); e != nil {
		return e
	}
	reader, e := l.cumulativeReader(ctx, c, p)
	if e != nil {
		return e
	}
	_, base, e := l.replacementModels(ctx)
	if e != nil {
		return e
	}
	control, found, e := c.InspectRuntime(ctx, p.ScopeID, "control")
	if e != nil {
		return e
	}
	model := base
	if !found || control.ID != p.Before.Services["control"].ID {
		if !cumulativeStepIntended(l.events, "control-remove") {
			return failure("OFFLINE_SCOPE_CUMULATIVE_UPGRADE_PREVIOUS_CONTROL_CHANGED")
		}
		model = next
	}
	observed, e := w.ObserveRuntime(ctx, reader, l.cumulativeRenderContext(p), model)
	if e != nil {
		return e
	}
	if observed.NetworkID != p.Before.NetworkID || observed.IngressNetworkID != p.Before.IngressNetworkID {
		return failure("OFFLINE_SCOPE_CUMULATIVE_UPGRADE_NETWORK_CHANGED")
	}
	for role, before := range p.Before.Services {
		now, exists := observed.Services[role]
		if role == "control" {
			if !exists {
				if !cumulativeStepIntended(l.events, "control-remove") || cumulativeStepDone(l.events, "control-create") {
					return failure("OFFLINE_SCOPE_CUMULATIVE_UPGRADE_CONTROL_DISAPPEARED")
				}
				continue
			}
			if now.ID != before.ID {
				if !cumulativeStepDone(l.events, "control-remove") || !cumulativeStepIntended(l.events, "control-create") || now.State != "created" {
					return failure("OFFLINE_SCOPE_CUMULATIVE_UPGRADE_UNRECORDED_CONTROL")
				}
				if r := cumulativePhaseRecord(cumulativeUpgradeState(l.events).completed, "control-create"); r.ObjectID != "" && r.ObjectID != now.ID {
					return failure("OFFLINE_SCOPE_CUMULATIVE_UPGRADE_CONTROL_CHANGED")
				}
			} else if cumulativeStepDone(l.events, "control-remove") {
				return failure("OFFLINE_SCOPE_CUMULATIVE_UPGRADE_PREVIOUS_CONTROL_RETURNED")
			}
		} else if !exists || now.ID != before.ID {
			return failure("OFFLINE_SCOPE_CUMULATIVE_UPGRADE_PEER_CHANGED")
		}
		if now.ExitCode != 0 {
			return failure("OFFLINE_SCOPE_CUMULATIVE_UPGRADE_FAILED_PEER")
		}
		if cumulativeStepDone(l.events, "process-stop") {
			if now.State != "created" && now.State != "exited" {
				return failure("OFFLINE_SCOPE_CUMULATIVE_UPGRADE_STOPPED_PEERS_REQUIRED")
			}
		} else if !cumulativeStepIntended(l.events, "process-stop") && now.State != before.State {
			return failure("OFFLINE_SCOPE_CUMULATIVE_UPGRADE_ORIGINAL_PROCESS_CHANGED")
		}
	}
	if !cumulativeStepIntended(l.events, "process-stop") {
		pg, found, e := c.InspectRuntime(ctx, p.ScopeID, "postgres")
		if e != nil || !found || pg.State.PID != p.PostgresPID || pg.State.StartedAt != p.PostgresStartedAt {
			return failure("OFFLINE_SCOPE_CUMULATIVE_UPGRADE_ORIGINAL_POSTGRES_CHANGED")
		}
	}
	return nil
}
func (l *locked) executeCumulativeStep(ctx context.Context, c cumulativeUpgradeEngine, w cumulativeUpgradeOperator, p CumulativeUpgradePlan, model compose.Model, files map[string][]engine.ConfigurationFile, r CumulativeUpgradeRecord, observe bool) (CumulativeUpgradeRecord, error) {
	bad := func() (CumulativeUpgradeRecord, error) {
		return r, failure("OFFLINE_SCOPE_CUMULATIVE_UPGRADE_EFFECT_UNRESOLVED")
	}
	operator := strings.HasPrefix(r.Phase, "operator-")
	owner := cumulativeOwner(p, operator)
	payload := files["control-config"]
	if operator {
		payload = files["operator-config"]
	}
	phase := r.Phase
	if strings.HasPrefix(phase, "registry-release-") {
		owner = cumulativeRegistryReleaseOwner(p)
		payload = nil
		phase = "control-" + strings.TrimPrefix(phase, "registry-release-")
	} else if strings.HasPrefix(phase, "registry-") {
		owner = cumulativeRegistryOwner(p)
		payload = nil
		phase = "control-" + strings.TrimPrefix(phase, "registry-")
	}
	switch phase {
	case "plan-publish":
		if _, e := l.root.Lstat(cumulativeUpgradePlanFile); os.IsNotExist(e) && !observe {
			if e = writeNew(l.root, cumulativeUpgradePlanFile, encode(p)); e != nil {
				return r, e
			}
		}
		if e := l.syncNamedRestorePlan(cumulativeUpgradePlanFile, encode(p)); e != nil {
			return r, e
		}
	case "target-prepare":
		target, e := l.cumulativeTarget(ctx, p.Request)
		if e != nil || !reflect.DeepEqual(target.Manifest, p.Target) {
			return bad()
		}
	case "control-image-import":
		image, found, e := c.Inspect(ctx, p.ControlImage.ManifestDigest)
		if e != nil {
			return r, e
		}
		if !found && !observe {
			var asset wire.Asset
			for _, a := range p.Target.Assets {
				if a.ID == "control-image" {
					asset = a
				}
			}
			source, e := bundle.OpenImageSource(ctx, l.config.Verification, filepath.Join(p.Request.PreparedRoot, filepath.FromSlash(asset.Path)), asset, l.config.Engine.MaximumExpandedImageBytes)
			if e != nil {
				return r, e
			}
			e = l.importOne(ctx, c, source, p.ControlImage)
			ce := source.Close()
			if e != nil || ce != nil {
				return bad()
			}
			image, found, e = c.Inspect(ctx, p.ControlImage.ManifestDigest)
		}
		if e != nil || !found || !matchesImage(image, p.ControlImage) {
			return bad()
		}
	case "control-volume-create", "operator-volume-create":
		if !observe {
			if _, e := c.CreateVolume(ctx, owner); e != nil {
				return r, e
			}
		}
		if _, found, e := c.InspectVolume(ctx, owner); e != nil || !found {
			return bad()
		}
	case "control-utility-create", "operator-utility-create":
		if !observe {
			if _, e := c.CreateUtility(ctx, owner, p.ControlImage.ManifestDigest, p.UID, p.GID); e != nil {
				return r, e
			}
		}
		u, found, e := c.InspectUtility(ctx, owner, p.ControlImage.ManifestDigest, p.UID, p.GID)
		if e != nil || !found {
			return bad()
		}
		r.ObjectID = u.ID
	case "control-payload-write", "operator-payload-write":
		if strings.HasPrefix(r.Phase, "registry-release-") {
			o, e := l.registryReleasePayload(ctx, c, owner, p.ControlImage.ManifestDigest, p.UID, p.GID, p.Request.PreparedRoot, p.Request.ArchiveSHA256, p.Request.ManifestSHA256, p.RegistryReleaseMetadataSHA256, p.RegistryReleaseProfile, !observe)
			if e != nil {
				return r, e
			}
			if o.Status != engine.ConfigurationPayloadVerified {
				return bad()
			}
			return r, nil
		}
		if !observe {
			if e := c.PutConfiguration(ctx, owner, p.ControlImage.ManifestDigest, p.UID, p.GID, payload); e != nil {
				return r, e
			}
		}
		observation, e := c.InspectConfiguration(ctx, owner, p.ControlImage.ManifestDigest, p.UID, p.GID, payload)
		if e != nil || observation.Status != engine.ConfigurationPayloadVerified {
			return bad()
		}
	case "control-utility-remove", "operator-utility-remove":
		if !observe {
			if e := c.RemoveUtility(ctx, owner, p.ControlImage.ManifestDigest, p.UID, p.GID); e != nil {
				return r, e
			}
		}
		if _, found, e := c.InspectUtility(ctx, owner, p.ControlImage.ManifestDigest, p.UID, p.GID); e != nil || found {
			return bad()
		}
		if e := c.VerifyVolumeConsumers(ctx, owner, nil); e != nil {
			return r, e
		}
	case "database-create":
		t := cumulativeTask(p)
		if !observe {
			if _, e := c.CreateDatabaseTask(ctx, t); e != nil {
				return r, e
			}
		}
		task, found, e := c.InspectDatabaseTask(ctx, t)
		if e != nil || !found || task.State.Status != "created" {
			return bad()
		}
		r.ObjectID = task.ID
	case "database-execute":
		t := cumulativeTask(p)
		id := cumulativePhaseRecord(cumulativeUpgradeState(l.events).completed, "database-create").ObjectID
		if !observe {
			if _, e := c.StartDatabaseTask(ctx, t, id, files["operator-config"], l.config.Compose.Process.ReservePercent); e != nil {
				return r, e
			}
		}
		timer := time.NewTimer(time.Duration(l.config.Compose.Process.ObserveSeconds) * time.Second)
		defer timer.Stop()
		for {
			task, found, e := c.InspectDatabaseTask(ctx, t)
			if e != nil || !found || task.ID != id {
				return bad()
			}
			if task.State.Status == "exited" {
				if task.State.ExitCode != 0 || task.State.OOMKilled {
					return bad()
				}
				observation, e := c.InspectDatabaseTaskInput(ctx, t, id, files["operator-config"])
				if e != nil || observation.Status != engine.ConfigurationPayloadVerified {
					return bad()
				}
				break
			}
			if observe {
				return bad()
			}
			select {
			case <-ctx.Done():
				return bad()
			case <-timer.C:
				return bad()
			case <-time.After(time.Duration(l.config.Compose.Process.PollMilliseconds) * time.Millisecond):
			}
		}
	case "database-remove":
		t := cumulativeTask(p)
		id := cumulativePhaseRecord(cumulativeUpgradeState(l.events).completed, "database-create").ObjectID
		if !observe {
			if e := c.RemoveDatabaseTask(ctx, t, id); e != nil {
				return r, e
			}
		}
		if _, found, e := c.InspectDatabaseTask(ctx, t); e != nil || found {
			return bad()
		}
	case "process-stop":
		_, base, e := l.replacementModels(ctx)
		if e != nil {
			return r, e
		}
		if !observe {
			if _, e = w.StopRuntime(ctx, c, l.cumulativeRenderContext(p), base, p.Before); e != nil {
				return r, e
			}
		}
		status, e := w.ObserveRuntime(ctx, c, l.cumulativeRenderContext(p), base)
		if e != nil {
			return r, e
		}
		for _, s := range status.Services {
			if s.State != "created" && s.State != "exited" || s.ExitCode != 0 {
				return bad()
			}
		}
	case "control-remove":
		var previous engine.RuntimeContainer
		if wire.UniqueJSON([]byte(p.PreviousControlJSON), &previous) != nil {
			return bad()
		}
		if !observe {
			if e := c.RemoveStoppedControl(ctx, p.ScopeID, previous); e != nil {
				return r, e
			}
		}
		if _, found, e := c.InspectRuntime(ctx, p.ScopeID, "control"); e != nil || found {
			return bad()
		}
	case "control-create":
		before := p.Before
		before.Services = cloneStoppedUpgradePeers(p.Before.Services)
		var status compose.RuntimeStatus
		var e error
		if observe {
			status, e = w.ObserveControlReplacement(ctx, c, l.cumulativeRenderContext(p), model, before, true)
		} else {
			status, e = w.CreateReplacementControl(ctx, c, l.cumulativeRenderContext(p), model, before)
		}
		if e != nil {
			return r, e
		}
		r.ObjectID = status.Services["control"].ID
	case "control-verify", "replacement-complete":
		id := cumulativePhaseRecord(cumulativeUpgradeState(l.events).completed, "control-create").ObjectID
		target := ""
		for _, m := range model.Services["control"].Mounts {
			if m.Source == cumulativeOwner(p, false).Role {
				target = m.Target
			}
		}
		observed, e := c.InspectStoppedControlConfiguration(ctx, cumulativeOwner(p, false), id, target, p.UID, p.GID, files["control-config"])
		if e != nil || observed.Status != engine.ConfigurationPayloadVerified {
			return bad()
		}
		if r.Phase == "replacement-complete" {
			next := replacementEffectiveRecord(*runtimeState(l.events).record, p.ModelSHA256, id)
			r.Effective = &next
		}
	default:
		return bad()
	}
	return r, nil
}
func cloneStoppedUpgradePeers(source map[string]compose.RuntimeServiceStatus) map[string]compose.RuntimeServiceStatus {
	out := map[string]compose.RuntimeServiceStatus{}
	for role, s := range source {
		if role == "postgres" {
			s.State = "exited"
		}
		out[role] = s
	}
	return out
}
