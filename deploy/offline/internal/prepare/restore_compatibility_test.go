package prepare

import (
	"context"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	"testing"
)

func TestCompatibleRestoreRequiresExplicitOperation(t *testing.T) {
	if !Supported("inspect-compatible-restore-target") || Supported("inspect-arbitrary-compatible-target") {
		t.Fatal("command selection changed")
	}
	if _, e := Run(context.Background(), "inspect-compatible-restore-target", configuration.Config{}); e == nil || e.Error() != "OFFLINE_OPERATOR_RESTORE_COMPATIBILITY_OPERATION_REQUIRED" {
		t.Fatal("operation-free compatible restore accepted", e)
	}
}
