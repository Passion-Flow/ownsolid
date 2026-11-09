package datasets

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	agentwire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	wire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	trainingwire "github.com/Passion-Flow/ownsolid/api/training/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/models"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

const (
	maximumTokenizerPreviewRows     = 8
	maximumTokenizerPreviewBytes    = 64 * 1024
	maximumTokenizerPreviewEnvelope = 512 * 1024
)

//go:embed tokenizer_preview.py
var tokenizerPreviewProgram string

var tokenizerPreviewAssets = [...]string{"config.json", "merges.txt", "tokenizer.json", "tokenizer_config.json", "vocab.json"}

// TokenizerPreviewRequest selects a ready model and a bounded first page of draft samples.
type TokenizerPreviewRequest struct {
	ModelID       string `json:"model_id"`
	SampleLimit   int    `json:"sample_limit"`
	MaximumTokens int64  `json:"maximum_tokens"`
}

// TokenizerPreviewSample reports training tokenization and the first excluded token, if any.
type TokenizerPreviewSample struct {
	LineNumber            int64  `json:"line_number"`
	ContentSHA256         string `json:"content_sha256"`
	RenderedPrompt        string `json:"rendered_prompt"`
	Completion            string `json:"completion"`
	EOS                   string `json:"eos"`
	PromptTokens          int64  `json:"prompt_tokens"`
	CompletionTokens      int64  `json:"completion_tokens"`
	TotalTokens           int64  `json:"total_tokens"`
	FirstExcessTokenIndex *int64 `json:"first_excess_token_index,omitempty"`
	FirstExcessSection    string `json:"first_excess_section,omitempty"`
}

// TokenizerPreviewResult binds the sample counts to the admitted model catalogue and budget.
type TokenizerPreviewResult struct {
	ModelCatalogSHA256 string                   `json:"model_catalog_sha256"`
	MaximumTokens      int64                    `json:"maximum_tokens"`
	Samples            []TokenizerPreviewSample `json:"samples"`
}

type tokenizerPreviewRow struct {
	LineNumber int64 `json:"line_number"`
	wire.Record
}

// TokenizerPreviewConfiguration identifies the separately installed preview runtime.
type TokenizerPreviewConfiguration struct {
	PythonExecutable string
	TrainingRoot     string
	ScratchRoot      string
	Resources        wire.Resources
}

// TokenizerPreviewProcessor owns the optional installed runtime and one concurrent preview.
type TokenizerPreviewProcessor struct {
	processor     *Processor
	models        *models.Service
	configuration TokenizerPreviewConfiguration
	slot          chan struct{}
}

// NewTokenizerPreviewProcessor selects trusted installed code and a private scratch directory.
func NewTokenizerPreviewProcessor(processor *Processor, modelSource *models.Service, configuration TokenizerPreviewConfiguration) (*TokenizerPreviewProcessor, error) {
	python, trainingRoot, scratchRoot := configuration.PythonExecutable, configuration.TrainingRoot, configuration.ScratchRoot
	if processor == nil || modelSource == nil || modelSource.RootDirectory == "" || configuration.Resources.Validate() != nil ||
		!filepath.IsAbs(trainingRoot) || filepath.Clean(trainingRoot) != trainingRoot ||
		!filepath.IsAbs(python) || filepath.Clean(python) != python ||
		trustedProgram(python, true) != nil ||
		trustedProgram(filepath.Join(trainingRoot, "projectone_training", "preview.py"), false) != nil {
		return nil, failure("DATASET_STORAGE_UNAVAILABLE")
	}
	if _, e := privatePath(scratchRoot, "", true, 0); e != nil {
		return nil, failure("DATASET_STORAGE_UNAVAILABLE")
	}
	for _, durable := range []string{processor.configuration.ArtifactRoot, modelSource.RootDirectory} {
		if scratchRoot == durable || strings.HasPrefix(scratchRoot, durable+string(filepath.Separator)) ||
			strings.HasPrefix(durable, scratchRoot+string(filepath.Separator)) {
			return nil, failure("DATASET_STORAGE_UNAVAILABLE")
		}
	}
	for _, name := range tokenizerPreviewAssets {
		asset, ok := agentwire.ModelFileForName(name)
		if !ok || asset.SizeBytes > configuration.Resources.MaximumResultBytes {
			return nil, failure("DATASET_STORAGE_UNAVAILABLE")
		}
	}
	return &TokenizerPreviewProcessor{
		processor: processor, models: modelSource, configuration: configuration, slot: make(chan struct{}, 1),
	}, nil
}

