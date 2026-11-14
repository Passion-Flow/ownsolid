// Package knowledge owns scoped TXT indexing and retrieval orchestration.
package knowledge

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	datawire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
)

func failure(code string) error { return errors.New(code) }

type workerContract struct {
	ProcessControl struct {
		ParentPIDEnvironment        string `json:"parent_pid_environment"`
		WallSecondsEnvironment      string `json:"wall_seconds_environment"`
		PollMillisecondsEnvironment string `json:"poll_milliseconds_environment"`
		MaximumWallSeconds          int    `json:"maximum_wall_seconds"`
		TerminationSignal           string `json:"termination_signal"`
	} `json:"process_control"`
	SchemaVersion              int      `json:"schema_version"`
	Dimension                  int      `json:"dimension"`
	MaximumChunkTokens         int      `json:"maximum_chunk_tokens"`
	MaximumSourceBytes         int      `json:"maximum_source_bytes"`
	RequestMetadataBudgetBytes int      `json:"request_metadata_budget_bytes"`
	MaximumRequestBytes        int      `json:"maximum_request_bytes"`
	MaximumResultBytes         int      `json:"maximum_result_bytes"`
	MaximumDiagnosticBytes     int      `json:"maximum_diagnostic_bytes"`
	VectorNormTolerance        float64  `json:"vector_norm_tolerance"`
	EncoderCatalogSHA256       string   `json:"encoder_catalog_sha256"`
	Errors                     []string `json:"errors"`
}
type encoderCatalog struct {
	Files []struct {
		Path      string `json:"path"`
		SizeBytes int64  `json:"size_bytes"`
		SHA256    string `json:"sha256"`
	} `json:"files"`
}
type WorkerLimits struct {
	ChunkBytes    int `json:"chunk_bytes"`
	SourceBytes   int `json:"source_bytes"`
	SourceTokens  int `json:"source_tokens"`
	Chunks        int `json:"chunks"`
	ChunkTokens   int `json:"chunk_tokens"`
	OverlapTokens int `json:"overlap_tokens"`
	QueryTokens   int `json:"query_tokens"`
}
type WorkerResources struct {
	Threads   int `json:"threads"`
	BatchSize int `json:"batch_size"`
}
type workerRequest struct {
	SchemaVersion        int             `json:"schema_version"`
	Operation            string          `json:"operation"`
	EncoderCatalogSHA256 string          `json:"encoder_catalog_sha256"`
	ModelDirectory       string          `json:"model_directory"`
	Text                 string          `json:"text"`
	Limits               WorkerLimits    `json:"limits"`
	Resources            WorkerResources `json:"resources"`
}
type Chunk struct {
	Ordinal    int       `json:"ordinal"`
	StartByte  int       `json:"start_byte"`
	EndByte    int       `json:"end_byte"`
	TokenCount int       `json:"token_count"`
	Embedding  []float64 `json:"embedding"`
}
type WorkerResult struct {
	SchemaVersion        int     `json:"schema_version"`
	EncoderCatalogSHA256 string  `json:"encoder_catalog_sha256"`
	Dimension            int     `json:"dimension"`
	SourceSHA256         string  `json:"source_sha256"`
	Chunks               []Chunk `json:"chunks"`
}
type Processor struct {
	lifecycle     context.Context
	stopLifecycle context.CancelFunc
	lifecycleMu   sync.Mutex
	scratchMu     sync.Mutex
	closed        bool
	running       sync.WaitGroup
	configuration config.KnowledgeConfiguration
	policy        config.KnowledgePolicy
	contract      workerContract
	active        chan struct{}
}

func CatalogDigest() string {
	value := sha256.Sum256([]byte(EncoderCatalogJSON))
	return hex.EncodeToString(value[:])
}
func hashBytes(value []byte) string { sum := sha256.Sum256(value); return hex.EncodeToString(sum[:]) }

