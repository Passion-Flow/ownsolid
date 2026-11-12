package install

import (
	"os"
	"testing"
)

func TestMaterializeScheduledBackupFixture(t *testing.T) {
	path := os.Getenv("OWNSOLID_TIMER_FIXTURE_CONFIG")
	if path == "" {
		t.Fatal("isolated fixture configuration is required")
	}
	config, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := Materialize(config); err != nil {
		t.Fatal(err)
	}
}