// PreviewDraftTokenizer reads authorized draft rows and previews the ready model in the same workspace.
func (s *Service) PreviewDraftTokenizer(ctx context.Context, a identity.Auth, workspace, dataset, draft string, input TokenizerPreviewRequest, worker *TokenizerPreviewProcessor) (TokenizerPreviewResult, error) {
	var out TokenizerPreviewResult
	configuration := trainingwire.DefaultConfiguration()
	configuration.MaximumTokens = input.MaximumTokens
	if !wire.ValidID(input.ModelID) || input.SampleLimit < 1 || input.SampleLimit > maximumTokenizerPreviewRows ||
		configuration.Validate() != nil || input.MaximumTokens > int64(agentwire.ApprovedModel().MaxContext) {
		return out, failure("VALIDATION_FAILED")
	}
	if worker == nil || worker.processor != s.Processor {
		return out, failure("DATASET_STORAGE_UNAVAILABLE")
	}
	page, e := s.RowPage(ctx, a, workspace, dataset, draft, url.Values{"limit": {strconv.Itoa(input.SampleLimit)}})
	if e != nil {
		return out, e
	}
	rows := make([]tokenizerPreviewRow, 0, len(page.Items))
	digests := make([]string, 0, len(page.Items))
	remaining := maximumTokenizerPreviewBytes
	for _, row := range page.Items {
		if row.ErrorCode != nil || row.SourceID == nil || row.Prompt == nil || row.Completion == nil {
			return out, failure("DATASET_VALIDATION_FAILED")
		}
		value := wire.Record{SourceID: *row.SourceID, Prompt: *row.Prompt, Completion: *row.Completion}
		remaining -= len(value.SourceID) + len(value.Prompt) + len(value.Completion)
		if remaining < 0 {
			return out, failure("PAYLOAD_TOO_LARGE")
		}
		digest, e := value.ContentDigest(s.policy().Limits)
		if e != nil {
			return out, failure("DATASET_VALIDATION_FAILED")
		}
		rows = append(rows, tokenizerPreviewRow{row.LineNumber, value})
		digests = append(digests, digest)
	}
	if len(rows) == 0 {
		return out, failure("DATASET_EMPTY_SOURCE")
	}
	select {
	case worker.slot <- struct{}{}:
		defer func() { <-worker.slot }()
	default:
		return out, failure("RATE_LIMITED")
	}
	if _, e := privatePath(worker.configuration.ScratchRoot, "", true, 0); e != nil {
		return out, failure("DATASET_STORAGE_UNAVAILABLE")
	}
	scope, e := os.MkdirTemp(worker.configuration.ScratchRoot, "preview-")
	if e != nil {
		return out, failure("DATASET_STORAGE_UNAVAILABLE")
	}
	defer os.RemoveAll(scope)
	modelPath := filepath.Join(scope, "model")
	if e = os.Mkdir(modelPath, 0700); e != nil {
		return out, failure("DATASET_STORAGE_UNAVAILABLE")
	}
	e = s.transaction(ctx, a, workspace, false, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace) error {
		for _, name := range tokenizerPreviewAssets {
			asset, ok := agentwire.ModelFileForName(name)
			if !ok {
				return failure("DATASET_STORAGE_UNAVAILABLE")
			}
			if e := worker.stageAsset(ctx, tx, w, input.ModelID, modelPath, asset); e != nil {
				return e
			}
		}
		return nil
	})
	if e != nil {
		return out, e
	}
	return worker.execute(ctx, scope, input.MaximumTokens, rows, digests)
}

func (p *TokenizerPreviewProcessor) stageAsset(ctx context.Context, tx transaction.Executor, workspace workspaces.Workspace, model, path string, asset agentwire.ModelFile) error {
	target, e := os.OpenFile(filepath.Join(path, asset.Path), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return failure("DATASET_STORAGE_UNAVAILABLE")
	}
	defer target.Close()
	hash := sha256.New()
	output := io.MultiWriter(target, hash)
	for offset := int64(0); offset < asset.SizeBytes; offset += agentwire.ModelTransferChunkBytes {
		length := min(agentwire.ModelTransferChunkBytes, asset.SizeBytes-offset)
		download, e := p.models.OpenTransferWithin(ctx, tx, workspace.TenantID, workspace.ID, model,
			agentwire.ModelFileRequest{File: asset.Path, Offset: offset, Length: length})
		if e != nil {
			switch e.Error() {
			case "RESOURCE_NOT_FOUND":
				return e
			case "MODEL_FILE_INVALID", "MODEL_FILE_CHANGED":
				return failure("DATASET_FILE_INVALID")
			default:
				return failure("DATASET_STORAGE_UNAVAILABLE")
			}
		}
		if download.File == nil {
			return failure("DATASET_FILE_INVALID")
		}
		n, copyError := io.Copy(output, io.NewSectionReader(download.File, download.Offset, download.Size))
		closeError := download.File.Close()
		if copyError != nil || closeError != nil || n != length || download.Offset != offset {
			return failure("DATASET_FILE_INVALID")
		}
	}
	if hex.EncodeToString(hash.Sum(nil)) != asset.SHA256 {
		return failure("DATASET_FILE_INVALID")
	}
	return nil
}

