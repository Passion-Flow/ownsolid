// Package modelartifact verifies immutable model bytes shared by control and node consumers.
package modelartifact

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
)

func fail(code string) error { return errors.New(code) }

type contextReader struct {
	ctx    context.Context
	source io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if e := r.ctx.Err(); e != nil {
		return 0, e
	}
	return r.source.Read(p)
}

func VerifyDirectory(ctx context.Context, directory string) error {
	info, e := os.Lstat(directory)
	if e != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fail("MODEL_PATH_INVALID")
	}
	files, e := os.ReadDir(directory)
	if e != nil || len(files) != len(wire.ApprovedModel().Files) {
		return fail("MODEL_FILE_SET_INVALID")
	}
	for _, item := range files {
		expected, ok := wire.ModelFileForName(item.Name())
		if !ok {
			return fail("MODEL_FILE_SET_INVALID")
		}
		path := filepath.Join(directory, item.Name())
		before, e := os.Lstat(path)
		if e != nil || !before.Mode().IsRegular() || before.Mode().Perm()&0111 != 0 || before.Size() != expected.SizeBytes {
			return fail("MODEL_FILE_INVALID")
		}
		file, e := os.Open(path)
		if e != nil {
			return fail("MODEL_FILE_INVALID")
		}
		opened, e := file.Stat()
		if e != nil || !os.SameFile(before, opened) {
			file.Close()
			return fail("MODEL_FILE_CHANGED")
		}
		hash := sha256.New()
		n, copyError := io.Copy(hash, contextReader{ctx, io.LimitReader(file, expected.SizeBytes+1)})
		if copyError != nil {
			file.Close()
			if ctx.Err() != nil {
				return fail("MODEL_CANCELLED")
			}
			return fail("MODEL_FILE_INVALID")
		}
		if n != expected.SizeBytes || hex.EncodeToString(hash.Sum(nil)) != expected.SHA256 {
			file.Close()
			return fail("MODEL_DIGEST_MISMATCH")
		}
		if item.Name() == "model.safetensors" {
			_, e = file.Seek(0, io.SeekStart)
			if e == nil {
				e = VerifySafetensors(file, expected.SizeBytes)
			}
		}
		after, statError := file.Stat()
		file.Close()
		if e != nil {
			return e
		}
		if statError != nil || after.Size() != before.Size() || !after.ModTime().Equal(before.ModTime()) {
			return fail("MODEL_FILE_CHANGED")
		}
	}
	return verifyConfiguration(directory)
}
func verifyConfiguration(directory string) error {
	read := func(name string, target any) error {
		b, e := os.ReadFile(filepath.Join(directory, name))
		if e != nil || !utf8.Valid(b) || json.Unmarshal(b, target) != nil {
			return fail("MODEL_CONFIGURATION_INVALID")
		}
		return nil
	}
	var config struct {
		ModelType     string          `json:"model_type"`
		Architectures []string        `json:"architectures"`
		VocabSize     int             `json:"vocab_size"`
		HiddenSize    int             `json:"hidden_size"`
		Layers        int             `json:"num_hidden_layers"`
		AutoMap       json.RawMessage `json:"auto_map"`
	}
	if e := read("config.json", &config); e != nil {
		return e
	}
	if config.ModelType != "qwen3" || len(config.Architectures) != 1 || config.Architectures[0] != "Qwen3ForCausalLM" || config.VocabSize != 151936 || config.HiddenSize != 1024 || config.Layers != 28 || len(config.AutoMap) != 0 {
		return fail("MODEL_CONFIGURATION_INVALID")
	}
	var tokenizer struct {
		ChatTemplate   string          `json:"chat_template"`
		TokenizerClass string          `json:"tokenizer_class"`
		AutoMap        json.RawMessage `json:"auto_map"`
	}
	if e := read("tokenizer_config.json", &tokenizer); e != nil {
		return e
	}
	if !strings.Contains(tokenizer.ChatTemplate, "<|im_start|>") || !strings.Contains(tokenizer.ChatTemplate, "messages") || len(tokenizer.ChatTemplate) > 32768 || tokenizer.TokenizerClass != "Qwen2Tokenizer" || len(tokenizer.AutoMap) != 0 {
		return fail("MODEL_TOKENIZER_INVALID")
	}
	var vocabulary map[string]int
	if e := read("vocab.json", &vocabulary); e != nil {
		return e
	}
	if len(vocabulary) < 150000 || len(vocabulary) > 151936 {
		return fail("MODEL_TOKENIZER_INVALID")
	}
	var tokenizerBody struct {
		Model struct {
			Type  string         `json:"type"`
			Vocab map[string]int `json:"vocab"`
		} `json:"model"`
	}
	if e := read("tokenizer.json", &tokenizerBody); e != nil {
		return e
	}
	if tokenizerBody.Model.Type != "BPE" || len(tokenizerBody.Model.Vocab) != len(vocabulary) {
		return fail("MODEL_TOKENIZER_INVALID")
	}
	for token, id := range vocabulary {
		if value, ok := tokenizerBody.Model.Vocab[token]; !ok || value != id {
			return fail("MODEL_TOKENIZER_INVALID")
		}
	}
	var generation struct {
		BOS int   `json:"bos_token_id"`
		EOS []int `json:"eos_token_id"`
	}
	if e := read("generation_config.json", &generation); e != nil {
		return e
	}
	if generation.BOS != 151643 || len(generation.EOS) != 2 || generation.EOS[0] != 151645 || generation.EOS[1] != 151643 {
		return fail("MODEL_CONFIGURATION_INVALID")
	}
	return nil
}

