package mysqlbackup

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

	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/mysqloperator"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/mysqlstore"
)

type NativeArtifact struct {
	Kind            string `json:"kind"`
	File            string `json:"file"`
	SHA256          string `json:"sha256"`
	LicenseSPDX     string `json:"license_spdx"`
	LicenseFile     string `json:"license_file"`
	LicenseSHA256   string `json:"license_sha256"`
	ExceptionFile   string `json:"exception_file,omitempty"`
	ExceptionSHA256 string `json:"exception_sha256,omitempty"`
}
type NativeArtifactManifest struct {
	Format    string           `json:"format"`
	Version   string           `json:"version"`
	GOOS      string           `json:"goos"`
	GOARCH    string           `json:"goarch"`
	Artifacts []NativeArtifact `json:"artifacts"`
}
type Native struct {
	policy         config.MySQLBackupConfiguration
	settings       mysqlstore.Configuration
	manifestSHA256 string
}

func executablePlatform(file *os.File) error {
	if runtime.GOARCH != "arm64" && runtime.GOARCH != "amd64" {
		return errors.New("MYSQL_NATIVE_HOST_PLATFORM_UNSUPPORTED")
	}
	switch runtime.GOOS {
	case "linux":
		f, e := elf.NewFile(file)
		if e != nil {
			return errors.New("MYSQL_NATIVE_HOST_BINARY_REQUIRED")
		}
		machine := elf.EM_AARCH64
		if runtime.GOARCH == "amd64" {
			machine = elf.EM_X86_64
		}
		if f.Class != elf.ELFCLASS64 || f.Machine != machine || f.Type != elf.ET_EXEC && f.Type != elf.ET_DYN {
			return errors.New("MYSQL_NATIVE_HOST_BINARY_REQUIRED")
		}
	case "darwin":
		cpu := macho.CpuArm64
		if runtime.GOARCH == "amd64" {
			cpu = macho.CpuAmd64
		}
		f, e := macho.NewFile(file)
		if e == nil {
			if f.Cpu == cpu && f.Type == macho.TypeExec {
				return nil
			}
			return errors.New("MYSQL_NATIVE_HOST_BINARY_REQUIRED")
		}
		fat, e := macho.NewFatFile(file)
		if e != nil {
			return errors.New("MYSQL_NATIVE_HOST_BINARY_REQUIRED")
		}
		for _, arch := range fat.Arches {
			if arch.Cpu == cpu && arch.File.Type == macho.TypeExec {
				return nil
			}
		}
		return errors.New("MYSQL_NATIVE_HOST_BINARY_REQUIRED")
	default:
		return errors.New("MYSQL_NATIVE_HOST_PLATFORM_UNSUPPORTED")
	}
	return nil
}

func trustedFile(ctx context.Context, path, expected string, executable bool) error {
	resolved, e := filepath.EvalSymlinks(path)
	if e != nil || resolved != path || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return errors.New("MYSQL_NATIVE_ARTIFACT_PATH_INVALID")
	}
	before, e := os.Lstat(path)
	if e != nil || !before.Mode().IsRegular() || before.Mode().Perm()&0022 != 0 || executable && before.Mode().Perm()&0111 == 0 {
		return errors.New("MYSQL_NATIVE_ARTIFACT_PATH_INVALID")
	}
	owner, ok := before.Sys().(*syscall.Stat_t)
	if !ok || owner.Nlink != 1 || owner.Uid != 0 && owner.Uid != uint32(os.Geteuid()) {
		return errors.New("MYSQL_NATIVE_ARTIFACT_PATH_INVALID")
	}
	f, e := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if e != nil {
		return errors.New("MYSQL_NATIVE_ARTIFACT_PATH_INVALID")
	}
	defer f.Close()
	opened, e := f.Stat()
	if e != nil || !os.SameFile(before, opened) {
		return errors.New("MYSQL_NATIVE_ARTIFACT_CHANGED")
	}
	if executable {
		if e = executablePlatform(f); e != nil {
			return e
		}
	}
	if _, e = f.Seek(0, io.SeekStart); e != nil {
		return errors.New("MYSQL_NATIVE_ARTIFACT_CHANGED")
	}
	digest, size, e := hashStream(ctx, nil, f, before.Size())
	after, ae := f.Stat()
	current, ce := os.Lstat(path)
	if e != nil || ae != nil || ce != nil || size != before.Size() || digest != expected || !os.SameFile(before, after) || !os.SameFile(before, current) || after.Size() != before.Size() || current.Size() != before.Size() || after.Mode() != before.Mode() || current.Mode() != before.Mode() || !after.ModTime().Equal(before.ModTime()) || !current.ModTime().Equal(before.ModTime()) {
		return errors.New("MYSQL_NATIVE_ARTIFACT_CHANGED")
	}
	return nil
}

