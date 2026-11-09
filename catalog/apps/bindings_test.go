package apps

import (
	"errors"
	"fmt"
	"maps"
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

func TestVerifyInstallBindingsUsesSignedDependencies(t *testing.T) {
	manifest, scope, resolved := bindingFixture(t)
	root, key, private := transitionCatalog(t)
	pin := writeTransitionRelease(t, root, private, catalogReleaseFixture{
		version: "1.2.0", engine: "compose", dependencies: manifest.Dependencies,
	})
	list, err := ListReleaseMetadata(t.Context(), root, key, manifest.ID)
	if err != nil || len(list) != 1 || list[0].Pin != pin ||
		list[0].English.Title != "Example app" || list[0].SimplifiedChinese.Title != "\u793a\u4f8b\u5e94\u7528" {
		t.Fatalf("did not list the signed bilingual dependency declaration: %v", err)
	}
	request := InitialReleaseRequest{AppID: manifest.ID, Pin: list[0].Pin, PlatformVersion: "1.2.0"}
	installed, err := VerifyInitialRelease(t.Context(), root, key, testTransitionBudget, request)
	if err != nil || installed.Pin != request.Pin || !reflect.DeepEqual(installed.Manifest.Dependencies, manifest.Dependencies) {
		t.Fatalf("complete signed payload verification failed: %v", err)
	}
	plan, err := VerifyInstallBindings(t.Context(), root, key, request, scope, resolved)
	want, bindErr := manifest.Bind(scope, resolved)
	if err != nil || bindErr != nil || plan.Pin != pin || plan.Scope != scope || !reflect.DeepEqual(plan.Dependencies, want) {
		t.Fatalf("the exact signed release did not return a scoped plan: %v, %v", err, bindErr)
	}
	for _, tc := range []struct {
		name   string
		change func(*InitialReleaseRequest, map[string]ResolvedBinding, ResolvedBinding)
		want   error
	}{
		{"missing binding", func(_ *InitialReleaseRequest, entries map[string]ResolvedBinding, _ ResolvedBinding) {
			delete(entries, "app-database")
		}, ErrBindings},
		{"wrong resource type", func(_ *InitialReleaseRequest, entries map[string]ResolvedBinding, b ResolvedBinding) {
			b.Resource.Kind, b.Resource.Protocol = Storage, "s3"
			entries["app-database"] = b
		}, ErrBindings},
		{"foreign workspace", func(_ *InitialReleaseRequest, entries map[string]ResolvedBinding, b ResolvedBinding) {
			b.Resource.WorkspaceID = fixtureID(15)
			entries["app-database"] = b
		}, ErrBindings},
		{"foreign installation", func(_ *InitialReleaseRequest, entries map[string]ResolvedBinding, b ResolvedBinding) {
			b.Secret.Scope.InstallationID = fixtureID(16)
			entries["app-database"] = b
		}, ErrBindings},
		{"direct credential", func(_ *InitialReleaseRequest, entries map[string]ResolvedBinding, b ResolvedBinding) {
			b.Secret.ID = "synthetic-direct-value"
			entries["app-database"] = b
		}, ErrBindings},
		{"stale pin", func(selection *InitialReleaseRequest, _ map[string]ResolvedBinding, _ ResolvedBinding) {
			selection.Pin.BundleSHA256 = digestBytes([]byte("different signed inventory"))
		}, ErrBindings},
		{"incompatible platform", func(selection *InitialReleaseRequest, _ map[string]ResolvedBinding, _ ResolvedBinding) {
			selection.PlatformVersion = "2.0.0"
		}, ErrReleaseTransition},
	} {
		t.Run(tc.name, func(t *testing.T) {
			selection, entries := request, maps.Clone(resolved)
			tc.change(&selection, entries, entries["app-database"])
			result, err := VerifyInstallBindings(t.Context(), root, key, selection, scope, entries)
			if !errors.Is(err, tc.want) || result.Pin != (ReleasePin{}) || result.Scope != (BindingScope{}) || result.Dependencies != nil {
				t.Fatalf("expected a refusal with no partial binding plan: %v", err)
			}
			if strings.Contains(err.Error(), "synthetic-direct-value") {
				t.Fatal("binding error disclosed the submitted credential value")
			}
		})
	}
}

func TestVerifyInstallBindingsRefusesCredentialInSignedDeclaration(t *testing.T) {
	manifest, scope, resolved := bindingFixture(t)
	root, key, private := transitionCatalog(t)
	pin := writeTransitionRelease(t, root, private, catalogReleaseFixture{
		version: "1.2.0", engine: "helm", dependencies: manifest.Dependencies,
	})
	version, err := root.OpenRoot(pin.Version)
	if err != nil {
		t.Fatal(err)
	}
	defer version.Close()
	index, err := readIndex(version, key)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := version.ReadFile(manifestName)
	if err != nil {
		t.Fatal(err)
	}
	changed := strings.Replace(string(raw), `"protocol":"postgresql"`, `"protocol":"postgresql","password":"synthetic-direct-value"`, 1)
	if changed == string(raw) {
		t.Fatal("fixture did not put the value into the signed declaration")
	}
	if err := version.WriteFile(manifestName, []byte(changed), 0600); err != nil {
		t.Fatal(err)
	}
	index.Manifest = digestBytes([]byte(changed))
	signed := ociJSON(t, index)
	signFixtureIndex(t, version, private, signed)
	pin.BundleSHA256 = digestBytes(signed)
	request := InitialReleaseRequest{AppID: manifest.ID, Pin: pin, PlatformVersion: "1.2.0"}
	result, err := VerifyInstallBindings(t.Context(), root, key, request, scope, resolved)
	if !errors.Is(err, ErrManifest) || result.Pin != (ReleasePin{}) || result.Dependencies != nil ||
		strings.Contains(err.Error(), "synthetic-direct-value") {
		t.Fatalf("a signed declaration with a direct credential reached binding: %v", err)
	}
}
