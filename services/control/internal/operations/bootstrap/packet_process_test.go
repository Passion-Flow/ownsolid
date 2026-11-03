package bootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/databaseaccess"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
)

type packetChildInput struct {
	Configuration     config.Config
	Schema, Directory string
}

// This isolated process-loss harness is test-only, not a product fault hook.
func TestPreparedPacketAfterCommitChild(t *testing.T) {
	path := os.Getenv("PROJECT_ONE_TEST_PACKET_CHILD")
	if path == "" {
		return
	}
	raw, e := os.ReadFile(path)
	if e != nil {
		os.Exit(91)
	}
	var input packetChildInput
	if json.Unmarshal(raw, &input) != nil || !regexp.MustCompile(`^m01_[a-f0-9]{32}$`).MatchString(input.Schema) {
		os.Exit(92)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	pc, e := pgxpool.ParseConfig(os.Getenv("PROJECT_ONE_TEST_DSN"))
	if e != nil || pc.ConnConfig.Host != "127.0.0.1" || !strings.HasSuffix(pc.ConnConfig.Database, "_test") {
		os.Exit(93)
	}
	pc.ConnConfig.RuntimeParams["search_path"] = input.Schema
	pc.MaxConns = 4
	pool, e := pgxpool.NewWithConfig(ctx, pc)
	if e != nil {
		os.Exit(94)
	}
	held, e := pool.Acquire(ctx)
	if e != nil {
		os.Exit(94)
	}
	var locked bool
	if e = held.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", database.InstallationLockID).Scan(&locked); e != nil || !locked {
		os.Exit(95)
	}
	passwords, e := security.NewPasswords(filepath.Join(input.Directory, "child-blocklist"))
	if e != nil {
		os.Exit(96)
	}
	runtime, e := databaseaccess.LoadRuntime(filepath.Join(input.Directory, "runtime.yaml"))
	if e != nil {
		os.Exit(96)
	}
	s := &identity.Service{DB: pool, Config: input.Configuration, Passwords: passwords}
	status, e := RunPacket(ctx, s, held, ApplyPacketCommand, filepath.Join(input.Directory, "prepared-packet.json"), runtime)
	if e != nil || status != Verified {
		os.Exit(97)
	}
	// Deliberately lose the result after the real SQL commit and observation.
	os.Exit(98)
}

func TestActualPreparedPacketCommittedChildLossAndOriginalCredentialReconciliation(t *testing.T) {
	f := newPacketFixture(t)
	path := f.packet(t)
	before, _ := os.ReadFile(path)
	ctx := context.Background()
	input := packetChildInput{f.service.Config, f.service.DB.Config().ConnConfig.RuntimeParams["search_path"], f.root}
	raw, e := json.Marshal(input)
	if e != nil {
		t.Fatal(e)
	}
	inputPath := filepath.Join(f.root, "child-input.json")
	if os.WriteFile(inputPath, raw, 0600) != nil || os.WriteFile(filepath.Join(f.root, "child-blocklist"), []byte("passwordpassword\n"), 0600) != nil {
		t.Fatal("private child fixture")
	}
	if _, e = f.lease.Exec(ctx, "SELECT pg_advisory_unlock($1)", database.InstallationLockID); e != nil {
		t.Fatal(e)
	}
	f.lease.Release()
	f.lease = nil
	binary, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	childContext, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	child := exec.CommandContext(childContext, binary, "-test.run=^TestPreparedPacketAfterCommitChild$")
	child.Env = append(os.Environ(), "PROJECT_ONE_TEST_PACKET_CHILD="+inputPath)
	output, e := child.CombinedOutput()
	exit, ok := e.(*exec.ExitError)
	if !ok || exit.ExitCode() != 98 || len(output) != 0 {
		t.Fatal("controlled committed child did not lose its result as declared")
	}
	t.Logf("Actual prepared bootstrap child %d exited98 only after genuine SQL commit and credential observation; parent reconciles the original unchanged packet, without a new bootstrap effect.", child.Process.Pid)
	f.lease, e = f.service.DB.Acquire(ctx)
	if e != nil {
		t.Fatal(e)
	}
	var locked bool
	if e = f.lease.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", database.InstallationLockID).Scan(&locked); e != nil || !locked {
		t.Fatal("original installation lease after child exit", e)
	}
	for _, command := range []string{CheckPacketCommand, ApplyPacketCommand} {
		if status, e := RunPacket(ctx, f.service, f.lease, command, path, f.runtime); e != nil || status != Verified {
			t.Fatal("original committed packet recovery", e)
		}
	}
	if count(t, f.service, "SELECT count(*) FROM users") != 1 || count(t, f.service, "SELECT count(*) FROM audit_events WHERE action='installation.bootstrapped'") != 1 {
		t.Fatal("committed packet replayed")
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("lost-result credential replaced")
	}
}
