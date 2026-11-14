package release

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func restoreRootsInspectionFixture() PostgreSQLRestoreRootsInspectionRequest {
	return PostgreSQLRestoreRootsInspectionRequest{FormatVersion: 1, ApplicationProfile: MVPPostgreSQLCPUApplicationProfile, InspectionID: strings.Repeat("a", 32), TargetScopeID: strings.Repeat("b", 32), Binding: restoreTargetInitializationFixture().Binding, RuntimeConfigurationRoot: "/projection/private", RuntimeConfigurationSHA256: strings.Repeat("4", 64), ExpectedUID: 65534, ExpectedGID: 65534, Policy: PostgreSQLRestoreRootsInspectionPolicy{OperationSeconds: 15, MaximumRecordBytes: 65536}}
}

func TestPostgresRestoreRootsInspectionClosedRequest(t *testing.T) {
	r := restoreRootsInspectionFixture()
	raw, _ := json.Marshal(r)
	var decoded PostgreSQLRestoreRootsInspectionRequest
	if !r.Valid() || !ValidSHA256(r.SHA256()) || StrictJSON(raw, &decoded) != nil || !reflect.DeepEqual(r, decoded) {
		t.Fatal("exact original request")
	}
	for _, name := range []string{"unknown", "missing", "null", "alias", "duplicate", "binding-alias", "binding-missing", "policy-unknown", "policy-null"} {
		t.Run(name, func(t *testing.T) {
			var fields map[string]json.RawMessage
			json.Unmarshal(raw, &fields)
			switch name {
			case "unknown":
				fields["paths"] = json.RawMessage(`[]`)
			case "missing":
				delete(fields, "runtime_environment_sha256")
			case "null":
				fields["expected_uid"] = json.RawMessage(`null`)
			case "alias":
				fields["TargetScopeID"] = fields["target_scope_id"]
				delete(fields, "target_scope_id")
			case "binding-alias", "binding-missing":
				var inner map[string]json.RawMessage
				json.Unmarshal(fields["binding"], &inner)
				if name == "binding-alias" {
					inner["RestoreAttemptID"] = inner["restore_attempt_id"]
				}
				delete(inner, "restore_attempt_id")
				fields["binding"], _ = json.Marshal(inner)
			case "policy-unknown":
				fields["policy"] = json.RawMessage(`{"operation_seconds":15,"maximum_record_bytes":65536,"unknown":false}`)
			case "policy-null":
				fields["policy"] = json.RawMessage(`null`)
			}
			changed, _ := json.Marshal(fields)
			if name == "duplicate" {
				changed = append([]byte(`{"format_version":1,`), changed[1:]...)
			}
			if StrictJSON(changed, &decoded) == nil {
				t.Fatal("ambiguous request accepted")
			}
		})
	}
	for _, change := range []func(*PostgreSQLRestoreRootsInspectionRequest){func(v *PostgreSQLRestoreRootsInspectionRequest) { v.ExpectedUID = 0 }, func(v *PostgreSQLRestoreRootsInspectionRequest) { v.ExpectedGID = 0 }, func(v *PostgreSQLRestoreRootsInspectionRequest) { v.RuntimeConfigurationRoot = "/" }, func(v *PostgreSQLRestoreRootsInspectionRequest) { v.RuntimeConfigurationSHA256 = "" }, func(v *PostgreSQLRestoreRootsInspectionRequest) { v.Binding.TargetRuntimeSHA256 = "" }, func(v *PostgreSQLRestoreRootsInspectionRequest) { v.Policy.OperationSeconds = 301 }, func(v *PostgreSQLRestoreRootsInspectionRequest) { v.Policy.MaximumRecordBytes = 1 }} {
		v := r
		change(&v)
		if v.Valid() || v.SHA256() != "" {
			t.Fatal("invalid context accepted")
		}
	}
}

func TestPostgresRestoreRootsInspectionObservationHasExactlyFourReleasedRoots(t *testing.T) {
	r := restoreRootsInspectionFixture()
	roots := []PostgreSQLRestoreRoot{}
	for i, name := range []string{"models", "datasets", "training", "preparations"} {
		roots = append(roots, PostgreSQLRestoreRoot{Path: "/target/" + name, Device: 9, Inode: uint64(100 + i), UID: r.ExpectedUID, GID: r.ExpectedGID})
	}
	o := PostgreSQLRestoreRootsInspectionObservation{Format: PostgreSQLRestoreRootsInspectionFormat, RequestSHA256: r.SHA256(), InspectionID: r.InspectionID, TargetScopeID: r.TargetScopeID, Binding: r.Binding, RuntimeConfigurationSHA256: r.RuntimeConfigurationSHA256, EffectiveConfigurationSHA256: strings.Repeat("5", 64), Roots: roots, RootsSHA256: PostgreSQLRestoreRootsSHA256(roots), ReadOnly: true, ObservedAt: "2026-11-03T03:00:00Z", Qualification: PostgreSQLRestoreRootsInspectionQualification}
	if !o.ValidFor(r) {
		t.Fatal("original instant observation")
	}
	for _, name := range []string{"nil", "short", "long", "lock", "write", "foreign-request", "foreign-attempt", "owner", "alias", "digest", "timestamp", "qualification"} {
		t.Run(name, func(t *testing.T) {
			v := o
			v.Roots = append([]PostgreSQLRestoreRoot(nil), o.Roots...)
			switch name {
			case "nil":
				v.Roots = nil
			case "short":
				v.Roots = v.Roots[:3]
			case "long":
				v.Roots = append(v.Roots, v.Roots[0])
			case "lock":
				v.CurrentLockHeld = true
			case "write":
				v.ReadOnly = false
			case "foreign-request":
				v.RequestSHA256 = strings.Repeat("7", 64)
			case "foreign-attempt":
				v.Binding.RestoreAttemptID = strings.Repeat("8", 32)
			case "owner":
				v.Roots[0].UID++
			case "alias":
				v.Roots[1] = v.Roots[0]
			case "digest":
				v.RootsSHA256 = strings.Repeat("9", 64)
			case "timestamp":
				v.ObservedAt = "not-a-time"
			case "qualification":
				v.Qualification = "continuous physical custody"
			}
			if v.ValidFor(r) {
				t.Fatal("false observation accepted")
			}
			raw, _ := json.Marshal(v)
			var decoded PostgreSQLRestoreRootsInspectionObservation
			if StrictJSON(raw, &decoded) == nil && decoded.ValidFor(r) {
				t.Fatal("slice truncation or false decoded observation")
			}
		})
	}
	raw, _ := json.Marshal(o)
	for _, changed := range []string{strings.Replace(string(raw), `"read_only":true`, `"ReadOnly":true`, 1), strings.Replace(string(raw), `"current_lock_held":false`, `"current_lock_held":null`, 1), strings.Replace(string(raw), `"device":9`, `"Device":9`, 1), strings.Replace(string(raw), `"inode":100`, `"inode":100,"inode":101`, 1)} {
		var decoded PostgreSQLRestoreRootsInspectionObservation
		if StrictJSON([]byte(changed), &decoded) == nil {
			t.Fatal("ambiguous observation accepted")
		}
	}
}
