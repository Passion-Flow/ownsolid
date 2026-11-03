package scope

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/compose"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
	view "github.com/Passion-Flow/ownsolid/api/release/view"
)

// Reuses synthetic original-native actor/completion fixtures, never claims a
// native SQL run. Each authority is independently changed in these controls.
func TestMVPNativeRestoredSourceBindsOriginalCustodyAndActualTarget(t *testing.T) {
	fixture := func() (mvpNativeRestoredBackupOriginal, string) {
		_, scope, r, _, _, task, _, _ := mvpNativeRestoreActorFixture(t)
		_, _, _, _, _, c, o := mvpNativeRestoreCompletedObservationFixture(t)
		return mvpNativeRestoredBackupOriginal{Request: r, Writer: task, Completion: c, Reference: *o.NativePostgreSQL}, scope
	}
	original, scope := fixture()
	if !validMVPNativeRestoredOriginal(original, scope) {
		t.Fatal("original native custody rejected")
	}
	for _, fault := range []string{"scope", "schema22", "legacyprofile", "request", "completion", "intent", "postimage", "lock-held", "no-registry", "wrong-writer", "context-archive", "current-not-original"} {
		t.Run(fault, func(t *testing.T) {
			o, scope := fixture()
			switch fault {
			case "scope":
				scope = strings.Repeat("0", 32)
			case "schema22":
				o.Completion.RuntimeContext.SchemaVersion = 22
			case "legacyprofile":
				o.Request.ApplicationProfile = ""
			case "request":
				o.Request.TargetDatabase = "foreign_database"
			case "completion":
				o.Reference.CompletionSHA256 = strings.Repeat("0", 64)
			case "intent":
				o.Reference.IntentSHA256 = strings.Repeat("0", 64)
			case "postimage":
				o.Completion.PostDataSHA256 = strings.Repeat("0", 64)
			case "lock-held":
				o.Completion.CurrentLockHeld = true
			case "no-registry":
				o.Writer.NativePostgreSQLRecovery.Registry = nil
			case "wrong-writer":
				o.Writer.Kind = "postgres-restore"
			case "context-archive":
				o.Completion.RuntimeContext.SQLArchiveSHA256 = strings.Repeat("0", 64)
			case "current-not-original":
				o.Completion.Original = nil
			}
			if validMVPNativeRestoredOriginal(o, scope) {
				t.Fatal("changed original authority accepted", fault)
			}
		})
	}
}

func TestMVPNativeBackupDispatchRecognizesInstalledMVPWithoutRelabellingMarker(t *testing.T) {
	l := &locked{marker: Marker{Profile: wire.CumulativeCPUProfile}}
	if l.usesMVPNativeBackup() {
		t.Fatal("M17 became MVP without installed release")
	}
	l.events = []Event{{Action: "cumulative_upgrade", Outcome: "completed", CumulativeUpgrade: &CumulativeUpgradeRecord{Installed: &view.InstalledRelease{Profile: wire.MVPPostgreSQLCPUProfile, SchemaVersion: 29}}}}
	// Dispatch routes an MVP discriminator into the strict native getter. It does
	// not grant its incomplete journal successful admission or legacy fallback.
	if !l.usesMVPNativeBackup() {
		t.Fatal("installed MVP would enter legacy backup")
	}
	if l.marker.Profile != wire.CumulativeCPUProfile {
		t.Fatal("original marker rewritten")
	}
	if _, e := l.mvpNativeCurrentSource(nil); e == nil {
		t.Fatal("incomplete installed journal admitted")
	}
}

