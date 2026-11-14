package apps

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

const fixtureDependencies = `[
	{"id":"assistant-model","kind":"model","protocol":"chat"},
	{"id":"app-database","kind":"database","protocol":"postgresql"},
	{"id":"app-storage","kind":"storage","protocol":"s3"}
]`

func withDependencies(raw string) string {
	return strings.Replace(fixtureManifest, `"form":`, `"dependencies":`+raw+`,"form":`, 1)
}

func fixtureID(i int) string { return fmt.Sprintf("aa000000-0000-4000-8000-%012x", i) }

func bindingFixture(t *testing.T) (Manifest, BindingScope, map[string]ResolvedBinding) {
	t.Helper()
	release, err := Load(releaseDirectory(t, withDependencies(fixtureDependencies), fixtureChinese))
	if err != nil {
		t.Fatal(err)
	}
	scope := BindingScope{TenantID: fixtureID(1), WorkspaceID: fixtureID(2), InstallationID: fixtureID(3)}
	resolved := make(map[string]ResolvedBinding)
	for i, slot := range release.Manifest.Dependencies {
		resource := ResourceGrant{
			ID: fixtureID(i + 4), TenantID: scope.TenantID, WorkspaceID: scope.WorkspaceID,
			Kind: slot.Kind, Protocol: slot.Protocol,
		}
		resolved[slot.ID] = ResolvedBinding{
			Resource: resource,
			Secret:   SecretRef{ID: fixtureID(i + 7), DependencyID: slot.ID, ResourceID: resource.ID, Scope: scope},
		}
	}
	return release.Manifest, scope, resolved
}

func TestBindLocalRelease(t *testing.T) {
	manifest, scope, resolved := bindingFixture(t)
	plan, err := manifest.Bind(scope, resolved)
	want := []BoundDependency{
		{Dependency: Dependency{ID: "assistant-model", Kind: Model, Protocol: "chat"}, ResourceID: fixtureID(4), SecretID: fixtureID(7)},
		{Dependency: Dependency{ID: "app-database", Kind: Database, Protocol: "postgresql"}, ResourceID: fixtureID(5), SecretID: fixtureID(8)},
		{Dependency: Dependency{ID: "app-storage", Kind: Storage, Protocol: "s3"}, ResourceID: fixtureID(6), SecretID: fixtureID(9)},
	}
	if err != nil || !reflect.DeepEqual(plan, want) {
		t.Fatalf("expected all bindings in manifest order, got %v, %v", plan, err)
	}
	if values, err := manifest.DecodeValues([]byte(`{"display-name":"Example","mode":"strict","app-database":"substitute"}`)); !errors.Is(err, ErrValues) || values != nil {
		t.Fatalf("form values overrode a binding slot: %v, %v", values, err)
	}

	release, err := Load(releaseDirectory(t, fixtureManifest, fixtureChinese))
	if err != nil {
		t.Fatal(err)
	}
	if plan, err := release.Manifest.Bind(scope, nil); err != nil || len(plan) != 0 {
		t.Fatalf("legacy manifest without dependencies requires no plan: %v, %v", plan, err)
	}
}

func TestRejectInvalidDependencyDeclaration(t *testing.T) {
	for name, raw := range map[string]string{
		"unknown kind":            strings.Replace(fixtureDependencies, `"storage"`, `"file"`, 1),
		"wrong protocol":          strings.Replace(fixtureDependencies, `"postgresql"`, `"chat"`, 1),
		"form ID collision":       strings.Replace(fixtureDependencies, `"app-storage"`, `"display-name"`, 1),
		"dependency ID collision": strings.Replace(fixtureDependencies, `"app-storage"`, `"app-database"`, 1),
		"unsafe binding name":     strings.Replace(fixtureDependencies, `"app-storage"`, `"../storage"`, 1),
		"raw endpoint":            strings.Replace(fixtureDependencies, `"protocol":"s3"`, `"protocol":"s3","endpoint":"https://example.invalid"`, 1),
		"raw secret":              strings.Replace(fixtureDependencies, `"protocol":"s3"`, `"protocol":"s3","secret":"synthetic-value"`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Load(releaseDirectory(t, withDependencies(raw), fixtureChinese)); !errors.Is(err, ErrManifest) {
				t.Fatalf("expected invalid manifest, got %v", err)
			}
		})
	}
}

func TestRejectUnscopedAndIncompleteBindings(t *testing.T) {
	manifest, scope, resolved := bindingFixture(t)
	for name, change := range map[string]func(*ResolvedBinding){
		"wrong workspace":    func(b *ResolvedBinding) { b.Resource.WorkspaceID = fixtureID(10) },
		"wrong tenant":       func(b *ResolvedBinding) { b.Resource.TenantID = fixtureID(10) },
		"wrong protocol":     func(b *ResolvedBinding) { b.Resource.Protocol = "mysql" },
		"wrong kind":         func(b *ResolvedBinding) { b.Resource.Kind = Model },
		"foreign install":    func(b *ResolvedBinding) { b.Secret.Scope.InstallationID = fixtureID(10) },
		"foreign secret":     func(b *ResolvedBinding) { b.Secret.Scope.WorkspaceID = fixtureID(10) },
		"wrong slot":         func(b *ResolvedBinding) { b.Secret.DependencyID = "app-storage" },
		"wrong resource":     func(b *ResolvedBinding) { b.Secret.ResourceID = fixtureID(10) },
		"credential reused":  func(b *ResolvedBinding) { b.Secret.ID = resolved["assistant-model"].Secret.ID },
		"raw path":           func(b *ResolvedBinding) { b.Secret.ID = "/run/secrets/foreign" },
		"missing credential": func(b *ResolvedBinding) { b.Secret.ID = "" },
	} {
		t.Run(name, func(t *testing.T) {
			const id = "app-database"
			original := resolved[id]
			changed := original
			change(&changed)
			resolved[id] = changed
			defer func() { resolved[id] = original }()
			if plan, err := manifest.Bind(scope, resolved); !errors.Is(err, ErrBindings) || plan != nil {
				t.Fatalf("expected rejection with no partial plan, got %v, %v", plan, err)
			}
		})
	}

	binding := resolved["app-database"]
	delete(resolved, "app-database")
	if _, err := manifest.Bind(scope, resolved); !errors.Is(err, ErrBindings) {
		t.Fatalf("accepted a missing binding: %v", err)
	}
	resolved["unknown"] = binding
	if _, err := manifest.Bind(scope, resolved); !errors.Is(err, ErrBindings) {
		t.Fatalf("accepted an unknown binding in place of a required one: %v", err)
	}
	if _, err := (Manifest{}).Bind(scope, nil); !errors.Is(err, ErrManifest) {
		t.Fatalf("accepted an invalid manifest: %v", err)
	}
	if _, err := manifest.Bind(BindingScope{}, resolved); !errors.Is(err, ErrBindings) {
		t.Fatalf("accepted an invalid binding scope: %v", err)
	}
}
