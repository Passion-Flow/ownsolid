package scope

import (
	"context"
	"math"
	"path/filepath"
	"reflect"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
	view "github.com/Passion-Flow/ownsolid/api/release/view"
)

// These are structural format allowances, not adjustable deployment policies.
// Docker's accepted original tar may carry PAX path/size records and zero padding.
const mvpNativeTarEntryOverhead = 4 * 512
const mvpNativeTarEndAllowance = 1024 + 10240
const mvpNativeWrapperMetadataFiles = 10

type mvpNativeBackupStoragePhase struct {
	Name          string `json:"name"`
	NativeBytes   int64  `json:"native_bytes"`
	HostBytes     int64  `json:"host_bytes"`
	NativeEntries int64  `json:"native_entries"`
	HostEntries   int64  `json:"host_entries"`
	ScopeBytes    int64  `json:"scope_bytes"`
	ScopeEntries  int64  `json:"scope_entries"`
}

// Immutable logical-byte/entry bounds. Fresh free-space observations are kept
// separate: neither a timestamp nor virtual filesystem free space proves backing.
type mvpNativeBackupStoragePlan struct {
	FormatVersion                 int                           `json:"format_version"`
	MaximumMetadataBytes          int64                         `json:"maximum_metadata_bytes"`
	MaximumSourceManifestBytes    int64                         `json:"maximum_source_manifest_bytes"`
	RegistryManifestMaximumBytes  int64                         `json:"registry_manifest_maximum_bytes"`
	NativeInputBytes              int64                         `json:"native_input_bytes"`
	NativeConfigurationBytes      int64                         `json:"native_configuration_bytes"`
	NativeOutputMaximumBytes      int64                         `json:"native_output_maximum_bytes"`
	NativeTarMaximumBytes         int64                         `json:"native_tar_maximum_bytes"`
	WrapperMaximumBytes           int64                         `json:"wrapper_maximum_bytes"`
	WriterScratchProjectedBytes   int64                         `json:"writer_scratch_projected_bytes"`
	VerifierScratchProjectedBytes int64                         `json:"verifier_scratch_projected_bytes"`
	NativeHighWaterBytes          int64                         `json:"native_high_water_bytes"`
	HostHighWaterBytes            int64                         `json:"host_high_water_bytes"`
	CombinedHighWaterBytes        int64                         `json:"combined_high_water_bytes"`
	MinimumFreeBytes              int64                         `json:"minimum_free_bytes"`
	ScopeMaximumJournalBytes      int64                         `json:"scope_maximum_journal_bytes"`
	ScopeHighWaterBytes           int64                         `json:"scope_high_water_bytes"`
	Phases                        []mvpNativeBackupStoragePhase `json:"phases"`
}

func mvpNativeStorageSum(values ...int64) (int64, bool) {
	var total int64
	for _, v := range values {
		if v < 0 || total > math.MaxInt64-v {
			return 0, false
		}
		total += v
	}
	return total, true
}

