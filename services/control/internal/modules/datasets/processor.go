package datasets

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
)

func failure(code string) error { return errors.New(code) }

// Processor prepares private files only. The business worker separately fences and authorizes every commit.
type Processor struct {
	configuration config.DatasetConfiguration
	policy        config.DatasetPolicy
}

func NewProcessor(configuration config.DatasetConfiguration, policy config.DatasetPolicy) (*Processor, error) {
	if configuration.ArtifactRoot == "" || configuration.Validate("") != nil || policy.Validate() != nil {
		return nil, failure("DATASET_PROCESSOR_CONFIGURATION_INVALID")
	}
	if trustedProgram(configuration.PythonExecutable, true) != nil || trustedProgram(configuration.ProcessorFile, false) != nil {
		return nil, failure("DATASET_PROCESSOR_PROGRAM_INVALID")
	}
	return &Processor{configuration: configuration, policy: policy}, nil
}

// Installed code is a trusted operator asset, outside the writable dataset namespace.
func trustedProgram(path string, executable bool) error {
	canonical, e := filepath.EvalSymlinks(path)
	if e != nil || canonical != path {
		return failure("DATASET_PROCESSOR_PROGRAM_INVALID")
	}
	info, e := os.Lstat(path)
	if e != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0022 != 0 || executable && info.Mode().Perm()&0111 == 0 {
		return failure("DATASET_PROCESSOR_PROGRAM_INVALID")
	}
	owner, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(owner.Uid) != os.Geteuid() && owner.Uid != 0 {
		return failure("DATASET_PROCESSOR_PROGRAM_INVALID")
	}
	// The installed directory must itself be protected; ancestors such as an operator-managed shared volume are a deployment trust boundary.
	parent, e := os.Lstat(filepath.Dir(path))
	if e != nil || !parent.IsDir() || parent.Mode().Perm()&0022 != 0 {
		return failure("DATASET_PROCESSOR_PROGRAM_INVALID")
	}
	return nil
}

func privatePath(path, scope string, directory bool, maximum int64) (os.FileInfo, error) {
	canonical, e := filepath.EvalSymlinks(path)
	if e != nil || canonical != path || !filepath.IsAbs(path) || filepath.Clean(path) != path || scope != "" && !strings.HasPrefix(path, scope+string(filepath.Separator)) {
		return nil, failure("DATASET_PROCESSOR_PATH_INVALID")
	}
	info, e := os.Lstat(path)
	if e != nil || info.Mode().Perm()&0077 != 0 || directory && !info.IsDir() || !directory && (!info.Mode().IsRegular() || info.Size() > maximum) {
		return nil, failure("DATASET_PROCESSOR_PATH_INVALID")
	}
	owner, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(owner.Uid) != os.Geteuid() {
		return nil, failure("DATASET_PROCESSOR_PATH_INVALID")
	}
	if scope != "" {
		for parent := filepath.Dir(path); parent != scope; parent = filepath.Dir(parent) {
			if parent == filepath.Dir(parent) {
				return nil, failure("DATASET_PROCESSOR_PATH_INVALID")
			}
			if _, e = privatePath(parent, "", true, 0); e != nil {
				return nil, e
			}
		}
	}
	return info, nil
}

func writeNew(path string, raw []byte) error {
	file, e := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return failure("DATASET_OUTPUT_CONFLICT")
	}
	_, e = file.Write(raw)
	if e == nil {
		e = file.Sync()
	}
	closeError := file.Close()
	if e != nil || closeError != nil {
		return failure("DATASET_STORAGE_UNAVAILABLE")
	}
	dir, e := os.Open(filepath.Dir(path))
	if e != nil {
		return failure("DATASET_STORAGE_UNAVAILABLE")
	}
	defer dir.Close()
	if dir.Sync() != nil {
		return failure("DATASET_STORAGE_UNAVAILABLE")
	}
	return nil
}

