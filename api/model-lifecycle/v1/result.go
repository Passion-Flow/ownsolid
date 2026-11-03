package v1

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"unicode/utf8"

	dataset "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	training "github.com/Passion-Flow/ownsolid/api/training/v1"
)

type Prediction struct {
	Label                *string `json:"predicted_label"`
	SHA256               string  `json:"output_sha256"`
	GeneratedTokens      int64   `json:"generated_tokens"`
	DurationMilliseconds int64   `json:"duration_milliseconds"`
}
type Score struct {
	Correct   int64     `json:"correct"`
	Rows      int64     `json:"row_count"`
	Confusion [][]int64 `json:"confusion"`
}
type EvaluationCase struct {
	SourceID     string     `json:"source_id"`
	PromptSHA256 string     `json:"prompt_sha256"`
	Expected     string     `json:"expected_label"`
	Base         Prediction `json:"base"`
	Candidate    Prediction `json:"candidate"`
}
type SerializedAliases struct {
	LMHeadWeight string `json:"lm_head.weight"`
}
type PreparedResult struct {
	SchemaVersion               int                        `json:"schema_version"`
	WorkerVersion               string                     `json:"worker_version"`
	Status                      string                     `json:"status"`
	RecipeID                    string                     `json:"recipe_id"`
	RecipeSHA256                string                     `json:"recipe_sha256"`
	Configuration               Configuration              `json:"configuration"`
	ConfigurationSHA256         string                     `json:"configuration_sha256"`
	ModelCatalogSHA256          string                     `json:"model_catalog_sha256"`
	AdapterFiles                []dataset.Asset            `json:"adapter_files"`
	HeldoutSHA256               string                     `json:"heldout_sha256"`
	TrainingConfigurationSHA256 string                     `json:"training_configuration_sha256"`
	InstructionSHA256           string                     `json:"instruction_sha256"`
	ChatTemplateSHA256          string                     `json:"chat_template_sha256"`
	Packages                    training.Packages          `json:"packages"`
	Device                      string                     `json:"device_profile"`
	ComputationDtype            string                     `json:"computation_dtype"`
	StorageDtype                string                     `json:"storage_dtype"`
	RowCount                    int64                      `json:"row_count"`
	Base                        Score                      `json:"base"`
	Candidate                   Score                      `json:"candidate"`
	Cases                       []EvaluationCase           `json:"cases"`
	ModelFiles                  []dataset.Asset            `json:"model_files"`
	ChangedTargets              []string                   `json:"changed_target_tensors"`
	Aliases                     SerializedAliases          `json:"tied_weight_aliases"`
	GatePassed                  bool                       `json:"gate_passed"`
	TenantID                    string                     `json:"tenant_id"`
	WorkspaceID                 string                     `json:"workspace_id"`
	PreparationID               string                     `json:"preparation_id"`
	AttemptID                   string                     `json:"attempt_id"`
	TrainingJobID               string                     `json:"training_job_id"`
	TrainingAttemptID           string                     `json:"training_attempt_id"`
	HeldoutID                   string                     `json:"heldout_id"`
	Identity                    training.ExecutionIdentity `json:"execution_identity"`
	Resources                   Resources                  `json:"resources"`
	MemoryEnforcement           string                     `json:"memory_enforcement"`
	EventCount                  int64                      `json:"event_count"`
	RequestSHA256               string                     `json:"request_sha256"`
}
type FailedResult struct {
	SchemaVersion int    `json:"schema_version"`
	WorkerVersion string `json:"worker_version"`
	Status        string `json:"status"`
	PreparationID string `json:"preparation_id"`
	AttemptID     string `json:"attempt_id"`
	ErrorCode     string `json:"error_code"`
}
type ExpectedCase struct {
	SourceID     string
	PromptSHA256 string
	Label        string
}
type ResultExpectation struct {
	Request            ProcessorRequest
	RequestBytes       []byte
	Cases              []ExpectedCase
	BaseFiles          []dataset.Asset
	ChatTemplateSHA256 string
}

