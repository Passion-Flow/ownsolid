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

// ImportInstallImage verifies the signed release and confirms every exact
// manifest in the local Docker containerd image store. A failed call may leave
// verified images registered for reuse on retry. The caller authorizes and
// audits the import and keeps staging private and immutable. Workload admission
// and startup belong to the installer.
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
	verified, archive, err := verifyBundle(ctx, root, key, options.MaximumPayloadBytes, true)
	if err != nil {
		return InstallBundle{}, err
	}
	defer archive.file.Close()
	if len(archive.members) == 1 {
		err = importSingleImage(ctx, stagingDirectory, verified.Archive, options)
	} else {
		err = importImageSet(ctx, stagingDirectory, archive, options)
	}
	if err != nil {
		return InstallBundle{}, err
	}
	return verified, nil
}

func importSingleImage(ctx context.Context, stagingDirectory string, archive OCIArchive, options ImageImportOptions) error {
	source, err := openImportSource(ctx, options.Verification, filepath.Join(stagingDirectory, ImageArchiveName),
		imageAsset(archive.Images[0], archive.Bytes, archive.Digest), options.MaximumExpandedBytes)
	if err != nil {
		return err
	}
	defer source.Close()
	return registerImages(ctx, options.DockerSocket, archive.Images, func(store dockerImageStore, _ int) error {
		return loadTagless(ctx, store, source)
	})
}

func importImageSet(ctx context.Context, stagingDirectory string, archive *verifiedOCIArchive, options ImageImportOptions) error {
	if err := checkProjectionStaging(stagingDirectory, archive, options.Verification); err != nil {
		return err
	}
	expanded := make([]int64, len(archive.members))
	remaining := options.MaximumExpandedBytes
	for i, member := range archive.members {
		err := archive.withProjectedImage(ctx, member, options.Verification, max(1, remaining), func(source *bundle.ImageSource) error {
			expanded[i] = source.Inventory().ExpandedBytes
			if expanded[i] > remaining {
				return imageImportError("the image set exceeds the expanded byte limit")
			}
			remaining -= expanded[i]
			return nil
		})
		if err != nil {
			return fmt.Errorf("verify image %s: %w", member.image.ManifestDigest, err)
		}
	}
	return registerImages(ctx, options.DockerSocket, archive.archive.Images, func(store dockerImageStore, i int) error {
		return archive.withProjectedImage(ctx, archive.members[i], options.Verification, max(1, expanded[i]), func(source *bundle.ImageSource) error {
			return loadTagless(ctx, store, source)
		})
	})
}

func imageAsset(image OCIImage, size int64, digest string) wire.Asset {
	return wire.Asset{
		Kind: "image_archive", Size: size, SHA256: strings.TrimPrefix(digest, "sha256:"),
		ImageID: image.ManifestDigest, ImageConfigDigest: image.ConfigDigest, Architecture: "amd64",
	}
}

func openImportSource(ctx context.Context, policy bundle.Verification, filename string, asset wire.Asset, limit int64) (*bundle.ImageSource, error) {
	source, err := bundle.OpenCurrentImageSource(ctx, policy, filename, asset, limit)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("%w: verify the image source and expanded layers: %w", ErrImageImport, err)
	}
	return source, nil
}

func loadTagless(ctx context.Context, store dockerImageStore, source *bundle.ImageSource) error {
	return store.load(ctx, func(output io.Writer) error {
		_, err := source.WriteTagless(ctx, output)
		return err
	})
}

func registerImages(ctx context.Context, socket string, images []OCIImage, load func(dockerImageStore, int) error) error {
	store, err := newDockerImageStore(socket)
	if err != nil {
		return err
	}
	defer store.client.CloseIdleConnections()
	if err := store.requireContainerd(ctx); err != nil {
		return err
	}
	for i, image := range images {
		found, err := store.inspect(ctx, image.ManifestDigest)
		if err != nil {
			return fmt.Errorf("inspect image %s: %w", image.ManifestDigest, err)
		}
		if found {
			if len(images) == 1 {
				return nil
			}
			continue
		}
		if err := load(store, i); err != nil {
			return fmt.Errorf("load image %s: %w", image.ManifestDigest, err)
		}
	}
	for _, image := range images {
		found, err := store.inspect(ctx, image.ManifestDigest)
		if err != nil {
			return fmt.Errorf("confirm image %s: %w", image.ManifestDigest, err)
		}
		if !found {
			return fmt.Errorf("%w: Docker did not register signed image %s", ErrImageImport, image.ManifestDigest)
		}
	}
	return nil
}

func imageImportError(message string) error {
	return fmt.Errorf("%w: %s", ErrImageImport, message)
}
