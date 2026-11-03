//go:build !linux

package engine

import "context"

func (c *Client) preparedReleaseChannel(context.Context) (preparedReleaseConnection, error) {
	return preparedReleaseConnection{}, failure("OFFLINE_ENGINE_PREPARED_DIRECT_NATIVE_STORAGE_UNSUPPORTED")
}
func preparedReleaseStorage(context.Context, preparedReleaseConnection, Ownership) (NativePostgreSQLStorageObservation, error) {
	return NativePostgreSQLStorageObservation{}, failure("OFFLINE_ENGINE_PREPARED_DIRECT_NATIVE_STORAGE_UNSUPPORTED")
}
func preparedReleaseCapacityNow(NativePostgreSQLStorageOwner, int64, int64) error {
	return failure("OFFLINE_ENGINE_PREPARED_DIRECT_NATIVE_STORAGE_UNSUPPORTED")
}
