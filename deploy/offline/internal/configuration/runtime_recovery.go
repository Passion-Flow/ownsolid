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

const maximumRuntimeRecoveryInputBytes = 1 << 20
const maximumRuntimeRecoveryReasonRunes = 500

var runtimeRecoveryIdentity = regexp.MustCompile(`^[a-f0-9]{32}$`)

type RuntimeAbsenceRecoveryRequest struct {
	FormatVersion            int    `json:"format_version"`
	ScopeID                  string `json:"scope_id"`
	RuntimeCreateOperationID string `json:"runtime_create_operation_id"`
	Reason                   string `json:"reason"`
}

func (r RuntimeAbsenceRecoveryRequest) Validate() error {
	if r.FormatVersion != 1 || !runtimeRecoveryIdentity.MatchString(r.ScopeID) || !runtimeRecoveryIdentity.MatchString(r.RuntimeCreateOperationID) || !utf8.ValidString(r.Reason) || strings.TrimSpace(r.Reason) == "" || utf8.RuneCountInString(r.Reason) > maximumRuntimeRecoveryReasonRunes || strings.ContainsFunc(r.Reason, unicode.IsControl) {
		return errors.New("OFFLINE_OPERATOR_RUNTIME_RECOVERY_CONFIGURATION_INVALID")
	}
	return nil
}

func LoadRuntimeAbsenceRecoveryRequest(raw, environment []byte) (RuntimeAbsenceRecoveryRequest, error) {
	var r RuntimeAbsenceRecoveryRequest
	invalid := errors.New("OFFLINE_OPERATOR_RUNTIME_RECOVERY_CONFIGURATION_INVALID")
	if len(raw) == 0 || len(raw) > maximumRuntimeRecoveryInputBytes || len(environment) > maximumRuntimeRecoveryInputBytes || !utf8.Valid(raw) || !utf8.Valid(environment) || wire.StrictJSON(raw, &r) != nil {
		return r, invalid
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return r, invalid
	}
	for name := range fields {
		if name != "format_version" && name != "scope_id" && name != "runtime_create_operation_id" && name != "reason" {
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
			return r, errors.New("OFFLINE_OPERATOR_RUNTIME_RECOVERY_ENVIRONMENT_INVALID")
		}
		seen[key] = true
		switch key {
		case "PROJECT_ONE_OFFLINE_RUNTIME_RECOVERY_SCOPE_ID":
			r.ScopeID = item
		case "PROJECT_ONE_OFFLINE_RUNTIME_RECOVERY_CREATE_OPERATION_ID":
			r.RuntimeCreateOperationID = item
		case "PROJECT_ONE_OFFLINE_RUNTIME_RECOVERY_REASON":
			r.Reason = item
		default:
			return r, errors.New("OFFLINE_OPERATOR_RUNTIME_RECOVERY_ENVIRONMENT_INVALID")
		}
	}
	return r, r.Validate()
}
