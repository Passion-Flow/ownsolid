package training_test

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	wire "github.com/Passion-Flow/ownsolid/api/training/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/training"
)

func tensorFixture(t *testing.T, mutation string) (*os.File, training.BaseShape, wire.Configuration) {
	t.Helper()
	shape := training.BaseShape{ModelType: "qwen3", HiddenSize: 16, Layers: 1, AttentionHeads: 2, KeyValueHeads: 1, HeadDimension: 8}
	configuration := wire.DefaultConfiguration()
	configuration.Rank = 4
	path := filepath.Join(t.TempDir(), "synthetic.safetensors")
	if e := os.WriteFile(path, syntheticAdapterTensors(t, shape, configuration, mutation), 0600); e != nil {
		t.Fatal(e)
	}
	file, e := os.Open(path)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { file.Close() })
	return file, shape, configuration
}

func syntheticAdapterTensors(t *testing.T, shape training.BaseShape, c wire.Configuration, mutation string) []byte {
	t.Helper()
	header := map[string]any{"__metadata__": map[string]string{"format": "pt"}}
	var names []string
	var position int64
	for layer := int64(0); layer < shape.Layers; layer++ {
		for _, module := range []string{"q_proj", "v_proj"} {
			heads := shape.AttentionHeads
			if module == "v_proj" {
				heads = shape.KeyValueHeads
			}
			for _, matrix := range []string{"A", "B"} {
				name := "base_model.model.model.layers." + strconv.FormatInt(layer, 10) + ".self_attn." + module + ".lora_" + matrix + ".weight"
				dimensions := []int64{c.Rank, shape.HiddenSize}
				if matrix == "B" {
					dimensions = []int64{heads * shape.HeadDimension, c.Rank}
				}
				size := dimensions[0] * dimensions[1] * 4
				header[name] = map[string]any{"dtype": "F32", "shape": dimensions, "data_offsets": []int64{position, position + size}}
				names = append(names, name)
				position += size
			}
		}
	}
	data := make([]byte, position)
	switch mutation {
	case "nan":
		binary.LittleEndian.PutUint32(data, math.Float32bits(float32(math.NaN())))
	case "infinity":
		binary.LittleEndian.PutUint32(data, math.Float32bits(float32(math.Inf(1))))
	case "dtype":
		header[names[0]].(map[string]any)["dtype"] = "BF16"
	case "shape":
		header[names[0]].(map[string]any)["shape"] = []int64{4, 17}
	case "overlap":
		header[names[1]].(map[string]any)["data_offsets"] = []int64{0, 256}
	case "hole":
		header[names[0]].(map[string]any)["data_offsets"] = []int64{4, 260}
	case "unknown_tensor":
		header["arbitrary.weight"] = header[names[0]]
	case "metadata":
		header["__metadata__"] = map[string]any{"format": "pt", "code": "REJECT"}
	case "trailing_payload":
		data = append(data, 0, 0, 0, 0)
	}
	raw, e := json.Marshal(header)
	if e != nil {
		t.Fatal(e)
	}
	if mutation == "duplicate_key" {
		raw = append([]byte(`{"__metadata__":{"format":"pt"},`), raw[1:]...)
	}
	prefix := make([]byte, 8)
	binary.LittleEndian.PutUint64(prefix, uint64(len(raw)))
	if mutation == "oversized_header" {
		binary.LittleEndian.PutUint64(prefix, 1<<30)
	}
	return append(append(prefix, raw...), data...)
}
func TestTrainingF32AdapterTensorEnvelopeAndFiniteValues(t *testing.T) {
	file, shape, configuration := tensorFixture(t, "")
	if e := training.VerifyAdapterTensors(context.Background(), file, shape, configuration, 1048576); e != nil {
		t.Fatal("exact finite synthetic tensor envelope", e)
	}
	for _, mutation := range []string{"nan", "infinity", "dtype", "shape", "overlap", "hole", "unknown_tensor", "metadata", "trailing_payload", "duplicate_key", "oversized_header"} {
		t.Run(mutation, func(t *testing.T) {
			file, shape, c := tensorFixture(t, mutation)
			if e := training.VerifyAdapterTensors(context.Background(), file, shape, c, 1048576); e == nil {
				t.Fatal("invalid tensor envelope accepted")
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if e := training.VerifyAdapterTensors(ctx, file, shape, configuration, 1048576); e == nil {
		t.Fatal("cancelled tensor scan continued")
	}
}
func TestTrainingPinnedPeftMetadataRejectsUnknownBehaviorAndPrivateBasePaths(t *testing.T) {
	raw, e := os.ReadFile("../../../../../api/training/v1/adapter-template.json")
	if e != nil {
		t.Fatal(e)
	}
	c := wire.DefaultConfiguration()
	c.Rank = 4
	if e = wire.ValidateAdapterConfiguration(raw, c, "Qwen/Qwen3-0.6B"); e != nil {
		t.Fatal("actual pinned PEFT template", e)
	}
	for name, change := range map[string]func(map[string]any){"private_base": func(v map[string]any) { v["base_model_name_or_path"] = "/private/operator/model" }, "auto_mapping": func(v map[string]any) { v["auto_mapping"] = map[string]any{"custom_class": "REJECT"} }, "dora": func(v map[string]any) { v["use_dora"] = true }, "modules": func(v map[string]any) { v["target_modules"] = []string{"q_proj", "q_proj"} }, "rank": func(v map[string]any) { v["r"] = 8 }, "missing": func(v map[string]any) { delete(v, "peft_version") }, "unknown": func(v map[string]any) { v["unknown"] = true }, "dropout": func(v map[string]any) { v["lora_dropout"] = 0.5 }, "mutable_revision": func(v map[string]any) { v["revision"] = "main" }} {
		t.Run(name, func(t *testing.T) {
			if e := wire.ValidateAdapterConfiguration(mutateMetadata(t, raw, change), c, "Qwen/Qwen3-0.6B"); e == nil {
				t.Fatal("unsupported PEFT metadata accepted")
			}
		})
	}
	if e = wire.ValidateAdapterConfiguration([]byte(`{"r":`+strconv.FormatInt(c.Rank, 10)+`,"r":4}`), c, "Qwen/Qwen3-0.6B"); e == nil {
		t.Fatal("duplicate metadata accepted")
	}
}
func mutateMetadata(t *testing.T, raw []byte, change func(map[string]any)) []byte {
	t.Helper()
	var values map[string]any
	if json.Unmarshal(raw, &values) != nil {
		t.Fatal("fixture JSON")
	}
	change(values)
	out, e := json.Marshal(values)
	if e != nil {
		t.Fatal(e)
	}
	return out
}
