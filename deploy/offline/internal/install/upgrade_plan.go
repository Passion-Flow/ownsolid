package install

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"

	"github.com/Passion-Flow/ownsolid/tools/release/offline/pkg/bundle"
)

type upgradePlan struct {
	InstalledRelease   string   `json:"installed_release"`
	InstalledManifest  string   `json:"installed_manifest_sha256"`
	CandidateRelease   string   `json:"candidate_release"`
	CandidateManifest  string   `json:"candidate_manifest_sha256"`
	CurrentSchema      int      `json:"current_schema"`
	TargetSchema       int      `json:"target_schema"`
	MinimumPredecessor int      `json:"minimum_predecessor_schema"`
	MaximumPredecessor int      `json:"maximum_predecessor_schema"`
	MigrationRequired  bool     `json:"migration_required"`
	BackupRequired     bool     `json:"backup_required"`
	StopServices       []string `json:"stop_services"`
}

// PlanUpgrade compares a fully verified candidate with the live signed
// installation. It takes a read lock and creates no files or containers.
func PlanUpgrade(ctx context.Context, configPath, archive string, policy bundle.Verification, runner StreamRunner, out io.Writer) error {
	c, err := LoadPreflight(configPath)
	if err != nil {
		return err
	}
	if c.ComposeFile != "" || c.Images != (ImagesConfig{}) {
		return errors.New("INSTALL_UPGRADE_BOUND_RELEASE_REQUIRED: use the site configuration of a signed installation")
	}
	if err := pendingDataRecovery(c); err != nil {
		return err
	}
	candidate, err := bundle.InspectCurrent(ctx, policy, archive)
	if err != nil {
		return fmt.Errorf("INSTALL_BUNDLE_VERIFICATION_FAILED: %w", err)
	}
	if err := checkLocalDocker(ctx, runner); err != nil {
		return err
	}
	lock, err := os.OpenFile(filepath.Join(c.InstallRoot, operationLockName), os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return errors.New("INSTALL_UPGRADE_BOUND_RELEASE_REQUIRED: cannot open the signed installation's operation lock")
	}
	defer lock.Close()
	if st, err := lock.Stat(); err != nil || !st.Mode().IsRegular() {
		return errors.New("INSTALL_UPGRADE_BOUND_RELEASE_REQUIRED: the operation lock is invalid")
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_SH|syscall.LOCK_NB); err != nil {
		return errors.New("INSTALL_BUSY: another operation holds the installation lock; retry the plan")
	}
	// Candidate verification can take time. Recheck under the shared lock
	// before reading the installed binding or a potentially fenced database.
	if err := pendingDataRecovery(c); err != nil {
		return err
	}
	if err := checkInstalledProject(c); err != nil {
		return err
	}
	if exists(filepath.Join(c.InstallRoot, upgradeIntentName)) {
		return errors.New("INSTALL_UPGRADE_PENDING: resume the existing upgrade before planning another")
	}
	source, err := readBundleBinding(c)
	if err != nil {
		return err
	}
	if source == nil {
		return errors.New("INSTALL_UPGRADE_BOUND_RELEASE_REQUIRED: no signed release is bound to this installation")
	}
	c.ComposeFile, c.Images = c.Layout().ComposeFile, source.images()
	version, err := newOperation(c, runner, io.Discard).schemaVersion(ctx, databaseName)
	if err != nil {
		return errors.New("INSTALL_UPGRADE_SCHEMA_UNAVAILABLE: start the installed PostgreSQL service and verify its schema")
	}
	plan, err := makeUpgradePlan(*source, version, candidate)
	if err != nil {
		return err
	}
	return json.NewEncoder(out).Encode(plan)
}

func makeUpgradePlan(source bundleBinding, version int, candidate bundle.Result) (upgradePlan, error) {
	m := candidate.Manifest
	if candidate.ManifestSHA256 == source.ManifestSHA256 {
		return upgradePlan{}, errors.New("INSTALL_UPGRADE_SAME_RELEASE: the candidate is already bound to this installation")
	}
	if candidate.ReleaseID == source.ReleaseID {
		return upgradePlan{}, errors.New("INSTALL_UPGRADE_RELEASE_ID_REUSED: a different candidate must have its own release ID")
	}
	if version < m.MinimumPredecessor || version > m.MaximumPredecessor || version > m.SchemaVersion {
		return upgradePlan{}, fmt.Errorf("INSTALL_UPGRADE_PREDECESSOR_UNSUPPORTED: schema %d is outside the signed predecessor interval %d through %d for target %d",
			version, m.MinimumPredecessor, m.MaximumPredecessor, m.SchemaVersion)
	}
	assets, _ := bundleInventory(candidate)
	for index, role := range bundleImageRoles[1:] {
		if assets[role].ImageID != source.ImageIDs[index+1] {
			return upgradePlan{}, fmt.Errorf("INSTALL_UPGRADE_STORAGE_IMAGE_CHANGED: %s needs a separate data migration procedure", role)
		}
	}
	return upgradePlan{
		InstalledRelease: source.ReleaseID, InstalledManifest: source.ManifestSHA256,
		CandidateRelease: candidate.ReleaseID, CandidateManifest: candidate.ManifestSHA256,
		CurrentSchema: version, TargetSchema: m.SchemaVersion,
		MinimumPredecessor: m.MinimumPredecessor, MaximumPredecessor: m.MaximumPredecessor,
		MigrationRequired: version != m.SchemaVersion, BackupRequired: true, StopServices: []string{"control"},
	}, nil
}
