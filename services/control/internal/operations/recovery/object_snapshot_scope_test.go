package recovery

import (
	"context"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
)

func TestObjectSnapshotRejectsInvalidScopeBeforeDatabaseOrFilesystem(t *testing.T) {
	accounts := &identity.Service{Config: config.Config{ObjectStorage: &config.ObjectStorageConfiguration{Enabled: true}}}
	const valid = "f9a7709b-a8b9-4fb7-83e6-e4452a184dd1"
	for _, invalid := range []string{"", "../object", "00000000-0000-0000-0000-000000000000", valid + "/child"} {
		for _, scope := range [][2]string{{invalid, valid}, {valid, invalid}} {
			err := VerifyArtifactSnapshot(context.Background(), accounts, scope[0], scope[1], "/unopened", "Synthetic snapshot scope refusal")
			if err == nil || err.Error() != "VALIDATION_FAILED" {
				t.Fatalf("invalid scope reached database or filesystem: %q %q: %v", scope[0], scope[1], err)
			}
		}
	}
}