// VerifySafetensors checks bounded JSON/header structure and the complete contiguous tensor layout.
// Tensor values and model quality are not inferred from this structural check.
func VerifySafetensors(source io.Reader, size int64) error {
	invalid := fail("MODEL_SAFETENSORS_INVALID")
	var prefix [8]byte
	if _, e := io.ReadFull(source, prefix[:]); e != nil {
		return invalid
	}
	n := binary.LittleEndian.Uint64(prefix[:])
	if n < 2 || n > 1024*1024 || size < 10 || n > uint64(size-8) {
		return invalid
	}
	buffer := make([]byte, int(n))
	if _, e := io.ReadFull(source, buffer); e != nil || buffer[0] != '{' || !utf8.Valid(buffer) {
		return invalid
	}
	decoder := json.NewDecoder(strings.NewReader(string(buffer)))
	if token, e := decoder.Token(); e != nil || token != json.Delim('{') {
		return invalid
	}
	type tensor struct {
		DType   string  `json:"dtype"`
		Shape   []int64 `json:"shape"`
		Offsets []int64 `json:"data_offsets"`
	}
	type interval struct{ start, end int64 }
	intervals := []interval{}
	names := map[string]bool{}
	for decoder.More() {
		token, e := decoder.Token()
		name, ok := token.(string)
		if e != nil || !ok || name == "" || len(name) > 1024 || names[name] || len(names) > 4096 {
			return invalid
		}
		names[name] = true
		if name == "__metadata__" {
			var metadata map[string]string
			if decoder.Decode(&metadata) != nil {
				return invalid
			}
			continue
		}
		var value tensor
		if decoder.Decode(&value) != nil || len(value.Offsets) != 2 || len(value.Shape) > 8 {
			return invalid
		}
		width := int64(0)
		switch value.DType {
		case "BF16", "F16":
			width = 2
		case "F32":
			width = 4
		default:
			return invalid
		}
		elements := int64(1)
		for _, dimension := range value.Shape {
			if dimension < 0 || dimension > size || dimension != 0 && elements > (size/width)/dimension {
				return invalid
			}
			elements *= dimension
		}
		if value.Offsets[0] < 0 || value.Offsets[1] < value.Offsets[0] || value.Offsets[1] > size-8-int64(n) || value.Offsets[1]-value.Offsets[0] != elements*width {
			return invalid
		}
		intervals = append(intervals, interval{value.Offsets[0], value.Offsets[1]})
	}
	if token, e := decoder.Token(); e != nil || token != json.Delim('}') {
		return invalid
	}
	if decoder.Decode(new(any)) != io.EOF || len(intervals) == 0 {
		return invalid
	}
	sort.Slice(intervals, func(i, j int) bool { return intervals[i].start < intervals[j].start })
	end := int64(0)
	for _, item := range intervals {
		if item.start != end {
			return invalid
		}
		end = item.end
	}
	if end != size-8-int64(n) {
		return invalid
	}
	return nil
}
