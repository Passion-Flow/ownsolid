package prepare

import (
	"context"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	"testing"
)

func TestJournalCapacityFixedCommandRequiresExplicitProtectedScope(t *testing.T) {
	if !Supported("extend-journal-capacity") || !JournalCapacityCommand("extend-journal-capacity") || JournalCapacityCommand("extend-arbitrary-capacity") || Supported("extend-arbitrary-capacity") {
		t.Fatal("fixed capacity entrance required")
	}
	r := configuration.JournalCapacityRequest{FormatVersion: 1, ExpectedCurrentBytes: 16384, MaximumJournalBytes: 65536, Reason: "Synthetic explicit operation"}
	if _, e := RunJournalCapacity(context.Background(), configuration.Config{}, r); e == nil {
		t.Fatal("implicit scope/configuration admitted")
	}
}