func reportError() error { return errors.New("MODEL_PREPARATION_REPORT_INVALID") }
func labelIndex(value string) int {
	for index, label := range Labels() {
		if value == label {
			return index
		}
	}
	return -1
}
func blankScore(rows int64) Score {
	return Score{Rows: rows, Confusion: [][]int64{{0, 0, 0, 0}, {0, 0, 0, 0}, {0, 0, 0, 0}}}
}
func increment(score *Score, expected int, prediction Prediction, configuration Configuration, wall int64) error {
	actual := len(Labels())
	if prediction.Label != nil {
		actual = labelIndex(*prediction.Label)
		if actual < 0 {
			return reportError()
		}
	}
	if !dataset.ValidDigest(prediction.SHA256) || prediction.GeneratedTokens < 1 || prediction.GeneratedTokens > configuration.MaximumNewTokens || prediction.DurationMilliseconds < 0 || prediction.DurationMilliseconds > wall*1000 {
		return reportError()
	}
	score.Confusion[expected][actual]++
	if actual == expected {
		score.Correct++
	}
	return nil
}
func DecodeFailedResult(raw []byte, request ProcessorRequest) (FailedResult, error) {
	var result FailedResult
	if len(raw) > int(request.Resources.MaximumResultBytes) || request.Validate() != nil || DecodeStrict(raw, &result) != nil || result.SchemaVersion != 1 || result.WorkerVersion != WorkerVersion || result.PreparationID != request.PreparationID || result.AttemptID != request.AttemptID || !WorkerErrorSupported(result.ErrorCode) || (result.Status != "failed" && result.Status != "cancelled") || (result.Status == "cancelled") != (result.ErrorCode == "MODEL_PREPARATION_CANCELLED") {
		return result, reportError()
	}
	return result, nil
}

