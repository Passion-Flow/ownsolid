package scope

import (
	"encoding/json"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	"reflect"
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/compose"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
	view "github.com/Passion-Flow/ownsolid/api/release/view"
)

func TestMVPConfigurationPreservesEveryM17PrivateValueAndViewReference(t *testing.T) {
	original := []byte(`{"server":{"application_profile":"releases","transport_mode":"http","listen_address":"127.0.0.1:8080","admin_origin":"http://127.0.0.1:8080"},"release_view_config_file":"/run/projectone/control/private/existing-view.json","database":{"dsn_file":"/run/projectone/private/runtime-dsn"},"models":{"artifact_root":"/var/lib/projectone/models/private"},"sso":{"enabled":false,"keyring_file":"/run/projectone/private/key"},"feature":{"retry":3,"default":false}}`)
	target, err := cumulativeMVPProfile(original, "/run/projectone/control/private/existing-view.json")
	if err != nil {
		t.Fatal(err)
	}
	var before, after map[string]any
	json.Unmarshal(original, &before)
	json.Unmarshal(target, &after)
	if _, present := after["server"].(map[string]any)["application_profile"]; present {
		t.Fatal("new application missing")
	}
	after["server"].(map[string]any)["application_profile"] = "releases"
	if !reflect.DeepEqual(before, after) {
		t.Fatal("unrelated configuration, secret reference, transport or false policy changed")
	}
	for _, old := range []string{"platform", "localization", "releases-cache-v1", wire.MVPPostgreSQLCPUApplicationProfile} {
		if _, err := cumulativeMVPProfile([]byte(strings.Replace(string(original), `"application_profile":"releases"`, `"application_profile":"`+old+`"`, 1)), "/run/projectone/control/private/existing-view.json"); err == nil {
			t.Fatal("wrong original profile admitted", old)
		}
	}
	if _, err := cumulativeMVPProfile(original, "/run/projectone/control/private/other-view.json"); err == nil {
		t.Fatal("original policy reference rewritten")
	}
}

func TestMVPUpgradeOnlyAcceptsTheTwoDeclaredProfileTransitions(t *testing.T) {
	profiles := []string{wire.CumulativePredecessorCPUProfile, wire.CumulativeCPUProfile, wire.MVPPostgreSQLCPUProfile, wire.CumulativeCacheCPUProfile, wire.CumulativeKubernetesCPUProfile, wire.PlatformProfile}
	for _, before := range profiles {
		for _, after := range profiles {
			want := before == wire.CumulativePredecessorCPUProfile && after == wire.CumulativeCPUProfile || before == wire.CumulativeCPUProfile && after == wire.MVPPostgreSQLCPUProfile
			if cumulativeProfileTransition(before, after) != want {
				t.Fatal("transition mismatch", before, after)
			}
		}
	}
}

func TestMVPUpgradeRetainsOriginalPolicyScopePublisherAndBothOperatorReferences(t *testing.T) {
	policy := view.Configuration{Directory: view.ContainerDirectory, ScopeID: strings.Repeat("a", 32), ManifestSHA256: strings.Repeat("b", 64), ConfigurationSHA256: strings.Repeat("c", 64), PublisherUID: 1234, PageSize: 50, OperationSeconds: 5}
	config := []byte(`{"server":{"application_profile":"releases"},"release_view_config_file":"/run/projectone/operator/private/existing-view.json"}`)
	files := []engine.ConfigurationFile{{Name: "runtime.yaml", Contents: config}, {Name: "control.yaml", Contents: config}, {Name: "existing-view.json", Contents: encode(policy)}}
	if name, err := cumulativeMVPPolicyName(files, "operator-config", policy); err != nil || name != "existing-view.json" {
		t.Fatal(name, err)
	}
	for _, change := range []func(*view.Configuration){func(v *view.Configuration) { v.ScopeID = strings.Repeat("d", 32) }, func(v *view.Configuration) { v.PublisherUID++ }, func(v *view.Configuration) { v.PageSize++ }, func(v *view.Configuration) { v.OperationSeconds++ }, func(v *view.Configuration) { v.ManifestSHA256 = strings.Repeat("e", 64) }, func(v *view.Configuration) { v.ConfigurationSHA256 = strings.Repeat("f", 64) }} {
		foreign := policy
		change(&foreign)
		if _, err := cumulativeMVPPolicyName(files, "operator-config", foreign); err == nil {
			t.Fatal("policy binding retargeted")
		}
	}
	changed := append([]engine.ConfigurationFile(nil), files...)
	changed[1].Contents = []byte(strings.Replace(string(config), "releases", "localization", 1))
	if _, err := cumulativeMVPPolicyName(changed, "operator-config", policy); err == nil {
		t.Fatal("operator/runtime profile disagreement admitted")
	}
}

