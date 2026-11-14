package training

import (
	"context"
	"errors"
	"syscall"
)

// Storage failures retain only a fixed operation and a path-free primitive
// cause. The public error code never contains a file name or private diagnostic.
type storageFailure struct {
	operation string
	errno     syscall.Errno
	cause     error
}

func (e *storageFailure) Error() string { return "TRAINING_STORAGE_UNAVAILABLE" }
func (e *storageFailure) Unwrap() error { return e.cause }

type StorageFailureDiagnostic struct {
	Operation string `json:"operation"`
	Errno     int    `json:"errno"`
}

func storageUnavailable(operation string, cause error) error {
	switch operation {
	case "store_root_close", "store_lock_close", "store_closed", "reserve_invalid", "statfs", "directory_open", "directory_sync", "directory_read", "directory_create_or_stat", "json_write_or_sync", "json_close", "checked_file_open", "upload_directory_stat", "chunk_stat", "chunk_open", "chunk_sync", "chunk_close", "chunk_pending_remove":
	default:
		operation = "unclassified"
	}
	out := &storageFailure{operation: operation}
	if errors.As(cause, &out.errno) {
		out.cause = out.errno
	} else if errors.Is(cause, context.DeadlineExceeded) {
		out.cause = context.DeadlineExceeded
	} else if errors.Is(cause, context.Canceled) {
		out.cause = context.Canceled
	}
	return out
}

// StorageDiagnostic is safe for structured operator/test evidence. It cannot
// expose an os.PathError's path, arbitrary message, request or credential.
func StorageDiagnostic(err error) StorageFailureDiagnostic {
	var value *storageFailure
	if errors.As(err, &value) {
		return StorageFailureDiagnostic{Operation: value.operation, Errno: int(value.errno)}
	}
	return StorageFailureDiagnostic{Operation: "unclassified"}
}
