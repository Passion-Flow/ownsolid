package bundle

import (
	"context"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
	"github.com/Passion-Flow/ownsolid/tools/release/offline/internal/factory"
)

// CurrentProfile identifies the installable single-node Linux amd64 inventory.
const CurrentProfile = factory.CurrentProfile

// RegistryPrerequisite identifies the optional signed site registry group.
const RegistryPrerequisite = factory.RegistryPrerequisite

// RegistryImageAsset names the exact Distribution runtime archive.
const RegistryImageAsset = factory.RegistryImageAsset

// RuntimeBlob describes a selected OCI image's immutable runtime content.
type RuntimeBlob = factory.RuntimeBlob

// InspectCurrent verifies the current inventory against independent public trust.
func InspectCurrent(ctx context.Context, p Verification, source string) (Result, error) {
	c, err := p.config()
	if err != nil {
		return Result{}, err
	}
	return factory.Inspect(ctx, factory.WithCurrentProfile(c), source)
}

// UnpackCurrent verifies the complete current inventory against independent public
// trust, then checks that exact archive and each signed byte again as it unpacks.
func UnpackCurrent(ctx context.Context, p Verification, source, destination string) (Result, error) {
	c, err := p.config()
	if err != nil {
		return Result{}, err
	}
	return factory.Unpack(ctx, factory.WithCurrentProfile(c), source, destination, "", "")
}

// OpenCurrentImageSource checks a selected current image and prepares tagless import.
func OpenCurrentImageSource(ctx context.Context, p Verification, filename string, asset wire.Asset, maximumExpandedBytes int64) (*ImageSource, error) {
	c, err := p.config()
	if err != nil {
		return nil, err
	}
	return factory.OpenImageSource(ctx, factory.WithCurrentProfile(c), filename, asset, maximumExpandedBytes)
}

// ImageIdentity pairs an approved runtime manifest with its exact image config.
type ImageIdentity = factory.ImageIdentity

// OpenCurrentImageSelection opens one image from an app's flat Linux amd64 OCI
// archive. The caller supplies the independently verified archive size, digest
// and exact selected manifest/config on asset, plus every approved image pair.
// The source checks the full local descriptor closure and bounds expansion of
// the selected image. Open every required selection before importing any image.
func OpenCurrentImageSelection(ctx context.Context, p Verification, filename string, asset wire.Asset, approved []ImageIdentity, maximumExpandedBytes int64) (*ImageSource, error) {
	c, err := p.config()
	if err != nil {
		return nil, err
	}
	return factory.OpenImageSelection(ctx, factory.WithCurrentProfile(c), filename, asset, approved, maximumExpandedBytes)
}

// RebuildCurrentPatch expands a verified patch against the exact installed baseline.
func RebuildCurrentPatch(ctx context.Context, p Verification, baseline, patch, output, expectedBaseline string) (Result, error) {
	c, err := p.config()
	if err != nil {
		return Result{}, err
	}
	c.OutputFile = output
	return factory.RebuildCurrentPatch(ctx, c, baseline, patch, expectedBaseline)
}
