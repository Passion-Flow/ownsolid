//go:build dataset_processor_integration

package datasets

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	wire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
)

func nativeProcessor(t *testing.T) (*Processor, string) {
	t.Helper()
	binary := os.Getenv("PROJECT_ONE_DATA_PYTHON")
	if binary == "" {
		t.Fatal("PROJECT_ONE_DATA_PYTHON must name the actual pinned external CPython runtime")
	}
	_, file, _, _ := runtime.Caller(0)
	script := filepath.Clean(filepath.Join(filepath.Dir(file), "../../../../../runtimes/data-processing/process.py"))
	root := t.TempDir()
	if e := os.Chmod(root, 0700); e != nil {
		t.Fatal(e)
	}
	p, e := NewProcessor(config.DatasetConfiguration{ArtifactRoot: root, PythonExecutable: binary, ProcessorFile: script}, config.DefaultDatasetPolicy())
	if e != nil {
		t.Fatal(e)
	}
	return p, root
}
func attempt(t *testing.T, p *Processor, raw string, publish bool) wire.ProcessorRequest {
	t.Helper()
	scope := filepath.Join(p.configuration.ArtifactRoot, uuid.NewString())
	if e := os.Mkdir(scope, 0700); e != nil {
		t.Fatal(e)
	}
	source := filepath.Join(scope, "input.jsonl")
	if e := os.WriteFile(source, []byte(raw), 0600); e != nil {
		t.Fatal(e)
	}
	output := filepath.Join(scope, "output")
	if e := os.Mkdir(output, 0700); e != nil {
		t.Fatal(e)
	}
	digest := sha256.Sum256([]byte(raw))
	r := wire.ProcessorRequest{SchemaVersion: 1, Operation: wire.InspectOperation, JobID: uuid.NewString(), AttemptID: uuid.NewString(), ScopeDirectory: scope, InputFile: source, InputSHA256: hex.EncodeToString(digest[:]), OutputDirectory: output, Limits: p.policy.Limits, Resources: p.policy.Resources}
	if publish {
		seed := "aabbccddeeff00112233445566778899"
		fraction := 2000
		r.Operation = wire.PublishOperation
		r.Seed = &seed
		r.ValidationBasisPoints = &fraction
	}
	return r
}
func row(source, prompt, completion string) string {
	b, _ := json.Marshal(wire.Record{SourceID: source, Prompt: prompt, Completion: completion})
	return string(b) + "\n"
}

