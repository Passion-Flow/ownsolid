package apps

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
	"github.com/Passion-Flow/ownsolid/tools/release/offline/pkg/bundle"
)

// ErrImageImport identifies a refused or unconfirmed local image import.
var ErrImageImport = errors.New("CATALOG_IMAGE_IMPORT_FAILED")

// ImageImportOptions comes from trusted installation policy. The verification
// policy supplies the factory mount and archive limits, while the publisher key
// is supplied independently when importing an app.
type ImageImportOptions struct {
	DockerSocket         string
	Verification         bundle.Verification
	MaximumPayloadBytes  int64
	MaximumExpandedBytes int64
	Timeout              time.Duration
}

// ImportInstallImage verifies a single-image release and ensures its exact
// manifest is registered in the local Docker containerd image store. The caller
// authorizes and audits the import and keeps staging private and immutable.
// Workload admission and startup belong to the installer.
func ImportInstallImage(ctx context.Context, stagingDirectory string, key ed25519.PublicKey, options ImageImportOptions) (InstallBundle, error) {
	if !filepath.IsAbs(stagingDirectory) || filepath.Clean(stagingDirectory) != stagingDirectory ||
		options.Timeout <= 0 || options.MaximumExpandedBytes <= 0 {
		return InstallBundle{}, imageImportError("configure an absolute staging directory, expansion limit and timeout")
	}
	ctx, cancel := context.WithTimeout(ctx, options.Timeout)
	defer cancel()
	root, err := os.OpenRoot(stagingDirectory)
	if err != nil {
		return InstallBundle{}, imageImportError("open the private release staging directory")
	}
	defer root.Close()
	verified, err := VerifyInstallBundle(ctx, root, key, options.MaximumPayloadBytes)
	if err != nil {
		return InstallBundle{}, err
	}
	if len(verified.Archive.Images) != 1 {
		return InstallBundle{}, imageImportError("the local importer requires a single Linux amd64 image")
	}

	image := verified.Archive.Images[0]
	asset := wire.Asset{
		Kind: "image_archive", Size: verified.Archive.Bytes,
		SHA256:  strings.TrimPrefix(verified.Archive.Digest, "sha256:"),
		ImageID: image.ManifestDigest, ImageConfigDigest: image.ConfigDigest,
		Architecture: "amd64",
	}
	source, err := bundle.OpenCurrentImageSource(ctx, options.Verification,
		filepath.Join(stagingDirectory, ImageArchiveName), asset, options.MaximumExpandedBytes)
	if err != nil {
		if ctx.Err() != nil {
			return InstallBundle{}, ctx.Err()
		}
		return InstallBundle{}, fmt.Errorf("%w: verify the image source and expanded layers: %w", ErrImageImport, err)
	}
	defer source.Close()

	store, err := newDockerImageStore(options.DockerSocket)
	if err != nil {
		return InstallBundle{}, err
	}
	defer store.client.CloseIdleConnections()
	if err := store.requireContainerd(ctx); err != nil {
		return InstallBundle{}, err
	}
	found, err := store.inspect(ctx, image.ManifestDigest)
	if err != nil {
		return InstallBundle{}, err
	}
	if found {
		return verified, nil
	}
	if err := store.load(ctx, func(output io.Writer) error {
		_, err := source.WriteTagless(ctx, output)
		return err
	}); err != nil {
		return InstallBundle{}, err
	}
	found, err = store.inspect(ctx, image.ManifestDigest)
	if err != nil {
		return InstallBundle{}, err
	}
	if !found {
		return InstallBundle{}, imageImportError("Docker did not register the signed image digest")
	}
	return verified, nil
}

func imageImportError(message string) error {
	return fmt.Errorf("%w: %s", ErrImageImport, message)
}
