package bundle

import (
	"context"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
	"github.com/Passion-Flow/ownsolid/tools/release/offline/internal/factory"
)

// CurrentProfile identifies the installable single-node Linux amd64 inventory.
const CurrentProfile = factory.CurrentProfile

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

// RebuildCurrentPatch expands a verified patch against the exact installed baseline.
func RebuildCurrentPatch(ctx context.Context, p Verification, baseline, patch, output, expectedBaseline string) (Result, error) {
	c, err := p.config()
	if err != nil {
		return Result{}, err
	}
	c.OutputFile = output
	return factory.RebuildCurrentPatch(ctx, c, baseline, patch, expectedBaseline)
}