func makeMVPNativeBackupStorage(p mvpNativeBackupPlan, inputs, projectedConfiguration, metadata, sourceManifest, registryManifest, journalMaximum int64) (mvpNativeBackupStoragePlan, error) {
	bad := failure("OFFLINE_SCOPE_NATIVE_BACKUP_STORAGE_POLICY_CONTRADICTION")
	policy := p.NativeRequest.Policy
	s := mvpNativeBackupStoragePlan{FormatVersion: 1, MaximumMetadataBytes: metadata, MaximumSourceManifestBytes: sourceManifest, RegistryManifestMaximumBytes: registryManifest, NativeInputBytes: inputs, NativeConfigurationBytes: projectedConfiguration, MinimumFreeBytes: policy.MinimumFreeBytes}
	if journalMaximum < configuration.MinimumJournalBytes || journalMaximum > configuration.MaximumJournalBytes {
		return s, bad
	}
	s.ScopeMaximumJournalBytes = journalMaximum
	s.ScopeHighWaterBytes = journalMaximum + 2*view.MaximumBytes + 3*maximumEventBytes
	if !p.NativeRequest.Valid() || p.NativeRequest.ApplicationProfile != wire.MVPPostgreSQLCPUApplicationProfile || p.Request.Validate() != nil || inputs < 1 || projectedConfiguration < 1 || projectedConfiguration > policy.MaximumConfigurationBytes || projectedConfiguration > inputs || metadata < 1024 || metadata > 16<<20 || sourceManifest < 1024 || sourceManifest > 4<<20 || registryManifest < 1024 || registryManifest > 4<<20 {
		return s, bad
	}
	var originalConfiguration int64
	for _, f := range p.ConfigurationFiles {
		var ok bool
		originalConfiguration, ok = mvpNativeStorageSum(originalConfiguration, f.File.Bytes)
		if !ok || f.File.Bytes < 1 {
			return s, bad
		}
	}
	// The original owner bounds snapshot+registry output together by store bytes;
	// configurationSnapshot bounds effective-control independently by config bytes.
	// Four manifest allowances retain its existing conservative preflight bound.
	var ok bool
	s.NativeOutputMaximumBytes, ok = mvpNativeStorageSum(policy.MaximumStoreBytes, policy.MaximumSQLBytes, projectedConfiguration, policy.MaximumConfigurationBytes, 4*policy.MaximumManifestBytes, 2*registryManifest)
	if !ok {
		return s, bad
	}
	s.NativeTarMaximumBytes, ok = mvpNativeStorageSum(s.NativeOutputMaximumBytes, int64(p.Request.MaximumEntries)*mvpNativeTarEntryOverhead, 2*policy.MaximumManifestBytes, mvpNativeTarEndAllowance)
	if !ok {
		return s, bad
	}
	s.WrapperMaximumBytes, ok = mvpNativeStorageSum(originalConfiguration, sourceManifest, mvpNativeWrapperMetadataFiles*metadata)
	if !ok {
		return s, bad
	}
	// Keep a full SQL-sized allowance in each original native scratch owner until
	// an original-owner narrower bound is versioned. Never shrink frozen policy.
	s.WriterScratchProjectedBytes, ok = mvpNativeStorageSum(policy.MaximumSQLBytes, policy.MaximumManifestBytes)
	if !ok {
		return s, bad
	}
	s.VerifierScratchProjectedBytes = s.WriterScratchProjectedBytes
	hostMaximum, ok := mvpNativeStorageSum(s.WrapperMaximumBytes, s.NativeTarMaximumBytes, s.NativeOutputMaximumBytes)
	if !ok || hostMaximum > p.Request.MaximumBytes {
		return s, bad
	}
	wrapperEntries := int64(len(p.ConfigurationFiles) + mvpNativeWrapperMetadataFiles + 16)
	nativeEntries := int64(p.Request.MaximumEntries) - wrapperEntries
	// The original store file count is not silently lowered to the outer limit.
	if nativeEntries < int64(policy.MaximumStoreFiles)+16 {
		return s, bad
	}
	phase := func(name string, output, tar, copied, writerScratch, verifierScratch bool) bool {
		nativeValues := []int64{inputs}
		hostValues := []int64{s.WrapperMaximumBytes}
		nativeCount, hostCount := int64(len(p.ConfigurationFiles)+32), wrapperEntries
		if output {
			nativeValues = append(nativeValues, s.NativeOutputMaximumBytes)
			nativeCount += nativeEntries
		}
		if tar {
			hostValues = append(hostValues, s.NativeTarMaximumBytes)
			hostCount++
		}
		if copied {
			hostValues = append(hostValues, s.NativeOutputMaximumBytes)
			hostCount += nativeEntries
		}
		if writerScratch {
			nativeValues = append(nativeValues, s.WriterScratchProjectedBytes)
			nativeCount += 8
		}
		if verifierScratch {
			nativeValues = append(nativeValues, s.VerifierScratchProjectedBytes)
			nativeCount += 8
		}
		native, a := mvpNativeStorageSum(nativeValues...)
		host, b := mvpNativeStorageSum(hostValues...)
		combined, c := mvpNativeStorageSum(native, host, s.ScopeHighWaterBytes)
		if !a || !b || !c {
			return false
		}
		s.Phases = append(s.Phases, mvpNativeBackupStoragePhase{Name: name, NativeBytes: native, HostBytes: host, NativeEntries: nativeCount, HostEntries: hostCount, ScopeBytes: s.ScopeHighWaterBytes, ScopeEntries: 8})
		s.NativeHighWaterBytes = max(s.NativeHighWaterBytes, native)
		s.HostHighWaterBytes = max(s.HostHighWaterBytes, host)
		s.CombinedHighWaterBytes = max(s.CombinedHighWaterBytes, combined)
		return true
	}
	if !phase("prepare", false, false, false, false, false) || !phase("original-writer", true, false, false, true, false) || !phase("retain-original-tar", true, true, false, true, false) || !phase("extract-original-tree", true, true, true, true, false) || !phase("original-network-none-verifier", true, true, true, true, true) {
		return s, bad
	}
	s.Phases = append(s.Phases, mvpNativeBackupStoragePhase{Name: "published-host-source", HostBytes: hostMaximum, HostEntries: int64(p.Request.MaximumEntries), ScopeBytes: s.ScopeHighWaterBytes, ScopeEntries: 8})
	return s, nil
}

