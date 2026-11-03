package scope

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/Passion-Flow/ownsolid/tools/release/offline/pkg/bundle"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/compose"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
	view "github.com/Passion-Flow/ownsolid/api/release/view"
)

// The writer is joined even when LoadTagless fails. Neither side is retried.
func (l *locked) importOne(ctx context.Context, c imageEngine, source *bundle.ImageSource, expected ImageRecord) error {
	reader, writer := io.Pipe()
	type outcome struct {
		r bundle.ImportReceipt
		e error
	}
	done := make(chan outcome, 1)
	go func() { r, e := source.WriteTagless(ctx, writer); writer.CloseWithError(e); done <- outcome{r, e} }()
	loadError := c.LoadTagless(ctx, reader)
	reader.CloseWithError(loadError)
	result := <-done
	if loadError != nil {
		return loadError
	}
	if result.e != nil {
		return result.e
	}
	if result.r.SourceSHA256 != expected.SourceSHA256 || result.r.TransportSHA256 != expected.TransportSHA256 || result.r.TransportBytes != expected.TransportBytes || result.r.ExpandedBytes != expected.ExpandedBytes {
		return failure("OFFLINE_SCOPE_CUMULATIVE_UPGRADE_IMAGE_TRANSPORT_CHANGED")
	}
	return nil
}
func (l *locked) confirmCumulativeNotApplied(ctx context.Context, c cumulativeUpgradeEngine, w cumulativeUpgradeOperator, p CumulativeUpgradePlan, model compose.Model, files map[string][]engine.ConfigurationFile, r CumulativeUpgradeRecord) error {
	bad := failure("OFFLINE_SCOPE_CUMULATIVE_UPGRADE_NONAPPLICATION_NOT_ESTABLISHED")
	operator := r.Phase == "operator-volume-create" || r.Phase == "operator-utility-create" || r.Phase == "operator-payload-write" || r.Phase == "operator-utility-remove"
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
		_, e := l.root.Lstat(cumulativeUpgradePlanFile)
		if !os.IsNotExist(e) {
			return bad
		}
	case "control-image-import":
		if _, found, e := c.Inspect(ctx, p.ControlImage.ManifestDigest); e != nil || found {
			return bad
		}
	case "control-volume-create", "operator-volume-create":
		if _, found, e := c.InspectVolume(ctx, owner); e != nil || found {
			return bad
		}
	case "control-utility-create", "operator-utility-create":
		if _, found, e := c.InspectUtility(ctx, owner, p.ControlImage.ManifestDigest, p.UID, p.GID); e != nil || found {
			return bad
		}
	case "control-payload-write", "operator-payload-write":
		if strings.HasPrefix(r.Phase, "registry-release-") {
			o, e := l.registryReleasePayload(ctx, c, owner, p.ControlImage.ManifestDigest, p.UID, p.GID, p.Request.PreparedRoot, p.Request.ArchiveSHA256, p.Request.ManifestSHA256, p.RegistryReleaseMetadataSHA256, p.RegistryReleaseProfile, false)
			if e != nil || o.Status != "observed_empty_owned_volume" {
				return bad
			}
			return nil
		}
		o, e := c.InspectConfiguration(ctx, owner, p.ControlImage.ManifestDigest, p.UID, p.GID, payload)
		if e != nil || o.Status != "observed_empty_owned_volume" {
			return bad
		}
	case "control-utility-remove", "operator-utility-remove":
		if _, found, e := c.InspectUtility(ctx, owner, p.ControlImage.ManifestDigest, p.UID, p.GID); e != nil || !found {
			return bad
		}
	case "database-create":
		if _, found, e := c.InspectDatabaseTask(ctx, cumulativeTask(p)); e != nil || found {
			return bad
		}
	case "database-execute":
		o, found, e := c.InspectDatabaseTask(ctx, cumulativeTask(p))
		if e != nil || !found || o.ID != cumulativePhaseRecord(cumulativeUpgradeState(l.events).completed, "database-create").ObjectID || o.State.Status != "created" || o.State.StartedAt != "0001-01-01T00:00:00Z" {
			return bad
		}
	case "database-remove":
		o, found, e := c.InspectDatabaseTask(ctx, cumulativeTask(p))
		if e != nil || !found || o.ID != cumulativePhaseRecord(cumulativeUpgradeState(l.events).completed, "database-create").ObjectID || o.State.Status != "exited" || o.State.ExitCode != 0 {
			return bad
		}
	case "process-stop":
		_, base, e := l.replacementModels(ctx)
		if e != nil {
			return e
		}
		o, e := w.ObserveRuntime(ctx, c, l.cumulativeRenderContext(p), base)
		if e != nil || !reflect.DeepEqual(o.Services, p.Before.Services) {
			return bad
		}
	case "control-remove":
		o, found, e := c.InspectRuntime(ctx, p.ScopeID, "control")
		if e != nil || !found {
			return bad
		}
		raw := encodeWithoutNewline(o)
		if raw != p.PreviousControlJSON {
			return bad
		}
	case "control-create":
		if _, found, e := c.InspectRuntime(ctx, p.ScopeID, "control"); e != nil || found {
			return bad
		}
	default:
		return failure("OFFLINE_SCOPE_CUMULATIVE_UPGRADE_READONLY_RECONCILIATION_REQUIRED")
	}
	return nil
}
func encodeWithoutNewline(v any) string { raw := encode(v); return string(raw[:len(raw)-1]) }
func (l *locked) activateCumulativeUpgrade(ctx context.Context, c cumulativeUpgradeEngine, w cumulativeUpgradeOperator, r configuration.CumulativeUpgradeRequest) (CumulativeUpgradeResult, error) {
	result := func() CumulativeUpgradeResult { return l.cumulativeUpgradeResult() }
	state := cumulativeUpgradeState(l.events)
	if !state.replaced || state.pending != "" {
		return result(), failure("OFFLINE_SCOPE_CUMULATIVE_UPGRADE_REPLACEMENT_REQUIRED")
	}
	p, e := l.readCumulativeUpgradePlan()
	if e != nil || !reflect.DeepEqual(p.Request, r) {
		return result(), failure("OFFLINE_SCOPE_CUMULATIVE_UPGRADE_REQUEST_CHANGED")
	}
	v, e := l.verifyRuntimeContext(ctx, c, w, false)
	if e != nil {
		return result(), e
	}
	process := processState(l.events)
	if process.pending != "" || process.record == nil || process.outcome != "completed" || process.record.Target != "running" || process.record.CreationSHA256 != wire.Digest(encode(*v.creation.record)) {
		return result(), failure("OFFLINE_SCOPE_CUMULATIVE_UPGRADE_CURRENT_START_REQUIRED")
	}
	for _, s := range v.observed.Services {
		if s.State != "running" || s.ExitCode != 0 {
			return result(), failure("OFFLINE_SCOPE_CUMULATIVE_UPGRADE_RUNNING_PEERS_REQUIRED")
		}
	}
	target := ""
	for _, m := range v.model.Services["control"].Mounts {
		if m.Source == cumulativeOwner(p, false).Role {
			target = m.Target
		}
	}
	input := compose.ControlReadinessInput{Configuration: filepath.ToSlash(filepath.Join(target, "private", "runtime.yaml")), DialAddress: r.ReadinessDialAddress, TimeoutSeconds: r.ReadinessTimeoutSeconds}
	if r.ReadinessCAName != "" {
		input.CAFile = filepath.ToSlash(filepath.Join(target, "private", r.ReadinessCAName))
	}
	readiness, e := w.ProbeControlReadiness(ctx, c, v.scope, v.model, v.observed, input)
	if e != nil {
		return result(), e
	}
	if readiness.ControlID != v.creation.record.Services["control"].ID {
		return result(), failure("OFFLINE_SCOPE_CUMULATIVE_UPGRADE_CURRENT_PROCESS_CHANGED")
	}
	record := *state.record
	record.Effective = nil
	record.ObjectID = ""
	record.Readiness = &readiness
	record.ReadinessSHA256 = wire.Digest(encode(readiness))
	record.Installed = &view.InstalledRelease{ReleaseID: p.Target.ReleaseID, Profile: p.Target.Profile, ArchiveSHA256: p.Request.ArchiveSHA256, ManifestSHA256: p.Request.ManifestSHA256, SourceCommit: p.Target.Cumulative.SourceCommit, SchemaVersion: p.Target.SchemaVersion, ActivationSequence: len(l.events) + 1}
	id, e := nonce()
	if e != nil {
		return result(), e
	}
	e = l.appendEvent(Event{OperationID: id, Action: "upgrade_activate", Outcome: "completed", CumulativeUpgrade: &record})
	return result(), e
}
