package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestPlanIsOfflineAndNeverPlatformReady(t *testing.T) {
	var out bytes.Buffer
	if run([]string{"--mode", "plan"}, &out) != 0 {
		t.Fatal(out.String())
	}
	var r result
	if json.Unmarshal(out.Bytes(), &r) != nil || !r.Success || r.Report.FoundationReady || r.Report.PlatformReady || r.CleanupState != "not_created" || len(r.Plan) != 476 || r.Report.TotalSteps != len(r.Plan) || r.Plan[30].Object != "INSTALLATION_SEED" || r.Plan[31].Object != "JOURNAL_CAPACITY_V2_ADD" || r.Plan[32].Object != "JOURNAL_CAPACITY_V1_DROP" || r.Plan[458].Object != "AWS_RECEIPT_IMMUTABLE" || r.Plan[475].Object != "IC_ALIAS_INTENT_DELETE" {
		t.Fatal("offline plan contract")
	}
}
func TestSchemaCommandRequiresExplicitOperation(t *testing.T) {
	for _, args := range [][]string{nil, {"--mode", "automatic"}, {"--mode", "plan", "--config", "/private/secret"}, {"--mode", "grant", "--config", "/private/secret", "--schema-config", "/private/schema"}, {"--print-launch-environment", "--mode", "apply"}, {"--unknown", "secret-content"}, {"--config", "/private/secret", "--print-launch-settings", "--print-launch-environment"}, {"--config", "/private/secret", "--print-launch-settings", "--mode", "apply"}} {
		var out bytes.Buffer
		if run(args, &out) != 1 || strings.Contains(out.String(), "secret") {
			t.Fatal("argument refusal must redact inputs")
		}
		var r result
		if json.Unmarshal(out.Bytes(), &r) != nil || r.Code != "DAMENG_SCHEMA_ARGUMENTS_INVALID" || r.Success {
			t.Fatal(out.String())
		}
	}
}

func TestNativeOperatorArgumentsStayExplicit(t *testing.T) {
	for _, args := range [][]string{{"--mode", "bootstrap-admin-file"}, {"--mode", "check-license", "--input", "/private/request"}, {"--mode", "plan", "--operator-policy", "/private/policy"}, {"--mode", "apply", "--output", "/private/intent"}} {
		var out bytes.Buffer
		if run(args, &out) != 1 || strings.Contains(out.String(), "/private") {
			t.Fatal("invalid operator arguments escaped", out.String())
		}
	}
}