func validMVPNativeBackupStorage(p mvpNativeBackupPlan) bool {
	s := p.Storage
	expected, e := makeMVPNativeBackupStorage(p, s.NativeInputBytes, s.NativeConfigurationBytes, s.MaximumMetadataBytes, s.MaximumSourceManifestBytes, s.RegistryManifestMaximumBytes, s.ScopeMaximumJournalBytes)
	return e == nil && reflect.DeepEqual(expected, s)
}

// A native local Engine must attest its actual accessible host root and all
// source-volume mappings. An unavailable capability refuses before reservation;
// virtual/remote free space alone never substitutes for physical backing.
type mvpNativeBackupStorageEngine interface {
	ObserveNativePostgreSQLStorage(context.Context, engine.DatabaseTask) (engine.NativePostgreSQLStorageObservation, error)
}

type mvpNativeBackupFilesystem struct {
	BackingRole                      string
	Device                           uint64
	Inode                            uint64
	Total, Available, AllocationUnit int64
}

func observeMVPNativeBackupFilesystem(path string) (mvpNativeBackupFilesystem, error) {
	fs, e := engine.ObserveNativePostgreSQLDurableDirectory(path)
	if e != nil || fs.BackingRole != engine.NativePostgreSQLDurableBacking {
		return mvpNativeBackupFilesystem{}, failure("OFFLINE_SCOPE_NATIVE_BACKUP_DURABLE_BACKING_REQUIRED")
	}
	return mvpNativeBackupFilesystem{BackingRole: fs.BackingRole, Device: fs.Device, Inode: fs.Inode, Total: fs.TotalBytes, Available: fs.AvailableBytes, AllocationUnit: fs.AllocationUnitBytes}, nil
}

func mvpNativeFilesystemAdmission(s mvpNativeBackupStoragePlan, native, host, scope, system mvpNativeBackupFilesystem) error {
	bad := failure("OFFLINE_SCOPE_NATIVE_BACKUP_PEAK_HEADROOM_REQUIRED")
	for _, fs := range []mvpNativeBackupFilesystem{native, host, scope, system} {
		if fs.BackingRole != engine.NativePostgreSQLDurableBacking || fs.Device == 0 || fs.Total <= 0 || fs.Available < 0 || fs.Available > fs.Total || fs.AllocationUnit < 1 || fs.AllocationUnit > 1<<20 {
			return bad
		}
	}
	// Same device observations must describe one capacity, never separate pools.
	for _, pair := range [][2]mvpNativeBackupFilesystem{{native, host}, {native, system}, {host, system}, {scope, native}, {scope, host}, {scope, system}} {
		if pair[0].Device == pair[1].Device && (pair[0].Total != pair[1].Total || pair[0].AllocationUnit != pair[1].AllocationUnit) {
			return bad
		}
	}
	for _, phase := range s.Phases {
		need := map[uint64]int64{}
		observed := map[uint64]mvpNativeBackupFilesystem{}
		for _, part := range []struct {
			fs             mvpNativeBackupFilesystem
			bytes, entries int64
		}{{native, phase.NativeBytes, phase.NativeEntries}, {host, phase.HostBytes, phase.HostEntries}, {scope, phase.ScopeBytes, phase.ScopeEntries}, {system, 0, 0}} {
			// Account for file rounding and a directory/inode allocation allowance;
			// retained free reserve remains necessary for filesystem metadata/journals.
			if part.entries < 0 || part.entries > math.MaxInt64/(2*part.fs.AllocationUnit) {
				return bad
			}
			total, ok := mvpNativeStorageSum(need[part.fs.Device], part.bytes, part.entries*2*part.fs.AllocationUnit)
			if !ok {
				return bad
			}
			need[part.fs.Device] = total
			previous, exists := observed[part.fs.Device]
			if !exists || part.fs.Available < previous.Available {
				observed[part.fs.Device] = part.fs
			}
		}
		for device, fs := range observed {
			reserve := s.MinimumFreeBytes
			if device == system.Device {
				floor := fs.Total/100*30 + (fs.Total%100*30+99)/100
				reserve = max(reserve, floor)
			}
			required, ok := mvpNativeStorageSum(need[device], reserve)
			if !ok || fs.Available < required {
				return bad
			}
		}
	}
	return nil
}

