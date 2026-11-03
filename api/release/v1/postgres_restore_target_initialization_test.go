package release

import (
	"encoding/json"
	"strings"
	"testing"
)

func restoreTargetInitializationFixture() PostgreSQLRestoreTargetInitializationRequest {
	return PostgreSQLRestoreTargetInitializationRequest{FormatVersion: 1, ApplicationProfile: MVPPostgreSQLCPUApplicationProfile, InitializationID: strings.Repeat("a", 32), TargetScopeID: strings.Repeat("b", 32), Binding: PostgreSQLRestoreTargetInitializationBinding{RestoreAttemptID: strings.Repeat("c", 32), ReservationPlanSHA256: strings.Repeat("d", 64), TargetDescriptorSHA256: strings.Repeat("e", 64), TargetRuntimeSHA256: strings.Repeat("f", 64), SourceCandidateSHA256: strings.Repeat("1", 64), NetworkID: strings.Repeat("2", 64), PostgresContainerID: strings.Repeat("3", 64)}, SourceDatabase: "original_source", TargetDatabase: "fresh_target", TargetOperator: "synthetic_operator", Endpoint: PostgreSQLRestoreTargetInitializationEndpoint{Host: "postgres", Port: 5432, SSLMode: "disable"}, ExpectedSystemIdentifier: "7703660038325649453", ExpectedDatabaseOID: 40000, InitializationRoot: "/records/target-initialization", Policy: PostgreSQLRestoreTargetInitializationPolicy{OperationSeconds: 60, CleanupSeconds: 2, MaximumRecordBytes: 65536, MinimumFreeBytes: 1 << 20}}
}

func TestPostgresRestoreTargetInitializationPinnedAndDiscoveryRequests(t *testing.T) {
	r := restoreTargetInitializationFixture()
	if !r.Valid() || !r.ValidForInspection() || !r.IdentityPinned() || !ValidSHA256(r.SHA256()) {
		t.Fatal("pinned request refused")
	}
	for name, change := range map[string]func(*PostgreSQLRestoreTargetInitializationRequest){
		"legacy-profile":  func(v *PostgreSQLRestoreTargetInitializationRequest) { v.ApplicationProfile = "releases" },
		"foreign-attempt": func(v *PostgreSQLRestoreTargetInitializationRequest) { v.Binding.RestoreAttemptID = "attempt" },
		"short-container": func(v *PostgreSQLRestoreTargetInitializationRequest) {
			v.Binding.PostgresContainerID = v.Binding.PostgresContainerID[:12]
		},
		"missing-reservation":     func(v *PostgreSQLRestoreTargetInitializationRequest) { v.Binding.ReservationPlanSHA256 = "" },
		"relative-records":        func(v *PostgreSQLRestoreTargetInitializationRequest) { v.InitializationRoot = "records" },
		"same-source":             func(v *PostgreSQLRestoreTargetInitializationRequest) { v.TargetDatabase = v.SourceDatabase },
		"administrative-database": func(v *PostgreSQLRestoreTargetInitializationRequest) { v.TargetDatabase = "postgres" },
		"sql-name":                func(v *PostgreSQLRestoreTargetInitializationRequest) { v.TargetDatabase = "target;DROP" },
		"sql-marker":              func(v *PostgreSQLRestoreTargetInitializationRequest) { v.TargetScopeID = "abc'" },
		"single-system-pin":       func(v *PostgreSQLRestoreTargetInitializationRequest) { v.ExpectedDatabaseOID = 0 },
		"single-oid-pin":          func(v *PostgreSQLRestoreTargetInitializationRequest) { v.ExpectedSystemIdentifier = "" },
		"zero-system":             func(v *PostgreSQLRestoreTargetInitializationRequest) { v.ExpectedSystemIdentifier = "0" },
		"noncanonical-system": func(v *PostgreSQLRestoreTargetInitializationRequest) {
			v.ExpectedSystemIdentifier = "07703660038325649453"
		},
		"oversized-system": func(v *PostgreSQLRestoreTargetInitializationRequest) {
			v.ExpectedSystemIdentifier = "18446744073709551616"
		},
		"host-credentials":   func(v *PostgreSQLRestoreTargetInitializationRequest) { v.Endpoint.Host = "user@host" },
		"host-path":          func(v *PostgreSQLRestoreTargetInitializationRequest) { v.Endpoint.Host = "/var/run/postgresql" },
		"bracketed-v6":       func(v *PostgreSQLRestoreTargetInitializationRequest) { v.Endpoint.Host = "[::1]" },
		"port":               func(v *PostgreSQLRestoreTargetInitializationRequest) { v.Endpoint.Port = 65536 },
		"transport-fallback": func(v *PostgreSQLRestoreTargetInitializationRequest) { v.Endpoint.SSLMode = "prefer" },
		"timeout":            func(v *PostgreSQLRestoreTargetInitializationRequest) { v.Policy.OperationSeconds = 0 },
		"cleanup":            func(v *PostgreSQLRestoreTargetInitializationRequest) { v.Policy.CleanupSeconds = 61 },
		"record-cap":         func(v *PostgreSQLRestoreTargetInitializationRequest) { v.Policy.MaximumRecordBytes = 1 << 21 },
		"reserve":            func(v *PostgreSQLRestoreTargetInitializationRequest) { v.Policy.MinimumFreeBytes = -1 },
	} {
		t.Run(name, func(t *testing.T) {
			v := r
			change(&v)
			if v.ValidForInspection() || v.Valid() {
				t.Fatal("invalid request accepted")
			}
		})
	}
	r.ExpectedSystemIdentifier = ""
	r.ExpectedDatabaseOID = 0
	if r.Valid() || r.IdentityPinned() || !r.ValidForInspection() {
		t.Fatal("unpinned discovery acquired mutation authority")
	}
	r.Endpoint.Host = "::1"
	if !r.ValidForInspection() {
		t.Fatal("explicit unbracketed IPv6 endpoint refused")
	}
}

