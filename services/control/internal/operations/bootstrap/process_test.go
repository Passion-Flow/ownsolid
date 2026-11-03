package bootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
)

// TestBootstrapChild is a test-only process harness, never a production fault hook.
func TestBootstrapChild(t *testing.T) {
	phase := os.Getenv("PROJECT_ONE_TEST_BOOTSTRAP_CHILD")
	if phase == "" {
		return
	}
	if phase != "before_commit" && phase != "after_commit" {
		os.Exit(91)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pc, e := pgxpool.ParseConfig(os.Getenv("PROJECT_ONE_TEST_DSN"))
	if e != nil {
		os.Exit(92)
	}
	if pc.ConnConfig.Host != "127.0.0.1" || len(pc.ConnConfig.Database) < 5 || pc.ConnConfig.Database[len(pc.ConnConfig.Database)-5:] != "_test" {
		os.Exit(93)
	}
	pc.ConnConfig.RuntimeParams["search_path"] = os.Getenv("PROJECT_ONE_TEST_BOOTSTRAP_SCHEMA")
	pc.ConnConfig.RuntimeParams["application_name"] = "projectone-bootstrap-fault-" + os.Getenv("PROJECT_ONE_TEST_BOOTSTRAP_SCHEMA")
	pc.ConnConfig.RuntimeParams["statement_timeout"] = "15000"
	pc.ConnConfig.RuntimeParams["lock_timeout"] = "10000"
	pc.MaxConns = 4
	db, e := pgxpool.NewWithConfig(ctx, pc)
	if e != nil {
		os.Exit(94)
	}
	defer db.Close()
	c, e := db.Acquire(ctx)
	if e != nil {
		os.Exit(94)
	}
	defer c.Release()
	var acquired bool
	if e = c.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", database.InstallationLockID).Scan(&acquired); e != nil || !acquired {
		os.Exit(95)
	}
	defer c.Exec(context.Background(), "SELECT pg_advisory_unlock($1)", database.InstallationLockID)
	dir := os.Getenv("PROJECT_ONE_TEST_BOOTSTRAP_DIRECTORY")
	var configuration config.Config
	b, re := os.ReadFile(filepath.Join(dir, "configuration.json"))
	if re != nil || json.Unmarshal(b, &configuration) != nil {
		os.Exit(96)
	}
	passwords, e := security.NewPasswords(filepath.Join(dir, "blocklist"))
	if e != nil {
		os.Exit(96)
	}
	s := &identity.Service{DB: db, Config: configuration, Passwords: passwords}
	status, e := Run(ctx, s, c, "bootstrap-admin-file", filepath.Join(dir, "request.json"), filepath.Join(dir, "credential.json"))
	if e != nil {
		fmt.Fprintln(os.Stderr, e.Error())
		os.Exit(97)
	}
	if phase == "after_commit" && status == Verified {
		os.Exit(98)
	}
	os.Exit(99)
}

