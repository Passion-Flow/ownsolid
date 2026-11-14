package factory

import (
	"archive/tar"
	"strconv"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// USTAR's size field admits eleven octal digits. This format boundary is not a
// deployment asset limit: the typed manifest/configuration keeps owning bounds.
const maximumUSTARFileBytes int64 = 1<<33 - 1

func releaseArchiveHeader(name string, size int64) *tar.Header {
	format := tar.FormatUSTAR
	if size > maximumUSTARFileBytes {
		// PAX supplies the exact decimal size through a standard extended
		// header. Existing small-asset archives retain their USTAR bytes.
		format = tar.FormatPAX
	}
	return &tar.Header{Name: name, Mode: 0644, Size: size, Typeflag: tar.TypeReg, Format: format}
}

func safeAssetHeader(h *tar.Header, asset wire.Asset) bool {
	if h == nil || !wire.SafePath(asset.Path) || h.Name != asset.Path || h.Size != asset.Size || h.Size < 1 {
		return false
	}
	if safeHeader(h) {
		return true
	}
	// Only the size extension emitted for a necessary large regular asset is
	// admitted. Metadata keeps using the original strict safeHeader predicate.
	// In particular, no path/link/owner/time/sparse/xattr override is accepted.
	return h.Typeflag == tar.TypeReg && h.Mode == 0644 && h.Uid == 0 && h.Gid == 0 && h.Linkname == "" && len(h.Xattrs) == 0 && h.Format == tar.FormatPAX && h.Size > maximumUSTARFileBytes && len(h.PAXRecords) == 1 && h.PAXRecords["size"] == strconv.FormatInt(h.Size, 10)
}