func fileDigest(path string, maximum int64) (string, int64, error) {
	file, e := os.Open(path)
	if e != nil {
		return "", 0, failure("DATASET_FILE_INVALID")
	}
	defer file.Close()
	h := sha256.New()
	n, e := io.Copy(h, io.LimitReader(file, maximum+1))
	if e != nil || n > maximum {
		return "", 0, failure("DATASET_FILE_INVALID")
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

type boundedDiagnostic struct {
	mu       sync.Mutex
	size     int
	overflow bool
}

func (b *boundedDiagnostic) Write(raw []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.size += len(raw)
	if b.size > wire.MaximumDiagnosticBytes {
		b.overflow = true
		return 0, failure("DATASET_PROCESSOR_DIAGNOSTIC_LIMIT")
	}
	return len(raw), nil
}
func (b *boundedDiagnostic) exceeded() bool { b.mu.Lock(); defer b.mu.Unlock(); return b.overflow }

func stopChild(command *exec.Cmd, done <-chan error, grace time.Duration) {
	// Only the freshly started and still-owned process group is signaled. Persisted PIDs are never used.
	select {
	case <-done:
		return
	default:
	}
	_ = syscall.Kill(-command.Process.Pid, syscall.SIGTERM)
	timer := time.NewTimer(grace)
	defer timer.Stop()
	select {
	case <-done:
		return
	case <-timer.C:
	}
	_ = syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
	<-done
}

// A terminal /proc observation is only a reason to reconcile the original
// owned Wait receipt. It never establishes a successful exit or result itself.
var errProcessorExitSampleUnavailable = errors.New("processor exited before final memory sample")

func processorMemoryExitReceipt(ctx context.Context, done <-chan error, monitorError error, wall <-chan time.Time, grace time.Duration) (error, error) {
	if ctx.Err() != nil {
		return nil, failure("DATASET_JOB_CANCELLED")
	}
	if !errors.Is(monitorError, errProcessorExitSampleUnavailable) {
		return nil, failure("DATASET_PROCESSOR_MEMORY_MONITOR_FAILED")
	}
	// Reuse the typed child-stop budget while retaining the original wall and
	// cancellation channels. Live sampling failures never enter this wait.
	receipt := time.NewTimer(grace)
	defer receipt.Stop()
	select {
	case exitError := <-done:
		return exitError, nil
	case <-ctx.Done():
		return nil, failure("DATASET_JOB_CANCELLED")
	case <-wall:
		return nil, failure("DATASET_PROCESSOR_TIMEOUT")
	case <-receipt.C:
		return nil, failure("DATASET_PROCESSOR_MEMORY_MONITOR_FAILED")
	}
}

func (p *Processor) Execute(ctx context.Context, request wire.ProcessorRequest) (wire.ProcessorResult, error) {
	var empty wire.ProcessorResult
	if request.Validate() != nil || request.Limits != p.policy.Limits || request.Resources != p.policy.Resources {
		return empty, failure("DATASET_PROCESSOR_REQUEST_INVALID")
	}
	if !strings.HasPrefix(request.ScopeDirectory, p.configuration.ArtifactRoot+string(filepath.Separator)) {
		return empty, failure("DATASET_PROCESSOR_PATH_INVALID")
	}
	if _, e := privatePath(request.ScopeDirectory, "", true, 0); e != nil {
		return empty, e
	}
	info, e := privatePath(request.InputFile, request.ScopeDirectory, false, request.Limits.MaximumBytes)
	if e != nil {
		return empty, e
	}
	if _, e = privatePath(request.OutputDirectory, request.ScopeDirectory, true, 0); e != nil {
		return empty, e
	}
	entries, e := os.ReadDir(request.OutputDirectory)
	if e != nil || len(entries) != 0 {
		return empty, failure("DATASET_OUTPUT_CONFLICT")
	}
	digest, size, e := fileDigest(request.InputFile, request.Limits.MaximumBytes)
	if e != nil || digest != request.InputSHA256 || size != info.Size() {
		return empty, failure("DATASET_SOURCE_CHANGED")
	}
	if trustedProgram(p.configuration.PythonExecutable, true) != nil || trustedProgram(p.configuration.ProcessorFile, false) != nil {
		return empty, failure("DATASET_PROCESSOR_PROGRAM_INVALID")
	}
	raw, e := json.Marshal(request)
	if e != nil || len(raw) > wire.MaxRequestBytes {
		return empty, failure("DATASET_PROCESSOR_REQUEST_INVALID")
	}
	requestFile := filepath.Join(request.ScopeDirectory, "request.json")
	if e = writeNew(requestFile, raw); e != nil {
		return empty, e
	}
	if ctx.Err() != nil {
		return empty, failure("DATASET_JOB_CANCELLED")
	}
	command := exec.Command(p.configuration.PythonExecutable, "-I", "-B", p.configuration.ProcessorFile, "--request-file", requestFile)
	command.Dir = request.ScopeDirectory
	command.Env = []string{"PATH=/usr/bin:/bin", "LANG=C.UTF-8", "LC_ALL=C.UTF-8", "TMPDIR=" + request.ScopeDirectory, "PYTHONNOUSERSITE=1", "PYTHONDONTWRITEBYTECODE=1"}
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	diagnostic := &boundedDiagnostic{}
	command.Stdout = diagnostic
	command.Stderr = diagnostic
	if command.Start() != nil {
		return empty, failure("DATASET_PROCESSOR_START_FAILED")
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	wall := time.NewTimer(time.Duration(request.Resources.WallSeconds) * time.Second)
	defer wall.Stop()
	memory := time.NewTicker(time.Duration(p.policy.MemoryPollMilliseconds) * time.Millisecond)
	defer memory.Stop()
	var exitError error
running:
	for {
		select {
		case exitError = <-done:
			break running
		case <-ctx.Done():
			stopChild(command, done, time.Duration(p.policy.ChildStopSeconds)*time.Second)
			return empty, failure("DATASET_JOB_CANCELLED")
		case <-wall.C:
			stopChild(command, done, time.Duration(p.policy.ChildStopSeconds)*time.Second)
			return empty, failure("DATASET_PROCESSOR_TIMEOUT")
		case <-memory.C:
			resident, monitorError := childResidentBytes(ctx, command.Process.Pid, time.Duration(p.policy.MemoryPollMilliseconds)*time.Millisecond)
			if monitorError != nil {
				if runtime.GOOS == "linux" {
					exitError, e = processorMemoryExitReceipt(ctx, done, monitorError, wall.C, time.Duration(p.policy.ChildStopSeconds)*time.Second)
					if e == nil {
						break running
					}
				} else {
					// Preserve the existing non-Linux sampler's receipt behavior;
					// only Linux supplies the terminal kernel observation above.
					select {
					case exitError = <-done:
						break running
					default:
					}
					e = failure("DATASET_PROCESSOR_MEMORY_MONITOR_FAILED")
				}
				stopChild(command, done, time.Duration(p.policy.ChildStopSeconds)*time.Second)
				return empty, e
			}
			if resident > request.Resources.MemoryBytes {
				stopChild(command, done, time.Duration(p.policy.ChildStopSeconds)*time.Second)
				return empty, failure("DATASET_PROCESSOR_MEMORY_EXCEEDED")
			}
		}
	}
	if ctx.Err() != nil {
		return empty, failure("DATASET_JOB_CANCELLED")
	}
	if diagnostic.exceeded() {
		return empty, failure("DATASET_PROCESSOR_RESULT_INVALID")
	}
	resultFile := filepath.Join(request.OutputDirectory, "result.json")
	resultInfo, e := privatePath(resultFile, request.OutputDirectory, false, request.Resources.MaximumResultBytes)
	if e != nil {
		return empty, failure("DATASET_PROCESSOR_RESULT_INVALID")
	}
	file, e := os.Open(resultFile)
	if e != nil {
		return empty, failure("DATASET_PROCESSOR_RESULT_INVALID")
	}
	raw, e = io.ReadAll(io.LimitReader(file, request.Resources.MaximumResultBytes+1))
	file.Close()
	if e != nil || int64(len(raw)) != resultInfo.Size() {
		return empty, failure("DATASET_PROCESSOR_RESULT_INVALID")
	}
	result, e := wire.DecodeResult(raw, request)
	if e != nil {
		return empty, e
	}
	if result.Status == "failed" {
		var code *exec.ExitError
		if !errors.As(exitError, &code) || code.ExitCode() != 1 {
			return empty, failure("DATASET_PROCESSOR_RESULT_INVALID")
		}
		return result, nil
	}
	if exitError != nil || result.SourceBytes != size {
		return empty, failure("DATASET_PROCESSOR_RESULT_INVALID")
	}
	expected := []string{"result.json"}
	if result.Status == "prepared" {
		expected = append(expected, "artifacts")
		if e = verifyPrepared(request, result); e != nil {
			return empty, e
		}
	}
	entries, e = os.ReadDir(request.OutputDirectory)
	if e != nil || len(entries) != len(expected) {
		return empty, failure("DATASET_PROCESSOR_RESULT_INVALID")
	}
	for _, entry := range entries {
		found := false
		for _, name := range expected {
			found = found || entry.Name() == name
		}
		if !found {
			return empty, failure("DATASET_PROCESSOR_RESULT_INVALID")
		}
	}
	return result, nil
}

func verifyPrepared(request wire.ProcessorRequest, result wire.ProcessorResult) error {
	directory := filepath.Join(request.OutputDirectory, "artifacts")
	if _, e := privatePath(directory, request.OutputDirectory, true, 0); e != nil {
		return failure("DATASET_FILE_INVALID")
	}
	if result.Manifest == nil {
		return failure("DATASET_FILE_INVALID")
	}
	manifest := *result.Manifest
	entries, e := os.ReadDir(directory)
	if e != nil || len(entries) != len(manifest.Files)+1 {
		return failure("DATASET_FILE_INVALID")
	}
	assets := append(append([]wire.Asset{}, manifest.Files...), manifest.ManifestFile)
	for _, asset := range assets {
		path := filepath.Join(directory, asset.Path)
		info, e := privatePath(path, directory, false, request.Resources.MaximumResultBytes)
		if e != nil || info.Size() != asset.SizeBytes {
			return failure("DATASET_FILE_INVALID")
		}
		digest, size, e := fileDigest(path, request.Resources.MaximumResultBytes)
		if e != nil || digest != asset.SHA256 || size != asset.SizeBytes {
			return failure("DATASET_FILE_INVALID")
		}
	}
	// The manifest file excludes its own digest; reconstruct this exact envelope and compare semantic JSON.
	raw, e := os.ReadFile(filepath.Join(directory, "manifest.json"))
	if e != nil {
		return failure("DATASET_FILE_INVALID")
	}
	encoded, e := json.Marshal(manifest)
	if e != nil {
		return failure("DATASET_FILE_INVALID")
	}
	var expected, actual map[string]json.RawMessage
	if json.Unmarshal(encoded, &expected) != nil || wire.DecodeStrict(raw, &actual) != nil {
		return failure("DATASET_FILE_INVALID")
	}
	delete(expected, "manifest_file")
	a, _ := json.Marshal(actual)
	b, _ := json.Marshal(expected)
	if !bytes.Equal(a, b) {
		return failure("DATASET_FILE_INVALID")
	}
	return verifyPartitions(request, manifest, directory)
}
