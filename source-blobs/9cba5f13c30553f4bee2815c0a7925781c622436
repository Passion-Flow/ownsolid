package postgresbackup

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"time"

	release "github.com/Passion-Flow/ownsolid/api/release/v1"
)

type restoreRootsInspectionLease interface {
	Check(bool) error
	Identities() []RootIdentity
	Close() error
}

// InspectRestoreRoots opens no SQL, DSN, store owner, process, scratch or output
// file. The only store handles are the original readonly directory FDs/flocks.
// No successful observation escapes until every original handle has closed.
func InspectRestoreRoots(ctx context.Context, configuration, environment, request, policyEnvironment string, r release.PostgreSQLRestoreRootsInspectionRequest) (release.PostgreSQLRestoreRootsInspectionObservation, error) {
	return inspectRestoreRootsUsing(ctx, configuration, environment, request, policyEnvironment, r, func(paths []string) (restoreRootsInspectionLease, error) { return acquireFreshRoots(paths) })
}

// The dependency is package-private and production always selects the original
// acquireFreshRoots. Tests can inject a real close failure after successful checks.
func inspectRestoreRootsUsing(ctx context.Context, configuration, environment, request, policyEnvironment string, r release.PostgreSQLRestoreRootsInspectionRequest, acquire func([]string) (restoreRootsInspectionLease, error)) (out release.PostgreSQLRestoreRootsInspectionObservation, result error) {
	bad := errors.New("POSTGRES_RESTORE_ROOTS_INSPECTION_INVALID")
	if ctx == nil || !r.Valid() || acquire == nil {
		return out, bad
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(r.Policy.OperationSeconds)*time.Second)
	defer cancel()
	if e := RefuseAmbient(); e != nil {
		return out, e
	}
	configurationRoot, e := openPrivateRoot(r.RuntimeConfigurationRoot)
	if e != nil {
		return out, bad
	}
	defer func() {
		result = errors.Join(result, configurationRoot.Close())
		if result != nil {
			out = release.PostgreSQLRestoreRootsInspectionObservation{}
		}
	}()
	initialInfo, e := configurationRoot.Stat(".")
	if e != nil {
		return out, bad
	}
	initialRoot, e := rootIdentity(r.RuntimeConfigurationRoot, initialInfo)
	if e != nil {
		return out, bad
	}
	result = withRestoreRootsInspectionFiles(ctx, restoreRootsInspectionRecords(configuration, environment, request, policyEnvironment), func() (result error) {
		c, e := captureRestoreRootsInspection(ctx, configuration, environment, request, policyEnvironment, r)
		if e != nil {
			return e
		}
		roots, e := acquire(storeRoots(c))
		if e != nil {
			return e
		}
		if roots == nil {
			return bad
		}
		defer func() { result = errors.Join(result, roots.Close()) }()
		if e = roots.Check(true); e != nil {
			return e
		}
		identities := roots.Identities()
		if len(identities) != 4 || !release.ValidPostgreSQLRestoreRoots(identities) {
			return bad
		}
		paths := storeRoots(c)
		for i, id := range identities {
			if id.Path != paths[i] || id.UID != r.ExpectedUID || id.GID != r.ExpectedGID {
				return bad
			}
		}
		latest, e := captureRestoreRootsInspection(ctx, configuration, environment, request, policyEnvironment, r)
		if e != nil || !reflect.DeepEqual(c, latest) {
			return bad
		}
		if e = roots.Check(true); e != nil {
			return e
		}
		info, e := configurationRoot.Stat(".")
		if e != nil {
			return bad
		}
		currentRoot, e := rootIdentity(r.RuntimeConfigurationRoot, info)
		if e != nil || currentRoot != initialRoot {
			return bad
		}
		// Also pin the named configuration root, not only its still-open handle.
		current, e := openPrivateRoot(r.RuntimeConfigurationRoot)
		if e != nil {
			return bad
		}
		info, e = current.Stat(".")
		ce := current.Close()
		if e != nil || ce != nil {
			return bad
		}
		currentRoot, e = rootIdentity(r.RuntimeConfigurationRoot, info)
		if e != nil || currentRoot != initialRoot {
			return bad
		}
		if ctx.Err() != nil {
			return errors.New("POSTGRES_RESTORE_ROOTS_INSPECTION_INTERRUPTED")
		}
		out = release.PostgreSQLRestoreRootsInspectionObservation{Format: release.PostgreSQLRestoreRootsInspectionFormat, RequestSHA256: r.SHA256(), InspectionID: r.InspectionID, TargetScopeID: r.TargetScopeID, Binding: r.Binding, RuntimeConfigurationSHA256: r.RuntimeConfigurationSHA256, RuntimeEnvironmentSHA256: r.RuntimeEnvironmentSHA256, EffectiveConfigurationSHA256: restoreConfigurationDigest(c), Roots: identities, RootsSHA256: release.PostgreSQLRestoreRootsSHA256(identities), ReadOnly: true, CurrentLockHeld: false, ObservedAt: time.Now().UTC().Format(time.RFC3339Nano), Qualification: release.PostgreSQLRestoreRootsInspectionQualification}
		return nil
	})
	if result != nil {
		return release.PostgreSQLRestoreRootsInspectionObservation{}, result
	}
	if ctx.Err() != nil || !out.ValidFor(r) {
		return release.PostgreSQLRestoreRootsInspectionObservation{}, bad
	}
	raw, e := json.Marshal(out)
	if e != nil || int64(len(raw)+1) > r.Policy.MaximumRecordBytes {
		return release.PostgreSQLRestoreRootsInspectionObservation{}, errors.New("POSTGRES_RESTORE_ROOTS_INSPECTION_OUTPUT_LIMIT_REACHED")
	}
	return out, nil
}
