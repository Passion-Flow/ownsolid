package install

import (
	"context"
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestInstalledAcceptanceGuards(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, "python3", "-m", "unittest", "discover", "-s", "testdata/installed-acceptance", "-p", "*_test.py", "-v")
	command.Env = append(os.Environ(), "PYTHONDONTWRITEBYTECODE=1")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("installed acceptance guards require Python 3 and a passing process/capture suite: %v\n%s", err, output)
	}
}