func TestMVPNativeForwardSourceKeepsOriginalPublisherAndCurrentSchemaSeparate(t *testing.T) {
	p, files := mvpNativeReleasePolicyFixture()
	old := strings.Repeat("d", 64)
	p.Forward = &mvpNativeForwardBackupBinding{Original: Marker{Profile: wire.CumulativeCPUProfile, ScopeID: p.ScopeID, ConfigurationSHA256: p.ConfigurationSHA256, ManifestSHA256: old, ArchiveSHA256: old}, Installed: view.InstalledRelease{Profile: wire.MVPPostgreSQLCPUProfile, SchemaVersion: 29, ReleaseID: p.ReleaseID, ManifestSHA256: p.ReleaseManifestSHA256, ArchiveSHA256: p.ReleaseArchiveSHA256, SourceCommit: strings.Repeat("a", 40), ActivationSequence: 100}, PayloadIndexSHA256: old, RuntimeConfigurationSHA256: old, RegistryOwner: engine.Ownership{ScopeID: p.ScopeID, OperationID: strings.Repeat("e", 32), Role: engine.NativePostgreSQLRegistryRole}}
	p.ReleaseViewBinding.ManifestSHA256 = old
	for role, items := range files {
		for i, f := range items {
			if f.Name == "view.json" {
				items[i].Contents = encode(*p.ReleaseViewBinding)
			}
		}
		files[role] = items
	}
	p.ConfigurationFiles = nil
	for role, items := range files {
		for _, f := range items {
			p.ConfigurationFiles = append(p.ConfigurationFiles, BackupConfigurationFile{Role: role, File: InputFile{Name: f.Name, Bytes: int64(len(f.Contents)), SHA256: wire.Digest(f.Contents)}})
		}
	}
	if !validMVPNativeForwardBinding(p) {
		t.Fatal("valid separate source identities refused")
	}
	if _, _, e := mvpNativeSourceReleasePolicy(p, files); e != nil {
		t.Fatal("original publisher identity rewritten", e)
	}
	p.Forward.Installed.SchemaVersion = 22
	if validMVPNativeForwardBinding(p) {
		t.Fatal("schema22 relabelled MVP")
	}
	p.Forward.Installed.SchemaVersion = 29
	p.Forward.Original.ManifestSHA256 = p.ReleaseManifestSHA256
	if validMVPNativeForwardBinding(p) {
		t.Fatal("original marker replaced by target")
	}
}

type mvpNativeMountedControlDouble struct {
	runtimeEngine
	called          int
	owner           engine.Ownership
	id, destination string
	uid, gid        int
	files           []engine.ConfigurationFile
	status          string
}

func (f *mvpNativeMountedControlDouble) InspectCurrentControlConfiguration(_ context.Context, o engine.Ownership, id, destination string, uid, gid int, files []engine.ConfigurationFile) (engine.ConfigurationObservation, error) {
	f.called++
	f.owner = o
	f.id = id
	f.destination = destination
	f.uid = uid
	f.gid = gid
	f.files = files
	return engine.ConfigurationObservation{Status: f.status}, nil
}

func TestMVPNativeSourceReadmissionReadsExactMountedControlWithoutReclassifyingWriter(t *testing.T) {
	owner := engine.Ownership{ScopeID: strings.Repeat("a", 32), OperationID: strings.Repeat("b", 32), Role: "control-config-generation"}
	files := []engine.ConfigurationFile{{Name: "runtime.yaml", Contents: []byte("synthetic current protected bytes")}}
	model := compose.Model{Services: map[string]compose.Service{"control": {Mounts: []compose.Mount{{Type: "volume", Source: owner.Role, Target: "/run/projectone/current", ReadOnly: true}}}}}
	f := &mvpNativeMountedControlDouble{status: engine.ConfigurationPayloadVerified}
	if e := readMVPNativeCurrentControl(context.Background(), f, model, owner.Role, owner, "actual-control-id", 1000, 1001, files); e != nil {
		t.Fatal(e)
	}
	if f.called != 1 || f.owner != owner || f.id != "actual-control-id" || f.destination != "/run/projectone/current" || f.uid != 1000 || f.gid != 1001 || !reflect.DeepEqual(f.files, files) {
		t.Fatal("exact current mount identity not read back")
	}
	for _, fault := range []string{"writable", "duplicate", "missing", "unverified"} {
		t.Run(fault, func(t *testing.T) {
			m := model
			s := m.Services["control"]
			s.Mounts = append([]compose.Mount(nil), s.Mounts...)
			m.Services = map[string]compose.Service{"control": s}
			f := &mvpNativeMountedControlDouble{status: engine.ConfigurationPayloadVerified}
			switch fault {
			case "writable":
				s.Mounts[0].ReadOnly = false
			case "duplicate":
				s.Mounts = append(s.Mounts, s.Mounts[0])
			case "missing":
				s.Mounts = nil
			case "unverified":
				f.status = "unverified"
			}
			m.Services["control"] = s
			if e := readMVPNativeCurrentControl(context.Background(), f, m, owner.Role, owner, "actual-control-id", 1000, 1001, files); e == nil {
				t.Fatal("changed current mounted payload admitted")
			}
		})
	}
}
