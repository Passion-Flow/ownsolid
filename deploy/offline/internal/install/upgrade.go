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
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
	"github.com/Passion-Flow/ownsolid/tools/release/offline/pkg/bundle"
)

const (
	upgradeIntentName  = "upgrade-intent.json"
	previousBundleName = "previous-release.json"
)

type upgradeIntent struct {
	FormatVersion int           `json:"format_version"`
	Source        bundleBinding `json:"source"`
	Target        bundleBinding `json:"target"`
	SourceSchema  int           `json:"source_schema"`
	TargetSchema  int           `json:"target_schema"`
	SiteSHA256    string        `json:"site_sha256"`
	BackupRoot    string        `json:"backup_root"`
	BackupTime    time.Time     `json:"backup_time"`
}

func (u upgradeIntent) backupPath() string {
	return filepath.Join(u.BackupRoot, backupPrefix+u.BackupTime.UTC().Format(backupTimeLayout))
}

func readUpgradeIntent(c Config) (*upgradeIntent, error) {
	path := filepath.Join(c.InstallRoot, upgradeIntentName)
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	raw, err := bundle.ReadProtectedConfiguration(path)
	var intent upgradeIntent
	if err != nil || wire.StrictJSON(raw, &intent) != nil || intent.FormatVersion != 1 ||
		!validBundleBinding(intent.Source) || !validBundleBinding(intent.Target) ||
		intent.Source == intent.Target || intent.SourceSchema < 1 || intent.TargetSchema < intent.SourceSchema ||
		!wire.ValidSHA256(intent.SiteSHA256) || !canonicalPath(intent.BackupRoot) || intent.BackupTime.IsZero() {
		return nil, errors.New("INSTALL_UPGRADE_INTENT_INVALID: the pending upgrade record cannot be trusted")
	}
	return &intent, nil
}

func validateUpgradeBackupRoot(c Config, root string) error {
	if err := admitBackupOutput(c, root); err != nil {
		return err
	}
	path, err := filepath.EvalSymlinks(root)
	if err != nil || path != root {
		return errors.New("INSTALL_UPGRADE_BACKUP_ROOT_INVALID: use a canonical private backup directory")
	}
	info, err := os.Stat(root)
	if err != nil || info.Mode().Perm()&0o077 != 0 || int(info.Sys().(*syscall.Stat_t).Uid) != os.Geteuid() {
		return errors.New("INSTALL_UPGRADE_BACKUP_ROOT_INVALID: use a backup directory owned by the installer with mode 0700")
	}
	return nil
}

// UpgradeBundle resumes or applies a verified application release after stopping
// its writer and making a verified PostgreSQL recovery point.
func UpgradeBundle(ctx context.Context, configPath, archive, backupRoot string, policy bundle.Verification, runner StreamRunner, out io.Writer) error {
	return upgradeBundle(ctx, configPath, archive, backupRoot, "", policy, runner, out)
}

