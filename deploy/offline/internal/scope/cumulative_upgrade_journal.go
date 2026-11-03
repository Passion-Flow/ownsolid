package scope

import (
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/compose"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
	view "github.com/Passion-Flow/ownsolid/api/release/view"
)

// CumulativeUpgradeRecord is metadata only. Private config and original runtime
// metadata live in the protected plan, never in the public installation view.
type CumulativeUpgradeRecord struct {
	TargetProfile   string                    `json:"target_profile,omitempty"`
	GenerationID    string                    `json:"generation_id"`
	PlanSHA256      string                    `json:"plan_sha256"`
	Step            int                       `json:"step"`
	Phase           string                    `json:"phase"`
	ObjectID        string                    `json:"object_id,omitempty"`
	Effective       *RuntimeRecord            `json:"effective_runtime,omitempty"`
	Installed       *view.InstalledRelease    `json:"installed_release,omitempty"`
	Readiness       *compose.ControlReadiness `json:"readiness,omitempty"`
	ReadinessSHA256 string                    `json:"readiness_sha256,omitempty"`
}

func cumulativeUpgradeSteps() []string {
	return []string{"plan-publish", "target-prepare", "control-image-import", "control-volume-create", "control-utility-create", "control-payload-write", "control-utility-remove", "operator-volume-create", "operator-utility-create", "operator-payload-write", "operator-utility-remove", "database-create", "database-execute", "database-remove", "process-stop", "control-remove", "control-create", "control-verify", "replacement-complete"}
}

func cumulativeUpgradeStepsFor(profile string) []string {
	steps := cumulativeUpgradeSteps()
	if profile == wire.MVPPostgreSQLCPUProfile {
		out := append([]string(nil), steps[:11]...)
		out = append(out, "registry-volume-create", "registry-utility-create", "registry-payload-write", "registry-utility-remove", "registry-release-volume-create", "registry-release-utility-create", "registry-release-payload-write", "registry-release-utility-remove")
		return append(out, steps[11:]...)
	}
	return steps
}
func cumulativePhaseRecord(completed map[int]CumulativeUpgradeRecord, phase string) CumulativeUpgradeRecord {
	for _, r := range completed {
		if r.Phase == phase {
			return r
		}
	}
	return CumulativeUpgradeRecord{}
}
func cumulativeRecordProfile(profile string) string {
	if profile == wire.MVPPostgreSQLCPUProfile {
		return profile
	}
	return ""
}

type cumulativeUpgradeRecords struct {
	record              *CumulativeUpgradeRecord
	pending             string
	action              string
	notApplied          bool
	recoveryReference   string
	completed           map[int]CumulativeUpgradeRecord
	replaced, activated bool
	activationSequence  int
}

