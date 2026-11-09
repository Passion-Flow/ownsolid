package apps

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"os"
	"regexp"
	"slices"
)

const maxDependencies = 32

// ErrBindings identifies an incomplete, mismatched or unauthorized binding plan.
var ErrBindings = errors.New("CATALOG_BINDINGS_INVALID")

var referenceID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

// DependencyKind classifies the resource that will satisfy a declared slot.
type DependencyKind string

const (
	// Model requires a model route through the workspace's gateway.
	Model DependencyKind = "model"
	// Database requires a database grant in the workspace.
	Database DependencyKind = "database"
	// Storage requires an object storage grant in the workspace.
	Storage DependencyKind = "storage"
)

// Dependency is a required slot. ID becomes the binding directory name inside
// the workload and is independent of the selected resource's identity.
type Dependency struct {
	ID       string         `json:"id"`
	Kind     DependencyKind `json:"kind"`
	Protocol string         `json:"protocol"`
}

func (d Dependency) valid() bool {
	switch d.Kind {
	case Model:
		return slices.Contains([]string{"chat", "embeddings", "rerank"}, d.Protocol)
	case Database:
		return slices.Contains([]string{"postgresql", "mysql", "redis"}, d.Protocol)
	case Storage:
		return d.Protocol == "s3"
	default:
		return false
	}
}

// BindingScope is the authenticated tenant, workspace and application installation.
type BindingScope struct {
	TenantID, WorkspaceID, InstallationID string
}

func (s BindingScope) valid() bool {
	return referenceID.MatchString(s.TenantID) &&
		referenceID.MatchString(s.WorkspaceID) &&
		referenceID.MatchString(s.InstallationID)
}

// ResourceGrant identifies a resource that its owner authorized for this workspace.
type ResourceGrant struct {
	ID, TenantID, WorkspaceID string
	Kind                      DependencyKind
	Protocol                  string
}

// SecretRef is an opaque reference from the secret owner to the files for one
// resource binding. It carries neither credential bytes nor host file paths.
type SecretRef struct {
	ID, DependencyID, ResourceID string
	Scope                        BindingScope
}

// ResolvedBinding pairs a resource grant with its installation-specific secret
// reference. Both must be obtained from their trusted owners.
type ResolvedBinding struct {
	Resource ResourceGrant
	Secret   SecretRef
}

// BoundDependency identifies the approved resource and secret files for one slot.
type BoundDependency struct {
	Dependency
	ResourceID, SecretID string
}

// InstallBindingPlan identifies the signed release, authenticated installation
// and owner-resolved dependency references for one operation.
type InstallBindingPlan struct {
	Pin          ReleasePin
	Scope        BindingScope
	Dependencies []BoundDependency
}

// VerifyInstallBindings binds the exact selected signed release to scoped
// references from the resource and secret owners. The caller verifies the
// payloads before installation and rechecks the grants before projection.
func VerifyInstallBindings(ctx context.Context, appRoot *os.Root, key ed25519.PublicKey, request InitialReleaseRequest, scope BindingScope, resolved map[string]ResolvedBinding) (InstallBindingPlan, error) {
	if appRoot == nil || len(key) != ed25519.PublicKeySize || !validID(request.AppID) || !request.Pin.valid() || !scope.valid() {
		return InstallBindingPlan{}, fmt.Errorf("%w: supply the selected signed release and authenticated installation", ErrBindings)
	}
	release, err := readSignedVersion(ctx, appRoot, key, request.AppID, request.Pin.Version)
	if err != nil {
		return InstallBindingPlan{}, err
	}
	if release.Pin != request.Pin {
		return InstallBindingPlan{}, fmt.Errorf("%w: reselect the exact signed release before resolving dependencies", ErrBindings)
	}
	if err := checkTransitionPlatform(release.Manifest, request.PlatformVersion); err != nil {
		return InstallBindingPlan{}, err
	}
	plan, err := release.Manifest.Bind(scope, resolved)
	if err != nil {
		return InstallBindingPlan{}, err
	}
	return InstallBindingPlan{Pin: release.Pin, Scope: scope, Dependencies: plan}, nil
}

// Bind validates a complete set of owner-resolved references and returns it in
// manifest order. The installer must recheck the references before projecting
// files read-only into the intended containers. Never bind client-supplied grants.
func (m Manifest) Bind(scope BindingScope, resolved map[string]ResolvedBinding) ([]BoundDependency, error) {
	if err := m.validate(); err != nil {
		return nil, err
	}
	if !scope.valid() || len(resolved) != len(m.Dependencies) {
		return nil, fmt.Errorf("%w: resolve each declared dependency in the application installation", ErrBindings)
	}
	plan := make([]BoundDependency, 0, len(m.Dependencies))
	seenSecrets := make(map[string]bool, len(m.Dependencies))
	for _, slot := range m.Dependencies {
		binding, found := resolved[slot.ID]
		if !found || !binding.matches(scope, slot) || seenSecrets[binding.Secret.ID] {
			return nil, fmt.Errorf("%w: resolve a matching resource and dedicated secret for %s", ErrBindings, slot.ID)
		}
		seenSecrets[binding.Secret.ID] = true
		plan = append(plan, BoundDependency{Dependency: slot, ResourceID: binding.Resource.ID, SecretID: binding.Secret.ID})
	}
	return plan, nil
}

func (b ResolvedBinding) matches(scope BindingScope, slot Dependency) bool {
	resource, secret := b.Resource, b.Secret
	return resource.TenantID == scope.TenantID && resource.WorkspaceID == scope.WorkspaceID &&
		resource.Kind == slot.Kind && resource.Protocol == slot.Protocol && referenceID.MatchString(resource.ID) &&
		secret.Scope == scope && secret.ResourceID == resource.ID && secret.DependencyID == slot.ID &&
		referenceID.MatchString(secret.ID)
}
