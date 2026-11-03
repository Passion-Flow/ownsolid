package postgresbackup

import (
	"bytes"
	"context"
	"debug/elf"
	"debug/macho"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	release "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// No shell, inherited environment, command substitution or caller-supplied
// argv. Callers select only the fixed operations below. Wait always joins the
// native process before the installation lock can be released.
func runNative(ctx context.Context, p release.PostgreSQLBackupPolicy, file string, args, environment []string, input io.Reader, output io.Writer) error {
	if ctx == nil || ctx.Err() != nil || !p.Valid() {
		return errors.New("POSTGRES_BACKUP_NATIVE_CONTEXT_INVALID")
	}
	cmd := exec.Command(file, args...)
	cmd.Dir = p.ScratchRoot
	cmd.Env = append([]string{"LC_ALL=C", "TZ=UTC", "TMPDIR=" + p.ScratchRoot}, environment...)
	cmd.Stdin = input
	cmd.Stdout = output
	diagnostic := &limitedBuffer{maximum: p.MaximumManifestBytes}
	cmd.Stderr = diagnostic
	// The fixed archive reader does not fork and stays in the parent Control
	// group. A supervising Control cancellation therefore reaches both levels.
	group := file != p.RestoreFile
	if group {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	}
	if cmd.Start() != nil {
		return errors.New("POSTGRES_BACKUP_NATIVE_START_FAILED")
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case e := <-done:
		if e != nil || cmd.ProcessState == nil || !cmd.ProcessState.Exited() || cmd.ProcessState.ExitCode() != 0 {
			return errors.New("POSTGRES_BACKUP_NATIVE_FAILED")
		}
		if ctx.Err() != nil {
			return errors.New("POSTGRES_BACKUP_INTERRUPTED")
		}
		return nil
	case <-ctx.Done():
		if group {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		} else {
			_ = cmd.Process.Signal(syscall.SIGTERM)
		}
		grace := p.TerminationGraceSeconds
		if file == p.ControlFile {
			grace = 2*grace + 5
		}
		t := time.NewTimer(time.Duration(grace) * time.Second)
		defer t.Stop()
		select {
		case <-done:
		case <-t.C:
			if group {
				_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			} else {
				_ = cmd.Process.Kill()
			}
			<-done
		}
		return errors.New("POSTGRES_BACKUP_INTERRUPTED")
	}
}

type limitedBuffer struct {
	bytes.Buffer
	maximum int64
}

func (b *limitedBuffer) Write(v []byte) (int, error) {
	if int64(len(v)) > b.maximum-int64(b.Len()) {
		return 0, errors.New("POSTGRES_BACKUP_NATIVE_OUTPUT_LIMIT_REACHED")
	}
	return b.Buffer.Write(v)
}

func executablePlatform(f *os.File) error {
	bad := errors.New("POSTGRES_BACKUP_NATIVE_PLATFORM_INVALID")
	if runtime.GOARCH != "arm64" && runtime.GOARCH != "amd64" {
		return bad
	}
	switch runtime.GOOS {
	case "linux":
		e, e2 := elf.NewFile(f)
		machine := elf.EM_AARCH64
		if runtime.GOARCH == "amd64" {
			machine = elf.EM_X86_64
		}
		if e2 != nil || e.Class != elf.ELFCLASS64 || e.Machine != machine || e.Type != elf.ET_EXEC && e.Type != elf.ET_DYN {
			return bad
		}
	case "darwin":
		cpu := macho.CpuArm64
		if runtime.GOARCH == "amd64" {
			cpu = macho.CpuAmd64
		}
		m, e := macho.NewFile(f)
		if e != nil || m.Cpu != cpu || m.Type != macho.TypeExec {
			return bad
		}
	default:
		return bad
	}
	return nil
}

func trustedTool(ctx context.Context, path, expected string) error {
	bad := errors.New("POSTGRES_BACKUP_NATIVE_ARTIFACT_CHANGED")
	if !release.PostgreSQLBackupPath(path) || !release.ValidSHA256(expected) {
		return bad
	}
	resolved, e := filepath.EvalSymlinks(path)
	if e != nil || resolved != path {
		return bad
	}
	before, e := os.Lstat(path)
	if e != nil || !before.Mode().IsRegular() || before.Mode().Perm()&0022 != 0 || before.Mode().Perm()&0111 == 0 {
		return bad
	}
	s, ok := before.Sys().(*syscall.Stat_t)
	if !ok || s.Nlink != 1 || s.Uid != 0 && s.Uid != uint32(os.Geteuid()) {
		return bad
	}
	f, e := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if e != nil {
		return bad
	}
	defer f.Close()
	opened, e := f.Stat()
	if e != nil || !os.SameFile(before, opened) {
		return bad
	}
	if executablePlatform(f) != nil {
		return bad
	}
	if _, e = f.Seek(0, io.SeekStart); e != nil {
		return bad
	}
	sha, n, e := hashStream(ctx, nil, f, before.Size())
	after, ae := f.Stat()
	named, ne := os.Lstat(path)
	if e != nil || ae != nil || ne != nil || sha != expected || n != before.Size() || !os.SameFile(before, after) || !os.SameFile(before, named) || after.Size() != before.Size() || named.Size() != before.Size() || before.Mode() != after.Mode() || before.Mode() != named.Mode() || !before.ModTime().Equal(after.ModTime()) || !before.ModTime().Equal(named.ModTime()) {
		return bad
	}
	return nil
}

func verifyTools(ctx context.Context, p release.PostgreSQLBackupPolicy, readerOnly bool) error {
	if !p.Valid() {
		return errors.New("POSTGRES_BACKUP_POLICY_INVALID")
	}
	tools := []struct{ path, sha, name string }{{p.RestoreFile, p.RestoreSHA256, "pg_restore"}}
	if !readerOnly {
		tools = append(tools, struct{ path, sha, name string }{p.DumpFile, p.DumpSHA256, "pg_dump"})
	}
	for _, tool := range tools {
		if e := trustedTool(ctx, tool.path, tool.sha); e != nil {
			return e
		}
		out := &limitedBuffer{maximum: p.MaximumManifestBytes}
		if e := runNative(ctx, p, tool.path, []string{"--version"}, nil, nil, out); e != nil {
			return e
		}
		if strings.TrimSpace(out.String()) != tool.name+" (PostgreSQL) "+p.NativeClientVersion() {
			return errors.New("POSTGRES_BACKUP_NATIVE_VERSION_INVALID")
		}
		if e := trustedTool(ctx, tool.path, tool.sha); e != nil {
			return e
		}
	}
	return nil
}
