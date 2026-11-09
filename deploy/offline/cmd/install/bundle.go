package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/install"
	"github.com/Passion-Flow/ownsolid/tools/release/offline/pkg/bundle"
)

func runBundle(command string, args []string, out io.Writer) error {
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	archive := flags.String("bundle", "", "signed offline release archive")
	policy := flags.String("verification", "", "protected public verification configuration")
	registry := flags.String("registry", "", "private site Registry IP and port")
	config := flags.String("config", "", "typed site configuration without delivery inputs")
	output := flags.String("output", "", "new directory for verified payloads on the configured mount")
	baseline := flags.String("baseline", "", "previous independently verified signed archive")
	backup := flags.String("backup-output", "", "private directory for the verified pre-upgrade recovery point")
	from := flags.String("from", "", "backup directory or encrypted archive to restore")
	identity := flags.String("identity-file", "", "protected X25519 private recovery identity file")
	timeout := flags.Duration("timeout", 30*time.Minute, "time limit for verifying or applying the release")
	restore := command == "restore-bundle"
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 || *archive == "" || *policy == "" ||
		(restore || command == "install-agent" || command == "apply-bundle" || command == "registry-bundle" || command == "upgrade-plan" || command == "upgrade-bundle" || command == "upgrade-patch" || command == "rollback-bundle") != (*config != "") || (command == "prepare-bundle") != (*output != "") || (command == "upgrade-bundle" || command == "upgrade-patch") != (*backup != "") || (command == "upgrade-patch") != (*baseline != "") || restore != (*from != "") || (command == "helm-images") != (*registry != "") || *timeout <= 0 {
		return errors.New(usage)
	}
	invalidIdentity := false
	flags.Visit(func(f *flag.Flag) {
		if f.Name == "identity-file" {
			invalidIdentity = !restore || *identity == ""
		}
	})
	if invalidIdentity {
		return errors.New(usage)
	}
	verification, err := install.LoadBundleVerification(*policy)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	switch command {
	case "install-agent":
		return install.InstallAgent(ctx, *config, *archive, verification, out)
	case "restore-bundle":
		return install.RestoreBundle(ctx, *config, *archive, *from, *identity, verification, install.ExecRunner{}, out)
	case "apply-bundle":
		return install.ApplyBundle(ctx, *config, *archive, verification, install.ExecRunner{}, out)
	case "registry-bundle":
		return install.ApplyRegistryBundle(ctx, *config, *archive, verification, install.ExecRunner{}, out)
	case "helm-images":
		return install.WriteHelmImageValues(ctx, *archive, *registry, verification, out)
	case "upgrade-plan":
		return install.PlanUpgrade(ctx, *config, *archive, verification, install.ExecRunner{}, out)
	case "upgrade-bundle":
		return install.UpgradeBundle(ctx, *config, *archive, *backup, verification, install.ExecRunner{}, out)
	case "upgrade-patch":
		return install.UpgradePatch(ctx, *config, *baseline, *archive, *backup, verification, install.ExecRunner{}, out)
	case "rollback-bundle":
		return install.RollbackBundle(ctx, *config, *archive, verification, install.ExecRunner{}, out)
	}
	var result bundle.Result
	if command == "prepare-bundle" {
		result, err = bundle.UnpackCurrent(ctx, verification, *archive, *output)
	} else {
		result, err = bundle.InspectCurrent(ctx, verification, *archive)
	}
	if err != nil {
		return err
	}
	if command == "bundle-notices" {
		return json.NewEncoder(out).Encode(bundleNotices(result))
	}
	return json.NewEncoder(out).Encode(result)
}
