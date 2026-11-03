//go:build repository_integration && postgres_maintenance_integration

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/postgresmaintenance"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
)

type maintenanceNativeOutput struct{ buffer bytes.Buffer }

func (w *maintenanceNativeOutput) Write(raw []byte) (int, error) {
	if len(raw) > 4096-w.buffer.Len() {
		return 0, context.Canceled
	}
	return w.buffer.Write(raw)
}

func TestActualPostgresMaintenanceNativeProtectedCheckReleasesOriginalSQLLock(t *testing.T) {
	binary, expected := os.Getenv("PROJECT_ONE_TEST_CONTROL_BINARY"), os.Getenv("PROJECT_ONE_TEST_CONTROL_BINARY_SHA256")
	raw, e := os.ReadFile(binary)
	sum := sha256.Sum256(raw)
	if e != nil || !filepath.IsAbs(binary) || len(expected) != 64 || hex.EncodeToString(sum[:]) != expected {
		t.Fatal("source-bound current native Control required")
	}
	file := os.Getenv("PROJECT_ONE_TEST_POSTGRES_MAINTENANCE_CONFIGURATION")
	c, e := config.LoadProtectedWithEnvironment(file, "")
	if e != nil || c.Server.Profile() != postgresmaintenance.ApplicationProfile {
		t.Fatal("protected current releases fixture configuration required", e)
	}
	dsn, e := config.Secret(c.Database.DSNFile)
	if e != nil {
		t.Fatal("private fixture DSN")
	}
	pc, e := pgxpool.ParseConfig(strings.TrimSpace(string(dsn)))
	if e != nil || pc.ConnConfig.Host != "127.0.0.1" || pc.ConnConfig.Port < 1024 || pc.ConnConfig.User != "projectone_operator" || !regexp.MustCompile(`^m18_maintenance_[a-f0-9]{32}_test$`).MatchString(pc.ConnConfig.Database) {
		t.Fatal("exact isolated maintenance database required")
	}
	pc.MaxConns = 2
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	pool, e := pgxpool.NewWithConfig(ctx, pc)
	if e != nil {
		t.Fatal(e)
	}
	defer pool.Close()
	scope := strings.TrimSuffix(strings.TrimPrefix(pc.ConnConfig.Database, "m18_maintenance_"), "_test")
	var marker string
	if pool.QueryRow(ctx, `SELECT pg_catalog.shobj_description((SELECT oid FROM pg_catalog.pg_database WHERE datname=pg_catalog.current_database()),'pg_database')`).Scan(&marker) != nil || marker != "projectone:m18-maintenance:"+scope+":full" {
		t.Fatal("disposable full database marker required")
	}
	fingerprint := func() string {
		t.Helper()
		var value string
		if pool.QueryRow(ctx, `SELECT jsonb_build_array((SELECT jsonb_agg(to_jsonb(v) ORDER BY v.id) FROM public.installation_state v),(SELECT jsonb_agg(to_jsonb(v) ORDER BY v.id) FROM public.tenants v),(SELECT jsonb_agg(to_jsonb(v) ORDER BY v.id) FROM public.workspaces v),(SELECT jsonb_agg(to_jsonb(v) ORDER BY v.id) FROM public.users v),(SELECT jsonb_agg(to_jsonb(v) ORDER BY v.version) FROM public.schema_migrations v))::text`).Scan(&value) != nil {
			t.Fatal("owned business/ledger fingerprint")
		}
		h := sha256.Sum256([]byte(value))
		return hex.EncodeToString(h[:])
	}
	before := fingerprint()
	invoke := func(extra ...string) ([]byte, error) {
		t.Helper()
		attempt, stop := context.WithTimeout(ctx, 15*time.Second)
		defer stop()
		args := append([]string{postgresMaintenanceCheckCommand, "--config", file}, extra...)
		cmd := exec.CommandContext(attempt, binary, args...)
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM) }
		cmd.WaitDelay = 2 * time.Second
		output := &maintenanceNativeOutput{}
		cmd.Stdout, cmd.Stderr = output, output
		e := cmd.Run()
		return bytes.Clone(output.buffer.Bytes()), e
	}
	raw, e = invoke()
	var result postgresmaintenance.Observation
	if e != nil || json.Unmarshal(raw, &result) != nil || result.Status != "historical_locked_quiescence_observation" || result.CurrentLockHeld || result.SchemaVersion != 22 || len(result.Checks) != 14 || result.TenantID == "" {
		t.Fatal("actual native locked SQL observation", result, e)
	}
	lease, e := pool.Acquire(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer lease.Release()
	var held bool
	if lease.QueryRow(ctx, "SELECT pg_catalog.pg_try_advisory_lock($1)", database.InstallationLockID).Scan(&held) != nil || !held {
		t.Fatal("native command retained original lock")
	}
	raw, e = invoke()
	if e == nil || strings.TrimSpace(string(raw)) != "INSTALLATION_PROCESS_ALREADY_RUNNING" {
		t.Fatal("busy original lock became authority", e)
	}
	var released bool
	if lease.QueryRow(ctx, "SELECT pg_catalog.pg_advisory_unlock($1)", database.InstallationLockID).Scan(&released) != nil || !released {
		t.Fatal("known owned SQL lock release")
	}
	raw, e = invoke("--reason=unused")
	if e == nil || strings.TrimSpace(string(raw)) != "POSTGRES_MAINTENANCE_ARGUMENT_INVALID" {
		t.Fatal("native unused input accepted", e)
	}
	raw, e = invoke()
	if e != nil || json.Unmarshal(raw, &result) != nil || result.CurrentLockHeld {
		t.Fatal("explicit new read-only check failed", result, e)
	}
	if fingerprint() != before {
		t.Fatal("native maintenance check changed business rows or ledger")
	}
	t.Logf("Actual native Control %s: protected releases22 SQL check, historical-only output, original-lock close/busy refusal and unchanged business/ledger. No actual runtime stop, backup or Kubernetes qualification.", expected)
}