// These controls establish the payload/mount boundary, not native SQL success.
func TestMVPCapabilityClosureRequiresRealPolicyFilesAndTypedFalse(t *testing.T) {
	root := "/run/projectone/control"
	raw := []byte(`{"server":{"application_profile":"mvp-postgresql-cpu-v1"},"database":{"dsn_file":"/original/runtime-dsn"}}`)
	caps := []byte(`{"middleware_config_file":"/run/projectone/control/private/middleware.json","private_cluster_config_file":"/run/projectone/control/private/cluster.json","monitoring_config_file":"/run/projectone/control/private/monitoring.json","model_cache":{"enabled":false},"image_registry_config_file":"/run/projectone/control/private/registry.json"}`)
	files := []engine.ConfigurationFile{{Name: "mvp-capabilities.json", Contents: caps}, {Name: "middleware.json", Contents: []byte(`{}`)}, {Name: "cluster.json", Contents: []byte(`{}`)}, {Name: "monitoring.json", Contents: []byte(`{}`)}, {Name: "registry.json", Contents: []byte(`{"store_root":"/registry/store/private","prepared_release_root":"/registry/private","verification":{"mount_root":"/registry"}}`)}}
	model := compose.Model{Volumes: []string{engine.NativePostgreSQLRegistryRole}, Services: map[string]compose.Service{"control": {User: "1001:1002", Mounts: []compose.Mount{{Type: "volume", Source: engine.NativePostgreSQLRegistryRole, Target: "/registry/store"}}}}}
	model, e := compose.MVPRegistryReleaseModel(model, "/registry")
	if e != nil {
		t.Fatal(e)
	}
	next, e := cumulativeMVPCapabilities(raw, files, nil, root, model)
	if e != nil {
		t.Fatal(e)
	}
	var values map[string]any
	json.Unmarshal(next, &values)
	if values["model_cache"].(map[string]any)["enabled"] != false || values["database"].(map[string]any)["dsn_file"] != "/original/runtime-dsn" {
		t.Fatal("false or original credential authority changed")
	}
	for _, edit := range []func([]engine.ConfigurationFile){
		func(v []engine.ConfigurationFile) {
			v[0].Contents = []byte(strings.Replace(string(caps), `"enabled":false`, `"enabled":"false"`, 1))
		},
		func(v []engine.ConfigurationFile) {
			v[0].Contents = []byte(strings.Replace(string(caps), `"model_cache":`, `"database":`, 1))
		},
		func(v []engine.ConfigurationFile) { v[1].Contents = []byte(`{"password_file":"/unmounted/password"}`) },
		func(v []engine.ConfigurationFile) {
			v[0].Contents = []byte(strings.Replace(string(caps), "monitoring.json", "missing.json", 1))
		},
	} {
		changed := append([]engine.ConfigurationFile(nil), files...)
		edit(changed)
		if _, e := cumulativeMVPCapabilities(raw, changed, nil, root, model); e == nil {
			t.Fatal("invalid capability/file/mount closure accepted")
		}
	}
	// The transformed configuration role is deliberately renamed: a literal
	// "control-config" exclusion cannot establish persistent data ownership.
	control := model.Services["control"]
	for role, target := range map[string]string{"cumulative-control-config-" + strings.Repeat("a", 32): root, "models": "/models", "datasets": "/datasets", "training": "/training", "preparations": "/preparations", "postgres": "/postgres", "unassigned-cache": "/cache"} {
		model.Volumes = append(model.Volumes, role)
		control.Mounts = append(control.Mounts, compose.Mount{Type: "volume", Source: role, Target: target, ReadOnly: strings.HasPrefix(role, "cumulative-control-config-")})
	}
	model.Services["control"] = control
	if _, e := cumulativeMVPCapabilities(raw, files, nil, root, model); e != nil {
		t.Fatal("independent fixed registry owner refused", e)
	}
	for _, target := range []string{root, root + "/private", root + "/private/provider", "/models", "/models/provider", "/datasets/provider", "/training/provider", "/preparations/provider", "/postgres/private", "/cache/private", "/registry/private", "/registry/store/private"} {
		for _, key := range []string{"cache_root", "cache_directory"} {
			changed := append([]engine.ConfigurationFile(nil), files...)
			var values map[string]any
			json.Unmarshal(caps, &values)
			values["model_cache"] = map[string]any{"enabled": true, key: target}
			changed[0].Contents = encode(values)
			if _, e := cumulativeMVPCapabilities(raw, changed, nil, root, model); e == nil {
				t.Fatal("provider borrowed unassigned/config/domain/registry data owner", key, target)
			}
		}
	}
	unused := append(append([]engine.ConfigurationFile(nil), files...), engine.ConfigurationFile{Name: "unused", Contents: []byte("not-consumed")})
	if _, e := cumulativeMVPCapabilities(raw, unused, nil, root, model); e == nil {
		t.Fatal("unreferenced payload accepted")
	}
}

