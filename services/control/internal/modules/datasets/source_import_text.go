package datasets

import (
	"bufio"
	"bytes"
	"encoding/csv"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"errors"
	"io"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"
)

var errSourceRecordLimit = errors.New("DATASET_IMPORT_RECORD_LIMIT")

// recordBudget permits parser read-ahead, but the next read cannot grow its
// working buffer past one record measured from the parser's consumed offset.
type recordBudget struct {
	reader io.Reader
	read   int64
	end    int64
}

func (r *recordBudget) Read(p []byte) (int, error) {
	available := r.end - r.read
	if available <= 0 {
		return 0, errSourceRecordLimit
	}
	n, err := r.reader.Read(p[:min(int64(len(p)), available)])
	r.read += int64(n)
	return n, err
}

func readSourceLine(reader *bufio.Reader, reuse []byte, limit int) ([]byte, error) {
	raw := reuse[:0]
	overflow := false
	for {
		part, err := reader.ReadSlice('\n')
		if !overflow && len(raw)+len(part) <= limit+2 {
			raw = append(raw, part...)
		} else {
			overflow = true
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if err != nil && !errors.Is(err, io.EOF) {
			return raw, err
		}
		if overflow {
			return raw, errSourceRecordLimit
		}
		if len(raw) == 0 && err != nil {
			return raw, err
		}
		raw = bytes.TrimSuffix(raw, []byte("\n"))
		raw = bytes.TrimSuffix(raw, []byte("\r"))
		if len(raw) > limit {
			return raw, errSourceRecordLimit
		}
		return raw, nil
	}
}

func (s *sourceImportState) readJSONL(source io.Reader) error {
	reader := bufio.NewReader(source)
	var raw []byte
	for line := int64(1); ; line++ {
		var err error
		raw, err = readSourceLine(reader, raw, s.limits.RecordBytes)
		if errors.Is(err, io.EOF) {
			return nil
		}
		position := SourceIssue{Line: line}
		if errors.Is(err, errSourceRecordLimit) {
			position.Code = errSourceRecordLimit.Error()
			err = s.reject(position)
		} else if err == nil {
			err = s.acceptJSONObject(raw, position)
		} else {
			position.Code = "DATASET_IMPORT_SOURCE_READ_FAILED"
			return position
		}
		if err != nil {
			return err
		}
	}
}

func (s *sourceImportState) acceptJSONObject(raw []byte, position SourceIssue) error {
	var row map[string]jsontext.Value
	if err := json.Unmarshal(raw, &row); err != nil || row == nil {
		position.Code = "DATASET_JSON_INVALID"
		return s.reject(position)
	}
	return s.accept(row, position)
}

func (s *sourceImportState) readJSONArray(source io.Reader) error {
	window := int64(s.limits.RecordBytes)
	budget := &recordBudget{reader: source, end: window + 1}
	reader := jsontext.NewDecoder(budget)
	first, err := reader.ReadToken()
	if err != nil || first.Kind() != jsontext.KindBeginArray {
		return arrayIssue(err, 1)
	}
	for row := int64(1); ; row++ {
		start := reader.InputOffset()
		budget.end = start + window + 1
		if reader.PeekKind() == jsontext.KindEndArray {
			if _, err := reader.ReadToken(); err != nil {
				return arrayIssue(err, row)
			}
			budget.end = reader.InputOffset() + window + 1
			if _, err := reader.ReadToken(); !errors.Is(err, io.EOF) {
				return arrayIssue(err, row)
			}
			return nil
		}
		raw, err := reader.ReadValue()
		if err != nil {
			return arrayIssue(err, row)
		}
		if reader.InputOffset()-start > window {
			return arrayIssue(errSourceRecordLimit, row)
		}
		if err = s.acceptJSONObject(raw, SourceIssue{Row: row}); err != nil {
			return err
		}
	}
}

func arrayIssue(err error, row int64) SourceIssue {
	if errors.Is(err, errSourceRecordLimit) {
		return SourceIssue{Code: errSourceRecordLimit.Error(), Row: row}
	}
	return SourceIssue{Code: "DATASET_JSON_ARRAY_INVALID", Row: row}
}

func (s *sourceImportState) readCSV(source io.Reader) error {
	buffer := bufio.NewReader(source)
	prefix, err := buffer.Peek(4)
	if err != nil && !errors.Is(err, io.EOF) {
		return failure("DATASET_IMPORT_SOURCE_READ_FAILED")
	}
	switch {
	case bytes.HasPrefix(prefix, []byte{0xef, 0xbb, 0xbf}):
		if s.opts.CSVEncoding == "gbk" {
			return SourceIssue{Code: "DATASET_CSV_ENCODING_CONFLICT", Line: 1}
		}
		_, _ = buffer.Discard(3)
	case bytes.HasPrefix(prefix, []byte{0xff, 0xfe}),
		bytes.HasPrefix(prefix, []byte{0xfe, 0xff}),
		bytes.HasPrefix(prefix, []byte{0, 0, 0xfe, 0xff}):
		return SourceIssue{Code: "DATASET_IMPORT_ENCODING_UNSUPPORTED", Line: 1}
	}

	budget := &recordBudget{reader: buffer}
	reader := csv.NewReader(budget)
	reader.ReuseRecord = true
	s.schema = true
	var header []string
	var nextLine int64 = 1
	for {
		start := reader.InputOffset()
		budget.end = start + int64(s.limits.RecordBytes) + 1
		record, err := reader.Read()
		if errors.Is(err, io.EOF) {
			return nil
		}
		line := nextLine
		var parse *csv.ParseError
		if errors.As(err, &parse) {
			line = int64(parse.StartLine)
		} else if len(record) != 0 {
			startLine, _ := reader.FieldPos(0)
			line = int64(startLine)
		}
		if errors.Is(err, errSourceRecordLimit) || reader.InputOffset()-start > int64(s.limits.RecordBytes) {
			return SourceIssue{Code: errSourceRecordLimit.Error(), Line: line}
		}
		if err != nil && !errors.Is(err, csv.ErrFieldCount) {
			return SourceIssue{Code: "DATASET_CSV_INVALID", Line: line}
		}
		nextLine = line + 1
		for index, value := range record {
			fieldLine, _ := reader.FieldPos(index)
			nextLine = int64(fieldLine + strings.Count(value, "\n") + 1)
			decoded, valid := decodeCSVField(value, s.opts.CSVEncoding)
			if !valid {
				code := "DATASET_CSV_UTF8_INVALID_SELECT_ENCODING"
				if s.opts.CSVEncoding == "gbk" {
					code = "DATASET_CSV_GBK_INVALID"
				}
				return SourceIssue{Code: code, Line: int64(fieldLine)}
			}
			record[index] = decoded
		}
		if err != nil {
			if err = s.reject(SourceIssue{Code: "DATASET_CSV_FIELD_COUNT", Line: line}); err != nil {
				return err
			}
			continue
		}
		if header == nil {
			if len(record) > s.limits.Fields {
				return SourceIssue{Code: "DATASET_IMPORT_FIELD_LIMIT", Line: line}
			}
			header = append([]string(nil), record...)
			for _, name := range header {
				if strings.TrimSpace(name) == "" || len(name) > importFieldNameBytes || s.fields[name] != nil {
					return SourceIssue{Code: "DATASET_CSV_HEADER_INVALID", Line: line}
				}
				s.fields[name] = &sourceFieldCount{types: []string{"string"}}
			}
			continue
		}
		row := make(map[string]jsontext.Value, len(record))
		for index, value := range record {
			if value == "" {
				row[header[index]] = jsontext.Value("null")
				continue
			}
			quoted, err := jsontext.AppendQuote(nil, value)
			if err != nil {
				return SourceIssue{Code: "DATASET_CSV_INVALID", Line: line}
			}
			row[header[index]] = quoted
		}
		if err = s.accept(row, SourceIssue{Line: line}); err != nil {
			return err
		}
	}
}

func decodeCSVField(raw, encoding string) (string, bool) {
	if strings.ContainsRune(raw, 0) {
		return "", false
	}
	if encoding != "gbk" {
		return raw, utf8.ValidString(raw)
	}
	decoded, err := simplifiedchinese.GBK.NewDecoder().String(raw)
	return decoded, err == nil && !strings.ContainsRune(decoded, utf8.RuneError)
}