func (n *Native) verify(ctx context.Context) error {
	if n == nil || n.policy.Validate() != nil || n.settings.Validate() != nil || n.settings.Host != n.settings.ServerName {
		return errors.New("MYSQL_NATIVE_CONFIGURATION_REQUIRED")
	}
	var manifest NativeArtifactManifest
	raw, e := readPrivateDocument(n.policy.ArtifactManifestFile, n.policy.MaximumManifestBytes, &manifest)
	if e != nil || mysqloperator.SHA256(raw) != n.manifestSHA256 || manifest.Format != "projectone.mysql.native-artifacts.v1" || manifest.Version != n.settings.ExpectedVersion || manifest.GOOS != runtime.GOOS || manifest.GOARCH != runtime.GOARCH || len(manifest.Artifacts) != 2 {
		return errors.New("MYSQL_NATIVE_ARTIFACT_MANIFEST_INVALID")
	}
	seen := map[string]bool{}
	for _, artifact := range manifest.Artifacts {
		file, digest := n.policy.ClientFile, n.policy.ClientSHA256
		if artifact.Kind == "mysqldump" {
			file, digest = n.policy.DumpFile, n.policy.DumpSHA256
		} else if artifact.Kind != "mysql" {
			return errors.New("MYSQL_NATIVE_ARTIFACT_MANIFEST_INVALID")
		}
		if seen[artifact.Kind] || artifact.File != file || artifact.SHA256 != digest || !digestPattern.MatchString(artifact.LicenseSHA256) || artifact.LicenseSPDX != "GPL-2.0-only" && artifact.LicenseSPDX != "GPL-2.0-only WITH Universal-FOSS-exception-1.0" {
			return errors.New("MYSQL_NATIVE_ARTIFACT_MANIFEST_INVALID")
		}
		seen[artifact.Kind] = true
		if e = trustedFile(ctx, artifact.File, artifact.SHA256, true); e != nil {
			return e
		}
		if e = trustedFile(ctx, artifact.LicenseFile, artifact.LicenseSHA256, false); e != nil {
			return e
		}
		if artifact.LicenseSPDX == "GPL-2.0-only WITH Universal-FOSS-exception-1.0" {
			if !digestPattern.MatchString(artifact.ExceptionSHA256) {
				return errors.New("MYSQL_NATIVE_ARTIFACT_MANIFEST_INVALID")
			}
			if e = trustedFile(ctx, artifact.ExceptionFile, artifact.ExceptionSHA256, false); e != nil {
				return e
			}
		} else if artifact.ExceptionFile != "" || artifact.ExceptionSHA256 != "" {
			return errors.New("MYSQL_NATIVE_ARTIFACT_MANIFEST_INVALID")
		}
	}
	return nil
}

// runNative starts no shell, inherits no ambient environment, and never returns
// before Wait has reaped the real process. The installation lease must outlive
// this call, including cancellation/termination; timeout is not cleanup proof.

type NativeExecution struct {
	File        string    `json:"file"`
	Argv        []string  `json:"argv"`
	GOOS        string    `json:"goos"`
	GOARCH      string    `json:"goarch"`
	PID         int       `json:"pid"`
	StartedAt   time.Time `json:"started_at"`
	CompletedAt time.Time `json:"completed_at"`
	ExitCode    int       `json:"exit_code"`
	Joined      bool      `json:"joined"`
}