func TestMVPExtraPayloadCannotLeakExistingOperatorOnlyAuthority(t *testing.T) {
	original := map[string][]engine.ConfigurationFile{"control-config": {{Name: "runtime-dsn", Contents: []byte("runtime-only")}}, "operator-config": {{Name: "operator-dsn", Contents: []byte("operator-only")}}}
	if cumulativeMVPSecretIsolation(map[string][]engine.ConfigurationFile{"control-config": {{Name: "provider-password", Contents: []byte("operator-only")}}}, original) {
		t.Fatal("operator authority copied to serving payload")
	}
	if !cumulativeMVPSecretIsolation(map[string][]engine.ConfigurationFile{"control-config": {{Name: "provider-password", Contents: []byte("new-provider-password")}}}, original) {
		t.Fatal("independent private provider input refused")
	}
}

func TestMVPRegistryColdRoleAndForwardPhaseBinding(t *testing.T) {
	n := len(roleServices)
	if inputRoleCount(wire.CumulativeCPUProfile) != n || inputRoleCount(wire.MVPPostgreSQLCPUProfile) != n+2 || len(roleServices) != n {
		t.Fatal("legacy role map changed")
	}
	if inputRoleAllowed(wire.CumulativeCPUProfile, engine.NativePostgreSQLRegistryRole) || !inputRoleAllowed(wire.MVPPostgreSQLCPUProfile, engine.NativePostgreSQLRegistryRole) {
		t.Fatal("wrong profile registry admission")
	}
	if len(cumulativeUpgradeSteps()) != 19 || len(cumulativeUpgradeStepsFor(wire.MVPPostgreSQLCPUProfile)) != 27 {
		t.Fatal("legacy or MVP step closure")
	}
	p := CumulativeUpgradePlan{ScopeID: strings.Repeat("a", 32), GenerationID: strings.Repeat("b", 32)}
	o := cumulativeRegistryOwner(p)
	if o.Role != engine.NativePostgreSQLRegistryRole || o.OperationID != p.GenerationID || o.ScopeID != p.ScopeID {
		t.Fatal("registry ownership guessed")
	}
	if cumulativeRegistryCompleted(nil) {
		t.Fatal("unexecuted registry reported complete")
	}
}

// Explicit synthetic transition metadata checks ordering and replay refusal. It
// is not execution or native completion evidence.
func TestMVPRegistryJournalRetainsUnknownIntentAndExactProfile(t *testing.T) {
	steps := cumulativeUpgradeStepsFor(wire.MVPPostgreSQLCPUProfile)
	var events []Event
	for i, phase := range steps[:11] {
		events = append(events, Event{OperationID: strings.Repeat("a", 32), Action: "upgrade_effect", Outcome: "completed", CumulativeUpgrade: &CumulativeUpgradeRecord{TargetProfile: wire.MVPPostgreSQLCPUProfile, GenerationID: strings.Repeat("b", 32), PlanSHA256: strings.Repeat("c", 64), Step: i, Phase: phase}})
	}
	start := Event{OperationID: strings.Repeat("d", 32), Action: "upgrade_effect", Outcome: "started", CumulativeUpgrade: &CumulativeUpgradeRecord{TargetProfile: wire.MVPPostgreSQLCPUProfile, GenerationID: strings.Repeat("b", 32), PlanSHA256: strings.Repeat("c", 64), Step: 11, Phase: "registry-volume-create"}}
	if e := validateCumulativeUpgradeEvent(events, start); e != nil {
		t.Fatal(e)
	}
	wrong := start
	r := *start.CumulativeUpgrade
	r.TargetProfile = ""
	wrong.CumulativeUpgrade = &r
	if validateCumulativeUpgradeEvent(events, wrong) == nil {
		t.Fatal("registry intent changed profile")
	}
	events = append(events, start)
	if validateCumulativeUpgradeEvent(events, start) == nil {
		t.Fatal("pending registry intent replayed")
	}
	unknown := start
	unknown.Outcome = "uncertain"
	unknown.Code = "OFFLINE_SCOPE_CUMULATIVE_UPGRADE_EFFECT_UNRESOLVED"
	if e := validateCumulativeUpgradeEvent(events, unknown); e != nil {
		t.Fatal(e)
	}
	events = append(events, unknown)
	done := start
	done.Outcome = "completed"
	if validateCumulativeUpgradeEvent(events, done) == nil {
		t.Fatal("lost registry mutation resent")
	}
	done.Action = "upgrade_reconcile"
	done.OperationID = strings.Repeat("e", 32)
	done.ReferenceOperationID = start.OperationID
	if e := validateCumulativeUpgradeEvent(events, done); e != nil {
		t.Fatal(e)
	}
	if cumulativeRegistryCompleted(append(events, done)) {
		t.Fatal("one registry step claimed complete lifecycle")
	}
}