func protected(path string, directory, executable, pythonLink bool) error {
	info, e := os.Lstat(path)
	if e != nil {
		return failure("KNOWLEDGE_INSTALLED_ASSET_INVALID")
	}
	if info.Mode()&os.ModeSymlink != 0 {
		if !pythonLink {
			return failure("KNOWLEDGE_INSTALLED_ASSET_INVALID")
		}
		resolved, e := filepath.EvalSymlinks(path)
		if e != nil || resolved == path {
			return failure("KNOWLEDGE_INSTALLED_ASSET_INVALID")
		}
		if e = protected(filepath.Dir(path), true, false, false); e != nil {
			return e
		}
		return protected(resolved, false, executable, false)
	}
	resolved, e := filepath.EvalSymlinks(path)
	owner, ok := info.Sys().(*syscall.Stat_t)
	if e != nil || resolved != path || !ok || (owner.Uid != 0 && int(owner.Uid) != os.Geteuid()) || info.Mode().Perm()&0022 != 0 || directory && !info.IsDir() || !directory && !info.Mode().IsRegular() || executable && info.Mode().Perm()&0111 == 0 {
		return failure("KNOWLEDGE_INSTALLED_ASSET_INVALID")
	}
	return nil
}
func verifyEncoder(directory string) error {
	if e := protected(directory, true, false, false); e != nil {
		return e
	}
	var catalog encoderCatalog
	if json.Unmarshal([]byte(EncoderCatalogJSON), &catalog) != nil {
		return failure("KNOWLEDGE_ENCODER_ASSET_INVALID")
	}
	expected := map[string]bool{}
	for _, item := range catalog.Files {
		expected[item.Path] = true
		path := filepath.Join(directory, filepath.FromSlash(item.Path))
		if e := protected(path, false, false, false); e != nil {
			return e
		}
		before, e := os.Stat(path)
		if e != nil || before.Size() != item.SizeBytes {
			return failure("KNOWLEDGE_ENCODER_ASSET_INVALID")
		}
		source, e := os.Open(path)
		if e != nil {
			return failure("KNOWLEDGE_ENCODER_ASSET_INVALID")
		}
		current, e := source.Stat()
		if e != nil || !os.SameFile(before, current) {
			source.Close()
			return failure("KNOWLEDGE_ENCODER_ASSET_INVALID")
		}
		digest := sha256.New()
		count, readError := io.Copy(digest, io.LimitReader(source, item.SizeBytes+1))
		after, statError := source.Stat()
		source.Close()
		if readError != nil || statError != nil || count != item.SizeBytes || after.Size() != before.Size() || !after.ModTime().Equal(before.ModTime()) || hex.EncodeToString(digest.Sum(nil)) != item.SHA256 {
			return failure("KNOWLEDGE_ENCODER_ASSET_INVALID")
		}
	}
	if e := filepath.WalkDir(directory, func(path string, entry os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if path == directory {
			return nil
		}
		if entry.IsDir() {
			return protected(path, true, false, false)
		}
		relative, e := filepath.Rel(directory, path)
		if e != nil || !expected[filepath.ToSlash(relative)] || entry.Type()&os.ModeSymlink != 0 {
			return failure("KNOWLEDGE_ENCODER_ASSET_INVALID")
		}
		return nil
	}); e != nil {
		return failure("KNOWLEDGE_ENCODER_ASSET_INVALID")
	}
	return nil
}
func NewProcessor(c config.KnowledgeConfiguration, p config.KnowledgePolicy) (*Processor, error) {
	if c.Validate() != nil || p.Validate() != nil {
		return nil, failure("KNOWLEDGE_CONFIGURATION_INVALID")
	}
	if !c.Enabled {
		return nil, nil
	}
	var contract workerContract
	if json.Unmarshal([]byte(WorkerContractJSON), &contract) != nil || contract.EncoderCatalogSHA256 != CatalogDigest() || contract.ProcessControl.ParentPIDEnvironment == "" || contract.ProcessControl.WallSecondsEnvironment == "" || contract.ProcessControl.PollMillisecondsEnvironment == "" || contract.ProcessControl.MaximumWallSeconds < p.IngestSeconds+p.ChildStopSeconds {
		return nil, failure("KNOWLEDGE_COMPILED_CONTRACT_INVALID")
	}
	if contract.MaximumSourceBytes < p.SourceBytes || contract.RequestMetadataBudgetBytes < 1 || contract.MaximumRequestBytes < contract.MaximumSourceBytes*6+contract.RequestMetadataBudgetBytes {
		return nil, failure("KNOWLEDGE_COMPILED_CONTRACT_INVALID")
	}
	if e := protected(c.PythonExecutable, false, true, true); e != nil {
		return nil, e
	}
	if e := protected(filepath.Dir(c.WorkerFile), true, false, false); e != nil {
		return nil, e
	}
	if e := protected(c.WorkerFile, false, false, false); e != nil {
		return nil, e
	}
	if e := verifyEncoder(c.ModelDirectory); e != nil {
		return nil, e
	}
	if e := protected(c.ScratchRoot, true, false, false); e != nil {
		return nil, e
	}
	info, e := os.Stat(c.ScratchRoot)
	if e != nil || info.Mode().Perm()&0077 != 0 || int(info.Sys().(*syscall.Stat_t).Uid) != os.Geteuid() {
		return nil, failure("KNOWLEDGE_SCRATCH_ROOT_INVALID")
	}
	lifecycle, stop := context.WithCancel(context.Background())
	processor := &Processor{configuration: c, policy: p, contract: contract, active: make(chan struct{}, p.Concurrency), lifecycle: lifecycle, stopLifecycle: stop}
	if e := processor.CleanupScratch(); e != nil {
		stop()
		return nil, e
	}
	return processor, nil
}

