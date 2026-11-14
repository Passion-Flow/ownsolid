package training

import (
	"context"
	"encoding/binary"
	"io"
	"math"
	"os"
	"sort"
	"strconv"

	wire "github.com/Passion-Flow/ownsolid/api/training/v1"
)

const tensorScanBufferBytes = 65536
const f32Bytes int64 = 4

// BaseShape must come from the owning module's hash-verified frozen base config, never from an adapter's self-description.
type BaseShape struct {
	ModelType      string `json:"model_type"`
	HiddenSize     int64  `json:"hidden_size"`
	Layers         int64  `json:"num_hidden_layers"`
	AttentionHeads int64  `json:"num_attention_heads"`
	KeyValueHeads  int64  `json:"num_key_value_heads"`
	HeadDimension  int64  `json:"head_dim"`
}

func (s BaseShape) Validate() error {
	if s.ModelType != "qwen3" || s.HiddenSize < 1 || s.HiddenSize > 65536 || s.Layers < 1 || s.Layers > 256 || s.AttentionHeads < 1 || s.AttentionHeads > 1024 || s.KeyValueHeads < 1 || s.KeyValueHeads > s.AttentionHeads || s.HeadDimension < 1 || s.HeadDimension > 1024 {
		return fail("TRAINING_BASE_SHAPE_INVALID")
	}
	return nil
}

type tensorHeader struct {
	Dtype   string  `json:"dtype"`
	Shape   []int64 `json:"shape"`
	Offsets []int64 `json:"data_offsets"`
}

// VerifyAdapterTensors checks exact LoRA tensor names/shapes, a complete contiguous F32 buffer and every finite value without loading executable objects.
func VerifyAdapterTensors(ctx context.Context, file *os.File, shape BaseShape, c wire.Configuration, maximumBytes int64) error {
	if file == nil || shape.Validate() != nil || c.Validate() != nil {
		return fail("TRAINING_ADAPTER_INVALID")
	}
	info, e := file.Stat()
	if e != nil || !info.Mode().IsRegular() || info.Size() < 8 || info.Size() > maximumBytes {
		return fail("TRAINING_ADAPTER_INVALID")
	}
	var prefix [8]byte
	if _, e = file.ReadAt(prefix[:], 0); e != nil {
		return fail("TRAINING_ADAPTER_INVALID")
	}
	headerBytes := binary.LittleEndian.Uint64(prefix[:])
	if headerBytes < 2 || headerBytes > wire.MaximumCheckpointMetadataBytes || headerBytes > uint64(info.Size()-8) {
		return fail("TRAINING_ADAPTER_INVALID")
	}
	raw := make([]byte, int(headerBytes))
	if _, e = file.ReadAt(raw, 8); e != nil || raw[0] != '{' {
		return fail("TRAINING_ADAPTER_INVALID")
	}
	expected := map[string][]int64{}
	keys := []string{"__metadata__"}
	for layer := int64(0); layer < shape.Layers; layer++ {
		for _, module := range []string{"q_proj", "v_proj"} {
			for _, matrix := range []string{"A", "B"} {
				name := "base_model.model.model.layers." + strconv.FormatInt(layer, 10) + ".self_attn." + module + ".lora_" + matrix + ".weight"
				dimensions := []int64{c.Rank, shape.HiddenSize}
				if matrix == "B" {
					heads := shape.AttentionHeads
					if module == "v_proj" {
						heads = shape.KeyValueHeads
					}
					dimensions = []int64{heads * shape.HeadDimension, c.Rank}
				}
				expected[name] = dimensions
				keys = append(keys, name)
			}
		}
	}
	values, e := wire.ExactObject(raw, keys)
	if e != nil {
		return fail("TRAINING_ADAPTER_INVALID")
	}
	var metadata struct {
		Format string `json:"format"`
	}
	if wire.DecodeStrict(values["__metadata__"], &metadata) != nil || metadata.Format != "pt" {
		return fail("TRAINING_ADAPTER_INVALID")
	}
	intervals := make([]tensorHeader, 0, len(expected))
	var total int64
	for name, dimensions := range expected {
		var header tensorHeader
		if wire.DecodeStrict(values[name], &header) != nil || header.Dtype != "F32" || len(header.Shape) != 2 || header.Shape[0] != dimensions[0] || header.Shape[1] != dimensions[1] || len(header.Offsets) != 2 {
			return fail("TRAINING_ADAPTER_INVALID")
		}
		bytes := dimensions[0] * dimensions[1] * f32Bytes
		if header.Offsets[0] < 0 || header.Offsets[1] < header.Offsets[0] || header.Offsets[1]-header.Offsets[0] != bytes || header.Offsets[1] > info.Size()-8-int64(headerBytes) {
			return fail("TRAINING_ADAPTER_INVALID")
		}
		total += bytes
		if total > maximumBytes {
			return fail("TRAINING_ADAPTER_INVALID")
		}
		intervals = append(intervals, header)
	}
	sort.Slice(intervals, func(i, j int) bool { return intervals[i].Offsets[0] < intervals[j].Offsets[0] })
	position := int64(0)
	for _, interval := range intervals {
		if interval.Offsets[0] != position {
			return fail("TRAINING_ADAPTER_INVALID")
		}
		position = interval.Offsets[1]
	}
	if position != info.Size()-8-int64(headerBytes) {
		return fail("TRAINING_ADAPTER_INVALID")
	}
	reader := io.NewSectionReader(file, 8+int64(headerBytes), position)
	buffer := make([]byte, tensorScanBufferBytes)
	for position > 0 {
		if e = ctx.Err(); e != nil {
			return e
		}
		length := min(int64(len(buffer)), position)
		if _, e = io.ReadFull(reader, buffer[:length]); e != nil {
			return fail("TRAINING_ADAPTER_INVALID")
		}
		for offset := int64(0); offset < length; offset += f32Bytes {
			value := math.Float32frombits(binary.LittleEndian.Uint32(buffer[offset : offset+f32Bytes]))
			if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
				return fail("TRAINING_ADAPTER_INVALID")
			}
		}
		position -= length
	}
	after, e := file.Stat()
	if e != nil || !os.SameFile(info, after) || info.Size() != after.Size() || !info.ModTime().Equal(after.ModTime()) {
		return fail("TRAINING_ADAPTER_INVALID")
	}
	return nil
}
