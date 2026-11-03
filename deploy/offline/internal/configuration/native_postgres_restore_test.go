package configuration

import (
	"encoding/json"
	"os"
	"path"
	"reflect"
	"strings"
	"testing"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

func nativeRestoreRootsBindingFixture(t *testing.T) (NativePostgreSQLRecoveryDescriptor, NativePostgreSQLRestoreBinding, wire.PostgreSQLRestoreRootsInspectionRequest, wire.PostgreSQLRestoreRootsInspectionObservation) {
	t.Helper()
	d, b := nativeRestoreDescriptorFixture(t)
	b.TargetRoots = nil
	h := strings.Repeat("a", 64)
	binding := wire.PostgreSQLRestoreTargetInitializationBinding{RestoreAttemptID: b.AttemptID, ReservationPlanSHA256: h, TargetDescriptorSHA256: h, TargetRuntimeSHA256: h, SourceCandidateSHA256: b.SourceReference.CandidateSHA256, NetworkID: h, PostgresContainerID: strings.Repeat("b", 64)}
	r, e := d.BindRestoreRootsInspection(NativePostgreSQLRestoreRootsBinding{ScopeID: b.ScopeID, InspectionID: strings.Repeat("5", 32), Binding: binding, RuntimeConfigurationSHA256: h, UID: b.UID, GID: b.GID})
	if e != nil {
		t.Fatal(e)
	}
	roots := []wire.PostgreSQLRestoreRoot{}
	for i, p := range d.StoreRoots {
		roots = append(roots, wire.PostgreSQLRestoreRoot{Path: p, Device: 1, Inode: uint64(20 + i), UID: uint32(b.UID), GID: uint32(b.GID)})
	}
	o := wire.PostgreSQLRestoreRootsInspectionObservation{Format: wire.PostgreSQLRestoreRootsInspectionFormat, RequestSHA256: r.SHA256(), InspectionID: r.InspectionID, TargetScopeID: r.TargetScopeID, Binding: r.Binding, RuntimeConfigurationSHA256: r.RuntimeConfigurationSHA256, RuntimeEnvironmentSHA256: r.RuntimeEnvironmentSHA256, EffectiveConfigurationSHA256: h, Roots: roots, RootsSHA256: wire.PostgreSQLRestoreRootsSHA256(roots), ReadOnly: true, ObservedAt: "2026-11-03T00:00:00Z", Qualification: wire.PostgreSQLRestoreRootsInspectionQualification}
	return d, b, r, o
}

func TestNativePostgreSQLRestoreObservedRootsFreezeNewFinalRequest(t *testing.T) {
	d, b, probe, observed := nativeRestoreRootsBindingFixture(t)
	got, e := d.BindRestoreObservedRoots(b, probe, observed)
	if e != nil || len(got.TargetRoots) != 4 || !reflect.DeepEqual(got.TargetRoots, observed.Roots) || probe.Policy.OperationSeconds != d.InitializationPolicy.OperationSeconds || probe.Policy.MaximumRecordBytes != d.InitializationPolicy.MaximumRecordBytes || probe.SHA256() == wire.Digest(encodeNativeRestoreConfiguration(got)) {
		t.Fatal("original roots did not bind final request", e)
	}
	observed.Roots[0].Inode++
	if got.TargetRoots[0].Inode == observed.Roots[0].Inode {
		t.Fatal("final request aliases observation")
	}
}

func encodeNativeRestoreConfiguration(v any) []byte { r, _ := json.Marshal(v); return append(r, '\n') }

func TestNativePostgreSQLRestoreObservedRootsRejectReboundOrInventedPins(t *testing.T) {
	for _, name := range []string{"override", "scope", "candidate", "attempt", "uid", "missing", "path", "lock", "runtime", "policy"} {
		t.Run(name, func(t *testing.T) {
			d, b, r, o := nativeRestoreRootsBindingFixture(t)
			switch name {
			case "override":
				b.TargetRoots = append([]wire.PostgreSQLRestoreRoot(nil), o.Roots...)
			case "scope":
				b.ScopeID = strings.Repeat("e", 32)
			case "candidate":
				b.SourceReference.CandidateSHA256 = strings.Repeat("e", 64)
			case "attempt":
				b.AttemptID = strings.Repeat("e", 32)
			case "uid":
				o.Roots[0].UID++
				o.RootsSHA256 = wire.PostgreSQLRestoreRootsSHA256(o.Roots)
			case "missing":
				o.Roots = o.Roots[:3]
				o.RootsSHA256 = wire.PostgreSQLRestoreRootsSHA256(o.Roots)
			case "path":
				o.Roots[0].Path = "/different/private"
				o.RootsSHA256 = wire.PostgreSQLRestoreRootsSHA256(o.Roots)
			case "lock":
				o.CurrentLockHeld = true
			case "runtime":
				o.RuntimeConfigurationSHA256 = strings.Repeat("e", 64)
			case "policy":
				r.Policy.OperationSeconds++
				o.RequestSHA256 = r.SHA256()
			}
			if _, e := d.BindRestoreObservedRoots(b, r, o); e == nil {
				t.Fatal("changed/ invented root identity admitted")
			}
		})
	}
}

// Synthetic public examples and measured-identity doubles only. No SQL, Engine,
// native initialization, original store measurement or signed source is claimed.
func nativeRestoreDescriptorFixture(t *testing.T) (NativePostgreSQLRecoveryDescriptor, NativePostgreSQLRestoreBinding) {
	t.Helper()
	load := func(name string, value any) {
		raw, e := os.ReadFile("../../../../services/control/config/examples/postgres-backup/" + name)
		if e != nil || wire.StrictJSON(raw, value) != nil {
			t.Fatal("versioned public request fixture unavailable", e)
		}
	}
	var source wire.PostgreSQLBackupRequest
	var template wire.PostgreSQLRestoreRequest
	load("mvp-postgresql-cpu-v1.backup.example.json", &source)
	load("mvp-postgresql-cpu-v1.restore.example.json", &template)
	source.RuntimeConfigurationFile = path.Join(source.ConfigurationRoot, "runtime.yaml")
	backup := source
	backup.SourceScopeID = strings.Repeat("3", 32)
	template.ConfigurationRoot = backup.ConfigurationRoot
	template.RuntimeConfigurationFile = backup.RuntimeConfigurationFile
	template.Policy = backup.Policy
	template.BackupRoot = "/synthetic/template-source/backup"
	policy := &wire.PostgreSQLRestoreTargetInitializationPolicy{OperationSeconds: 20, CleanupSeconds: 2, MaximumRecordBytes: 1 << 20, MinimumFreeBytes: 1 << 20}
	d := NativePostgreSQLRecoveryDescriptor{FormatVersion: 1, BackupTemplate: backup, RestoreTemplate: &template, InitializationPolicy: policy, InitializationParentRoot: "/backup/initializations", StoreRoots: [4]string{"/stores/models", "/stores/datasets", "/stores/training", "/stores/preparations"}, RegistryRoot: "/artifacts/registry", PreparedReleaseRoot: "/artifacts/prepared", RegistryScaffoldRoot: "/artifacts"}
	raw, _ := json.Marshal(source)
	h := func(c string) string { return strings.Repeat(c, 64) }
	reference := wire.NativePostgreSQLBackupReference{FormatVersion: 1, ApplicationProfile: wire.MVPPostgreSQLCPUApplicationProfile, SourceManifestSHA256: source.SourceManifestSHA256, SchemaVersion: 29, TableCount: 108, RequestSHA256: wire.Digest(append(raw, '\n')), CandidateSHA256: h("6"), CompletionSHA256: h("7"), ReadbackSHA256: h("8"), MVPSourceSHA256: h("9"), RegistryConfigured: true, Registry: &wire.NativePostgreSQLRegistryReference{ReceiptSHA256: h("1"), ManifestSHA256: h("2"), PayloadTreeSHA256: h("3")}}
	b := NativePostgreSQLRestoreBinding{ScopeID: backup.SourceScopeID, AttemptID: strings.Repeat("4", 32), SignedSourceSHA256: source.SourceManifestSHA256, ControlImageSHA256: source.ControlImageSHA256, PostgresImageSHA256: source.PostgresImageSHA256, SourceRequest: source, SourceReference: reference, TargetDatabase: "projectone_restored", TargetOperator: "projectone_operator", Reason: "Synthetic protected descriptor restore binding", UID: 65534, GID: 65534}
	for i, root := range d.StoreRoots {
		b.TargetRoots = append(b.TargetRoots, wire.PostgreSQLRestoreRoot{Path: root, Device: 10, Inode: uint64(100 + i), UID: 65534, GID: 65534})
	}
	if d.Validate() != nil || !reference.Valid() {
		t.Fatal("synthetic descriptor/source reference invalid")
	}
	return d, b
}

func TestNativePostgreSQLRestoreBindingPreservesTemplateSourceAndActualRoots(t *testing.T) {
	d, b := nativeRestoreDescriptorFixture(t)
	before, _ := json.Marshal(d)
	r, e := d.BindRestore(b)
	after, _ := json.Marshal(d)
	if e != nil || !r.Valid() || !reflect.DeepEqual(before, after) || r.RestoreID != b.AttemptID || r.TargetScopeID != b.ScopeID || r.SourceManifestSHA256 != b.SourceReference.CandidateSHA256 || r.SourceManifestSHA256 == b.SignedSourceSHA256 || r.BackupRoot != b.SourceRequest.BackupRoot || r.BackupRoot == d.RestoreTemplate.BackupRoot || path.Base(r.RestoreRoot) != b.AttemptID || r.Reason != b.Reason || !reflect.DeepEqual(r.TargetRoots, b.TargetRoots) {
		t.Fatal("original request/template/root binding failed", e)
	}
	b.TargetRoots[0].Inode++
	if r.TargetRoots[0].Inode == b.TargetRoots[0].Inode || d.RestoreTemplate.TargetRoots != nil {
		t.Fatal("bound request aliases mutable measurements or changed template")
	}
	private, _ := json.Marshal(b)
	if string(private) != "{}" {
		t.Fatal("private caller facts acquired a JSON input authority")
	}
}

func TestNativePostgreSQLRestoreBindingRejectsChangedSourceAndUnmeasuredTargets(t *testing.T) {
	for _, mutation := range []string{"source-request", "source-target-overlap", "source-attempt-overlap", "missing-reference", "registry-metadata-only", "candidate-as-signed-source", "control-image", "postgres-image", "scope", "attempt", "tenant", "missing-roots", "root-order", "root-owner", "root-alias", "reason", "template-policy"} {
		t.Run(mutation, func(t *testing.T) {
			d, b := nativeRestoreDescriptorFixture(t)
			switch mutation {
			case "source-request":
				b.SourceRequest.Policy.OperationSeconds++
			case "source-target-overlap":
				b.ScopeID = b.SourceRequest.SourceScopeID
			case "source-attempt-overlap":
				b.AttemptID = b.SourceRequest.BackupID
			case "missing-reference":
				b.SourceReference = wire.NativePostgreSQLBackupReference{}
			case "registry-metadata-only":
				b.SourceReference.RegistryConfigured, b.SourceReference.Registry = false, nil
			case "candidate-as-signed-source":
				b.SignedSourceSHA256 = b.SourceReference.CandidateSHA256
			case "control-image":
				b.ControlImageSHA256 = strings.Repeat("0", 64)
			case "postgres-image":
				b.PostgresImageSHA256 = strings.Repeat("0", 64)
			case "scope":
				b.ScopeID = strings.Repeat("5", 32)
			case "attempt":
				b.AttemptID = "../unbound"
			case "tenant":
				b.SourceRequest.TenantID = "22222222-2222-4222-8222-222222222222"
				raw, _ := json.Marshal(b.SourceRequest)
				b.SourceReference.RequestSHA256 = wire.Digest(append(raw, '\n'))
			case "missing-roots":
				b.TargetRoots = nil
			case "root-order":
				b.TargetRoots[0], b.TargetRoots[1] = b.TargetRoots[1], b.TargetRoots[0]
			case "root-owner":
				b.TargetRoots[0].UID++
			case "root-alias":
				b.TargetRoots[1].Inode = b.TargetRoots[0].Inode
			case "reason":
				b.Reason = ""
			case "template-policy":
				d.RestoreTemplate.Policy.OperationSeconds++
			}
			if _, e := d.BindRestore(b); e == nil {
				t.Fatal("changed source/target acquired original native request")
			}
		})
	}
}

func nativeRestoreTargetDiscoveryFixture(t *testing.T) (NativePostgreSQLRecoveryDescriptor, wire.PostgreSQLRestoreTargetInitializationRequest, wire.PostgreSQLRestoreTargetInitializationObservation) {
	t.Helper()
	d, b := nativeRestoreDescriptorFixture(t)
	r, e := d.BindRestore(b)
	if e != nil {
		t.Fatal(e)
	}
	h := func(c string) string { return strings.Repeat(c, 64) }
	bound := NativePostgreSQLRestoreTargetBinding{InitializationID: strings.Repeat("6", 32), SourceDatabase: b.SourceRequest.Database, Endpoint: wire.PostgreSQLRestoreTargetInitializationEndpoint{Host: "postgres", Port: 6432, SSLMode: "disable"}, Binding: wire.PostgreSQLRestoreTargetInitializationBinding{RestoreAttemptID: r.RestoreID, ReservationPlanSHA256: h("1"), TargetDescriptorSHA256: h("2"), TargetRuntimeSHA256: h("3"), SourceCandidateSHA256: r.SourceManifestSHA256, NetworkID: h("4"), PostgresContainerID: h("5")}}
	discovery, e := d.BindRestoreTargetInspection(r, bound)
	if e != nil || !discovery.ValidForInspection() || discovery.Valid() {
		t.Fatal("discovery acquired initializer authority", e)
	}
	identity := wire.PostgreSQLRestoreTargetInitializationIdentity{SystemIdentifier: "7312345678901234567", DatabaseOID: 42, Database: r.TargetDatabase, Operator: r.TargetOperator, BackendPID: 88, ServerVersion: 180006}
	observed := wire.PostgreSQLRestoreTargetInitializationObservation{Format: wire.PostgreSQLRestoreTargetObservationFormat, RequestSHA256: discovery.SHA256(), InitializationID: discovery.InitializationID, TargetScopeID: discovery.TargetScopeID, Binding: discovery.Binding, Target: identity, TargetBindingSHA256: identity.SHA256(), PristinePredicateSHA256: h("8"), ReadOnly: true, State: "uninitialized", Qualification: "Synthetic discovery; no SQL executed"}
	if !observed.ValidFor(discovery, true) {
		t.Fatal("synthetic original discovery invalid")
	}
	return d, discovery, observed
}

func TestNativePostgreSQLRestoreTargetPinsOnlyOriginalUninitializedDiscovery(t *testing.T) {
	d, discovery, observed := nativeRestoreTargetDiscoveryFixture(t)
	original := discovery
	r, e := d.BindRestoreTargetInitialization(discovery, observed, strings.Repeat("7", 32))
	if e != nil || !r.Valid() || r.ExpectedSystemIdentifier != observed.Target.SystemIdentifier || r.ExpectedDatabaseOID != observed.Target.DatabaseOID || r.InitializationID == discovery.InitializationID || r.InitializationRoot == discovery.InitializationRoot || r.Binding != discovery.Binding || discovery != original || r.SHA256() == discovery.SHA256() || r.Endpoint.Port != 6432 {
		t.Fatal("actual target pin/new custody binding failed", e)
	}
	observed.Target.BackendPID++
	if observed.TargetBindingSHA256 != observed.Target.SHA256() {
		t.Fatal("fresh process PID entered immutable target identity")
	}
	if _, e := d.BindRestoreTargetInitialization(discovery, observed, strings.Repeat("8", 32)); e != nil {
		t.Fatal("readonly process identity prevented a new pre-intent construction", e)
	}
}

func TestNativePostgreSQLRestoreTargetRefusesAdoptionUnknownAndChangedCustody(t *testing.T) {
	for _, mutation := range []string{"marker", "intent", "completion", "effect", "request", "binding", "partial-pins", "pinned-discovery", "same-id", "restore-id", "policy", "root"} {
		t.Run(mutation, func(t *testing.T) {
			d, request, observed := nativeRestoreTargetDiscoveryFixture(t)
			id := strings.Repeat("7", 32)
			switch mutation {
			case "marker":
				observed.Target.Marker = request.Marker()
				observed.TargetBindingSHA256 = observed.Target.SHA256()
			case "intent":
				observed.OriginalIntentPresent = true
				observed.IntentSHA256 = strings.Repeat("a", 64)
			case "completion":
				observed.OriginalCompletionPresent = true
				observed.CompletionSHA256 = strings.Repeat("b", 64)
			case "effect":
				observed.EffectObserved = true
			case "request":
				observed.RequestSHA256 = strings.Repeat("0", 64)
			case "binding":
				observed.Binding.NetworkID = strings.Repeat("0", 64)
			case "partial-pins":
				request.ExpectedSystemIdentifier = observed.Target.SystemIdentifier
			case "pinned-discovery":
				request.ExpectedSystemIdentifier, request.ExpectedDatabaseOID = observed.Target.SystemIdentifier, observed.Target.DatabaseOID
			case "same-id":
				id = request.InitializationID
			case "restore-id":
				id = request.Binding.RestoreAttemptID
			case "policy":
				d.InitializationPolicy.OperationSeconds++
			case "root":
				d.InitializationParentRoot += "/changed"
			}
			if _, e := d.BindRestoreTargetInitialization(request, observed, id); e == nil {
				t.Fatal("unproven target/initialization custody accepted")
			}
		})
	}
}