func cumulativeUpgradeState(events []Event) cumulativeUpgradeRecords {
	s := cumulativeUpgradeRecords{completed: map[int]CumulativeUpgradeRecord{}}
	for _, e := range events {
		if e.CumulativeUpgrade == nil {
			continue
		}
		r := *e.CumulativeUpgrade
		s.record = &r
		switch e.Outcome {
		case "started":
			s.pending = e.OperationID
			s.action = e.Action
			s.notApplied = false
		case "not_applied":
			s.pending = ""
			s.notApplied = true
			s.recoveryReference = e.OperationID
		case "completed":
			s.pending = ""
			s.notApplied = false
			if e.Action == "upgrade_activate" {
				s.activated = true
				s.activationSequence = e.Sequence
			} else {
				s.completed[r.Step] = r
				s.replaced = r.Phase == "replacement-complete"
			}
		}
	}
	return s
}
func cumulativeUpgradeAction(action string) bool {
	switch action {
	case "upgrade_effect", "upgrade_reconcile", "upgrade_recover", "upgrade_retry", "upgrade_activate":
		return true
	}
	return false
}
func cumulativeUpgradeIdentity(a, b CumulativeUpgradeRecord) bool {
	return a.GenerationID == b.GenerationID && a.PlanSHA256 == b.PlanSHA256 && a.TargetProfile == b.TargetProfile
}
func cumulativeUpgradePayloadOnly(e Event) bool {
	return e.JournalCapacity == nil && e.RestoredCurrentStores == nil && e.RestoredLiveDatabase == nil && e.ControlReplacement == nil && e.TLSProjection == nil && e.RestoreStores == nil && e.RestoreDatabase == nil && e.RestoreSQL == nil && e.Restore == nil && e.Backup == nil && e.Engine == nil && e.Image == nil && e.Inputs == nil && e.Volume == nil && e.Runtime == nil && e.Process == nil && e.ModelTask == nil && e.ModelTaskAction == nil && e.DatabaseTask == nil && e.DatabaseTaskAction == nil
}
func validateCumulativeUpgradeEvent(events []Event, e Event) error {
	bad := func() error { return failure("OFFLINE_SCOPE_CUMULATIVE_UPGRADE_TRANSITION_INVALID") }
	r := e.CumulativeUpgrade
	s := cumulativeUpgradeState(events)
	if r == nil {
		return bad()
	}
	steps := cumulativeUpgradeStepsFor(r.TargetProfile)
	if r.TargetProfile != "" && r.TargetProfile != wire.MVPPostgreSQLCPUProfile {
		return bad()
	}
	if !cumulativeUpgradeAction(e.Action) || !cumulativeUpgradePayloadOnly(e) || r == nil || !identityPattern.MatchString(r.GenerationID) || !wire.ValidSHA256(r.PlanSHA256) || r.Step < 0 || r.Step >= len(steps) || r.Phase != steps[r.Step] || s.activated || s.record != nil && !cumulativeUpgradeIdentity(*s.record, *r) {
		return bad()
	}
	if processState(events).pending != "" || len(imageState(events).pending) > 0 || len(volumeState(events).pending) > 0 || runtimeState(events).pending != "" || backupState(events).record != nil && !backupState(events).terminal {
		return bad()
	}
	if s.record == nil {
		backup := backupState(events)
		if r.Step != 0 || backup.record == nil || !backup.terminal || backup.aborted || backup.record.Phase != "complete" || !schemaQualified(events) || !runtimeAccessQualified(events) || !bootstrapQualified(events) {
			return bad()
		}
	}
	unused := func() bool {
		for _, p := range events {
			if p.OperationID == e.OperationID {
				return false
			}
		}
		return true
	}
	if e.Action == "upgrade_activate" {
		if r.Step != len(steps)-1 || !s.replaced || s.pending != "" || e.Outcome != "completed" || e.Code != "" || e.ReferenceOperationID != "" || !unused() || r.Effective != nil || r.ObjectID != "" || r.Installed == nil || !wire.ValidSHA256(r.ReadinessSHA256) {
			return bad()
		}
		installed := r.Installed
		if r.Readiness == nil {
			return bad()
		}
		process := processState(events)
		effectiveCurrent := effectiveRuntimeState(events)
		if process.pending != "" || process.record == nil || process.outcome != "completed" || process.record.Target != "running" || effectiveCurrent.record == nil || process.record.CreationSHA256 != wire.Digest(encode(*effectiveCurrent.record)) {
			return bad()
		}
		for _, after := range process.record.After {
			if after.State != "running" || after.ExitCode != 0 {
				return bad()
			}
		}
		if len(process.record.After) != len(effectiveCurrent.record.Services) || process.record.After["control"].ID != r.Readiness.ControlID || process.record.After["control"].StartedAt != r.Readiness.StartedAt {
			return bad()
		}
		proof := r.Readiness
		started, se := time.Parse(time.RFC3339Nano, proof.StartedAt)
		observed, oe := time.Parse(time.RFC3339Nano, proof.ObservedAt)
		effective := effectiveRuntimeState(events)
		if effective.record == nil || proof.ControlID != effective.record.Services["control"].ID || proof.PID < 1 || se != nil || oe != nil || started.IsZero() || observed.Before(started) || !slices.Equal(proof.Audiences, []string{"portal", "admin"}) || !wire.ValidSHA256(proof.InputSHA256) || r.ReadinessSHA256 != wire.Digest(encode(*proof)) {
			return bad()
		}
		profileValid := r.TargetProfile == "" && installed.Profile == wire.CumulativeCPUProfile && installed.SchemaVersion == 22 || r.TargetProfile == wire.MVPPostgreSQLCPUProfile && installed.Profile == wire.MVPPostgreSQLCPUProfile && installed.SchemaVersion == wire.MVPPostgreSQLCPUV1SchemaVersion
		if !profileValid || installed.ActivationSequence != len(events)+1 || !wire.ValidSHA256(installed.ArchiveSHA256) || !wire.ValidSHA256(installed.ManifestSHA256) || len(installed.SourceCommit) != 40 || !wire.ValidSHA256(installed.SourceCommit+strings.Repeat("0", 24)) || !regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`).MatchString(installed.ReleaseID) {
			return bad()
		}
		return nil
	}
	if s.replaced || r.Installed != nil || r.Readiness != nil || r.ReadinessSHA256 != "" {
		return bad()
	}
	if r.Step != len(s.completed) {
		return bad()
	}
	if e.Action == "upgrade_recover" {
		if s.pending == "" || e.ReferenceOperationID != s.pending || e.Outcome != "not_applied" || e.Code != "" || !unused() || r.ObjectID != "" || r.Effective != nil {
			return bad()
		}
		return nil
	}
	if e.Outcome == "started" {
		if s.pending != "" || e.Code != "" || !unused() || r.ObjectID != "" || r.Effective != nil {
			return bad()
		}
		if e.Action == "upgrade_retry" {
			if !s.notApplied || e.ReferenceOperationID != s.recoveryReference {
				return bad()
			}
		} else if e.Action != "upgrade_effect" || s.notApplied || e.ReferenceOperationID != "" {
			return bad()
		}
		return nil
	}
	if s.pending == "" || s.record == nil || r.Step != s.record.Step {
		return bad()
	}
	if e.Action == "upgrade_reconcile" {
		if !unused() || e.ReferenceOperationID != s.pending {
			return bad()
		}
	} else if e.OperationID != s.pending || e.Action != s.action || e.ReferenceOperationID != "" && e.ReferenceOperationID != s.recoveryReference {
		return bad()
	}
	if e.Action != "upgrade_reconcile" {
		for _, prior := range events {
			if prior.CumulativeUpgrade != nil && prior.OperationID == s.pending && prior.Outcome != "started" {
				return bad()
			}
		}
	}
	if e.Outcome == "uncertain" || e.Outcome == "unresolved" {
		if !codePattern.MatchString(e.Code) || r.ObjectID != "" || r.Effective != nil || e.Action == "upgrade_reconcile" != (e.Outcome == "unresolved") {
			return bad()
		}
		return nil
	}
	if e.Outcome != "completed" || e.Code != "" {
		return bad()
	}
	needsID := r.Phase == "registry-release-utility-create" || r.Phase == "registry-utility-create" || r.Phase == "control-utility-create" || r.Phase == "operator-utility-create" || r.Phase == "database-create" || r.Phase == "control-create"
	if needsID != utilityIDPattern.MatchString(r.ObjectID) || !needsID && r.ObjectID != "" {
		return bad()
	}
	if r.Phase == "replacement-complete" {
		original := effectiveRuntimeState(events)
		if original.record == nil || r.Effective == nil || r.Effective.NetworkID != original.record.NetworkID || r.Effective.IngressNetworkID != original.record.IngressNetworkID || r.Effective.DescriptorSHA256 != original.record.DescriptorSHA256 || !wire.ValidSHA256(r.Effective.ModelSHA256) || r.Effective.ModelSHA256 == original.record.ModelSHA256 || !validRuntimeRecord(r.Effective) || len(r.Effective.Services) != len(original.record.Services) || r.Effective.Services["control"].ID != cumulativePhaseRecord(s.completed, "control-create").ObjectID {
			return bad()
		}
		for name, peer := range original.record.Services {
			if name != "control" && !reflect.DeepEqual(r.Effective.Services[name], peer) {
				return bad()
			}
		}
	} else if r.Effective != nil {
		return bad()
	}
	return nil
}

// The only interleaved effects after replacement are ordinary lifecycle controls
// and complete backup. Other mutations must not reuse pre-upgrade source inputs.
func cumulativeUpgradeMutationGuard(events []Event, e Event) error {
	s := cumulativeUpgradeState(events)
	if e.CumulativeUpgrade != nil {
		return failure("OFFLINE_SCOPE_CUMULATIVE_UPGRADE_TRANSITION_INVALID")
	}
	if s.record == nil {
		return nil
	}
	if s.replaced && (strings.HasPrefix(e.Action, "process_") || strings.HasPrefix(e.Action, "backup_") || e.Action == journalCapacityAction) {
		return nil
	}
	return failure("OFFLINE_SCOPE_CUMULATIVE_UPGRADE_RECONCILIATION_REQUIRED")
}
