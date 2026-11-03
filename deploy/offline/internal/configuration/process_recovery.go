package configuration

import (
	"bytes"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

const maximumProcessRecoveryInputBytes = 1 << 20
const maximumProcessRecoveryReasonRunes = 500

var processRecoveryOperationPattern = regexp.MustCompile(`^[a-f0-9]{32}$`)

// ProcessRecoveryRequest names a recorded failed operation, never a runtime ID
// or a caller-selected scope. Runtime identity is resolved from protected state.
type ProcessRecoveryRequest struct {
	FormatVersion                 int    `json:"format_version"`
	FailedControlStartOperationID string `json:"failed_control_start_operation_id"`
	Reason                        string `json:"reason"`
}

func (r ProcessRecoveryRequest) Validate() error {
	if r.FormatVersion != 1 || !processRecoveryOperationPattern.MatchString(r.FailedControlStartOperationID) || !utf8.ValidString(r.Reason) || strings.TrimSpace(r.Reason) == "" || utf8.RuneCountInString(r.Reason) > maximumProcessRecoveryReasonRunes || strings.ContainsFunc(r.Reason, unicode.IsControl) {
		return errors.New("OFFLINE_OPERATOR_PROCESS_RECOVERY_CONFIGURATION_INVALID")
	}
	return nil
}

func LoadProcessRecoveryRequest(raw, environment []byte) (ProcessRecoveryRequest, error) {
	var r ProcessRecoveryRequest
	invalid := errors.New("OFFLINE_OPERATOR_PROCESS_RECOVERY_CONFIGURATION_INVALID")
	if len(raw) == 0 || len(raw) > maximumProcessRecoveryInputBytes || len(environment) > maximumProcessRecoveryInputBytes || !utf8.Valid(raw) || !utf8.Valid(environment) || wire.StrictJSON(raw, &r) != nil {
		return r, invalid
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return r, invalid
	}
	for name := range fields {
		if name != "format_version" && name != "failed_control_start_operation_id" && name != "reason" {
			return r, invalid
		}
	}
	seen := map[string]bool{}
	for _, line := range bytes.Split(environment, []byte("\n")) {
		value := strings.TrimSpace(string(line))
		if value == "" || strings.HasPrefix(value, "#") {
			continue
		}
		key, item, ok := strings.Cut(value, "=")
		if !ok || seen[key] || strings.ContainsAny(item, "\x00\r\n$`\"'") {
			return r, errors.New("OFFLINE_OPERATOR_PROCESS_RECOVERY_ENVIRONMENT_INVALID")
		}
		seen[key] = true
		switch key {
		case "PROJECT_ONE_OFFLINE_PROCESS_RECOVERY_FAILED_CONTROL_START_OPERATION_ID":
			r.FailedControlStartOperationID = item
		case "PROJECT_ONE_OFFLINE_PROCESS_RECOVERY_REASON":
			r.Reason = item
		default:
			return r, errors.New("OFFLINE_OPERATOR_PROCESS_RECOVERY_ENVIRONMENT_INVALID")
		}
	}
	return r, r.Validate()
}