func TestActualBootstrapProcessLossBeforeAndAfterCommit(t *testing.T) {
	if os.Getenv("PROJECT_ONE_TEST_BOOTSTRAP_CHILD") != "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	evidence := map[string]any{"scope": "Real child process/SQL bootstrap loss recovery; no product Compose install or native target qualification"}
	for _, phase := range []string{"before_commit", "after_commit"} {
		t.Run(phase, func(t *testing.T) {
			s := identityfixture.New(t)
			input, output := paths(t)
			dir := filepath.Dir(input)
			b, _ := json.Marshal(s.Config)
			if e := os.WriteFile(filepath.Join(dir, "configuration.json"), b, 0600); e != nil {
				t.Fatal(e)
			}
			if e := os.WriteFile(filepath.Join(dir, "blocklist"), []byte("passwordpassword\n"), 0600); e != nil {
				t.Fatal(e)
			}
			var schema string
			if e := s.DB.QueryRow(ctx, "SELECT current_schema()").Scan(&schema); e != nil {
				t.Fatal(e)
			}
			var unblock func()
			if phase == "before_commit" {
				tx, e := s.DB.Begin(ctx)
				if e != nil {
					t.Fatal(e)
				}
				if _, e = tx.Exec(ctx, "SELECT id FROM installation_state WHERE id=1 FOR UPDATE"); e != nil {
					t.Fatal(e)
				}
				unblock = func() { tx.Rollback(context.Background()) }
				defer unblock()
			}
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestBootstrapChild$", "-test.timeout=25s")
			cmd.Env = append(os.Environ(), "PROJECT_ONE_TEST_BOOTSTRAP_CHILD="+phase, "PROJECT_ONE_TEST_BOOTSTRAP_SCHEMA="+schema, "PROJECT_ONE_TEST_BOOTSTRAP_DIRECTORY="+dir)
			cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
			var diagnostics bytes.Buffer
			cmd.Stdout = &diagnostics
			cmd.Stderr = &diagnostics
			if e := cmd.Start(); e != nil {
				t.Fatal(e)
			}
			wait := make(chan error, 1)
			go func() { wait <- cmd.Wait() }()
			reaped := false
			defer func() {
				if !reaped {
					syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
					<-wait
				}
			}()
			var childError error
			if phase == "before_commit" {
				deadline := time.NewTimer(10 * time.Second)
				defer deadline.Stop()
				ticker := time.NewTicker(10 * time.Millisecond)
				defer ticker.Stop()
				found := false
				for !found {
					select {
					case e := <-wait:
						reaped = true
						t.Fatalf("child exited before durable intent: %v %s", e, diagnostics.String())
					case <-deadline.C:
						syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
						childError = <-wait
						reaped = true
						t.Fatal("intent timeout", childError)
					case <-ticker.C:
						var intent Intent
						_, e := readPrivate(output, &intent)
						var waiting bool
						qe := s.DB.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE application_name=$1 AND wait_event_type='Lock')", "projectone-bootstrap-fault-"+schema).Scan(&waiting)
						found = e == nil && qe == nil && waiting
					}
				}
				if e := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); e != nil {
					t.Fatal(e)
				}
				childError = <-wait
				reaped = true
				if childError == nil || !cmd.ProcessState.Sys().(syscall.WaitStatus).Signaled() {
					t.Fatal("expected real interrupted child", childError)
				}
				unblock()
			} else {
				childError = <-wait
				reaped = true
				var exit *exec.ExitError
				if !errors.As(childError, &exit) || exit.ExitCode() != 98 {
					t.Fatalf("expected committed lost response98: %v %s", childError, diagnostics.String())
				}
			}
			original := mustRead(t, output)
			intent := retained(t, output)
			// Session termination must release its actual PostgreSQL installation lock.
			for deadline := time.Now().Add(3 * time.Second); ; {
				var active bool
				if e := s.DB.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE application_name=$1)", "projectone-bootstrap-fault-"+schema).Scan(&active); e != nil {
					t.Fatal(e)
				}
				if !active {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("child backend did not terminate")
				}
				time.Sleep(10 * time.Millisecond)
			}
			c := lease(t, s)
			status, e := Run(ctx, s, c, "reconcile-bootstrap-file", input, output)
			if e != nil {
				t.Fatal(e)
			}
			expected := Verified
			if phase == "before_commit" {
				expected = NotCommitted
			}
			if status != expected {
				t.Fatal(status, expected)
			}
			status, e = Run(ctx, s, c, "resume-bootstrap-file", input, output)
			if e != nil || status != Verified {
				t.Fatal(status, e)
			}
			if !bytes.Equal(original, mustRead(t, output)) || retained(t, output).Password != intent.Password || count(t, s, "SELECT count(*) FROM users") != 1 || count(t, s, "SELECT count(*) FROM audit_events WHERE action='installation.bootstrapped'") != 1 {
				t.Fatal("duplicate mutation or changed credential")
			}
			identityfixture.Login(t, s, intent.Request.AdministratorEmail, intent.Password, "admin")
			evidence[phase] = map[string]any{"child_pid": cmd.Process.Pid, "child_exit": cmd.ProcessState.String(), "first_reconciliation": expected, "resume": status, "credential_file_sha256": digest(original), "one_admin_and_one_bootstrap_audit": true, "generated_credential_authenticates": true}
		})
	}
	if path := os.Getenv("PROJECT_ONE_TEST_BOOTSTRAP_EVIDENCE"); path != "" && !t.Failed() {
		b, _ := json.MarshalIndent(evidence, "", "  ")
		if e := os.WriteFile(path, append(b, '\n'), 0600); e != nil {
			t.Fatal(e)
		}
	}
}