// DecodePreparedResult recomputes report semantics. The owning module must additionally stream-verify every actual artifact and current authority before accepting it.
func DecodePreparedResult(raw []byte, expected ResultExpectation) (PreparedResult, error) {
	var result PreparedResult
	request := expected.Request
	decoded, e := DecodeProcessorRequest(expected.RequestBytes)
	if e != nil || !reflect.DeepEqual(decoded, request) || len(raw) > int(request.Resources.MaximumResultBytes) || DecodeStrict(raw, &result) != nil {
		return result, reportError()
	}
	requestSum := sha256.Sum256(expected.RequestBytes)
	config, e := request.Configuration.Digest()
	if e != nil {
		return result, reportError()
	}
	train, e := request.TrainingConfiguration.Digest()
	if e != nil {
		return result, reportError()
	}
	if result.SchemaVersion != 1 || result.WorkerVersion != WorkerVersion || result.Status != "prepared" || result.RecipeID != RecipeID || result.RecipeSHA256 != RecipeDigest() || result.Configuration != request.Configuration || result.ConfigurationSHA256 != config || result.ModelCatalogSHA256 != request.ModelCatalogSHA256 || !reflect.DeepEqual(result.AdapterFiles, request.AdapterFiles) || result.HeldoutSHA256 != request.HeldoutSHA256 || result.TrainingConfigurationSHA256 != train || result.InstructionSHA256 != InstructionDigest() || result.ChatTemplateSHA256 != expected.ChatTemplateSHA256 || !dataset.ValidDigest(result.ChatTemplateSHA256) || result.Packages != training.PinnedPackages() || result.Device != request.Configuration.Device || result.StorageDtype != "BF16" || result.TenantID != request.TenantID || result.WorkspaceID != request.WorkspaceID || result.PreparationID != request.PreparationID || result.AttemptID != request.AttemptID || result.TrainingJobID != request.TrainingJobID || result.TrainingAttemptID != request.TrainingAttemptID || result.HeldoutID != request.HeldoutID || !reflect.DeepEqual(result.Identity, request.Identity) || result.Resources != request.Resources || result.RequestSHA256 != hex.EncodeToString(requestSum[:]) || result.Aliases.LMHeadWeight != TiedWeightAliases()["lm_head.weight"] {
		return result, reportError()
	}
	dtype, enforcement := "bfloat16", "linux_cgroup_memory_cpu_fsize_wall"
	if request.Configuration.Device == training.VerificationProfile {
		dtype, enforcement = "float32", "native_cpu_fsize_wall_parent_RSS_required_verification"
		var fields map[string]json.RawMessage
		json.Unmarshal(raw, &fields)
		if _, e := training.ExactObject(fields["execution_identity"], []string{"profile", "python", "worker_source_sha256"}); e != nil {
			return result, reportError()
		}
	}
	if result.ComputationDtype != dtype || result.MemoryEnforcement != enforcement || result.EventCount != 2*result.RowCount+3 || result.EventCount > MaximumEvents || result.RowCount < MinimumRows || result.RowCount > MaximumRows || result.RowCount != int64(len(expected.Cases)) || len(result.Cases) != len(expected.Cases) {
		return result, reportError()
	}
	baseline, candidate := blankScore(result.RowCount), blankScore(result.RowCount)
	labels := map[string]bool{}
	prompts := map[string]bool{}
	var duration int64
	for index, item := range result.Cases {
		wanted := expected.Cases[index]
		label := labelIndex(wanted.Label)
		if label < 0 || !utf8.ValidString(wanted.SourceID) || wanted.SourceID == "" || int64(len(wanted.SourceID)) > request.DataLimits.MaximumSourceBytes || !dataset.ValidDigest(wanted.PromptSHA256) || prompts[wanted.PromptSHA256] || item.SourceID != wanted.SourceID || item.PromptSHA256 != wanted.PromptSHA256 || item.Expected != wanted.Label {
			return result, reportError()
		}
		prompts[wanted.PromptSHA256] = true
		labels[wanted.Label] = true
		if increment(&baseline, label, item.Base, request.Configuration, request.Resources.WallSeconds) != nil || increment(&candidate, label, item.Candidate, request.Configuration, request.Resources.WallSeconds) != nil {
			return result, reportError()
		}
		duration += item.Base.DurationMilliseconds + item.Candidate.DurationMilliseconds
	}
	if len(labels) != len(Labels()) || duration > request.Resources.WallSeconds*1000 || !reflect.DeepEqual(result.Base, baseline) || !reflect.DeepEqual(result.Candidate, candidate) {
		return result, reportError()
	}
	passed, e := QualityGate(baseline.Correct, candidate.Correct, result.RowCount, request.Configuration.MinimumAccuracyBasisPoints, request.Configuration.MaximumRegressionBasisPoints)
	if e != nil || result.GatePassed != passed {
		return result, reportError()
	}
	if len(result.ModelFiles) != len(expected.BaseFiles) || int64(len(result.ModelFiles)) > request.Resources.MaximumOutputFiles || len(result.ChangedTargets) < 1 || len(result.ChangedTargets) > MaximumChangedTargetTensors || !sort.StringsAreSorted(result.ChangedTargets) {
		return result, reportError()
	}
	base := map[string]dataset.Asset{}
	for _, asset := range expected.BaseFiles {
		if base[asset.Path].Path != "" || !dataset.ValidDigest(asset.SHA256) || asset.SizeBytes < 1 {
			return result, reportError()
		}
		base[asset.Path] = asset
	}
	seen := map[string]bool{}
	var total int64
	for _, asset := range result.ModelFiles {
		original, present := base[asset.Path]
		if !present || seen[asset.Path] || asset.SizeBytes < 1 || asset.SizeBytes > request.Resources.MaximumOutputBytes || !dataset.ValidDigest(asset.SHA256) {
			return result, reportError()
		}
		seen[asset.Path] = true
		total += asset.SizeBytes
		if asset.Path != "model.safetensors" && asset != original {
			return result, reportError()
		}
		if asset.Path == "model.safetensors" && asset.SHA256 == original.SHA256 {
			return result, reportError()
		}
	}
	if total > request.Resources.MaximumOutputBytes {
		return result, reportError()
	}
	for index, name := range result.ChangedTargets {
		if !targetTensorName.MatchString(name) || index > 0 && name == result.ChangedTargets[index-1] {
			return result, reportError()
		}
	}
	return result, nil
}
