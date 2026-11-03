package configuration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRuntimeAbsenceRecoveryRequestExactJSONAndExplicitEnvironment(t *testing.T) {
	base := RuntimeAbsenceRecoveryRequest{FormatVersion: 1, ScopeID: strings.Repeat("c", 32), RuntimeCreateOperationID: strings.Repeat("a", 32), Reason: "Explicit synthetic stopped-control recovery"}
	raw, _ := json.Marshal(base)
	if got, e := LoadRuntimeAbsenceRecoveryRequest(raw, nil); e != nil || got != base {
		t.Fatal("exact typed recovery input", e)
	}
	overlay := "# Explicit operator-only input\nPROJECT_ONE_OFFLINE_RUNTIME_RECOVERY_SCOPE_ID=" + strings.Repeat("d", 32) + "\nPROJECT_ONE_OFFLINE_RUNTIME_RECOVERY_CREATE_OPERATION_ID=" + strings.Repeat("b", 32) + "\nPROJECT_ONE_OFFLINE_RUNTIME_RECOVERY_REASON=Resume the same recorded failure\n"
	got, e := LoadRuntimeAbsenceRecoveryRequest(raw, []byte(overlay))
	if e != nil || got.ScopeID != strings.Repeat("d", 32) || got.RuntimeCreateOperationID != strings.Repeat("b", 32) || got.Reason != "Resume the same recorded failure" {
		t.Fatal("explicit overlay precedence", e)
	}
	if _, e = LoadRuntimeAbsenceRecoveryRequest([]byte(`{"format_version":1,"scope_id":"cccccccccccccccccccccccccccccccc"}`), []byte(overlay)); e != nil {
		t.Fatal("required operation fields may come from the explicit overlay", e)
	}
	base.Reason = strings.Repeat("\u754c", maximumRuntimeRecoveryReasonRunes)
	raw, _ = json.Marshal(base)
	if _, e = LoadRuntimeAbsenceRecoveryRequest(raw, nil); e != nil {
		t.Fatal("bounded Unicode operator reason is user content", e)
	}
}

func TestRuntimeAbsenceRecoveryVersionedExamplesUseTheTypedLoader(t *testing.T) {
	directory := filepath.Join("..", "..", "..", "..", "services", "control", "config", "examples", "release")
	raw, e := os.ReadFile(filepath.Join(directory, "runtime-absence-recovery.json"))
	if e != nil {
		t.Fatal(e)
	}
	environment, e := os.ReadFile(filepath.Join(directory, "runtime-absence-recovery.env.example"))
	if e != nil {
		t.Fatal(e)
	}
	base, e := LoadRuntimeAbsenceRecoveryRequest(raw, nil)
	if e != nil || base.ScopeID != strings.Repeat("1", 32) || base.RuntimeCreateOperationID != strings.Repeat("2", 32) {
		t.Fatal("versioned JSON example rejected", e)
	}
	overlaid, e := LoadRuntimeAbsenceRecoveryRequest(raw, environment)
	if e != nil || overlaid.ScopeID != base.ScopeID || overlaid.RuntimeCreateOperationID != base.RuntimeCreateOperationID || overlaid.Reason != "Record observed absence without resubmitting creation" {
		t.Fatal("documented explicit env mapping rejected", e)
	}
}

func TestRuntimeAbsenceRecoveryRequestInvalidOrAmbiguousInputsAreRedacted(t *testing.T) {
	base := `{"format_version":1,"scope_id":"cccccccccccccccccccccccccccccccc","runtime_create_operation_id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","reason":"Synthetic recovery"}`
	for name, raw := range map[string]string{
		"missing": `{}`, "null": `null`, "field-null": strings.Replace(base, `"Synthetic recovery"`, `null`, 1), "duplicate": strings.Replace(base, `"format_version":1`, `"format_version":1,"format_version":1`, 1), "unknown": strings.TrimSuffix(base, "}") + `,"SYNTHETIC_PRIVATE_VALUE":true}`, "case": strings.Replace(base, `"reason"`, `"Reason"`, 1), "boolean": strings.Replace(base, `"Synthetic recovery"`, `false`, 1), "array": `[]`, "oversize": strings.Repeat(" ", maximumRuntimeRecoveryInputBytes+1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, e := LoadRuntimeAbsenceRecoveryRequest([]byte(raw), nil); e == nil || strings.Contains(e.Error(), "SYNTHETIC_PRIVATE") {
				t.Fatal("invalid input accepted or disclosed", e)
			}
		})
	}
	for _, change := range []func(*RuntimeAbsenceRecoveryRequest){func(r *RuntimeAbsenceRecoveryRequest) { r.FormatVersion = 2 }, func(r *RuntimeAbsenceRecoveryRequest) { r.ScopeID = "" }, func(r *RuntimeAbsenceRecoveryRequest) { r.ScopeID = strings.Repeat("C", 32) }, func(r *RuntimeAbsenceRecoveryRequest) { r.RuntimeCreateOperationID = "" }, func(r *RuntimeAbsenceRecoveryRequest) { r.RuntimeCreateOperationID = strings.Repeat("A", 32) }, func(r *RuntimeAbsenceRecoveryRequest) { r.RuntimeCreateOperationID = "control-container-name" }, func(r *RuntimeAbsenceRecoveryRequest) { r.Reason = " " }, func(r *RuntimeAbsenceRecoveryRequest) { r.Reason = "newline\nreason" }, func(r *RuntimeAbsenceRecoveryRequest) {
		r.Reason = strings.Repeat("x", maximumRuntimeRecoveryReasonRunes+1)
	}} {
		r := RuntimeAbsenceRecoveryRequest{FormatVersion: 1, ScopeID: strings.Repeat("c", 32), RuntimeCreateOperationID: strings.Repeat("a", 32), Reason: "Synthetic recovery"}
		change(&r)
		if r.Validate() == nil {
			t.Fatal("invalid operation reference/reason accepted")
		}
	}
	for _, environment := range []string{"OTHER=SYNTHETIC_PRIVATE_VALUE", "PROJECT_ONE_OFFLINE_RUNTIME_RECOVERY_REASON=$(external)", "PROJECT_ONE_OFFLINE_RUNTIME_RECOVERY_REASON=one\nPROJECT_ONE_OFFLINE_RUNTIME_RECOVERY_REASON=two", "PROJECT_ONE_OFFLINE_RUNTIME_RECOVERY_REASON=", "PROJECT_ONE_OFFLINE_RUNTIME_RECOVERY_CREATE_OPERATION_ID=false", "PROJECT_ONE_OFFLINE_RUNTIME_RECOVERY_FORMAT_VERSION=2", "PROJECT_ONE_OFFLINE_RUNTIME_RECOVERY_REASON='quoted'"} {
		if _, e := LoadRuntimeAbsenceRecoveryRequest([]byte(base), []byte(environment)); e == nil || strings.Contains(e.Error(), "SYNTHETIC_PRIVATE") {
			t.Fatal("invalid explicit environment accepted or disclosed", e)
		}
	}
}
