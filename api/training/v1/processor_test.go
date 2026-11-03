package v1

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	dataset "github.com/Passion-Flow/ownsolid/api/datasets/v1"
)

func TestActualPythonConfigurationAndWorkerSourceHashVectors(t *testing.T) {
	raw, e := os.ReadFile("../../../tests/fixtures/training/configuration-v1.json")
	if e != nil {
		t.Fatal(e)
	}
	var vectors struct {
		Configurations []struct {
			Name          string
			Configuration Configuration
			SHA256        string
		} `json:"configurations"`
		WorkerSourceFiles  map[string]string `json:"worker_source_files"`
		WorkerSourceSHA256 string            `json:"worker_source_sha256"`
	}
	if e = json.Unmarshal(raw, &vectors); e != nil {
		t.Fatal(e)
	}
	for _, v := range vectors.Configurations {
		t.Run(v.Name, func(t *testing.T) {
			got, e := v.Configuration.Digest()
			if e != nil || got != v.SHA256 {
				t.Fatalf("cross-language configuration digest %s %v", got, e)
			}
		})
	}
	got, e := WorkerSourceDigest(vectors.WorkerSourceFiles)
	if e != nil || got != vectors.WorkerSourceSHA256 {
		t.Fatal("cross-language source-map digest", e)
	}
}

func validRequest() ProcessorRequest {
	job := "00000000-0000-4000-8000-000000000001"
	attempt := "00000000-0000-4000-8000-000000000002"
	scope := filepath.Join("/synthetic/jobs", job, attempt)
	c := DefaultConfiguration()
	c.Device = VerificationProfile
	return ProcessorRequest{SchemaVersion: 1, Operation: "train", TenantID: "00000000-0000-4000-8000-000000000003", WorkspaceID: "00000000-0000-4000-8000-000000000004", JobID: job, AttemptID: attempt, DatasetVersionID: "00000000-0000-4000-8000-000000000005", DatasetManifestSHA256: strings.Repeat("a", 64), ScopeDirectory: scope, ModelDirectory: "/synthetic/approved-model", ModelCatalogSHA256: strings.Repeat("b", 64), TrainFile: filepath.Join(scope, "inputs/train.jsonl"), TrainSHA256: strings.Repeat("c", 64), ValidationFile: filepath.Join(scope, "inputs/validation.jsonl"), ValidationSHA256: strings.Repeat("d", 64), OutputDirectory: filepath.Join(scope, "output"), Configuration: c, DataLimits: dataset.Limits{MaximumBytes: 1048576, MaximumRows: 100, MaximumLineBytes: 65536, MaximumTextBytes: 16384, MaximumSourceBytes: 512, MaximumErrors: 10}, Resources: DefaultResources(), ExecutionIdentity: ExecutionIdentity{Profile: "native_cpu_verification", Python: PythonVersion, WorkerSourceSHA256: strings.Repeat("e", 64)}}
}

func TestStrictNullableWorkerRequestAndScope(t *testing.T) {
	r := validRequest()
	raw, e := json.Marshal(r)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = DecodeRequest(raw); e != nil {
		t.Fatal("exact nullable request", e)
	}
	for name, change := range map[string]func([]byte) []byte{
		"duplicate":  func(b []byte) []byte { return append([]byte(`{"schema_version":1,`), b[1:]...) },
		"wrong_case": func(b []byte) []byte { return []byte(strings.Replace(string(b), `"job_id":`, `"Job_id":`, 1)) },
		"unknown":    func(b []byte) []byte { return append([]byte(`{"unknown":true,`), b[1:]...) },
		"null_configuration": func(b []byte) []byte {
			var v map[string]json.RawMessage
			json.Unmarshal(b, &v)
			v["configuration"] = json.RawMessage("null")
			out, _ := json.Marshal(v)
			return out
		},
		"null_native_image": func(b []byte) []byte {
			return []byte(strings.Replace(string(b), `"execution_identity":{`, `"execution_identity":{"image_id":null,`, 1))
		},
		"surrogate": func(b []byte) []byte {
			return []byte(strings.Replace(string(b), `"train_file":"`, `"train_file":"\ud800`, 1))
		},
	} {
		t.Run(name, func(t *testing.T) {
			if _, e := DecodeRequest(change(raw)); e == nil {
				t.Fatal("unsafe request accepted")
			}
		})
	}
	r.TrainFile = "/synthetic/foreign/train.jsonl"
	if r.Validate() == nil {
		t.Fatal("foreign input path")
	}
	r = validRequest()
	r.ResumeCheckpoint = &CheckpointReference{Directory: "/synthetic/foreign/checkpoint", ReceiptSHA256: strings.Repeat("a", 64)}
	if r.Validate() == nil {
		t.Fatal("foreign checkpoint path")
	}
	r = validRequest()
	r.ExecutionIdentity.Profile = ProductionProfile
	if r.Validate() == nil {
		t.Fatal("native profile disguised as NVIDIA")
	}
}

func TestTrainingConfigurationAndResourceBounds(t *testing.T) {
	for name, change := range map[string]func(*Configuration){
		"zero_interval": func(c *Configuration) { c.CheckpointSteps = 0 }, "too_many_checkpoints": func(c *Configuration) { c.MaximumSteps = 1000; c.CheckpointSteps = 1 },
		"decimal_exponent": func(c *Configuration) { c.LearningRate = "2e-4" }, "rate_range": func(c *Configuration) { c.LearningRate = "0.1" },
		"rank": func(c *Configuration) { c.Rank = 3 }, "dropout": func(c *Configuration) { c.Dropout = "0.6" }, "device": func(c *Configuration) { c.Device = "auto" },
	} {
		t.Run(name, func(t *testing.T) {
			v := DefaultConfiguration()
			change(&v)
			if v.Validate() == nil {
				t.Fatal("invalid configuration")
			}
		})
	}
	r := DefaultResources()
	if r.Validate() != nil {
		t.Fatal("generated defaults")
	}
	r.MemoryBytes = 0
	if r.Validate() == nil {
		t.Fatal("unbounded memory")
	}
	if _, e := WorkerSourceDigest(map[string]string{"../escape.py": strings.Repeat("a", 64)}); e == nil {
		t.Fatal("unsafe logical worker source")
	}
}
