package scope

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"os"
	"reflect"

	"github.com/Passion-Flow/ownsolid/tools/release/offline/pkg/bundle"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/compose"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	compat "github.com/Passion-Flow/ownsolid/api/release/restorecompat"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

type restoreCompatibilityProof struct {
	Authorization compat.RestoreCompatibility `json:"authorization"`
	verified      bool
}

func (p *restoreCompatibilityProof) matches(m Marker, s RestoreSourceResult) bool {
	if p == nil || !p.verified {
		return false
	}
	b := p.Authorization.Binding
	return b.Validate() == nil && b.SourceScopeID == s.SourceScopeID && b.TargetScopeID == m.ScopeID && b.SourceBackupManifestSHA256 == s.ManifestSHA256 && b.SourceReleaseID == s.ReleaseID && b.TargetReleaseID == m.ReleaseID && b.TargetManifestSHA256 == m.ManifestSHA256 && b.Profile == s.Profile && b.Profile == m.Profile && b.SchemaVersion == s.Source.SchemaVersion && reflect.DeepEqual(b.Stores, s.Stores)
}

func restoreTargetIdentityAuthorized(m Marker, s RestoreSourceResult, p ...*restoreCompatibilityProof) error {
	if len(p) == 0 || len(p) == 1 && p[0] == nil {
		return restoreTargetIdentity(m, s)
	}
	if len(p) != 1 || !p[0].matches(m, s) {
		return failure("OFFLINE_SCOPE_RESTORE_COMPATIBILITY_BINDING_REQUIRED")
	}
	return nil
}

func (l *locked) verifyRestoreCompatibility(p *restoreCompatibilityProof, s RestoreSourceResult) error {
	if p == nil {
		return nil
	}
	p.verified = false
	raw, e := bundle.ReadProtectedConfiguration(l.config.Verification.TrustFile)
	if e != nil {
		return failure("OFFLINE_SCOPE_RESTORE_COMPATIBILITY_TRUST_REQUIRED")
	}
	var trust struct {
		Keys map[string]string `json:"keys"`
	}
	if wire.StrictJSON(raw, &trust) != nil || len(trust.Keys) < 1 || len(trust.Keys) > 16 {
		return failure("OFFLINE_SCOPE_RESTORE_COMPATIBILITY_TRUST_INVALID")
	}
	keys := map[string]ed25519.PublicKey{}
	for id, value := range trust.Keys {
		key, e := base64.StdEncoding.Strict().DecodeString(value)
		if e != nil || len(key) != ed25519.PublicKeySize || base64.StdEncoding.EncodeToString(key) != value || wire.Digest(key) != id {
			return failure("OFFLINE_SCOPE_RESTORE_COMPATIBILITY_TRUST_INVALID")
		}
		keys[id] = ed25519.PublicKey(key)
	}
	if compat.VerifyRestoreCompatibility(p.Authorization, keys) != nil {
		return failure("OFFLINE_SCOPE_RESTORE_COMPATIBILITY_UNTRUSTED_OR_INVALID")
	}
	p.verified = true
	if !p.matches(l.marker, s) {
		p.verified = false
		return failure("OFFLINE_SCOPE_RESTORE_COMPATIBILITY_BINDING_REQUIRED")
	}
	return nil
}

func (l *locked) readRestoreCompatibility(name string, s RestoreSourceResult) (*restoreCompatibilityProof, error) {
	if name == "" {
		return nil, nil
	}
	raw, e := bundle.ReadProtectedConfiguration(name)
	var a compat.RestoreCompatibility
	if e != nil || int64(len(raw)) > l.config.Verification.MaximumMetadataBytes || wire.StrictJSON(raw, &a) != nil {
		return nil, failure("OFFLINE_SCOPE_RESTORE_COMPATIBILITY_FILE_INVALID")
	}
	p := &restoreCompatibilityProof{Authorization: a}
	if e = l.verifyRestoreCompatibility(p, s); e != nil {
		return nil, e
	}
	return p, nil
}

func (l *locked) validRestorePlan(p RestorePlan) bool {
	if (p.Request.CompatibilityFile == "") != (p.Compatibility == nil) || l.verifyRestoreCompatibility(p.Compatibility, p.Source) != nil {
		return false
	}
	return validRestorePlan(p, l.marker, l.events)
}

// A recorded private plan is the authority after durable publication. A lost
// publication still requires the original protected certificate to reconstruct
// that exact intent; it never silently adopts another authorization.
func (l *locked) restoreReservationCompatibility(r configuration.RestoreRequest, source RestoreSourceResult) (*restoreCompatibilityProof, error) {
	if r.CompatibilityFile != "" && restoreState(l.events).record != nil {
		if _, e := l.root.Lstat(restorePlanFile); e == nil {
			p, e := l.readBoundRestorePlan()
			if e != nil || !reflect.DeepEqual(p.Request, r) || !reflect.DeepEqual(p.Source, source) {
				return nil, failure("OFFLINE_SCOPE_RESTORE_RESERVATION_CONTEXT_CHANGED")
			}
			return p.Compatibility, nil
		} else if !os.IsNotExist(e) {
			return nil, failure("OFFLINE_SCOPE_RESTORE_PLAN_UNAVAILABLE")
		}
	}
	return l.readRestoreCompatibility(r.CompatibilityFile, source)
}

func InspectCompatibleRestoreTarget(ctx context.Context, c configuration.Config, r configuration.RestoreRequest) (RestoreTargetResult, error) {
	if r.Validate() != nil || r.CompatibilityFile == "" || c.Engine == nil || c.Compose == nil || c.Compose.Process == nil {
		return RestoreTargetResult{}, failure("OFFLINE_SCOPE_RESTORE_COMPATIBILITY_REQUEST_REQUIRED")
	}
	s, e := InspectRestoreSource(ctx, c, r.RestoreSourceRequest)
	if e != nil {
		return RestoreTargetResult{}, e
	}
	l, e := open(c, false)
	if e != nil {
		return RestoreTargetResult{}, e
	}
	defer l.close()
	p, e := l.readRestoreCompatibility(r.CompatibilityFile, s)
	if e != nil {
		return RestoreTargetResult{}, e
	}
	client, e := engine.New(*c.Engine)
	if e != nil {
		return RestoreTargetResult{}, e
	}
	defer client.Close()
	worker, e := compose.New(c)
	if e != nil {
		return RestoreTargetResult{}, e
	}
	return l.inspectRestoreTarget(ctx, r.RestoreSourceRequest, s, client, worker, p)
}
