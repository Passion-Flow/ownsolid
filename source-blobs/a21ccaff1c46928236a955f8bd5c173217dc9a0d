package factory

import (
	"bytes"
	"io"
	"strconv"
	"strings"
)

// validateDeb checks the complete bounded ar envelope, without installing/executing packages.
// Package dependency satisfiability and target OS behavior require the declared lock/qualification.
func validateDeb(r io.Reader, maximum int64) error {
	magic := make([]byte, 8)
	if _, e := io.ReadFull(r, magic); e != nil || string(magic) != "!<arch>\n" {
		return fail("RELEASE_DEB_FORMAT_INVALID")
	}
	consumed := int64(8)
	for i := 0; i < 3; i++ {
		h := make([]byte, 60)
		if _, e := io.ReadFull(r, h); e != nil || string(h[58:60]) != "`\n" {
			return fail("RELEASE_DEB_MEMBER_INVALID")
		}
		consumed += 60
		name := strings.TrimSuffix(strings.TrimSpace(string(h[:16])), "/")
		size, e := strconv.ParseInt(strings.TrimSpace(string(h[48:58])), 10, 64)
		if e != nil || size < 1 || size > maximum-consumed {
			return fail("RELEASE_DEB_MEMBER_INVALID")
		}
		if i == 0 {
			if name != "debian-binary" || size != 4 {
				return fail("RELEASE_DEB_VERSION_INVALID")
			}
			value := make([]byte, 4)
			if _, e := io.ReadFull(r, value); e != nil || string(value) != "2.0\n" {
				return fail("RELEASE_DEB_VERSION_INVALID")
			}
		} else {
			prefix := "control.tar"
			if i == 2 {
				prefix = "data.tar"
			}
			var expected []byte
			switch name {
			case prefix + ".gz":
				expected = []byte{0x1f, 0x8b}
			case prefix + ".xz":
				expected = []byte{0xfd, 0x37, 0x7a, 0x58, 0x5a, 0x00}
			case prefix + ".zst":
				expected = []byte{0x28, 0xb5, 0x2f, 0xfd}
			default:
				return fail("RELEASE_DEB_COMPRESSION_UNSUPPORTED")
			}
			if size <= int64(len(expected)) {
				return fail("RELEASE_DEB_MEMBER_INVALID")
			}
			header := make([]byte, len(expected))
			if _, e := io.ReadFull(r, header); e != nil || !bytes.Equal(header, expected) {
				return fail("RELEASE_DEB_COMPRESSION_INVALID")
			}
			if _, e := io.CopyN(io.Discard, r, size-int64(len(expected))); e != nil {
				return fail("RELEASE_DEB_MEMBER_TRUNCATED")
			}
		}
		consumed += size
		if size%2 != 0 {
			padding := make([]byte, 1)
			if _, e := io.ReadFull(r, padding); e != nil || padding[0] != '\n' {
				return fail("RELEASE_DEB_PADDING_INVALID")
			}
			consumed++
		}
	}
	var trailing [1]byte
	if n, e := r.Read(trailing[:]); n != 0 || e != io.EOF || consumed != maximum {
		return fail("RELEASE_DEB_TRAILING_OR_SIZE_INVALID")
	}
	return nil
}
