//go:build knowledge_encoder_integration

package knowledge

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
)

func actualProcessor(t *testing.T) (*Processor, config.KnowledgeConfiguration) {
	t.Helper()
	root := t.TempDir()
	if os.Chmod(root, 0700) != nil {
		t.Fatal("private scratch")
	}
	c := config.KnowledgeConfiguration{Enabled: true, PythonExecutable: os.Getenv("PROJECT_ONE_KNOWLEDGE_PYTHON"), ModelDirectory: os.Getenv("PROJECT_ONE_KNOWLEDGE_MODEL_DIRECTORY"), WorkerFile: filepath.Join(os.Getenv("PROJECT_ONE_REPOSITORY_ROOT"), "runtimes/knowledge/embed.py"), ScratchRoot: root}
	if c.PythonExecutable == "" || c.ModelDirectory == "" || os.Getenv("PROJECT_ONE_REPOSITORY_ROOT") == "" {
		t.Fatal("explicit actual native encoder environment required")
	}
	p, e := NewProcessor(c, config.DefaultKnowledgePolicy())
	if e != nil {
		t.Fatal("actual processor startup", e)
	}
	t.Cleanup(p.Close)
	return p, c
}
func TestM12ActualGoOfflineEncoderBridgeAndCompleteUnicodePositions(t *testing.T) {
	p, _ := actualProcessor(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	text := "  Caf\u00e9 😀 \u6c49\u5b57\r\n" + strings.Repeat("Exact original source offsets are retained. ", 30) + " Terminal ZEBRA-TAIL.\r\n"
	result, e := p.Execute(ctx, text, true, 32, 8)
	if e != nil {
		t.Fatal("actual Go to offline model", e)
	}
	if len(result.Chunks) < 3 || result.Chunks[0].StartByte != 0 || result.Chunks[len(result.Chunks)-1].EndByte != len(text) || result.SourceSHA256 != hashBytes([]byte(text)) {
		t.Fatal("source identity or tail missing")
	}
	result, e = p.Execute(ctx, "Where is the launch city?", false, 192, 32)
	if e != nil || len(result.Chunks) != 1 || result.Dimension != 384 {
		t.Fatal("actual query bridge", e)
	}
	entries, e := os.ReadDir(p.configuration.ScratchRoot)
	if e != nil || len(entries) != 0 {
		t.Fatal("owned scratch not cleaned", e)
	}
}
func TestM12ActualGoEncoderCancellationLeavesNoChildScratch(t *testing.T) {
	p, _ := actualProcessor(t)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, e := p.Execute(ctx, "A bounded cancellation query.", false, 192, 32)
	if e == nil || e.Error() != "KNOWLEDGE_REQUEST_CANCELLED" || time.Since(started) > 5*time.Second {
		t.Fatal("actual parent cancellation", e)
	}
	entries, e := os.ReadDir(p.configuration.ScratchRoot)
	if e != nil || len(entries) != 0 {
		t.Fatal("cancelled owned scratch not cleaned", e)
	}
}
func faultProcessor(t *testing.T, program string, mutate func(*config.KnowledgePolicy)) *Processor {
	t.Helper()
	_, c := actualProcessor(t)
	directory := t.TempDir()
	c.WorkerFile = filepath.Join(directory, "explicit_worker_fault_double.py")
	if e := os.WriteFile(c.WorkerFile, []byte(program), 0600); e != nil {
		t.Fatal(e)
	}
	policy := config.DefaultKnowledgePolicy()
	if mutate != nil {
		mutate(&policy)
	}
	p, e := NewProcessor(c, policy)
	if e != nil {
		t.Fatal(e)
	}
	return p
}

// Programs below are explicit worker fault doubles. Resource use, pipes, process groups,
// timeout/cancellation and strict output validation are real; no encoder success is substituted.
func TestM12ActualGoWorkerTimeoutRSSAndOutputBounds(t *testing.T) {
	tests := []struct {
		name, program, code string
		mutate              func(*config.KnowledgePolicy)
	}{
		{"wall", "import time;time.sleep(30)", "KNOWLEDGE_WORKER_TIMEOUT", func(p *config.KnowledgePolicy) { p.QuerySeconds = 5 }},
		{"resident", "import time;data=bytearray(600*1024*1024);time.sleep(30)", "KNOWLEDGE_WORKER_MEMORY_EXCEEDED", func(p *config.KnowledgePolicy) { p.WorkerMemoryBytes = 536870912; p.MemoryPollMilliseconds = 100 }},
		{"stdout", "import sys;sys.stdout.buffer.write(b'x'*(67108864+1));sys.stdout.buffer.flush()", "KNOWLEDGE_WORKER_OUTPUT_LIMIT", nil},
		{"stderr", "import sys;sys.stderr.write('x'*70000);sys.stderr.flush();sys.exit(1)", "KNOWLEDGE_WORKER_OUTPUT_LIMIT", nil},
		{"unknown_error", "import sys;sys.stderr.write('{\"error\":\"synthetic-private-source-must-not-escape\"}');sys.exit(1)", "KNOWLEDGE_WORKER_FAILED", nil},
		{"known_error", "import sys;sys.stderr.write('{\"error\":\"KNOWLEDGE_QUERY_TOO_LONG\"}');sys.exit(1)", "KNOWLEDGE_QUERY_TOO_LONG", nil},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			p := faultProcessor(t, test.program, test.mutate)
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			_, e := p.Execute(ctx, "Synthetic worker fault input.", false, 192, 32)
			if e == nil || e.Error() != test.code {
				t.Fatal("actual process guard", e)
			}
			entries, e := os.ReadDir(p.configuration.ScratchRoot)
			if e != nil || len(entries) != 0 {
				t.Fatal("fault scratch not cleaned", e)
			}
		})
	}
}
func TestM12StrictGoWorkerOutputRejectsScopeDimensionNonfiniteAndOffsets(t *testing.T) {
	p, _ := actualProcessor(t)
	text := "A😀 caf\u00e9\r\n\u5c3e"
	valid := WorkerResult{SchemaVersion: 1, EncoderCatalogSHA256: CatalogDigest(), Dimension: 384, SourceSHA256: hashBytes([]byte(text)), Chunks: []Chunk{{Ordinal: 0, StartByte: 0, EndByte: len(text), TokenCount: 1, Embedding: append([]float64{1}, make([]float64, 383)...)}}}
	for _, mutate := range []func(*WorkerResult){func(v *WorkerResult) { v.EncoderCatalogSHA256 = strings.Repeat("a", 64) }, func(v *WorkerResult) { v.SourceSHA256 = strings.Repeat("b", 64) }, func(v *WorkerResult) { v.Dimension = 3 }, func(v *WorkerResult) { v.Chunks[0].EndByte = 3 }, func(v *WorkerResult) { v.Chunks[0].Embedding = make([]float64, 384) }, func(v *WorkerResult) { v.Chunks[0].TokenCount = 255 }} {
		raw, _ := json.Marshal(valid)
		var bad WorkerResult
		json.Unmarshal(raw, &bad)
		mutate(&bad)
		if p.validateResult(bad, text, true, 192) == nil {
			t.Fatal("malformed worker data accepted")
		}
	}
	for _, raw := range []string{`{}`, `{"schema_version":null}`, `{"schema_version":1,"schema_version":1}`, `{"embedding":[NaN]}`} {
		program := "import sys;sys.stdout.write(" + strconvQuote(raw) + ")"
		worker := faultProcessor(t, program, nil)
		_, e := worker.Execute(context.Background(), text, false, 192, 32)
		if e == nil || e.Error() != "KNOWLEDGE_WORKER_RESULT_INVALID" {
			t.Fatal("strict actual pipe result", e)
		}
	}
}
func strconvQuote(value string) string { raw, _ := json.Marshal(value); return string(raw) }
func TestM12GoWorkerAdmissionBoundAndDisabledMissingAssets(t *testing.T) {
	p := faultProcessor(t, "import time;time.sleep(30)", nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, e := p.Execute(ctx, "First bounded request.", false, 192, 32); done <- e }()
	deadline := time.Now().Add(time.Second)
	for len(p.active) == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if len(p.active) == 0 {
		t.Fatal("first request not admitted")
	}
	_, e := p.Execute(context.Background(), "Second bounded request.", false, 192, 32)
	if e == nil || e.Error() != "KNOWLEDGE_BUSY" {
		t.Fatal("worker capacity not enforced", e)
	}
	cancel()
	if e = <-done; e == nil || e.Error() != "KNOWLEDGE_REQUEST_CANCELLED" {
		t.Fatal(e)
	}
	disabled, e := NewProcessor(config.KnowledgeConfiguration{}, config.DefaultKnowledgePolicy())
	if e != nil || disabled != nil {
		t.Fatal("disabled must not load missing assets", e)
	}
}

func TestM12ActualProcessorShutdownCancelsAndJoinsCurrentChildren(t *testing.T) {
	p := faultProcessor(t, "import time;time.sleep(30)", nil)
	done := make(chan error, 1)
	go func() { _, e := p.Execute(context.Background(), "Bounded shutdown query.", false, 192, 32); done <- e }()
	deadline := time.Now().Add(time.Second)
	for len(p.active) == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if len(p.active) == 0 {
		t.Fatal("child not admitted")
	}
	started := time.Now()
	p.Close()
	if time.Since(started) > 5*time.Second {
		t.Fatal("shutdown did not join bounded child")
	}
	if e := <-done; e == nil || e.Error() != "KNOWLEDGE_REQUEST_CANCELLED" {
		t.Fatal("shutdown failed to cancel", e)
	}
	if _, e := p.Execute(context.Background(), "Closed processor.", false, 192, 32); e == nil {
		t.Fatal("closed processor admitted new work")
	}
	entries, e := os.ReadDir(p.configuration.ScratchRoot)
	if e != nil || len(entries) != 0 {
		t.Fatal("shutdown left owned scratch", e)
	}
}
