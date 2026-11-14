//go:build !darwin && (!linux || (!amd64 && !arm64))

package factory

import "os"

func prepareStreamDescriptor(_ *os.File) error { return fail("RELEASE_STREAM_PLATFORM_UNSUPPORTED") }
func discardStreamRange(_ *os.File, _, _ int64) error {
	return fail("RELEASE_STREAM_PLATFORM_UNSUPPORTED")
}