type boundedOutput struct {
	mu       sync.Mutex
	buffer   bytes.Buffer
	maximum  int
	exceeded bool
}

func (b *boundedOutput) Write(raw []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(raw) > b.maximum-b.buffer.Len() {
		b.exceeded = true
		return 0, failure("KNOWLEDGE_WORKER_OUTPUT_LIMIT")
	}
	return b.buffer.Write(raw)
}
func (b *boundedOutput) Bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]byte(nil), b.buffer.Bytes()...)
}
func (b *boundedOutput) Exceeded() bool { b.mu.Lock(); defer b.mu.Unlock(); return b.exceeded }
func stop(command *exec.Cmd, done <-chan error, grace time.Duration) {
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

// Available reports lifecycle readiness without admitting work or exposing installed paths.
func (p *Processor) Available() bool {
	if p == nil {
		return false
	}
	p.lifecycleMu.Lock()
	defer p.lifecycleMu.Unlock()
	return !p.closed && p.lifecycle != nil && p.active != nil
}

func (p *Processor) reserve() bool {
	p.lifecycleMu.Lock()
	defer p.lifecycleMu.Unlock()
	if p.closed || p.lifecycle == nil || p.active == nil {
		return false
	}
	select {
	case p.active <- struct{}{}:
		p.running.Add(1)
		return true
	default:
		return false
	}
}
func (p *Processor) release() { <-p.active; p.running.Done() }

// Close prevents new work, cancels every active query/job and joins its child cleanup.
func (p *Processor) Close() {
	if p == nil {
		return
	}
	p.lifecycleMu.Lock()
	if !p.closed {
		p.closed = true
		if p.stopLifecycle != nil {
			p.stopLifecycle()
		}
	}
	p.lifecycleMu.Unlock()
	p.running.Wait()
}
func (p *Processor) Execute(ctx context.Context, text string, document bool, chunkTokens, overlapTokens int) (WorkerResult, error) {
	return p.execute(ctx, text, document, chunkTokens, overlapTokens, false)
}
func (p *Processor) execute(ctx context.Context, text string, document bool, chunkTokens, overlapTokens int, reserved bool) (WorkerResult, error) {
	var empty WorkerResult
	if !p.Available() {
		return empty, failure("KNOWLEDGE_UNAVAILABLE")
	}
	if !utf8.ValidString(text) || strings.TrimSpace(text) == "" || strings.ContainsRune(text, 0) || len(text) > p.policy.SourceBytes || !document && len(text) > p.policy.QueryBytes || chunkTokens < 1 || chunkTokens > p.contract.MaximumChunkTokens || overlapTokens < 0 || overlapTokens >= chunkTokens {
		return empty, failure("VALIDATION_FAILED")
	}
	for _, r := range text {
		if r < 32 && r != '\t' && r != '\r' && r != '\n' {
			return empty, failure("VALIDATION_FAILED")
		}
	}
	if !reserved {
		if !p.reserve() {
			return empty, failure("KNOWLEDGE_BUSY")
		}
		defer p.release()
	}
	operationContext, cancelOperation := context.WithCancel(ctx)
	stopLifecycleWatch := context.AfterFunc(p.lifecycle, cancelOperation)
	defer func() { stopLifecycleWatch(); cancelOperation() }()
	ctx = operationContext
	if e := protected(p.configuration.PythonExecutable, false, true, true); e != nil {
		return empty, e
	}
	if e := protected(p.configuration.WorkerFile, false, false, false); e != nil {
		return empty, e
	}
	if ctx.Err() != nil {
		return empty, failure("KNOWLEDGE_REQUEST_CANCELLED")
	}
	scratch, e := p.createScratch()
	if e != nil {
		return empty, failure("KNOWLEDGE_SCRATCH_UNAVAILABLE")
	}
	defer p.removeScratch(scratch)
	operation := "query"
	seconds := p.policy.QuerySeconds
	if document {
		operation = "document"
		seconds = p.policy.IngestSeconds
	}
	request := workerRequest{SchemaVersion: p.contract.SchemaVersion, Operation: operation, EncoderCatalogSHA256: p.contract.EncoderCatalogSHA256, ModelDirectory: p.configuration.ModelDirectory, Text: text, Limits: WorkerLimits{ChunkBytes: p.policy.ChunkBytes, SourceBytes: p.policy.SourceBytes, SourceTokens: p.policy.SourceTokens, Chunks: p.policy.MaximumChunks, ChunkTokens: chunkTokens, OverlapTokens: overlapTokens, QueryTokens: p.policy.QueryTokens}, Resources: WorkerResources{Threads: p.policy.WorkerThreads, BatchSize: p.policy.WorkerBatchSize}}
	raw, e := json.Marshal(request)
	if e != nil || len(raw) > p.contract.MaximumRequestBytes {
		return empty, failure("KNOWLEDGE_WORKER_REQUEST_INVALID")
	}
	command := exec.Command(p.configuration.PythonExecutable, "-I", "-B", p.configuration.WorkerFile)
	command.Stdin = bytes.NewReader(raw)
	command.Dir = scratch
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	threads := strconv.Itoa(p.policy.WorkerThreads)
	command.Env = []string{"PATH=/usr/bin:/bin", "LANG=C.UTF-8", "LC_ALL=C.UTF-8", "PYTHONNOUSERSITE=1", "PYTHONDONTWRITEBYTECODE=1", "TMPDIR=" + scratch, "HF_HOME=" + filepath.Join(scratch, "huggingface"), "HF_HUB_OFFLINE=1", "TRANSFORMERS_OFFLINE=1", "HF_HUB_DISABLE_TELEMETRY=1", "HF_HUB_DISABLE_PROGRESS_BARS=1", "TOKENIZERS_PARALLELISM=false", "OMP_NUM_THREADS=" + threads, "MKL_NUM_THREADS=" + threads, "OPENBLAS_NUM_THREADS=" + threads, "VECLIB_MAXIMUM_THREADS=" + threads}
	command.Env = append(command.Env, p.contract.ProcessControl.ParentPIDEnvironment+"="+strconv.Itoa(os.Getpid()), p.contract.ProcessControl.WallSecondsEnvironment+"="+strconv.Itoa(seconds+p.policy.ChildStopSeconds), p.contract.ProcessControl.PollMillisecondsEnvironment+"="+strconv.Itoa(p.policy.MemoryPollMilliseconds))
	stdout := &boundedOutput{maximum: p.contract.MaximumResultBytes}
	stderr := &boundedOutput{maximum: p.contract.MaximumDiagnosticBytes}
	command.Stdout = stdout
	command.Stderr = stderr
	if command.Start() != nil {
		return empty, failure("KNOWLEDGE_WORKER_START_FAILED")
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	wall := time.NewTimer(time.Duration(seconds) * time.Second)
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
			stop(command, done, time.Duration(p.policy.ChildStopSeconds)*time.Second)
			return empty, failure("KNOWLEDGE_REQUEST_CANCELLED")
		case <-wall.C:
			stop(command, done, time.Duration(p.policy.ChildStopSeconds)*time.Second)
			return empty, failure("KNOWLEDGE_WORKER_TIMEOUT")
		case <-memory.C:
			resident, probeError := childResidentBytes(ctx, command.Process.Pid, time.Duration(p.policy.MemoryPollMilliseconds)*time.Millisecond)
			if probeError != nil {
				timer := time.NewTimer(time.Duration(p.policy.MemoryPollMilliseconds) * time.Millisecond)
				select {
				case exitError = <-done:
					timer.Stop()
					break running
				case <-timer.C:
				}
				stop(command, done, time.Duration(p.policy.ChildStopSeconds)*time.Second)
				return empty, failure("KNOWLEDGE_WORKER_MEMORY_MONITOR_FAILED")
			}
			if resident > p.policy.WorkerMemoryBytes {
				stop(command, done, time.Duration(p.policy.ChildStopSeconds)*time.Second)
				return empty, failure("KNOWLEDGE_WORKER_MEMORY_EXCEEDED")
			}
		}
	}
	if ctx.Err() != nil {
		return empty, failure("KNOWLEDGE_REQUEST_CANCELLED")
	}
	if stdout.Exceeded() || stderr.Exceeded() {
		return empty, failure("KNOWLEDGE_WORKER_OUTPUT_LIMIT")
	}
	if exitError != nil {
		var diagnostic struct {
			Error string `json:"error"`
		}
		if datawire.DecodeStrict(stderr.Bytes(), &diagnostic) == nil {
			for _, code := range p.contract.Errors {
				if diagnostic.Error == code {
					return empty, failure(code)
				}
			}
		}
		return empty, failure("KNOWLEDGE_WORKER_FAILED")
	}
	var result WorkerResult
	if datawire.DecodeStrict(stdout.Bytes(), &result) != nil || p.validateResult(result, text, document, chunkTokens) != nil {
		return empty, failure("KNOWLEDGE_WORKER_RESULT_INVALID")
	}
	return result, nil
}
func (p *Processor) validateResult(result WorkerResult, text string, document bool, chunkTokens int) error {
	bad := failure("KNOWLEDGE_WORKER_RESULT_INVALID")
	if result.SchemaVersion != p.contract.SchemaVersion || result.EncoderCatalogSHA256 != p.contract.EncoderCatalogSHA256 || result.Dimension != p.contract.Dimension || result.SourceSHA256 != hashBytes([]byte(text)) || len(result.Chunks) < 1 || len(result.Chunks) > p.policy.MaximumChunks || !document && len(result.Chunks) != 1 {
		return bad
	}
	previous := -1
	limit := chunkTokens
	if !document {
		limit = p.policy.QueryTokens
	}
	for i, chunk := range result.Chunks {
		if chunk.Ordinal != i || chunk.StartByte < 0 || chunk.StartByte < previous || chunk.EndByte <= chunk.StartByte || chunk.EndByte > len(text) || chunk.EndByte-chunk.StartByte > p.policy.ChunkBytes || !utf8.ValidString(text[chunk.StartByte:chunk.EndByte]) || chunk.TokenCount < 1 || chunk.TokenCount > limit || len(chunk.Embedding) != p.contract.Dimension {
			return bad
		}
		norm := 0.0
		for _, v := range chunk.Embedding {
			if math.IsNaN(v) || math.IsInf(v, 0) {
				return bad
			}
			norm += v * v
		}
		if math.Abs(math.Sqrt(norm)-1) > p.contract.VectorNormTolerance {
			return bad
		}
		previous = chunk.StartByte
	}
	if result.Chunks[0].StartByte != 0 || result.Chunks[len(result.Chunks)-1].EndByte != len(text) {
		return bad
	}
	return nil
}
