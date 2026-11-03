package scope

import (
	"context"
	"math"
	"reflect"
	"testing"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
)

func TestMVPNativeStorageFrozenPolicyFitsCompleteRetainedOuterSource(t *testing.T) {
	p := mvpNativeSourcePlanFixture()
	s := p.Storage
	if !validMVPNativeBackupStorage(p) || len(s.Phases) != 6 || s.CombinedHighWaterBytes <= s.HostHighWaterBytes || s.CombinedHighWaterBytes <= s.NativeHighWaterBytes {
		t.Fatal("missing complete coexistence bound", s)
	}
	q := p.NativeRequest.Policy
	if s.NativeOutputMaximumBytes != q.MaximumStoreBytes+q.MaximumSQLBytes+s.NativeConfigurationBytes+q.MaximumConfigurationBytes+4*q.MaximumManifestBytes+2*s.RegistryManifestMaximumBytes {
		t.Fatal("native aggregate bound excludes original owner output")
	}
	if s.HostHighWaterBytes != s.WrapperMaximumBytes+s.NativeTarMaximumBytes+s.NativeOutputMaximumBytes {
		t.Fatal("tar/extracted child/original wrapper not all charged")
	}
	for name, change := range map[string]func(*mvpNativeBackupPlan){
		"SQL16GiB cannot fit outer": func(p *mvpNativeBackupPlan) {
			p.Request.MaximumBytes = 1 << 30
			p.NativeRequest.Policy.MaximumSQLBytes = 16 << 30
		},
		"store cannot fit outer":                   func(p *mvpNativeBackupPlan) { p.Request.MaximumBytes = 1 << 20 },
		"original store file policy exceeds outer": func(p *mvpNativeBackupPlan) { p.NativeRequest.Policy.MaximumStoreFiles = p.Request.MaximumEntries },
	} {
		t.Run(name, func(t *testing.T) {
			copy := p
			change(&copy)
			original := copy.NativeRequest.Policy
			if _, e := makeMVPNativeBackupStorage(copy, s.NativeInputBytes, s.NativeConfigurationBytes, s.MaximumMetadataBytes, s.MaximumSourceManifestBytes, s.RegistryManifestMaximumBytes, s.ScopeMaximumJournalBytes); e == nil {
				t.Fatal("contradictory frozen limits accepted")
			}
			if !reflect.DeepEqual(copy.NativeRequest.Policy, original) {
				t.Fatal("policy silently shrunk")
			}
		})
	}
	t.Run("exact logical byte boundary", func(t *testing.T) {
		local := p
		local.Request.MaximumBytes = s.HostHighWaterBytes
		if _, e := makeMVPNativeBackupStorage(local, s.NativeInputBytes, s.NativeConfigurationBytes, s.MaximumMetadataBytes, s.MaximumSourceManifestBytes, s.RegistryManifestMaximumBytes, s.ScopeMaximumJournalBytes); e != nil {
			t.Fatal(e)
		}
		local.Request.MaximumBytes--
		if _, e := makeMVPNativeBackupStorage(local, s.NativeInputBytes, s.NativeConfigurationBytes, s.MaximumMetadataBytes, s.MaximumSourceManifestBytes, s.RegistryManifestMaximumBytes, s.ScopeMaximumJournalBytes); e == nil {
			t.Fatal("one-byte shortage accepted")
		}
	})
	if _, ok := mvpNativeStorageSum(math.MaxInt64, 1); ok {
		t.Fatal("overflow accepted")
	}
	if _, e := makeMVPNativeBackupStorage(p, math.MaxInt64, 1, s.MaximumMetadataBytes, s.MaximumSourceManifestBytes, s.RegistryManifestMaximumBytes, s.ScopeMaximumJournalBytes); e == nil {
		t.Fatal("phase high-water overflow accepted")
	}
	largerRegistry, e := makeMVPNativeBackupStorage(p, s.NativeInputBytes, s.NativeConfigurationBytes, s.MaximumMetadataBytes, s.MaximumSourceManifestBytes, 4<<20, s.ScopeMaximumJournalBytes)
	if e != nil || largerRegistry.NativeOutputMaximumBytes-s.NativeOutputMaximumBytes != 6<<20 {
		t.Fatal("independent registry manifest policy omitted", e)
	}
	p.Storage.Phases[3].HostBytes--
	if validMVPNativeBackupStorage(p) {
		t.Fatal("tampered phase ledger accepted")
	}
}

type mvpNativeUnexpectedMappingDouble struct {
	*mvpNativeNoReplayDouble
	mappings int
}

func (d *mvpNativeUnexpectedMappingDouble) ObserveNativePostgreSQLStorage(context.Context, engine.DatabaseTask) (engine.NativePostgreSQLStorageObservation, error) {
	d.mappings++
	return engine.NativePostgreSQLStorageObservation{}, nil
}