func TestPostgresRestoreTargetInitializationClosedJSON(t *testing.T) {
	r := restoreTargetInitializationFixture()
	raw, e := json.Marshal(r)
	if e != nil {
		t.Fatal(e)
	}
	var decoded PostgreSQLRestoreTargetInitializationRequest
	if StrictJSON(raw, &decoded) != nil || decoded != r {
		t.Fatal("exact request roundtrip")
	}
	for _, name := range []string{"unknown", "missing", "alias", "null", "duplicate", "nested-unknown", "nested-missing", "nested-alias"} {
		t.Run(name, func(t *testing.T) {
			var fields map[string]json.RawMessage
			json.Unmarshal(raw, &fields)
			switch name {
			case "unknown":
				fields["unknown"] = json.RawMessage("false")
			case "missing":
				delete(fields, "expected_database_oid")
			case "alias":
				fields["TargetScopeID"] = fields["target_scope_id"]
				delete(fields, "target_scope_id")
			case "null":
				fields["expected_database_oid"] = json.RawMessage("null")
			case "nested-unknown", "nested-missing", "nested-alias":
				var inner map[string]json.RawMessage
				json.Unmarshal(fields["endpoint"], &inner)
				if name == "nested-unknown" {
					inner["unknown"] = json.RawMessage("false")
				}
				if name == "nested-missing" {
					delete(inner, "port")
				}
				if name == "nested-alias" {
					inner["Host"] = inner["host"]
					delete(inner, "host")
				}
				fields["endpoint"], _ = json.Marshal(inner)
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
}

func TestPostgresRestoreTargetInitializationObservationAuthority(t *testing.T) {
	r := restoreTargetInitializationFixture()
	target := PostgreSQLRestoreTargetInitializationIdentity{SystemIdentifier: r.ExpectedSystemIdentifier, DatabaseOID: r.ExpectedDatabaseOID, Database: r.TargetDatabase, Operator: r.TargetOperator, BackendPID: 100, ServerVersion: 180006, Marker: r.Marker()}
	o := PostgreSQLRestoreTargetInitializationObservation{Format: PostgreSQLRestoreTargetObservationFormat, RequestSHA256: r.SHA256(), InitializationID: r.InitializationID, TargetScopeID: r.TargetScopeID, Binding: r.Binding, Target: target, TargetBindingSHA256: target.SHA256(), PristinePredicateSHA256: strings.Repeat("4", 64), IntentSHA256: strings.Repeat("5", 64), CompletionSHA256: strings.Repeat("6", 64), IdentityPinned: true, OriginalIntentPresent: true, OriginalCompletionPresent: true, EffectObserved: true, State: "initialized", Qualification: "Synthetic protocol test only"}
	if !o.ValidFor(r, false) {
		t.Fatal("completed initialization observation")
	}
	o.ReadOnly = true
	o.OriginalCompletionPresent = false
	o.CompletionSHA256 = ""
	if !o.ValidFor(r, true) {
		t.Fatal("original effect observed without manufacturing completion")
	}
	for name, change := range map[string]func(*PostgreSQLRestoreTargetInitializationObservation){
		"unowned-marker": func(v *PostgreSQLRestoreTargetInitializationObservation) {
			v.OriginalIntentPresent = false
			v.IntentSHA256 = ""
		},
		"foreign-system": func(v *PostgreSQLRestoreTargetInitializationObservation) { v.Target.SystemIdentifier = "1" },
		"foreign-oid":    func(v *PostgreSQLRestoreTargetInitializationObservation) { v.Target.DatabaseOID++ },
		"foreign-marker": func(v *PostgreSQLRestoreTargetInitializationObservation) { v.Target.Marker += "0" },
		"foreign-attempt": func(v *PostgreSQLRestoreTargetInitializationObservation) {
			v.Binding.RestoreAttemptID = strings.Repeat("7", 32)
		},
		"claim-lock":             func(v *PostgreSQLRestoreTargetInitializationObservation) { v.CurrentLockHeld = true },
		"claim-complete":         func(v *PostgreSQLRestoreTargetInitializationObservation) { v.OriginalCompletionPresent = true },
		"claim-discovery-pinned": func(v *PostgreSQLRestoreTargetInitializationObservation) { v.IdentityPinned = false },
	} {
		t.Run(name, func(t *testing.T) {
			v := o
			change(&v)
			if v.ValidFor(r, true) {
				t.Fatal("foreign/false authority accepted")
			}
		})
	}
	before := target.SHA256()
	target.BackendPID++
	if target.SHA256() != before {
		t.Fatal("new observer PID changed pinned target identity")
	}
	o.Target.Marker = ""
	o.TargetBindingSHA256 = o.Target.SHA256()
	o.EffectObserved = false
	o.State = "effect_not_observed"
	if !o.ValidFor(r, true) {
		t.Fatal("original uncertain intent with no current effect")
	}
	o.OriginalIntentPresent = false
	o.IntentSHA256 = ""
	o.State = "uninitialized"
	r.ExpectedSystemIdentifier = ""
	r.ExpectedDatabaseOID = 0
	o.RequestSHA256 = r.SHA256()
	o.IdentityPinned = false
	if !o.ValidFor(r, true) || o.ValidFor(r, false) {
		t.Fatal("readonly discovery escalated")
	}
	raw, _ := json.Marshal(o)
	var fields map[string]json.RawMessage
	json.Unmarshal(raw, &fields)
	delete(fields, "read_only")
	raw, _ = json.Marshal(fields)
	if StrictJSON(raw, &o) == nil {
		t.Fatal("omitted explicit false field accepted")
	}
}
