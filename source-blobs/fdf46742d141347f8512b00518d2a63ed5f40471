package install

import (
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func assertInterruptedApplyResumes(t *testing.T, c Config, installer, config string, run func(string, ...string) (string, error)) {
	t.Helper()
	interruptAtControlStart(t)
	args := []string{"apply", "--config", config}
	out, err := run(installer, args...)
	t.Logf("interrupted apply:\n%s", out)
	var exitError *exec.ExitError
	if !errors.As(err, &exitError) {
		t.Fatalf("expected a killed installer: %v", err)
	}
	status, ok := exitError.Sys().(syscall.WaitStatus)
	if !ok || status.Signal() != syscall.SIGKILL || !strings.Contains(out, "step administrator: ok") ||
		strings.Contains(out, "INSTALL_COMPLETE") {
		t.Fatalf("installer did not stop at the expected checkpoint: %v", err)
	}
	layout := c.Layout()
	for _, filename := range []string{layout.ComposeFile, layout.ComposeEnv, c.CredentialPath()} {
		if info, err := os.Stat(filename); err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
			t.Fatalf("missing persisted installation file %s: %v", filename, err)
		}
	}
	if id, err := run("docker", "compose", "--env-file", layout.ComposeEnv, "-f", layout.ComposeFile, "ps", "--status", "running", "--quiet", "control"); err != nil || strings.TrimSpace(id) != "" {
		t.Fatalf("control must be stopped at the checkpoint: %v: %s", err, id)
	}
	credential, err := os.ReadFile(c.CredentialPath())
	if err != nil {
		t.Fatal(err)
	}
	op := newOperation(c, ExecRunner{}, io.Discard)
	schema, err := op.schemaVersion(t.Context(), databaseName)
	if err != nil || schema < 1 {
		t.Fatalf("database schema was not persisted: %d: %v", schema, err)
	}
	before := installedAdministrator(t, op)
	t.Logf("SIGKILL after bootstrap left PostgreSQL schema %d and the administrator credential committed, with control stopped", schema)

	out, err = run(installer, args...)
	t.Logf("identical apply after interruption:\n%s", out)
	if err != nil || !strings.Contains(out, "INSTALL_COMPLETE") {
		t.Fatalf("could not resume with the same command: %v", err)
	}
	if after, err := os.ReadFile(c.CredentialPath()); err != nil || !bytes.Equal(credential, after) {
		t.Fatal("resuming changed the administrator credential")
	}
	if after := installedAdministrator(t, op); before != after {
		t.Fatal("resuming changed the committed administrator, tenant or workspace")
	}
	out, err = run(installer, "status", "--config", config)
	t.Logf("status after resume:\n%s", out)
	if err != nil || !strings.Contains(out, c.Origins.Admin+": ready") || !strings.Contains(out, c.Origins.Portal+": ready") {
		t.Fatalf("both entrances must be ready after resume: %v", err)
	}
	t.Log("administrator identity, tenant, workspace and credential bytes are unchanged")
}

func installedAdministrator(t *testing.T, op *operation) string {
	t.Helper()
	identity, err := op.query(t.Context(), databaseName, `
		SELECT concat_ws('|', i.primary_admin_user_id, i.tenant_id, i.default_workspace_id, u.email_canonical,
			(SELECT count(*) FROM users WHERE is_platform_admin))
		FROM installation_state i
		JOIN users u ON u.id=i.primary_admin_user_id
		JOIN workspace_memberships m ON m.user_id=u.id AND m.tenant_id=i.tenant_id AND m.workspace_id=i.default_workspace_id
		WHERE i.id=1 AND i.bootstrap_complete AND u.is_platform_admin AND u.status='active' AND m.role='owner'`)
	fields := strings.Split(identity, "|")
	if err != nil || len(fields) != 5 || fields[0] == "" || fields[1] == "" || fields[2] == "" ||
		fields[3] != op.config.Administrator.Email || fields[4] != "1" {
		t.Fatalf("expected one committed active platform administrator with workspace ownership: %v", err)
	}
	return identity
}

func interruptAtControlStart(t *testing.T) {
	t.Helper()
	realDocker, err := exec.LookPath("docker")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.Symlink(realDocker, filepath.Join(dir, "docker-real")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "interrupt"), []byte("once\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// The installer invokes Docker directly, so this process can stop its parent
	// at the checkpoint without racing the launch of the control service.
	const wrapper = `#!/bin/sh
set -eu
directory=${0%/*}
case "$*" in
    "compose "*' up --detach --wait --no-build control')
        if [ -f "$directory/interrupt" ]; then
            unlink "$directory/interrupt"
            kill -KILL "$PPID"
            exit 125
        fi
        ;;
esac
exec "$(readlink "$directory/docker-real")" "$@"
`
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(wrapper), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}
