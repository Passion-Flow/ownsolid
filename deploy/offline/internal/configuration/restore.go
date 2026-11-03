package configuration

import (
	"bytes"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

type RestoreRequest struct {
	RestoreSourceRequest
	Reason            string `json:"reason"`
	CompatibilityFile string `json:"compatibility_file,omitempty"`
}

func (r RestoreRequest) Validate() error {
	if r.RestoreSourceRequest.Validate() != nil || !utf8.ValidString(r.Reason) || strings.TrimSpace(r.Reason) == "" || utf8.RuneCountInString(r.Reason) > 500 || strings.ContainsFunc(r.Reason, unicode.IsControl) {
		return errors.New("OFFLINE_OPERATOR_RESTORE_CONFIGURATION_INVALID")
	}
	if r.CompatibilityFile != "" && (!filepath.IsAbs(r.CompatibilityFile) || filepath.Clean(r.CompatibilityFile) != r.CompatibilityFile || len(r.CompatibilityFile) > 1024 || !utf8.ValidString(r.CompatibilityFile) || strings.ContainsFunc(r.CompatibilityFile, unicode.IsControl)) {
		return errors.New("OFFLINE_OPERATOR_RESTORE_CONFIGURATION_INVALID")
	}
	return nil
}

func LoadRestoreRequest(raw, environment []byte) (RestoreRequest, error) {
	r := RestoreRequest{RestoreSourceRequest: RestoreSourceDefaults()}
	if wire.StrictJSON(raw, &r) != nil {
		return r, errors.New("OFFLINE_OPERATOR_RESTORE_CONFIGURATION_INVALID")
	}
	sourceEnv := []byte{}
	seenReason := false
	seenCompatibility := false
	for _, line := range bytes.Split(environment, []byte("\n")) {
		s := strings.TrimSpace(string(line))
		key, value, ok := strings.Cut(s, "=")
		if key == "PROJECT_ONE_OFFLINE_RESTORE_REASON" {
			if !ok || seenReason || strings.ContainsAny(value, "\x00\r\n$`\"'") {
				return r, errors.New("OFFLINE_OPERATOR_RESTORE_ENVIRONMENT_INVALID")
			}
			r.Reason = value
			seenReason = true
		} else if key == "PROJECT_ONE_OFFLINE_RESTORE_COMPATIBILITY_FILE" {
			if !ok || seenCompatibility || value == "" || strings.ContainsAny(value, "\x00\r\n$`\"'") {
				return r, errors.New("OFFLINE_OPERATOR_RESTORE_ENVIRONMENT_INVALID")
			}
			r.CompatibilityFile = value
			seenCompatibility = true
		} else {
			sourceEnv = append(sourceEnv, line...)
			sourceEnv = append(sourceEnv, '\n')
		}
	}
	// The source loader remains the sole authority for its explicit env keys.
	// Encoding only its typed fields avoids a second configuration source.
	sourceRaw, _ := json.Marshal(r.RestoreSourceRequest)
	var e error
	r.RestoreSourceRequest, e = LoadRestoreSourceRequest(sourceRaw, sourceEnv)
	if e != nil {
		return r, e
	}
	return r, r.Validate()
}