func runNative(ctx context.Context, file string, args []string, scratch string, grace time.Duration, input io.Reader, output, diagnostic io.Writer) error {
	_, e := runNativeMeasured(ctx, file, args, scratch, grace, input, output, diagnostic)
	return e
}
func runNativeMeasured(ctx context.Context, file string, args []string, scratch string, grace time.Duration, input io.Reader, output, diagnostic io.Writer) (NativeExecution, error) {
	if ctx == nil || ctx.Err() != nil || grace <= 0 {
		return NativeExecution{}, errors.New("MYSQL_NATIVE_PROCESS_CONTEXT_INVALID")
	}
	cmd := exec.Command(file, args...)
	cmd.Env = []string{"LC_ALL=C", "TZ=UTC", "TMPDIR=" + scratch}
	cmd.Dir = scratch
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = input, output, diagnostic
	started := time.Now().UTC()
	if e := cmd.Start(); e != nil {
		return NativeExecution{}, errors.New("MYSQL_NATIVE_PROCESS_START_FAILED")
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case e := <-done:
		if e != nil {
			return NativeExecution{}, errors.New("MYSQL_NATIVE_PROCESS_FAILED")
		}
		if e = ctx.Err(); e != nil {
			return NativeExecution{}, e
		}
		if cmd.ProcessState == nil || !cmd.ProcessState.Exited() || cmd.ProcessState.ExitCode() != 0 {
			return NativeExecution{}, errors.New("MYSQL_NATIVE_PROCESS_FAILED")
		}
		return NativeExecution{file, append([]string{}, args...), runtime.GOOS, runtime.GOARCH, cmd.Process.Pid, started, time.Now().UTC(), cmd.ProcessState.ExitCode(), true}, nil
	case <-ctx.Done():
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		timer := time.NewTimer(grace)
		defer timer.Stop()
		select {
		case <-done:
		case <-timer.C:
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			<-done
		}
		return NativeExecution{}, ctx.Err()
	}
}

type cappedBuffer struct {
	value   bytes.Buffer
	maximum int64
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	if int64(len(p))+int64(b.value.Len()) > b.maximum {
		return 0, errors.New("MYSQL_NATIVE_DIAGNOSTIC_LIMIT_REACHED")
	}
	return b.value.Write(p)
}

func nativeVersionValid(text, kind, version string) bool {
	if strings.ContainsAny(text, "\r\n") {
		return false
	}
	fields := strings.Fields(text)
	return len(fields) >= 8 && filepath.Base(fields[0]) == kind && fields[1] == "Ver" && fields[2] == version && fields[3] == "for" && fields[5] == "on" && strings.HasPrefix(fields[7], "(") && strings.HasSuffix(text, ")")
}

func NewNative(ctx context.Context, policy config.MySQLBackupConfiguration, settings mysqlstore.Configuration) (*Native, error) {
	if policy.Validate() != nil || settings.Validate() != nil || settings.Host != settings.ServerName {
		return nil, errors.New("MYSQL_NATIVE_CONFIGURATION_REQUIRED")
	}
	var manifest NativeArtifactManifest
	raw, e := readPrivateDocument(policy.ArtifactManifestFile, policy.MaximumManifestBytes, &manifest)
	if e != nil {
		return nil, e
	}
	n := &Native{policy: policy, settings: settings, manifestSHA256: mysqloperator.SHA256(raw)}
	if e = n.verify(ctx); e != nil {
		return nil, e
	}
	root, e := openPrivateRoot(policy.ScratchRoot)
	if e != nil {
		return nil, e
	}
	if e = root.Close(); e != nil {
		return nil, errors.New("MYSQL_NATIVE_SCRATCH_CLOSE_FAILED")
	}
	for _, tool := range []struct{ file, name string }{{policy.ClientFile, "mysql"}, {policy.DumpFile, "mysqldump"}} {
		out, diagnostic := &cappedBuffer{maximum: policy.MaximumManifestBytes}, &cappedBuffer{maximum: policy.MaximumManifestBytes}
		if e = runNative(ctx, tool.file, []string{"--no-defaults", "--no-login-paths", "--version"}, policy.ScratchRoot, time.Duration(policy.TerminationGraceSeconds)*time.Second, nil, out, diagnostic); e != nil {
			return nil, e
		}
		text := strings.TrimSpace(out.value.String())
		if !nativeVersionValid(text, tool.name, settings.ExpectedVersion) {
			return nil, errors.New("MYSQL_NATIVE_VERSION_INVALID")
		}
	}
	if e = n.verify(ctx); e != nil {
		return nil, e
	}
	return n, nil
}
