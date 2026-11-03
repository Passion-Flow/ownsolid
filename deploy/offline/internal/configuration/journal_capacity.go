package configuration

import (
	"bytes"
	"errors"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// These bounds also cap journal read memory before effective-policy validation.
const MinimumJournalBytes int64 = 16384
const MaximumJournalBytes int64 = 16 << 20

type JournalCapacityRequest struct {
	FormatVersion        int    `json:"format_version"`
	ExpectedCurrentBytes int64  `json:"expected_current_bytes"`
	MaximumJournalBytes  int64  `json:"maximum_journal_bytes"`
	Reason               string `json:"reason"`
}

func (r JournalCapacityRequest) Validate() error {
	if r.FormatVersion != 1 || r.ExpectedCurrentBytes < MinimumJournalBytes || r.ExpectedCurrentBytes >= MaximumJournalBytes || r.MaximumJournalBytes <= r.ExpectedCurrentBytes || r.MaximumJournalBytes > MaximumJournalBytes || !utf8.ValidString(r.Reason) || strings.TrimSpace(r.Reason) == "" || utf8.RuneCountInString(r.Reason) > 500 || strings.ContainsFunc(r.Reason, unicode.IsControl) {
		return errors.New("OFFLINE_OPERATOR_JOURNAL_CAPACITY_CONFIGURATION_INVALID")
	}
	return nil
}

func LoadJournalCapacityRequest(raw, environment []byte) (JournalCapacityRequest, error) {
	var r JournalCapacityRequest
	if wire.StrictJSON(raw, &r) != nil {
		return r, errors.New("OFFLINE_OPERATOR_JOURNAL_CAPACITY_CONFIGURATION_INVALID")
	}
	seen := map[string]bool{}
	for _, line := range bytes.Split(environment, []byte("\n")) {
		s := strings.TrimSpace(string(line))
		if s == "" || strings.HasPrefix(s, "#") {
			continue
		}
		key, value, ok := strings.Cut(s, "=")
		if !ok || seen[key] || strings.ContainsAny(value, "\x00\r\n$`\"'") {
			return r, errors.New("OFFLINE_OPERATOR_JOURNAL_CAPACITY_ENVIRONMENT_INVALID")
		}
		seen[key] = true
		if key == "PROJECT_ONE_OFFLINE_JOURNAL_CAPACITY_REASON" {
			r.Reason = value
			continue
		}
		n, e := strconv.ParseInt(value, 10, 64)
		if e != nil {
			return r, errors.New("OFFLINE_OPERATOR_JOURNAL_CAPACITY_ENVIRONMENT_INVALID")
		}
		switch key {
		case "PROJECT_ONE_OFFLINE_JOURNAL_EXPECTED_CURRENT_BYTES":
			r.ExpectedCurrentBytes = n
		case "PROJECT_ONE_OFFLINE_JOURNAL_MAXIMUM_BYTES":
			r.MaximumJournalBytes = n
		default:
			return r, errors.New("OFFLINE_OPERATOR_JOURNAL_CAPACITY_ENVIRONMENT_INVALID")
		}
	}
	return r, r.Validate()
}