func TestMVPNativeStorageChargesSharedBackingOnceWithCombinedPeak(t *testing.T) {
	s := mvpNativeBackupStoragePlan{MinimumFreeBytes: 1, Phases: []mvpNativeBackupStoragePhase{{NativeBytes: 301, HostBytes: 399}}}
	fs := mvpNativeBackupFilesystem{BackingRole: engine.NativePostgreSQLDurableBacking, Device: 1, Total: 1000, Available: 1000, AllocationUnit: 1}
	if e := mvpNativeFilesystemAdmission(s, fs, fs, fs, fs); e != nil {
		t.Fatal(e)
	}
	fs.Available--
	if e := mvpNativeFilesystemAdmission(s, fs, fs, fs, fs); e == nil {
		t.Fatal("shared physical byte counted as two capacity pools")
	}
	fs.Available = 1000
	s.Phases[0].HostEntries = 1
	if e := mvpNativeFilesystemAdmission(s, fs, fs, fs, fs); e == nil {
		t.Fatal("entry/block allowance omitted")
	}
	// Different real devices do not charge native allocation to the host pool.
	s.Phases[0].HostEntries = 0
	native := mvpNativeBackupFilesystem{BackingRole: engine.NativePostgreSQLDurableBacking, Device: 2, Total: 1000, Available: 302, AllocationUnit: 1}
	host := fs
	host.Available = 699
	if e := mvpNativeFilesystemAdmission(s, native, host, host, host); e != nil {
		t.Fatal(e)
	}
	native.Available--
	if e := mvpNativeFilesystemAdmission(s, native, host, host, host); e == nil {
		t.Fatal("original fixed minimum free reserve omitted")
	}
	wrong := host
	wrong.Total++
	if e := mvpNativeFilesystemAdmission(s, wrong, host, host, host); e == nil {
		t.Fatal("same-device conflicting backing admitted")
	}
	s.Phases[0].ScopeBytes = 10
	journal := mvpNativeBackupFilesystem{BackingRole: engine.NativePostgreSQLDurableBacking, Device: 3, Total: 1000, Available: 11, AllocationUnit: 1}
	native.Available = 302
	if e := mvpNativeFilesystemAdmission(s, native, host, journal, host); e != nil {
		t.Fatal("independent original Scope filesystem refused", e)
	}
	journal.Available--
	if e := mvpNativeFilesystemAdmission(s, native, host, journal, host); e == nil {
		t.Fatal("original journal filesystem omitted")
	}
}

func TestMVPNativeStorageRefusesMissingPhysicalMappingBeforeAnyCreateOrStart(t *testing.T) {
	p := mvpNativeSourcePlanFixture()
	d := &mvpNativeNoReplayDouble{}
	if e := admitMVPNativeBackupStorage(context.Background(), d, p, mvpNativeBackupExecution{Writer: engine.DatabaseTask{}}, "", nil); e == nil {
		t.Fatal("missing physical mapping inferred from virtual capacity")
	}
	if d.starts != 0 || d.observes != 0 || d.completionReads != 0 {
		t.Fatal("effects before physical admission")
	}
	contradictory := p
	contradictory.Request.MaximumBytes = 1 << 20
	mapped := &mvpNativeUnexpectedMappingDouble{mvpNativeNoReplayDouble: d}
	if e := admitMVPNativeBackupStorage(context.Background(), mapped, contradictory, mvpNativeBackupExecution{}, "", nil); e == nil || mapped.mappings != 0 {
		t.Fatal("contradictory native policy reached Engine observation")
	}
}

func TestMVPNativeResumedStorageKeepsFreshAdmissionAndBoundedCleanup(t *testing.T) {
	s := mvpNativeSourcePlanFixture().Storage
	for _, phase := range []string{"volume-create", "payload-write", "task-create", "task-execute", "archive-publish", "manifest-publish"} {
		if !reflect.DeepEqual(mvpNativeNextStepStorage(s, BackupStep{Phase: phase}), s) {
			t.Fatal("resumed writer skipped complete peak", phase)
		}
	}
	for _, phase := range []string{"utility-remove", "task-remove", "volume-remove", "complete"} {
		p := mvpNativeNextStepStorage(s, BackupStep{Phase: phase})
		if len(p.Phases) != 1 || p.Phases[0].ScopeBytes != s.ScopeHighWaterBytes || p.Phases[0].NativeBytes != 0 || p.Phases[0].HostBytes != 0 {
			t.Fatal("cleanup lost journal bound or charged old payload again", phase)
		}
	}
	fs := mvpNativeBackupFilesystem{BackingRole: "ram", Device: 1, Total: 1000, Available: 1000, AllocationUnit: 1}
	if e := mvpNativeFilesystemAdmission(mvpNativeBackupStoragePlan{Phases: []mvpNativeBackupStoragePhase{{}}}, fs, fs, fs, fs); e == nil {
		t.Fatal("non-durable role admitted")
	}
}