func TestMVPRegistryReleaseBindingKeepsSignedTargetAndIndependentReadonlyOwner(t *testing.T) {
	archive, manifest := strings.Repeat("a", 64), strings.Repeat("b", 64)
	model := compose.Model{Services: map[string]compose.Service{"control": {User: "1001:1002", Mounts: []compose.Mount{{Type: "volume", Source: "control-config", Target: "/control", ReadOnly: true}}}}, Volumes: []string{"control-config"}}
	var e error
	model, e = compose.MVPRegistryModel(model, "/registry/store")
	if e != nil {
		t.Fatal(e)
	}
	model, e = compose.MVPRegistryReleaseModel(model, "/registry")
	if e != nil {
		t.Fatal(e)
	}
	registry := map[string]any{"store_root": "/registry/store/private", "prepared_release_root": "/registry/private", "verification": map[string]any{"mount_root": "/registry"}, "release_archive_sha256": archive, "release_manifest_sha256": manifest}
	files := map[string][]engine.ConfigurationFile{"control-config": {{Name: "runtime.yaml", Contents: []byte(`{"image_registry_config_file":"/control/private/registry.json"}`)}, {Name: "registry.json", Contents: encode(registry)}}, "operator-config": {{Name: "runtime.yaml", Contents: []byte(`{"image_registry_config_file":"/run/projectone/operator/private/registry.json"}`)}, {Name: "control.yaml", Contents: []byte(`{"image_registry_config_file":"/run/projectone/operator/private/registry.json"}`)}, {Name: "registry.json", Contents: encode(registry)}}}
	if e := validateMVPRegistryReleaseBinding(files, model, archive, manifest); e != nil {
		t.Fatal(e)
	}
	for _, key := range []string{"prepared_release_root", "release_archive_sha256", "release_manifest_sha256"} {
		v := map[string]any{}
		for k, x := range registry {
			v[k] = x
		}
		v[key] = "/models/foreign"
		original := files["control-config"][1].Contents
		files["control-config"][1].Contents = encode(v)
		if validateMVPRegistryReleaseBinding(files, model, archive, manifest) == nil {
			t.Fatal("foreign artifact authority", key)
		}
		files["control-config"][1].Contents = original
	}
}

func TestMVPRegistryNativeDescriptorPreservesTheFourCoreRoots(t *testing.T) {
	_, _, d := mvpNativeProjectionFixture()
	var e error
	if _, e = configuration.LoadNativePostgreSQLRecoveryDescriptor(encode(d)); e != nil {
		t.Fatal(e)
	}
	model := compose.Model{Services: map[string]compose.Service{"control": {User: "1001:1002"}}}
	model, e = compose.MVPRegistryModel(model, "/registry/store")
	if e != nil {
		t.Fatal(e)
	}
	model, e = compose.MVPRegistryReleaseModel(model, "/registry")
	if e != nil {
		t.Fatal(e)
	}
	d.RegistryRoot = "/registry/store/private"
	d.PreparedReleaseRoot = "/registry/private"
	d.RegistryScaffoldRoot = "/registry"
	core := map[string]any{}
	for i, key := range []string{"models", "datasets", "training", "model_preparation"} {
		core[key] = map[string]any{"artifact_root": d.StoreRoots[i]}
	}
	if e := validateMVPNativeRegistryDescriptor(encode(core), encode(d), model); e != nil {
		t.Fatal(e)
	}
	for _, key := range []string{"models", "datasets", "training", "model_preparation"} {
		old := core[key]
		core[key] = map[string]any{"artifact_root": "/unrelated/private"}
		if validateMVPNativeRegistryDescriptor(encode(core), encode(d), model) == nil {
			t.Fatal("changed original core root", key)
		}
		core[key] = old
	}
	d.RegistryRoot = "/models/provider"
	if validateMVPNativeRegistryDescriptor(encode(core), encode(d), model) == nil {
		t.Fatal("foreign registry owner in descriptor")
	}
}
