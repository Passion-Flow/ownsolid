package apps

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
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
	// ExpectedPin binds the staged import to a previously verified transition.
	ExpectedPin *ReleasePin
	// RenderedCompose optionally checks the selected Compose model before loading images.
	RenderedCompose *RenderedCompose
	// ExpectedComposePlan binds the import to the installer's admitted model.
	// A supplied plan requires RenderedCompose.
	ExpectedComposePlan *ComposeImagePlan
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
	verified, err := VerifyInstallBundle(ctx, root, key, options.MaximumPayloadBytes)
	if err != nil {
		return InstallBundle{}, err
	}
	if options.ExpectedPin != nil && verified.Pin != *options.ExpectedPin {
		return InstallBundle{}, imageImportError("the signed bundle differs from the selected release pin")
	}
	expected := options.ExpectedComposePlan
	if expected != nil && options.RenderedCompose == nil {
		return InstallBundle{}, fmt.Errorf("%w: supply the rendered bytes of the admitted Compose model", ErrComposeImagePlan)
	}
	if options.RenderedCompose != nil {
		plan, err := verified.VerifyComposeImagePlan(*options.RenderedCompose)
		if err != nil {
			return InstallBundle{}, err
		}
		if expected != nil && (plan.Pin != expected.Pin || plan.ArtifactSHA256 != expected.ArtifactSHA256 ||
			plan.ModelSHA256 != expected.ModelSHA256 || !slices.Equal(plan.Services, expected.Services)) {
			return InstallBundle{}, fmt.Errorf("%w: the rendered model differs from the admitted Compose image plan", ErrComposeImagePlan)
		}
	}

	archive := verified.Archive
	var approved []bundle.ImageIdentity
	if len(archive.Images) > 1 {
		approved = make([]bundle.ImageIdentity, 0, len(archive.Images))
		for _, image := range archive.Images {
			approved = append(approved, bundle.ImageIdentity(image))
		}
	}

	// Pin and validate every selected image before the first Docker request.
	sources := make([]*bundle.ImageSource, 0, len(archive.Images))
	defer func() {
		for _, source := range sources {
			source.Close()
		}
	}()
	filename := filepath.Join(stagingDirectory, ImageArchiveName)
	remaining := options.MaximumExpandedBytes
	for _, image := range archive.Images {
		asset := imageAsset(image, archive.Bytes, archive.Digest)
		source, err := openImportSource(ctx, options.Verification, filename, asset, max(1, remaining), approved)
		if err != nil {
			return InstallBundle{}, fmt.Errorf("verify image %s: %w", image.ManifestDigest, err)
		}
		sources = append(sources, source)
		expanded := source.Inventory().ExpandedBytes
		if expanded > remaining {
			return InstallBundle{}, imageImportError("the image set exceeds the expanded byte limit")
		}
		remaining -= expanded
	}
	if err := registerImages(ctx, options.DockerSocket, archive.Images, sources); err != nil {
		return InstallBundle{}, err
	}
	return verified, nil
}

func imageAsset(image OCIImage, size int64, digest string) wire.Asset {
	return wire.Asset{
		Kind: "image_archive", Size: size, SHA256: strings.TrimPrefix(digest, "sha256:"),
		ImageID: image.ManifestDigest, ImageConfigDigest: image.ConfigDigest, Architecture: "amd64",
	}
}

func openImportSource(ctx context.Context, policy bundle.Verification, filename string, asset wire.Asset, limit int64, approved []bundle.ImageIdentity) (*bundle.ImageSource, error) {
	var source *bundle.ImageSource
	var err error
	if approved == nil {
		source, err = bundle.OpenCurrentImageSource(ctx, policy, filename, asset, limit)
	} else {
		source, err = bundle.OpenCurrentImageSelection(ctx, policy, filename, asset, approved, limit)
	}
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("%w: verify the image source and expanded layers: %w", ErrImageImport, err)
	}
	return source, nil
}

func registerImages(ctx context.Context, socket string, images []OCIImage, sources []*bundle.ImageSource) error {
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
		if err := store.load(ctx, func(output io.Writer) error {
			_, err := sources[i].WriteTagless(ctx, output)
			return err
		}); err != nil {
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
