package install

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/Passion-Flow/ownsolid/tools/release/offline/pkg/bundle"
)

// RollbackBundle restarts the previously recorded signed application release
// when it can run on the live schema. Database restore is a separate operation.
func RollbackBundle(ctx context.Context, configPath, archive string, policy bundle.Verification, runner StreamRunner, out io.Writer) error {
	c, err := loadBundleSite(configPath)
	if err != nil {
		return err
	}
	return rollbackBundle(ctx, c, archive, policy, runner, out)
}

func rollbackBundle(ctx context.Context, siteConfig Config, archive string, policy bundle.Verification, runner StreamRunner, out io.Writer) error {
	if err := pendingDataRecovery(siteConfig); err != nil {
		return err
	}
	p, cleanup, err := prepareBundleForSite(ctx, siteConfig, archive, policy)
	if err != nil {
		return err
	}
	defer cleanup()
	c := p.config
	if err := checkBundleHost(ctx, c, runner, out); err != nil {
		return retryWith(err, "rollback-bundle")
	}
	unlock, err := lockOperation(c)
	if err != nil {
		return err
	}
	defer unlock()
	if err := checkInstalledProject(c); err != nil {
		return err
	}
	active, err := readBundleBinding(c)
	if err != nil {
		return err
	}
	if active == nil {
		return errors.New("INSTALL_ROLLBACK_BOUND_RELEASE_REQUIRED: no signed release is bound to this installation")
	}
	intent, err := readUpgradeIntent(c)
	if err != nil {
		return err
	}
	prior, err := readReleaseBinding(c, previousBundleName)
	if err != nil {
		return err
	}
	if intent != nil {
		prior = &intent.Source
	}
	if prior == nil || *prior != p.binding {
		return errors.New("INSTALL_ROLLBACK_RELEASE_MISMATCH: supply the exact previously recorded signed release")
	}
	installed := c
	installed.ComposeFile, installed.Images = c.Layout().ComposeFile, active.images()
	current := newOperation(installed, runner, out)
	step, cancel := context.WithTimeout(ctx, time.Duration(c.StepSeconds)*time.Second)
	version, err := current.schemaVersion(step, databaseName)
	cancel()
	if err != nil {
		return errors.New("INSTALL_UPGRADE_SCHEMA_UNAVAILABLE: start the installation's PostgreSQL service and retry")
	}
	if err := checkRollbackSchema(version, p.result.Manifest.SchemaVersion); err != nil {
		return err
	}
	if *active == p.binding && intent == nil {
		if err := Status(ctx, installed, runner, out); err != nil {
			return err
		}
		_, err := fmt.Fprintf(out, "ROLLBACK_ALREADY_ACTIVE release=%s schema=%d\n", p.binding.ReleaseID, version)
		return err
	}
	if err := p.loadImages(ctx, runner, policy); err != nil {
		return retryWith(err, "rollback-bundle")
	}
	previous := p.operation(runner, out)
	step, cancel = context.WithTimeout(ctx, time.Duration(c.StepSeconds)*time.Second)
	err = current.verifyServingImage(step, p.binding.ImageIDs[0])
	cancel()
	if err != nil {
		return retryWith(wrapStep("rollback-serving-transport", err), "rollback-bundle")
	}
	steps := []struct {
		name string
		run  func(context.Context) error
	}{
		{"rollback-stop-control", current.stopControl},
		{"rollback-configuration", func(context.Context) error { return Materialize(c) }},
		{"rollback-check-schema", previous.checkSchema},
		{"rollback-control", previous.control},
		{"rollback-health", previous.health},
	}
	for _, operation := range steps {
		step, cancel := context.WithTimeout(ctx, time.Duration(c.StepSeconds)*time.Second)
		err := operation.run(step)
		cancel()
		if err != nil {
			previous.record("rollback", "failed", p.binding.ReleaseID)
			return retryWith(wrapStep(operation.name, err), "rollback-bundle")
		}
		fmt.Fprintf(out, "step %s: ok\n", operation.name)
	}
	if err := writeReleaseRecord(c, bundleBindingName, p.binding); err != nil {
		return err
	}
	if intent != nil {
		if err := os.Remove(filepath.Join(c.InstallRoot, upgradeIntentName)); err != nil {
			return err
		}
		if err := syncDirectory(c.InstallRoot); err != nil {
			return err
		}
	}
	previous.record("rollback", "complete", p.binding.ReleaseID)
	_, err = fmt.Fprintf(out, "ROLLBACK_COMPLETE release=%s schema=%d\n", p.binding.ReleaseID, version)
	return err
}

func checkRollbackSchema(actual, accepted int) error {
	if actual != accepted {
		return fmt.Errorf("INSTALL_ROLLBACK_DATABASE_RESTORE_REQUIRED: the prior release expects schema %d, the database is at %d; recover the database explicitly before retrying", accepted, actual)
	}
	return nil
}
