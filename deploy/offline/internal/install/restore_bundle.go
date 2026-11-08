package install

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/Passion-Flow/ownsolid/tools/release/offline/pkg/bundle"
)

const restoreBundleCommand = "restore-bundle"

// RestoreBundle restores a checked logical backup using the exact signed release.
// The site file supplies only deployment inputs, including the target root and ports.
func RestoreBundle(ctx context.Context, configPath, archive, from, identityFile string, policy bundle.Verification, runner StreamRunner, out io.Writer) error {
	p, cleanup, err := prepareBundle(ctx, configPath, archive, policy)
	if err != nil {
		return err
	}
	defer cleanup()

	c := p.config
	var manifest backupManifest
	if identityFile == "" {
		manifest, err = readSignedRestoreDirectory(c, from)
		if err != nil {
			return err
		}
		if err := p.checkRestoreBackup(manifest); err != nil {
			return err
		}
	}

	unlock, err := lockOperation(c)
	if err != nil {
		return err
	}
	defer unlock()
	ctx, cancel := context.WithTimeout(ctx, time.Duration(c.BackupSeconds)*time.Second)
	defer cancel()
	source := from
	if identityFile != "" {
		work, decoded, cleanup, err := loadEncryptedBackup(ctx, c, from, identityFile, out)
		if err != nil {
			return err
		}
		defer cleanup()
		from, manifest = work, decoded
		if err := p.checkRestoreBackup(manifest); err != nil {
			return err
		}
	}

	if err := checkInstalledProject(c); err != nil {
		return err
	}
	pending, err := readUpgradeIntent(c)
	if err != nil {
		return err
	}
	if pending != nil {
		return errors.New("INSTALL_UPGRADE_PENDING: resolve the pending signed upgrade before restoring")
	}
	if err := checkBundleHost(ctx, c, runner, out); err != nil {
		return retryWith(err, restoreBundleCommand)
	}
	if err := checkRestoreProject(ctx, c, runner); err != nil {
		return err
	}
	if err := bindRelease(c, p.binding); err != nil {
		return err
	}
	fmt.Fprintf(out, "INSTALL_BUNDLE_VERIFIED release=%s manifest_sha256=%s\n", p.result.ReleaseID, p.result.ManifestSHA256)
	if manifest.ReleaseManifestSHA256 == "" {
		fmt.Fprintln(out, "step backup-release: legacy image and schema match")
	}
	if err := p.loadImages(ctx, runner, policy); err != nil {
		return retryWith(err, restoreBundleCommand)
	}
	o := p.operation(runner, out)
	o.signedRestore = true
	return o.restoreVerified(ctx, from, source, manifest)
}

func readSignedRestoreDirectory(c Config, path string) (backupManifest, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil || !canonicalPath(path) || resolved != path ||
		path == c.InstallRoot || strings.HasPrefix(path, c.InstallRoot+string(filepath.Separator)) {
		return backupManifest{}, errors.New("INSTALL_BACKUP_INVALID: use a canonical private backup directory outside the installation")
	}
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 || info.Sys().(*syscall.Stat_t).Uid != 0 {
		return backupManifest{}, errors.New("INSTALL_BACKUP_INVALID: use a root-owned backup directory with mode 0700")
	}
	return readBackup(path)
}

func (p preparedBundle) checkRestoreBackup(manifest backupManifest) error {
	if manifest.Recovery != nil {
		return errors.New("INSTALL_BACKUP_INVALID: use the PostgreSQL replay procedure for a point-in-time activation backup")
	}
	if manifest.SchemaVersion != p.result.Manifest.SchemaVersion ||
		manifest.ControlImage != p.config.Images.Control || manifest.PostgresImage != p.config.Images.Postgres ||
		(manifest.ReleaseManifestSHA256 != "" && manifest.ReleaseManifestSHA256 != p.binding.ManifestSHA256) {
		return errors.New("INSTALL_BACKUP_RELEASE_MISMATCH: use the signed release that produced this backup")
	}
	if _, err := encryptedMembers(manifest); err != nil {
		return errors.New("INSTALL_BACKUP_INVALID: the backup contains an invalid file inventory")
	}
	required := append(slices.Clone(keyFiles), "secrets/postgres-password")
	for _, path := range required {
		if !slices.ContainsFunc(manifest.Files, func(file backupFile) bool { return file.Path == path && file.Bytes > 0 }) {
			return fmt.Errorf("INSTALL_BACKUP_INVALID: the backup has no usable %s", path)
		}
	}
	return nil
}

// Compose addresses containers by project name even if their config files came
// from a different root. Refuse to touch that project's writer or database.
func checkRestoreProject(ctx context.Context, c Config, runner Runner) error {
	const format = `{{json (.Label "com.docker.compose.project.working_dir")}}`
	dirs, err := runner.Run(ctx, "docker", "ps", "--all", "--filter", "label=com.docker.compose.project="+c.Project, "--format", format)
	if err != nil {
		return errors.New("INSTALL_BUNDLE_PROJECT_UNAVAILABLE: cannot check the Docker Compose project")
	}
	if strings.TrimSpace(dirs) == "" {
		return nil
	}
	for _, line := range strings.Split(strings.TrimSpace(dirs), "\n") {
		var dir string
		if json.Unmarshal([]byte(line), &dir) != nil || dir != c.InstallRoot {
			return errors.New("INSTALL_BUNDLE_PROJECT_CHANGED: use a Compose project that belongs to this installation")
		}
	}
	return nil
}