func admitMVPNativeBackupStorage(ctx context.Context, base backupEngine, p mvpNativeBackupPlan, e mvpNativeBackupExecution, scopeRoot string, expected *engine.Observation, next ...BackupStep) error {
	if !validMVPNativeBackupStorage(p) {
		return failure("OFFLINE_SCOPE_NATIVE_BACKUP_STORAGE_POLICY_CONTRADICTION")
	}
	client, ok := base.(mvpNativeBackupStorageEngine)
	if !ok {
		return failure("OFFLINE_SCOPE_NATIVE_BACKUP_PHYSICAL_STORAGE_MAPPING_REQUIRED")
	}
	if !validEngine(expected) || expected.Status != engine.NativeStatus {
		return failure("OFFLINE_SCOPE_NATIVE_BACKUP_PHYSICAL_STORAGE_MAPPING_REQUIRED")
	}
	observation, err := client.ObserveNativePostgreSQLStorage(ctx, e.Writer)
	if err != nil {
		return err
	}
	root := observation.AllocationRoot
	if observation.Status != engine.NativePostgreSQLStorageObserved || observation.EngineSHA256 != expected.EngineSHA256 || observation.EndpointSHA256 != expected.EndpointSHA256 {
		return failure("OFFLINE_SCOPE_NATIVE_BACKUP_PHYSICAL_STORAGE_MAPPING_REQUIRED")
	}
	profile := e.Writer.NativePostgreSQLRecovery
	if profile == nil || profile.Registry == nil || len(observation.SourceOwners) != 5 {
		return failure("OFFLINE_SCOPE_NATIVE_BACKUP_PHYSICAL_STORAGE_MAPPING_REQUIRED")
	}
	for i, owner := range observation.SourceOwners {
		want := profile.Registry.Owner
		if i < 4 {
			want = profile.Stores[i].Owner
		}
		if owner.Owner != want || owner.Filesystem.Device != observation.Filesystem.Device || owner.Filesystem.Inode == 0 || owner.Filesystem.TotalBytes != observation.Filesystem.TotalBytes || owner.Filesystem.AllocationUnitBytes != observation.Filesystem.AllocationUnitBytes {
			return failure("OFFLINE_SCOPE_NATIVE_BACKUP_PHYSICAL_STORAGE_MAPPING_REQUIRED")
		}
	}
	if root == "" || root == string(filepath.Separator) {
		return failure("OFFLINE_SCOPE_NATIVE_BACKUP_PHYSICAL_STORAGE_MAPPING_REQUIRED")
	}
	native, err := observeMVPNativeBackupFilesystem(root)
	if err != nil {
		return err
	}
	if native.Device != observation.Filesystem.Device || native.Inode != observation.Filesystem.Inode || native.Total != observation.Filesystem.TotalBytes || native.AllocationUnit != observation.Filesystem.AllocationUnitBytes {
		return failure("OFFLINE_SCOPE_NATIVE_BACKUP_PHYSICAL_STORAGE_MAPPING_REQUIRED")
	}
	native.Available = min(native.Available, observation.Filesystem.AvailableBytes)
	host, err := observeMVPNativeBackupFilesystem(filepath.Dir(p.Request.DestinationRoot))
	if err != nil {
		return err
	}
	system, err := observeMVPNativeBackupFilesystem(string(filepath.Separator))
	if err != nil {
		return err
	}
	scope, err := observeMVPNativeBackupFilesystem(scopeRoot)
	if err != nil {
		return err
	}
	if ctx.Err() != nil {
		return failure("OFFLINE_SCOPE_OPERATION_INTERRUPTED")
	}
	return mvpNativeFilesystemAdmission(mvpNativeNextStepStorage(p.Storage, next...), native, host, scope, system)
}

// Removing an original task/temporary volume allocates no new payload. Preserve
// fresh original Engine/backing checks and the bounded journal/view allowance;
// allocating phases conservatively readmit the complete coexistence peak.
func mvpNativeNextStepStorage(s mvpNativeBackupStoragePlan, next ...BackupStep) mvpNativeBackupStoragePlan {
	if len(next) != 1 {
		return s
	}
	switch next[0].Phase {
	case "utility-remove", "task-remove", "volume-remove", "complete":
		s.Phases = []mvpNativeBackupStoragePhase{{Name: "current-step-journal-and-view", ScopeBytes: s.ScopeHighWaterBytes, ScopeEntries: 8}}
	}
	return s
}