func (p *TokenizerPreviewProcessor) execute(ctx context.Context, scope string, budget int64, rows []tokenizerPreviewRow, digests []string) (TokenizerPreviewResult, error) {
	var out TokenizerPreviewResult
	if trustedProgram(p.configuration.PythonExecutable, true) != nil ||
		trustedProgram(filepath.Join(p.configuration.TrainingRoot, "projectone_training", "preview.py"), false) != nil ||
		trustedProgram(p.processor.configuration.ProcessorFile, false) != nil {
		return out, failure("DATASET_STORAGE_UNAVAILABLE")
	}
	resources := p.configuration.Resources
	catalogue := agentwire.ModelCatalogDigest()
	raw, e := json.Marshal(struct {
		ModelCatalogSHA256 string                `json:"model_catalog_sha256"`
		MaximumTokens      int64                 `json:"maximum_tokens"`
		Rows               []tokenizerPreviewRow `json:"rows"`
		Resources          wire.Resources        `json:"resources"`
	}{catalogue, budget, rows, resources})
	if e != nil || len(raw) > maximumTokenizerPreviewEnvelope {
		return out, failure("PAYLOAD_TOO_LARGE")
	}
	request := filepath.Join(scope, "request.json")
	if e = writeNew(request, raw); e != nil {
		return out, e
	}
	command := exec.Command(p.configuration.PythonExecutable, "-I", "-B", "-c", tokenizerPreviewProgram,
		filepath.Dir(p.processor.configuration.ProcessorFile), p.configuration.TrainingRoot, request)
	exitError, e := p.processor.run(ctx, command, scope, resources,
		[]string{"OPENBLAS_NUM_THREADS=1", "OMP_NUM_THREADS=1", "TOKENIZERS_PARALLELISM=false"})
	if e != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return out, failure("DATASET_PROCESSOR_TIMEOUT")
		}
		return out, e
	}
	result := filepath.Join(scope, "result.json")
	if _, e = privatePath(result, scope, false, maximumTokenizerPreviewEnvelope); e != nil {
		return out, failure("DATASET_STORAGE_UNAVAILABLE")
	}
	file, e := os.Open(result)
	if e != nil {
		return out, failure("DATASET_STORAGE_UNAVAILABLE")
	}
	raw, e = io.ReadAll(io.LimitReader(file, maximumTokenizerPreviewEnvelope+1))
	file.Close()
	var response struct {
		Preview   *TokenizerPreviewResult `json:"preview,omitempty"`
		ErrorCode string                  `json:"error_code,omitempty"`
	}
	if e != nil || len(raw) > maximumTokenizerPreviewEnvelope || wire.DecodeStrict(raw, &response) != nil {
		return out, failure("DATASET_DATA_INVALID")
	}
	if exit, ok := exitError.(*exec.ExitError); ok && exit.ExitCode() == 1 && response.Preview == nil {
		switch response.ErrorCode {
		case "TOKENIZER_PREVIEW_SAMPLE_INVALID", "TOKENIZER_PREVIEW_SAMPLE_LIMIT", "TRAINING_RESERVED_TOKEN", "TRAINING_EMPTY_TARGET":
			return out, failure("DATASET_VALIDATION_FAILED")
		case "TOKENIZER_PREVIEW_ASSET_CHANGED", "TOKENIZER_PREVIEW_ASSET_UNAVAILABLE":
			return out, failure("DATASET_FILE_INVALID")
		case "DATASET_PROCESSOR_TIMEOUT":
			return out, failure(response.ErrorCode)
		default:
			return out, failure("DATASET_STORAGE_UNAVAILABLE")
		}
	}
	value := response.Preview
	if exitError != nil || response.ErrorCode != "" || value == nil ||
		value.ModelCatalogSHA256 != catalogue || value.MaximumTokens != budget || len(value.Samples) != len(rows) {
		return out, failure("DATASET_DATA_INVALID")
	}
	for i, sample := range value.Samples {
		if sample.LineNumber != rows[i].LineNumber || sample.ContentSHA256 != digests[i] ||
			sample.Completion != rows[i].Completion || sample.RenderedPrompt == "" || sample.EOS == "" ||
			!validPreviewCounts(sample, budget) {
			return out, failure("DATASET_DATA_INVALID")
		}
	}
	return *value, nil
}

func validPreviewCounts(sample TokenizerPreviewSample, budget int64) bool {
	if sample.PromptTokens < 1 || sample.CompletionTokens < 2 ||
		sample.TotalTokens > maximumTokenizerPreviewEnvelope ||
		sample.PromptTokens >= sample.TotalTokens || sample.CompletionTokens != sample.TotalTokens-sample.PromptTokens {
		return false
	}
	if sample.TotalTokens <= budget {
		return sample.FirstExcessTokenIndex == nil && sample.FirstExcessSection == ""
	}
	section := "completion"
	if budget < sample.PromptTokens {
		section = "prompt"
	} else if budget == sample.TotalTokens-1 {
		section = "eos"
	}
	return sample.FirstExcessTokenIndex != nil && *sample.FirstExcessTokenIndex == budget && sample.FirstExcessSection == section
}