func upgradeBundle(ctx context.Context, configPath, archive, backupRoot, expectedBaseline string, policy bundle.Verification, runner StreamRunner, out io.Writer) error {
	p, cleanup, err := prepareBundle(ctx, configPath, archive, policy)
	if err != nil {
		return err
	}
	defer cleanup()
	c := p.config
	if err := validateUpgradeBackupRoot(c, backupRoot); err != nil {
		return err
	}
	if err := checkBundleHost(ctx, c, runner, out); err != nil {
		return retryWith(err, "upgrade-bundle")
	}
	unlock, err := lockOperation(c)
	if err != nil {
		return err
	}
	defer unlock()
	if err := checkInstalledProject(c); err != nil {
		return err
	}
	source, err := readBundleBinding(c)
	if err != nil {
		return err
	}
	if source == nil {
		return errors.New("INSTALL_UPGRADE_BOUND_RELEASE_REQUIRED: no signed release is bound to this installation")
	}
	intent, err := readUpgradeIntent(c)
	if err != nil {
		return err
	}
	site := c
	site.ComposeFile, site.Images = "", ImagesConfig{}
	siteJSON, err := json.Marshal(site)
	if err != nil {
		return errors.New("INSTALL_UPGRADE_SITE_INVALID: cannot bind the site configuration to the pending upgrade")
	}
	siteSHA256 := wire.Digest(siteJSON)
	if intent != nil && (intent.SiteSHA256 != siteSHA256 || intent.Target != p.binding || (*source != intent.Source && *source != intent.Target) || filepath.Dir(intent.BackupRoot) != backupRoot) {
		return errors.New("INSTALL_UPGRADE_PENDING: retry with the same signed candidate, site configuration and backup directory")
	}
	installed := c
	installed.ComposeFile, installed.Images = c.Layout().ComposeFile, source.images()
	previous := newOperation(installed, runner, out)
	schemaContext, cancel := context.WithTimeout(ctx, time.Duration(c.StepSeconds)*time.Second)
	version, err := previous.schemaVersion(schemaContext, databaseName)
	cancel()
	if err != nil {
		return errors.New("INSTALL_UPGRADE_SCHEMA_UNAVAILABLE: start the installation's PostgreSQL service and retry")
	}
	baseline := *source
	if intent != nil {
		baseline = intent.Source
	}
	if expectedBaseline != "" && expectedBaseline != baseline.ManifestSHA256 && *source != p.binding {
		return errors.New("INSTALL_PATCH_BASELINE_CHANGED: the installed release changed while preparing the patch")
	}
	if intent == nil && *source == p.binding {
		if version != p.result.Manifest.SchemaVersion {
			return errors.New("INSTALL_UPGRADE_SCHEMA_CHANGED: active release differs from the database schema")
		}
		if err := Status(ctx, installed, runner, out); err != nil {
			return err
		}
		_, err := fmt.Fprintf(out, "UPGRADE_ALREADY_ACTIVE release=%s schema=%d\n", source.ReleaseID, version)
		return err
	}
	if intent == nil {
		plan, err := makeUpgradePlan(*source, version, p.result)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "UPGRADE_PLAN_READY source=%s target=%s schema=%d..%d\n", source.ReleaseID, p.binding.ReleaseID, plan.CurrentSchema, plan.TargetSchema)
	}
	if err := p.loadImages(ctx, runner, policy); err != nil {
		return retryWith(err, "upgrade-bundle")
	}
	if intent == nil {
		root, err := os.MkdirTemp(backupRoot, "ownsolid-upgrade-")
		if err != nil {
			return errors.New("INSTALL_UPGRADE_BACKUP_ROOT_INVALID: cannot reserve a private recovery directory")
		}
		intent = &upgradeIntent{FormatVersion: 1, Source: *source, Target: p.binding,
			SourceSchema: version, TargetSchema: p.result.Manifest.SchemaVersion, SiteSHA256: siteSHA256, BackupRoot: root, BackupTime: time.Now().UTC()}
		if err := syncDirectory(backupRoot); err != nil {
			return err
		}
		if err := writeReleaseRecord(c, upgradeIntentName, intent); err != nil {
			return err
		}
	}
	if err := validateUpgradeBackupRoot(c, intent.BackupRoot); err != nil {
		return err
	}
	backup := intent.backupPath()
	_, statError := os.Lstat(backup)
	if statError != nil && !errors.Is(statError, os.ErrNotExist) {
		return errors.New("INSTALL_UPGRADE_BACKUP_INVALID: cannot read the reserved recovery point")
	}
	if version < intent.SourceSchema || version > intent.TargetSchema || statError != nil && (version != intent.SourceSchema || *source == intent.Target) {
		return errors.New("INSTALL_UPGRADE_SCHEMA_CHANGED: observed schema does not match a resumable upgrade")
	}
	stopContext, cancel := context.WithTimeout(ctx, time.Duration(c.StepSeconds)*time.Second)
	err = previous.stopControl(stopContext)
	cancel()
	if err != nil {
		return retryWith(wrapStep("stop-control", err), "upgrade-bundle")
	}
	fmt.Fprintln(out, "step upgrade-stop-control: ok")
	if errors.Is(statError, os.ErrNotExist) {
		if exists(backup + backupPartialSuffix) {
			// Keep an interrupted recovery attempt intact and reserve a fresh path.
			old := backup + backupPartialSuffix
			hold, err := os.MkdirTemp(intent.BackupRoot, "interrupted-")
			if err != nil || os.Rename(old, filepath.Join(hold, "backup.partial")) != nil {
				return errors.New("INSTALL_UPGRADE_BACKUP_INVALID: cannot preserve the interrupted backup")
			}
			if err := syncDirectory(intent.BackupRoot); err != nil {
				return err
			}
		}
		previous.now = func() time.Time { return intent.BackupTime }
		backupContext, cancel := context.WithTimeout(ctx, time.Duration(c.BackupSeconds)*time.Second)
		_, err = previous.backup(backupContext, intent.BackupRoot)
		cancel()
		if err != nil {
			// A published recovery point must stay the last writable state.
			// If publication is uncertain, leave the writer stopped.
			if _, statErr := os.Lstat(backup); !errors.Is(statErr, os.ErrNotExist) {
				return retryWith(err, "upgrade-bundle")
			}
			restartContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Duration(c.StepSeconds)*time.Second)
			restartErr := previous.control(restartContext)
			cancel()
			return retryWith(errors.Join(err, restartErr), "upgrade-bundle")
		}
	}
	manifest, err := readBackup(backup)
	if err != nil || manifest.SchemaVersion != intent.SourceSchema || manifest.ControlImage != intent.Source.ImageIDs[0] ||
		manifest.PostgresImage != intent.Source.ImageIDs[1] {
		return errors.New("INSTALL_UPGRADE_BACKUP_INVALID: recovery point does not match the original release and schema")
	}
	fmt.Fprintf(out, "UPGRADE_BACKUP_VERIFIED path=%s schema=%d\n", backup, manifest.SchemaVersion)
	target := p.operation(runner, out)
	target.installer.complete = func() error {
		if err := writeReleaseRecord(c, previousBundleName, intent.Source); err != nil {
			return err
		}
		if err := writeReleaseRecord(c, bundleBindingName, intent.Target); err != nil {
			return err
		}
		if err := os.Remove(filepath.Join(c.InstallRoot, upgradeIntentName)); err != nil {
			return err
		}
		return syncDirectory(c.InstallRoot)
	}
	if err := target.installer.apply(ctx); err != nil {
		target.record("upgrade", "failed", p.binding.ReleaseID)
		return retryWith(err, "upgrade-bundle")
	}
	target.record("upgrade", "complete", p.binding.ReleaseID)
	_, err = fmt.Fprintf(out, "UPGRADE_COMPLETE release=%s schema=%d\n", p.binding.ReleaseID, p.result.Manifest.SchemaVersion)
	return err
}
