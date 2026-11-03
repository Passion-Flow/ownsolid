package bundle

import (
	"context"
	"github.com/Passion-Flow/ownsolid/tools/release/offline/internal/factory"
)

type ImageProof = factory.ImageProof
type ImageProofResult = factory.ImageProofResult

func VerifyImageProof(ctx context.Context, p Verification, proof ImageProof) (ImageProofResult, error) {
	c, e := p.config()
	if e != nil {
		return ImageProofResult{}, e
	}
	return factory.VerifyImageProof(ctx, c, proof)
}
func PreparedImageProof(ctx context.Context, p Verification, directory, archiveSHA, manifestSHA, asset string) (ImageProof, ImageProofResult, error) {
	c, e := p.config()
	if e != nil {
		return ImageProof{}, ImageProofResult{}, e
	}
	return factory.PreparedImageProof(ctx, c, directory, archiveSHA, manifestSHA, asset)
}
