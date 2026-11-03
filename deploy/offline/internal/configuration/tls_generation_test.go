package configuration

import (
	"encoding/json"
	"strings"
	"testing"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

func TestTLSGenerationRequestStrictReferencesAndEnvironment(t *testing.T) {
	r := TLSGenerationRequest{FormatVersion: 1, CertificateFile: "/external/operator/chain.pem", PrivateKeyFile: "/external/operator/key.pem", TrustBundleFile: "/external/operator/ca.pem", Reason: "Explicit isolated replacement"}
	raw, _ := json.Marshal(r)
	got, e := LoadTLSGenerationRequest(raw, []byte("# explicit owner\nPROJECT_ONE_OFFLINE_TLS_REASON=New serving generation\nPROJECT_ONE_OFFLINE_TLS_CERTIFICATE_FILE=/external/new/chain.pem\nPROJECT_ONE_OFFLINE_TLS_PRIVATE_KEY_FILE=/external/new/key.pem\nPROJECT_ONE_OFFLINE_TLS_TRUST_BUNDLE_FILE=/external/new/ca.pem\n"))
	if e != nil || got.Reason != "New serving generation" || got.CertificateFile != "/external/new/chain.pem" || got.PrivateKeyFile != "/external/new/key.pem" || got.TrustBundleFile != "/external/new/ca.pem" {
		t.Fatal("explicit precedence", e)
	}
	for _, fault := range []string{"version", "empty", "relative", "traversal", "duplicate", "reason", "control", "long", "invalid-utf8", "long-path"} {
		t.Run(fault, func(t *testing.T) {
			v := r
			switch fault {
			case "version":
				v.FormatVersion = 0
			case "empty":
				v.CertificateFile = ""
			case "relative":
				v.PrivateKeyFile = "key.pem"
			case "traversal":
				v.PrivateKeyFile = "/external/a/../key.pem"
			case "duplicate":
				v.TrustBundleFile = v.CertificateFile
			case "reason":
				v.Reason = " "
			case "control":
				v.Reason = "test\nkey"
			case "long":
				v.Reason = strings.Repeat("x", 501)
			case "invalid-utf8":
				v.CertificateFile = "/external/" + string([]byte{255})
			case "long-path":
				v.CertificateFile = "/" + strings.Repeat("x", 1025)
			}
			if v.Validate() == nil {
				t.Fatal("invalid request admitted")
			}
		})
	}
	for _, env := range []string{"PROJECT_ONE_OFFLINE_TLS_REASON=", "PROJECT_ONE_OFFLINE_TLS_REASON=x\nPROJECT_ONE_OFFLINE_TLS_REASON=y", "PROJECT_ONE_OFFLINE_TLS_REASON=$HOME", "PROJECT_ONE_OFFLINE_TLS_REASON=`command`", "PROJECT_ONE_OFFLINE_TLS_UNKNOWN=false", "PROJECT_ONE_OFFLINE_TLS_PRIVATE_KEY_FILE", "PROJECT_ONE_OFFLINE_TLS_CERTIFICATE_FILE=false"} {
		if _, e := LoadTLSGenerationRequest(raw, []byte(env)); e == nil {
			t.Fatal("invalid environment admitted")
		}
	}
	for _, input := range []string{`{}`, `{"format_version":1,"format_version":1}`, `{"private_key":"SECRET"}`, `null`} {
		if _, e := LoadTLSGenerationRequest([]byte(input), nil); e == nil {
			t.Fatal("unsafe JSON admitted")
		}
	}
}

func TestTLSGenerationRequestV1SerializationAndOverlayRemainStable(t *testing.T) {
	const golden = `{"format_version":1,"certificate_file":"/external/operator/chain.pem","private_key_file":"/external/operator/key.pem","trust_bundle_file":"/external/operator/ca.pem","reason":"Explicit isolated replacement"}`
	request := TLSGenerationRequest{FormatVersion: 1, CertificateFile: "/external/operator/chain.pem", PrivateKeyFile: "/external/operator/key.pem", TrustBundleFile: "/external/operator/ca.pem", Reason: "Explicit isolated replacement"}
	raw, e := json.Marshal(request)
	if e != nil || string(raw) != golden || wire.Digest(raw) != "0278b4458b6492113875f91399b29d127a8c712be9753bff9bb52c4153d0c7a7" {
		t.Fatal("legacy five-field bytes or digest changed")
	}
	environment := []byte("PROJECT_ONE_OFFLINE_TLS_CERTIFICATE_FILE=/external/operator/chain.pem\nPROJECT_ONE_OFFLINE_TLS_PRIVATE_KEY_FILE=/external/operator/key.pem\nPROJECT_ONE_OFFLINE_TLS_TRUST_BUNDLE_FILE=/external/operator/ca.pem\nPROJECT_ONE_OFFLINE_TLS_REASON=Explicit isolated replacement\n")
	for _, item := range []struct{ name, raw string }{
		{"canonical", golden},
		{"omitted_fields_with_explicit_overlay", `{"format_version":1}`},
		{"empty_fields_with_explicit_overlay", `{"format_version":1,"certificate_file":"","private_key_file":"","trust_bundle_file":"","reason":""}`},
		{"legacy_case_spelling", strings.Replace(golden, `"certificate_file"`, `"Certificate_File"`, 1)},
	} {
		t.Run(item.name, func(t *testing.T) {
			got, e := LoadTLSGenerationRequest([]byte(item.raw), environment)
			encoded, je := json.Marshal(got)
			if e != nil || je != nil || got != request || string(encoded) != golden {
				t.Fatal("legacy overlay/source semantics changed", e)
			}
		})
	}
	for _, value := range []string{`""`, `"browser"`, `"machine"`, `null`, `false`, `0`} {
		t.Run("forbidden_raw_target_"+value, func(t *testing.T) {
			input := strings.TrimSuffix(golden, "}") + `,"target":` + value + "}"
			if _, e := LoadTLSGenerationRequest([]byte(input), environment); e == nil || e.Error() != "OFFLINE_OPERATOR_TLS_GENERATION_CONFIGURATION_INVALID" {
				t.Fatal("explicit V1 target was accepted or repaired by overlay")
			}
		})
	}
	request.Target = "browser"
	if request.Validate() == nil {
		t.Fatal("typed V1 target was accepted")
	}
}

func TestTLSGenerationRequestV2TargetEnvironment(t *testing.T) {
	base := TLSGenerationRequest{FormatVersion: 2, CertificateFile: "/external/operator/chain.pem", PrivateKeyFile: "/external/operator/key.pem", TrustBundleFile: "/external/operator/ca.pem", Reason: "Explicit selected replacement", Target: "browser"}
	t.Setenv("PROJECT_ONE_OFFLINE_TLS_TARGET", "invalid-ambient-target")
	t.Setenv("PROJECT_ONE_OFFLINE_TLS_CERTIFICATE_FILE", "invalid-ambient-reference")
	for _, target := range []string{"browser", "machine"} {
		for _, overlay := range []bool{false, true} {
			name := target + "_base"
			if overlay {
				name = target + "_explicit_overlay"
			}
			t.Run(name, func(t *testing.T) {
				request := base
				request.Target = target
				raw, _ := json.Marshal(request)
				var environment []byte
				if overlay {
					request.Target = "browser"
					if target == "browser" {
						request.Target = "machine"
					}
					environment = []byte("PROJECT_ONE_OFFLINE_TLS_TARGET=" + request.Target + "\n")
				}
				got, e := LoadTLSGenerationRequest(raw, environment)
				if e != nil || got != request || got.FormatVersion != 2 {
					t.Fatal("explicit V2 target selection changed", e)
				}
			})
		}
	}
	raw, _ := json.Marshal(base)
	for _, environment := range []string{"PROJECT_ONE_OFFLINE_TLS_TARGET=", "PROJECT_ONE_OFFLINE_TLS_TARGET=false", "PROJECT_ONE_OFFLINE_TLS_TARGET=Machine", "PROJECT_ONE_OFFLINE_TLS_TARGET=unknown", "PROJECT_ONE_OFFLINE_TLS_TARGET=$HOME", "PROJECT_ONE_OFFLINE_TLS_TARGET=`command`", "PROJECT_ONE_OFFLINE_TLS_TARGET=\"machine\"", "PROJECT_ONE_OFFLINE_TLS_TARGET=machine\nPROJECT_ONE_OFFLINE_TLS_TARGET=browser", "PROJECT_ONE_OFFLINE_TLS_FORMAT_VERSION=1", "PROJECT_ONE_OFFLINE_TLS_UNKNOWN=false", "#\xff\n"} {
		t.Run("invalid_environment_"+wire.Digest([]byte(environment))[:12], func(t *testing.T) {
			if _, e := LoadTLSGenerationRequest(raw, []byte(environment)); e == nil || e.Error() != "OFFLINE_OPERATOR_TLS_GENERATION_ENVIRONMENT_INVALID" {
				t.Fatal("unsafe V2 environment accepted")
			}
		})
	}
	t.Run("v1_target_overlay_cannot_promote", func(t *testing.T) {
		request := base
		request.FormatVersion = 1
		request.Target = ""
		old, _ := json.Marshal(request)
		if _, e := LoadTLSGenerationRequest(old, []byte("PROJECT_ONE_OFFLINE_TLS_TARGET=machine\n")); e == nil || e.Error() != "OFFLINE_OPERATOR_TLS_GENERATION_ENVIRONMENT_INVALID" {
			t.Fatal("V1 was implicitly promoted")
		}
	})
	t.Run("all_five_explicit_keys", func(t *testing.T) {
		got, e := LoadTLSGenerationRequest(raw, []byte("PROJECT_ONE_OFFLINE_TLS_CERTIFICATE_FILE=/external/new/chain.pem\nPROJECT_ONE_OFFLINE_TLS_PRIVATE_KEY_FILE=/external/new/key.pem\nPROJECT_ONE_OFFLINE_TLS_TRUST_BUNDLE_FILE=/external/new/ca.pem\nPROJECT_ONE_OFFLINE_TLS_REASON=Selected replacement\nPROJECT_ONE_OFFLINE_TLS_TARGET=machine\n"))
		if e != nil || got.CertificateFile != "/external/new/chain.pem" || got.PrivateKeyFile != "/external/new/key.pem" || got.TrustBundleFile != "/external/new/ca.pem" || got.Reason != "Selected replacement" || got.Target != "machine" || got.FormatVersion != 2 {
			t.Fatal("V2 explicit key mapping changed", e)
		}
	})
}

func TestTLSGenerationRequestV2StrictRawShape(t *testing.T) {
	base := map[string]any{"format_version": 2, "certificate_file": "/external/operator/chain.pem", "private_key_file": "/external/operator/key.pem", "trust_bundle_file": "/external/operator/ca.pem", "reason": "Explicit selected replacement", "target": "machine"}
	repair := []byte("PROJECT_ONE_OFFLINE_TLS_CERTIFICATE_FILE=/external/new/chain.pem\nPROJECT_ONE_OFFLINE_TLS_PRIVATE_KEY_FILE=/external/new/key.pem\nPROJECT_ONE_OFFLINE_TLS_TRUST_BUNDLE_FILE=/external/new/ca.pem\nPROJECT_ONE_OFFLINE_TLS_REASON=Replacement\nPROJECT_ONE_OFFLINE_TLS_TARGET=browser\n")
	for _, field := range []string{"format_version", "certificate_file", "private_key_file", "trust_bundle_file", "reason", "target"} {
		for _, fault := range []string{"missing", "null", "false"} {
			t.Run(field+"_"+fault, func(t *testing.T) {
				selected := map[string]any{}
				for key, value := range base {
					selected[key] = value
				}
				switch fault {
				case "missing":
					delete(selected, field)
				case "null":
					selected[field] = nil
				case "false":
					selected[field] = false
				}
				raw, _ := json.Marshal(selected)
				if _, e := LoadTLSGenerationRequest(raw, repair); e == nil || e.Error() != "OFFLINE_OPERATOR_TLS_GENERATION_CONFIGURATION_INVALID" {
					t.Fatal("invalid V2 base was repaired or accepted")
				}
			})
		}
	}
	canonical, _ := json.Marshal(base)
	for _, item := range []struct{ name, raw string }{
		{"unknown", strings.TrimSuffix(string(canonical), "}") + `,"unknown":true}`},
		{"duplicate", strings.TrimSuffix(string(canonical), "}") + `,"target":"browser"}`},
		{"case_alias", strings.Replace(string(canonical), `"target"`, `"Target"`, 1)},
		{"target_empty", strings.Replace(string(canonical), `"target":"machine"`, `"target":""`, 1)},
		{"target_unknown", strings.Replace(string(canonical), `"target":"machine"`, `"target":"both"`, 1)},
		{"relative_reference", strings.Replace(string(canonical), `"/external/operator/key.pem"`, `"key.pem"`, 1)},
		{"duplicate_reference", strings.Replace(string(canonical), `"/external/operator/key.pem"`, `"/external/operator/chain.pem"`, 1)},
		{"unknown_version", strings.Replace(string(canonical), `"format_version":2`, `"format_version":3`, 1)},
		{"trailing", string(canonical) + `{}`},
		{"invalid_utf8_v1", "{\"format_version\":1,\"certificate_file\":\"/external/\xff.pem\",\"private_key_file\":\"/external/key.pem\",\"trust_bundle_file\":\"/external/ca.pem\",\"reason\":\"Explicit replacement\"}"},
		{"case_duplicate_v1", `{"format_version":1,"certificate_file":"/external/a.pem","Certificate_File":"/external/b.pem","private_key_file":"/external/key.pem","trust_bundle_file":"/external/ca.pem","reason":"Explicit replacement"}`},
	} {
		t.Run(item.name, func(t *testing.T) {
			environment := repair
			if strings.HasSuffix(item.name, "_v1") {
				environment = nil
			}
			if _, e := LoadTLSGenerationRequest([]byte(item.raw), environment); e == nil || e.Error() != "OFFLINE_OPERATOR_TLS_GENERATION_CONFIGURATION_INVALID" {
				t.Fatal("unsafe raw request accepted")
			}
		})
	}
}
