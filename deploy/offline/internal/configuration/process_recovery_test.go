package configuration

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestProcessRecoveryRequestExactJSONAndExplicitEnvironment(t *testing.T) {
	base := ProcessRecoveryRequest{FormatVersion: 1, FailedControlStartOperationID: strings.Repeat("a", 32), Reason: "Explicit synthetic stopped-control recovery"}
	raw, _ := json.Marshal(base)
	if got, e := LoadProcessRecoveryRequest(raw, nil); e != nil || got != base {
		t.Fatal("exact typed recovery input", e)
	}
	overlay := "# Explicit operator-only input\nPROJECT_ONE_OFFLINE_PROCESS_RECOVERY_FAILED_CONTROL_START_OPERATION_ID=" + strings.Repeat("b", 32) + "\nPROJECT_ONE_OFFLINE_PROCESS_RECOVERY_REASON=Resume the same recorded failure\n"
	got, e := LoadProcessRecoveryRequest(raw, []byte(overlay))
	if e != nil || got.FailedControlStartOperationID != strings.Repeat("b", 32) || got.Reason != "Resume the same recorded failure" {
		t.Fatal("explicit overlay precedence", e)
	}
	if _, e = LoadProcessRecoveryRequest([]byte(`{"format_version":1}`), []byte(overlay)); e != nil {
		t.Fatal("required operation fields may come from the explicit overlay", e)
	}
	base.Reason = strings.Repeat("\u754c", maximumProcessRecoveryReasonRunes)
	raw, _ = json.Marshal(base)
	if _, e = LoadProcessRecoveryRequest(raw, nil); e != nil {
		t.Fatal("bounded Unicode operator reason is user content", e)
	}
}

func TestProcessRecoveryRequestInvalidOrAmbiguousInputsAreRedacted(t *testing.T) {
	base := `{"format_version":1,"failed_control_start_operation_id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","reason":"Synthetic recovery"}`
	for name, raw := range map[string]string{
		"missing": `{}`, "null": `null`, "field-null": strings.Replace(base, `"Synthetic recovery"`, `null`, 1), "duplicate": strings.Replace(base, `"format_version":1`, `"format_version":1,"format_version":1`, 1), "unknown": strings.TrimSuffix(base, "}") + `,"SYNTHETIC_PRIVATE_VALUE":true}`, "case": strings.Replace(base, `"reason"`, `"Reason"`, 1), "boolean": strings.Replace(base, `"Synthetic recovery"`, `false`, 1), "array": `[]`, "oversize": strings.Repeat(" ", maximumProcessRecoveryInputBytes+1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, e := LoadProcessRecoveryRequest([]byte(raw), nil); e == nil || strings.Contains(e.Error(), "SYNTHETIC_PRIVATE") {
				t.Fatal("invalid input accepted or disclosed", e)
			}
		})
	}
	for _, change := range []func(*ProcessRecoveryRequest){func(r *ProcessRecoveryRequest) { r.FormatVersion = 2 }, func(r *ProcessRecoveryRequest) { r.FailedControlStartOperationID = "" }, func(r *ProcessRecoveryRequest) { r.FailedControlStartOperationID = strings.Repeat("A", 32) }, func(r *ProcessRecoveryRequest) { r.FailedControlStartOperationID = "control-container-name" }, func(r *ProcessRecoveryRequest) { r.Reason = " " }, func(r *ProcessRecoveryRequest) { r.Reason = "newline\nreason" }, func(r *ProcessRecoveryRequest) { r.Reason = strings.Repeat("x", maximumProcessRecoveryReasonRunes+1) }} {
		r := ProcessRecoveryRequest{FormatVersion: 1, FailedControlStartOperationID: strings.Repeat("a", 32), Reason: "Synthetic recovery"}
		change(&r)
		if r.Validate() == nil {
			t.Fatal("invalid operation reference/reason accepted")
		}
	}
	for _, environment := range []string{"OTHER=SYNTHETIC_PRIVATE_VALUE", "PROJECT_ONE_OFFLINE_PROCESS_RECOVERY_REASON=$(external)", "PROJECT_ONE_OFFLINE_PROCESS_RECOVERY_REASON=one\nPROJECT_ONE_OFFLINE_PROCESS_RECOVERY_REASON=two", "PROJECT_ONE_OFFLINE_PROCESS_RECOVERY_REASON=", "PROJECT_ONE_OFFLINE_PROCESS_RECOVERY_FAILED_CONTROL_START_OPERATION_ID=false", "PROJECT_ONE_OFFLINE_PROCESS_RECOVERY_FORMAT_VERSION=2", "PROJECT_ONE_OFFLINE_PROCESS_RECOVERY_REASON='quoted'"} {
		if _, e := LoadProcessRecoveryRequest([]byte(base), []byte(environment)); e == nil || strings.Contains(e.Error(), "SYNTHETIC_PRIVATE") {
			t.Fatal("invalid explicit environment accepted or disclosed", e)
		}
	}
}
