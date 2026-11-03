package training_test

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/training"
	wire "github.com/Passion-Flow/ownsolid/api/training/v1"
)

func tensorFixture(t *testing.T, mutation string) (*os.File, training.BaseShape, wire.Configuration) {
	t.Helper()
	shape := training.BaseShape{ModelType: "qwen3", HiddenSize: 16, Layers: 1, AttentionHeads: 2, KeyValueHeads: 1, HeadDimension: 8}
	configuration := wire.DefaultConfiguration()
	configuration.Rank = 4
	header := map[string]any{"__metadata__": map[string]string{"format": "pt"}}
	var names []string
	for _, module := range []string{"q_proj", "v_proj"} {
		for _, matrix := range []string{"A", "B"} {
			names = append(names, "base_model.model.model.layers.0.self_attn."+module+".lora_"+matrix+".weight")
		}
	}
	sort.Strings(names)
	position := int64(0)
	for _, name := range names {
		dimensions := []int64{4, 16}
		if name[len(name)-8:] != "A.weight" {
			heads := shape.AttentionHeads
			if name == "base_model.model.model.layers.0.self_attn.v_proj.lora_B.weight" {
				heads = shape.KeyValueHeads
			}
			dimensions = []int64{heads * 8, 4}
		}
		bytes := dimensions[0] * dimensions[1] * 4
		header[name] = map[string]any{"dtype": "F32", "shape": dimensions, "data_offsets": []int64{position, position + bytes}}
		position += bytes
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
	raw, _ := json.Marshal(header)
	if mutation == "duplicate_key" {
		raw = append([]byte(`{"__metadata__":{"format":"pt"},`), raw[1:]...)
	}
	prefix := make([]byte, 8)
	binary.LittleEndian.PutUint64(prefix, uint64(len(raw)))
	if mutation == "oversized_header" {
		binary.LittleEndian.PutUint64(prefix, 1<<30)
	}
	path := filepath.Join(t.TempDir(), "synthetic.safetensors")
	file, e := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { file.Close() })
	file.Write(prefix)
	file.Write(raw)
	file.Write(data)
	file.Sync()
	return file, shape, configuration
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
