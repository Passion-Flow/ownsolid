package training

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"syscall"
	"testing"
)

func TestTrainingStorageDiagnosticPreservesPrimitiveCauseWithoutPrivatePathOrText(t *testing.T) {
	if value := StorageDiagnostic(storageUnavailable("/synthetic/private/credential", nil)); value.Operation != "unclassified" {
		t.Fatal("arbitrary operation retained")
	}
	for name, cause := range map[string]error{"errno": &os.PathError{Op: "open", Path: "/synthetic/private/credential", Err: syscall.EIO}, "arbitrary": errors.New("synthetic-private-token"), "deadline": context.DeadlineExceeded, "cancelled": context.Canceled} {
		t.Run(name, func(t *testing.T) {
			err := storageUnavailable("chunk_open", cause)
			if err.Error() != "TRAINING_STORAGE_UNAVAILABLE" || strings.Contains(fmt.Sprintf("%+v", err), "private") {
				t.Fatal("private diagnostic exposed")
			}
			diagnostic := StorageDiagnostic(fmt.Errorf("safe wrapper: %w", err))
			if diagnostic.Operation != "chunk_open" {
				t.Fatal("operation missing")
			}
			if name == "errno" && (diagnostic.Errno != int(syscall.EIO) || !errors.Is(err, syscall.EIO)) {
				t.Fatal("primitive errno lost")
			}
			var path *os.PathError
			if errors.As(err, &path) {
				t.Fatal("private path retained in diagnostic chain")
			}
			if name == "arbitrary" && errors.Unwrap(err) != nil {
				t.Fatal("arbitrary private cause retained")
			}
			if name == "deadline" && !errors.Is(err, context.DeadlineExceeded) || name == "cancelled" && !errors.Is(err, context.Canceled) {
				t.Fatal("cancellation classification lost")
			}
		})
	}
}
