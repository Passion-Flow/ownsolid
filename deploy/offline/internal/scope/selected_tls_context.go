package scope

import (
	"context"
	"reflect"
	"slices"
	"time"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/compose"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/tlsidentity"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

func (l *locked) previousSelectedTLSPlan(generation string) (RestoredTLSPlan, error) {
	view := *l
	view.events = generationPrefix(l.events, generation, false)
	previous := lastCompletedReplacement(view.events)
	if previous == nil {
		return RestoredTLSPlan{}, failure("OFFLINE_SCOPE_SELECTED_TLS_COMPLETED_PREDECESSOR_REQUIRED")
	}
	for i := len(view.events) - 1; i >= 0; i-- {
		if r := view.events[i].TLSProjection; r != nil && r.GenerationID == previous.GenerationID {
			return view.readTLSProjectionPlan(*r)
		}
	}
	return RestoredTLSPlan{}, failure("OFFLINE_SCOPE_SELECTED_TLS_COMPLETED_PREDECESSOR_REQUIRED")
}

type selectedTLSConfigurationReader interface {
	ReadCurrentControlConfiguration(context.Context, engine.Ownership, string, string, int, int, []engine.ConfigurationFileDigest) ([]engine.ConfigurationFile, error)
}

// Actual preceding bytes come only from the owned mount with an independently
// admitted full index. This is used before any new generation/container effect.
func (l *locked) readSelectedTLSPredecessor(ctx context.Context, client runtimeEngine, generation, target string, v verifiedRuntimeContext) ([]engine.ConfigurationFile, error) {
	previous, e := l.previousSelectedTLSPlan(generation)
	if e != nil {
		return nil, e
	}
	reader, ok := client.(selectedTLSConfigurationReader)
	if !ok {
		return nil, failure("OFFLINE_SCOPE_SELECTED_TLS_PRIVATE_READBACK_REQUIRED")
	}
	_, model, e := l.replacementModels(ctx)
	if e != nil {
		return nil, e
	}
	if _, e = selectedRestoredTLSProfile(v.inputs, model, target); e != nil {
		return nil, e
	}
	id, owner, payload, e := l.previousTLSConfiguration(ctx, generation)
	if e != nil || owner != previous.ConfigurationOwner || payload != previous.PayloadSHA256 {
		return nil, failure("OFFLINE_SCOPE_SELECTED_TLS_PREDECESSOR_CHANGED")
	}
	object, found, e := client.InspectRuntime(ctx, l.marker.ScopeID, "control")
	if e != nil || !found || object.ID != id || !slices.Equal(object.Config.Entrypoint, []string{"/usr/local/bin/control"}) || !slices.Equal(object.Config.Cmd, model.Services["control"].Command) {
		return nil, failure("OFFLINE_SCOPE_SELECTED_TLS_LITERAL_RUNTIME_REQUIRED")
	}
	targetPath := ""
	for _, mount := range model.Services["control"].Mounts {
		if mount.Source == "control-config" {
			targetPath = mount.Target
		}
	}
	index := make([]engine.ConfigurationFileDigest, len(previous.ConfigurationFiles))
	for i, file := range previous.ConfigurationFiles {
		index[i] = engine.ConfigurationFileDigest{Name: file.Name, Bytes: file.Bytes, SHA256: file.SHA256}
	}
	files, e := reader.ReadCurrentControlConfiguration(ctx, owner, id, targetPath, previous.UID, previous.GID, index)
	if e != nil {
		return nil, e
	}
	if restoreConfigurationDigest(files) != previous.PayloadSHA256 {
		return nil, failure("OFFLINE_SCOPE_SELECTED_TLS_PREDECESSOR_CHANGED")
	}
	return files, nil
}

func validateSelectedTLSMetadata(p RestoredTLSPlan, snapshot preparedInputs, model compose.Model, previous RestoredTLSPlan, maximum int64) error {
	bad := func() error { return failure("OFFLINE_SCOPE_TLS_PROJECTION_PLAN_CONTEXT_CHANGED") }
	if previous.PayloadSHA256 != p.PreviousPayloadSHA256 || wire.Digest(encode(previous.ConfigurationFiles)) != p.PreviousPayloadSHA256 {
		return bad()
	}
	profile, e := selectedRestoredTLSProfile(snapshot, model, p.Request.Target)
	if e != nil || p.CertificateName != profile.CertificateName || p.PrivateKeyName != profile.PrivateKeyName {
		return bad()
	}
	var hosts []string
	if p.Request.Target == "machine" {
		hosts, e = tlsidentity.MachineOriginHosts(profile.Origins[0])
	} else {
		hosts, e = tlsidentity.OriginHosts(profile.Origins)
	}
	if e != nil || !reflect.DeepEqual(hosts, p.Identity.Hosts) {
		return bad()
	}
	before, be := time.Parse(time.RFC3339, p.Identity.NotBefore)
	after, ae := time.Parse(time.RFC3339, p.Identity.NotAfter)
	if be != nil || ae != nil || !after.After(before) {
		return bad()
	}
	for _, hash := range []string{p.Identity.LeafSHA256, p.Identity.CertificateSHA256, p.Identity.PrivateKeySHA256, p.Identity.TrustBundleSHA256, p.PayloadSHA256} {
		if !wire.ValidSHA256(hash) {
			return bad()
		}
	}
	if len(p.ConfigurationFiles) != len(previous.ConfigurationFiles) || p.PayloadSHA256 == p.PreviousPayloadSHA256 || wire.Digest(encode(p.ConfigurationFiles)) != p.PayloadSHA256 {
		return bad()
	}
	for i, file := range p.ConfigurationFiles {
		old := previous.ConfigurationFiles[i]
		if file.Name != old.Name || file.Bytes < 1 || file.Bytes > maximum || !wire.ValidSHA256(file.SHA256) {
			return bad()
		}
		switch file.Name {
		case p.CertificateName:
			if file.SHA256 != p.Identity.CertificateSHA256 || file.SHA256 == old.SHA256 {
				return bad()
			}
		case p.PrivateKeyName:
			if file.SHA256 != p.Identity.PrivateKeySHA256 {
				return bad()
			}
		default:
			if file != old {
				return bad()
			}
		}
	}
	return nil
}

func (l *locked) admitSelectedTLSCurrent(ctx context.Context, p RestoredTLSPlan, now time.Time) error {
	if p.FormatVersion != 2 {
		return nil
	}
	files, trust, e := l.readGenerationPayload(ctx, p)
	if e != nil {
		return e
	}
	base, model, e := l.replacementModels(ctx)
	if e != nil {
		return e
	}
	base.files = map[string][]engine.ConfigurationFile{"control-config": files}
	profile, e := selectedRestoredTLSProfile(base, model, p.Request.Target)
	if e != nil {
		return e
	}
	certificate, e := restoreNamedFile(files, profile.CertificateName)
	if e != nil {
		return e
	}
	key, e := restoreNamedFile(files, profile.PrivateKeyName)
	if e != nil {
		return e
	}
	var identity tlsidentity.Identity
	if p.Request.Target == "machine" {
		identity, e = tlsidentity.ValidateMachine(certificate, key, trust, profile.Origins[0], now)
	} else {
		identity, e = tlsidentity.Validate(certificate, key, trust, profile.Origins, now)
	}
	if e != nil {
		return e
	}
	if !reflect.DeepEqual(identity, p.Identity) {
		return failure("OFFLINE_SCOPE_SELECTED_TLS_CURRENT_IDENTITY_CHANGED")
	}
	return nil
}

func (l *locked) admitEffectiveTLSStart(ctx context.Context) error {
	previous := lastCompletedReplacement(l.events)
	if previous == nil {
		return nil
	}
	for i := len(l.events) - 1; i >= 0; i-- {
		if r := l.events[i].TLSProjection; r != nil && r.GenerationID == previous.GenerationID {
			if !r.PayloadCustody {
				return nil
			}
			p, e := l.readTLSProjectionPlan(*r)
			if e != nil {
				return e
			}
			return l.admitSelectedTLSCurrent(ctx, p, time.Now())
		}
	}
	return failure("OFFLINE_SCOPE_SELECTED_TLS_COMPLETED_PREDECESSOR_REQUIRED")
}
