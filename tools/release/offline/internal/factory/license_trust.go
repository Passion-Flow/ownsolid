package factory

import (
	"context"
	"io"

	licensewire "github.com/Passion-Flow/ownsolid/pkg/license/v1"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// Customer release artifacts carry public manufacturer trust only. Validate
// before copying so an accidentally selected issuer seed never reaches an archive.
func checkLicenseTrust(ctx context.Context, input io.Reader, output io.Writer, asset wire.Asset) (string, error) {
	if asset.Size < 1 || asset.Size > licensewire.MaximumKeyBytes {
		return "", fail("RELEASE_LICENSE_TRUST_INVALID")
	}
	raw, e := io.ReadAll(io.LimitReader(checkedReader{ctx, input}, licensewire.MaximumKeyBytes+1))
	if e != nil {
		return "", fail("RELEASE_LICENSE_TRUST_READ_FAILED")
	}
	if int64(len(raw)) != asset.Size {
		return "", fail("RELEASE_LICENSE_TRUST_SIZE_INVALID")
	}
	if _, e = licensewire.ParseTrust(raw); e != nil {
		return "", fail("RELEASE_LICENSE_TRUST_INVALID")
	}
	if output != nil {
		n, e := output.Write(raw)
		if e != nil || n != len(raw) {
			return "", fail("RELEASE_LICENSE_TRUST_WRITE_FAILED")
		}
	}
	return wire.Digest(raw), nil
}
