//go:build !linux

package engine

import "context"

func nativePostgreSQLDurableDirectory(string) (NativePostgreSQLStorageFilesystem, error) {
	return NativePostgreSQLStorageFilesystem{}, failure("OFFLINE_ENGINE_NATIVE_STORAGE_DIRECT_LINUX_REQUIRED")
}

func (c *Client) observeDirectNativePostgreSQLStorage(context.Context, DatabaseTask) (NativePostgreSQLStorageObservation, error) {
	return NativePostgreSQLStorageObservation{}, failure("OFFLINE_ENGINE_NATIVE_STORAGE_DIRECT_LINUX_REQUIRED")
}
