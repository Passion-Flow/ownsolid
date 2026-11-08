package install

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/Passion-Flow/ownsolid/tools/release/offline/pkg/bundle"
)

// UpgradePatch verifies a transport patch and reuses the same upgrade procedure
// as a complete bundle after reconstructing the exact signed target locally.
func UpgradePatch(ctx context.Context, configPath, baseline, patch, backupRoot string, policy bundle.Verification, runner StreamRunner, out io.Writer) error {
	c, err := LoadPreflight(configPath)
	if err != nil {
		return err
	}
	if c.ComposeFile != "" || c.Images != (ImagesConfig{}) {
		return errors.New("INSTALL_UPGRADE_BOUND_RELEASE_REQUIRED: use the site configuration of a signed installation")
	}
	active, err := readBundleBinding(c)
	if err != nil {
		return err
	}
	if active == nil {
		return errors.New("INSTALL_UPGRADE_BOUND_RELEASE_REQUIRED: no signed release is bound to this installation")
	}
	intent, err := readUpgradeIntent(c)
	if err != nil {
		return err
	}
	expected := active.ManifestSHA256
	if intent != nil {
		expected = intent.Source.ManifestSHA256
	}
	mount, err := filepath.EvalSymlinks(policy.MountRoot)
	if err != nil || mount != policy.MountRoot || !canonicalPath(mount) || mount == "/" {
		return errors.New("INSTALL_BUNDLE_STAGING_UNAVAILABLE: use a real mounted volume for patch reconstruction")
	}
	work, err := os.MkdirTemp(mount, ".ownsolid-patch-upgrade-")
	if err != nil {
		return errors.New("INSTALL_BUNDLE_STAGING_UNAVAILABLE: cannot reserve patch reconstruction space")
	}
	defer os.RemoveAll(work)
	output := filepath.Join(work, "target.tar.gz")
	_, err = bundle.RebuildCurrentPatch(ctx, policy, baseline, patch, output, expected)
	if err != nil && intent == nil && bundle.ErrorCode(err) == "RELEASE_PATCH_BASELINE_MISMATCH" {
		// A response may be lost after the target became active. Only the
		// recorded previous release may reconstruct an already active target.
		prior, readErr := readReleaseBinding(c, previousBundleName)
		if readErr != nil {
			return readErr
		}
		if prior != nil && *prior != *active {
			result, retryErr := bundle.RebuildCurrentPatch(ctx, policy, baseline, patch, output, prior.ManifestSHA256)
			if retryErr == nil && result.ManifestSHA256 == active.ManifestSHA256 {
				expected, err = prior.ManifestSHA256, nil
			}
		}
	}
	if err != nil {
		return fmt.Errorf("INSTALL_PATCH_VERIFICATION_FAILED: %w", err)
	}
	return upgradeBundle(ctx, configPath, output, backupRoot, expected, policy, runner, out)
}