func TestActualProcessorInspectErrorsAndFatalRowBound(t *testing.T) {
	p, _ := nativeProcessor(t)
	request := attempt(t, p, row("a", "\u4e2d\u6587\u2028<>&", "answer")+"{bad}\n"+row("b", "second", "reply"), false)
	result, e := p.Execute(context.Background(), request)
	if e != nil {
		t.Fatal(e)
	}
	if result.Status != "invalid" || result.TotalRows != 3 || len(result.Rows) != 2 || len(result.Errors) != 1 || result.Errors[0].LineNumber != 2 {
		t.Fatalf("incorrect inspection: %+v", result)
	}
	p.policy.Limits.MaximumRows = 2
	p.policy.Limits.MaximumErrors = 2
	request = attempt(t, p, row("a", "first", "a")+row("b", "second", "b")+row("c", "third", "c"), false)
	result, e = p.Execute(context.Background(), request)
	if e != nil || result.Status != "failed" || result.ErrorCode != "DATASET_ROW_LIMIT" || result.ErrorLineNumber == nil || *result.ErrorLineNumber != 3 {
		t.Fatalf("fatal bound: %+v %v", result, e)
	}
}
func TestActualProcessorPublicationAndIndependentPartitionVerification(t *testing.T) {
	p, _ := nativeProcessor(t)
	// Duplicate source bridges must be joined before deduplication; distinct Unicode content stays distinct.
	source := row("a\u2028<>", "same", "answer") + row("b", "same", "answer") + row("b", "bridge", "reply") + row("c", "bridge", "reply") + row("d", "e\u0301", "other") + row("e", "\u00e9", "other")
	request := attempt(t, p, source, true)
	result, e := p.Execute(context.Background(), request)
	if e != nil {
		t.Fatal(e)
	}
	if result.Status != "prepared" || result.Manifest.UniqueRows != 4 || result.Manifest.DuplicateRows != 2 || result.Manifest.SourceGroups != 3 {
		t.Fatalf("incorrect prepared counts: %+v", result)
	}
	path := filepath.Join(request.OutputDirectory, "artifacts", "samples.jsonl")
	if e = os.WriteFile(path, []byte(row("foreign", "wrong", "value")), 0600); e != nil {
		t.Fatal(e)
	}
	if e = verifyPrepared(request, result); e == nil {
		t.Fatal("changed artifact accepted")
	}
}
func TestActualProcessorInvalidSplitIsPreparationFailure(t *testing.T) {
	p, _ := nativeProcessor(t)
	request := attempt(t, p, row("a", "first", "a")+row("a", "second", "b"), true)
	result, e := p.Execute(context.Background(), request)
	if e != nil || result.Status != "failed" || result.ErrorCode != "DATASET_SPLIT_INSUFFICIENT_GROUPS" {
		t.Fatalf("%+v %v", result, e)
	}
}
func TestProcessorPrivatePathsDigestAndNoOverwrite(t *testing.T) {
	p, _ := nativeProcessor(t)
	request := attempt(t, p, row("a", "first", "a"), false)
	request.InputSHA256 = strings.Repeat("0", 64)
	if _, e := p.Execute(context.Background(), request); e == nil || e.Error() != "DATASET_SOURCE_CHANGED" {
		t.Fatalf("digest: %v", e)
	}
	request = attempt(t, p, row("a", "first", "a"), false)
	os.Chmod(request.InputFile, 0644)
	if _, e := p.Execute(context.Background(), request); e == nil {
		t.Fatal("public input accepted")
	}
	request = attempt(t, p, row("a", "first", "a"), false)
	marker := filepath.Join(request.OutputDirectory, "preserve")
	os.WriteFile(marker, []byte("keep"), 0600)
	if _, e := p.Execute(context.Background(), request); e == nil || e.Error() != "DATASET_OUTPUT_CONFLICT" {
		t.Fatalf("%v", e)
	}
	raw, _ := os.ReadFile(marker)
	if string(raw) != "keep" {
		t.Fatal("unknown file changed")
	}
	request = attempt(t, p, row("a", "first", "a"), false)
	if _, e := p.Execute(context.Background(), request); e != nil {
		t.Fatal(e)
	}
	if _, e := p.Execute(context.Background(), request); e == nil {
		t.Fatal("attempt output overwritten")
	}
}
func fixtureProgram(t *testing.T, p *Processor, body string) {
	t.Helper()
	dir := t.TempDir()
	os.Chmod(dir, 0700)
	path := filepath.Join(dir, "test_only_processor.py")
	if e := os.WriteFile(path, []byte(body), 0600); e != nil {
		t.Fatal(e)
	}
	p.configuration.ProcessorFile = path
}
func TestActualChildCancellationMinimalEnvironmentAndReaping(t *testing.T) {
	p, _ := nativeProcessor(t)
	t.Setenv("PROJECT_ONE_TEST_SECRET", "must-not-enter-child")
	fixtureProgram(t, p, "import json,os,sys,time\nr=json.load(open(sys.argv[2]));f=os.open(r['scope_directory']+'/observed.json',os.O_WRONLY|os.O_CREAT|os.O_EXCL,0o600);os.write(f,json.dumps({'pid':os.getpid(),'secret_present':'PROJECT_ONE_TEST_SECRET' in os.environ}).encode());os.close(f);time.sleep(30)\n")
	request := attempt(t, p, row("a", "first", "a"), false)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan error, 1)
	go func() { _, e := p.Execute(ctx, request); finished <- e }()
	path := filepath.Join(request.ScopeDirectory, "observed.json")
	deadline := time.Now().Add(3 * time.Second)
	var raw []byte
	var e error
	for time.Now().Before(deadline) {
		raw, e = os.ReadFile(path)
		if e == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if e != nil {
		t.Fatal("actual child did not start")
	}
	var observed struct {
		PID           int  `json:"pid"`
		SecretPresent bool `json:"secret_present"`
	}
	if json.Unmarshal(raw, &observed) != nil || observed.PID < 1 || observed.SecretPresent {
		t.Fatal("child environment not isolated")
	}
	cancel()
	select {
	case e = <-finished:
		if e == nil || e.Error() != "DATASET_JOB_CANCELLED" {
			t.Fatalf("%v", e)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("child cancellation not bounded")
	}
	if e = syscall.Kill(observed.PID, 0); !errors.Is(e, syscall.ESRCH) {
		t.Fatalf("owned child not reaped: %v", e)
	}
}
func TestActualChildParentDeadlineAndRSSLimit(t *testing.T) {
	p, _ := nativeProcessor(t)
	p.policy.Resources.WallSeconds = 1
	p.policy.Resources.CpuSeconds = 1
	fixtureProgram(t, p, "import time;time.sleep(30)\n")
	request := attempt(t, p, row("a", "first", "a"), false)
	started := time.Now()
	if _, e := p.Execute(context.Background(), request); e == nil || e.Error() != "DATASET_PROCESSOR_TIMEOUT" {
		t.Fatalf("deadline %v", e)
	}
	if time.Since(started) > 4*time.Second {
		t.Fatal("timeout unbounded")
	}
	p.policy.Resources.WallSeconds = 5
	p.policy.Resources.CpuSeconds = 3
	p.policy.Resources.MemoryBytes = 64 * 1024 * 1024
	fixtureProgram(t, p, "import time;x=bytearray(128*1024*1024);time.sleep(30)\n")
	request = attempt(t, p, row("a", "first", "a"), false)
	if _, e := p.Execute(context.Background(), request); e == nil || e.Error() != "DATASET_PROCESSOR_MEMORY_EXCEEDED" {
		t.Fatalf("actual RSS %v", e)
	}
}
func TestActualChildMalformedResultAndOutputLimitsFail(t *testing.T) {
	p, _ := nativeProcessor(t)
	fixtureProgram(t, p, "import json,os,sys\nr=json.load(open(sys.argv[2]));p=r['output_directory']+'/result.json';f=os.open(p,os.O_WRONLY|os.O_CREAT|os.O_EXCL,0o600);os.write(f,b'{\"status\":\"valid\"}');os.close(f)\n")
	request := attempt(t, p, row("a", "first", "a"), false)
	if _, e := p.Execute(context.Background(), request); e == nil || e.Error() != "DATASET_PROCESSOR_RESULT_INVALID" {
		t.Fatalf("%v", e)
	}
	fixtureProgram(t, p, "print('x'*20000)\n")
	request = attempt(t, p, row("a", "first", "a"), false)
	if _, e := p.Execute(context.Background(), request); e == nil || e.Error() != "DATASET_PROCESSOR_RESULT_INVALID" {
		t.Fatalf("%v", e)
	}
}
