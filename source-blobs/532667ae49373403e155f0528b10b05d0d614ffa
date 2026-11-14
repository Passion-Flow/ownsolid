package databaseaccess

import (
	"context"
	"encoding/json"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
)

type processInput struct {
	OperatorDSNFile   string `json:"operator_dsn_file"`
	RuntimeConfigFile string `json:"runtime_config_file"`
}

func TestActualCommittedRuntimeAccessChild(t *testing.T) {
	profile := os.Getenv("PROJECT_ONE_TEST_RUNTIME_ACCESS_CHILD_PROFILE")
	if profile == "" {
		return
	}
	b, e := readPrivate(profile, maximumConfigurationBytes)
	if e != nil {
		t.Fatal(e)
	}
	var input processInput
	if json.Unmarshal(b, &input) != nil {
		t.Fatal("private process profile")
	}
	b, e = readPrivate(input.OperatorDSNFile, maximumDSNBytes)
	if e != nil {
		t.Fatal(e)
	}
	pc, e := pgxpool.ParseConfig(string(b))
	if e != nil || pc.ConnConfig.Host != "127.0.0.1" || !strings.HasSuffix(pc.ConnConfig.Database, "_test") {
		t.Fatal("isolated loopback test target required")
	}
	pc.MaxConns = 4
	pc.MinConns = 1
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	p, e := pgxpool.NewWithConfig(ctx, pc)
	if e != nil {
		t.Fatal(e)
	}
	defer p.Close()
	lease, e := p.Acquire(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer lease.Release()
	var locked bool
	if e = lease.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", database.InstallationLockID).Scan(&locked); e != nil || !locked {
		t.Fatal("child installation lock", e)
	}
	runtime, e := LoadRuntime(input.RuntimeConfigFile)
	if e != nil {
		t.Fatal(e)
	}
	if status, e := Prepare(ctx, p, lease, runtime); e != nil || status != Verified {
		t.Fatal(status, e)
	}
	// Actual process loss after the committed result, before a caller receipt.
	os.Exit(87)
}
func TestActualRuntimeAccessCommittedProcessLossReconcilesWithoutReset(t *testing.T) {
	f := newActualFixture(t)
	ctx := context.Background()
	root := t.TempDir()
	os.Chmod(root, 0700)
	cc := f.service.DB.Config().ConnConfig
	q := url.Values{"sslmode": {"disable"}}
	for k, v := range cc.RuntimeParams {
		q.Set(k, v)
	}
	u := url.URL{Scheme: "postgres", Host: net.JoinHostPort(cc.Host, strconv.Itoa(int(cc.Port))), Path: "/" + cc.Database, User: url.UserPassword(cc.User, cc.Password), RawQuery: q.Encode()}
	write := func(name string, b []byte) string {
		t.Helper()
		p := filepath.Join(root, name)
		if e := os.WriteFile(p, b, 0600); e != nil {
			t.Fatal(e)
		}
		return p
	}
	op := write("operator-dsn", []byte(u.String()))
	u.User = url.UserPassword(f.role, f.input.connection.ConnConfig.Password)
	runtimeDSN := write("runtime-dsn", []byte(u.String()))
	runtimeConfig := writeRuntimeConfiguration(t, root, runtimeDSN)
	b, e := json.Marshal(processInput{OperatorDSNFile: op, RuntimeConfigFile: runtimeConfig})
	if e != nil {
		t.Fatal(e)
	}
	profile := write("process.json", b)
	if _, e = f.lease.Exec(ctx, "SELECT pg_advisory_unlock($1)", database.InstallationLockID); e != nil {
		t.Fatal(e)
	}
	childContext, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	child := exec.CommandContext(childContext, os.Args[0], "-test.run=^TestActualCommittedRuntimeAccessChild$", "-test.timeout=40s")
	for _, value := range os.Environ() {
		if !strings.HasPrefix(value, "PROJECT_ONE_TEST_DSN=") {
			child.Env = append(child.Env, value)
		}
	}
	child.Env = append(child.Env, "PROJECT_ONE_TEST_RUNTIME_ACCESS_CHILD_PROFILE="+profile)
	output, e := child.CombinedOutput()
	exit, ok := e.(*exec.ExitError)
	if !ok || exit.ExitCode() != 87 {
		t.Fatalf("expected real committed exit87: %v %s", e, output)
	}
	for deadline := time.Now().Add(5 * time.Second); ; {
		var locked bool
		e = f.lease.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", database.InstallationLockID).Scan(&locked)
		if e == nil && locked {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("child backend did not drain", e)
		}
		time.Sleep(25 * time.Millisecond)
	}
	var before, after uint32
	var verifierBefore, verifierAfter string
	f.lease.QueryRow(ctx, "SELECT oid,rolpassword FROM pg_authid WHERE rolname=$1", f.role).Scan(&before, &verifierBefore)
	if status, e := Check(ctx, f.service.DB, f.lease, f.input); e != nil || status != Verified {
		t.Fatal("actual committed reconciliation", status, e)
	}
	if status, e := Prepare(ctx, f.service.DB, f.lease, f.input); e != nil || status != Verified {
		t.Fatal("explicit completed repeat", status, e)
	}
	f.lease.QueryRow(ctx, "SELECT oid,rolpassword FROM pg_authid WHERE rolname=$1", f.role).Scan(&after, &verifierAfter)
	if before == 0 || before != after || verifierBefore != verifierAfter {
		t.Fatal("recovery reset role or credential")
	}
	actual, e := os.ReadFile(runtimeDSN)
	if e != nil || string(actual) != u.String() {
		t.Fatal("recovery changed credential file", e)
	}
}
