package datasets

import (
	"bufio"
	"context"
	json "encoding/json/v2"
	"errors"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
)

// ParquetImportConfig selects the protected offline decoder and bounds its
// batches, footer, decompressed row groups and Linux address space.
type ParquetImportConfig struct {
	PythonExecutable string
	DecoderFile      string
	BatchRows        int
	MetadataBytes    int
	RowGroupBytes    int64
	MemoryBytes      int64
}

func (c ParquetImportConfig) validate() error {
	if c.BatchRows < 1 || c.BatchRows > math.MaxInt32 ||
		c.MetadataBytes < 1 || c.MetadataBytes > math.MaxInt32 || c.RowGroupBytes < 1 ||
		c.MemoryBytes < int64(c.MetadataBytes) || c.MemoryBytes < c.RowGroupBytes ||
		!filepath.IsAbs(c.PythonExecutable) || !filepath.IsAbs(c.DecoderFile) ||
		trustedProgram(c.PythonExecutable, true) != nil || trustedProgram(c.DecoderFile, false) != nil {
		return failure("DATASET_PARQUET_CONFIGURATION_INVALID")
	}
	return nil
}

// An original owned by the draft caller is retained on success. Other
// callers use a temporary seekable file with the same decoder and limits.
func (s *sourceImportState) readParquet(source io.Reader, directory string, cfg ParquetImportConfig, original *os.File) error {
	raw := original
	if raw == nil {
		var err error
		raw, err = os.CreateTemp(directory, ".dataset-parquet-")
		if err != nil {
			return failure("DATASET_STORAGE_UNAVAILABLE")
		}
		defer os.Remove(raw.Name())
		defer raw.Close()
	}
	size, err := io.Copy(raw, source)
	if err != nil {
		return failure("DATASET_PARQUET_STAGE_FAILED")
	}
	if original == nil && raw.Close() != nil {
		return failure("DATASET_PARQUET_STAGE_FAILED")
	}
	if size > s.limits.SourceBytes {
		return failure("DATASET_FILE_TOO_LARGE")
	}
	if s.opts.ExpectedBytes > 0 && size != s.opts.ExpectedBytes {
		return failure("DATASET_SOURCE_CHANGED")
	}

	ctx, cancel := context.WithCancel(s.ctx)
	defer cancel()
	cmd := exec.CommandContext(ctx, cfg.PythonExecutable, "-I", "-B", cfg.DecoderFile, raw.Name(),
		strconv.Itoa(cfg.BatchRows), strconv.Itoa(cfg.MetadataBytes), strconv.FormatInt(cfg.RowGroupBytes, 10),
		strconv.FormatInt(cfg.MemoryBytes, 10), strconv.Itoa(s.limits.RecordBytes), strconv.Itoa(s.limits.Fields))
	cmd.Env = []string{"LC_ALL=C", "ARROW_DEFAULT_MEMORY_POOL=system", "OPENBLAS_NUM_THREADS=1", "OMP_NUM_THREADS=1"}
	stream, err := cmd.StdoutPipe()
	if err != nil || cmd.Start() != nil {
		return failure("DATASET_PARQUET_RUNTIME_UNAVAILABLE")
	}
	waited := false
	defer func() {
		if !waited {
			cancel()
			_ = cmd.Wait()
		}
	}()
	reader := bufio.NewReader(stream)
	header, err := readSourceLine(reader, nil, s.limits.RecordBytes)
	var schema map[string]string
	switch {
	case err == nil:
		if json.Unmarshal(header, &schema) != nil || len(schema) == 0 || len(schema) > s.limits.Fields {
			return failure("DATASET_PARQUET_SCHEMA_INVALID")
		}
	case errors.Is(err, io.EOF):
		waited = true
		return parquetExit(cmd.Wait(), 0)
	default:
		return failure("DATASET_PARQUET_SCHEMA_INVALID")
	}
	s.schema = true
	for name, kind := range schema {
		if len(name) > importFieldNameBytes || kind == "" || len(kind) > importFieldNameBytes {
			return failure("DATASET_PARQUET_SCHEMA_INVALID")
		}
		s.fields[name] = &sourceFieldCount{types: []string{kind}}
	}
	var record []byte
	for row := int64(1); ; row++ {
		record, err = readSourceLine(reader, record, s.limits.RecordBytes)
		if errors.Is(err, io.EOF) {
			waited = true
			if err = cmd.Wait(); err != nil {
				return parquetExit(err, row)
			}
			return nil
		}
		position := SourceIssue{Row: row}
		if err != nil {
			position.Code = "DATASET_PARQUET_STREAM_INVALID"
			return position
		}
		if len(record) == 1 && record[0] == '!' {
			position.Code = "DATASET_PARQUET_ROW_INVALID"
			err = s.reject(position)
		} else {
			err = s.acceptJSONObject(record, position)
		}
		if err != nil {
			return err
		}
	}
}

func parquetExit(err error, row int64) error {
	code := "DATASET_PARQUET_INVALID"
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		switch exit.ExitCode() {
		case 2:
			code = "DATASET_PARQUET_RUNTIME_UNAVAILABLE"
		case 4:
			code = "DATASET_PARQUET_RESOURCE_LIMIT"
		}
	}
	return SourceIssue{Code: code, Row: row}
}
