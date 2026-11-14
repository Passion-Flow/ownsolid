//go:build localization_database_integration

package localization_test

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"github.com/google/uuid"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	identityhttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/localization"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	"testing"
	"time"
)

func TestM16ActualIsolatedReadOnlyTargetClientOnBothTLSOrigins(t *testing.T) {
	fixture.PreflightOwnedPostgres(t)
	s := fixture.New(t)
	directory := os.Getenv("PROJECT_ONE_LOCALE_PACKAGE_DIRECTORY")
	python := os.Getenv("PROJECT_ONE_LOCALIZATION_FACTORY_PYTHON")
	mount := os.Getenv("PROJECT_ONE_QUALIFICATION_MOUNT_ROOT")
	evidence := os.Getenv("PROJECT_ONE_EVIDENCE_ROOT")
	if directory == "" || python == "" || mount == "" || evidence == "" {
		t.Fatal("explicit original package, Python, real mount and evidence required")
	}
	policy := config.DefaultLocalizationPolicy()
	policy.CacheSeconds = 0
	registry, e := localization.New(config.LocalizationConfiguration{PackageDirectory: directory, RequiredLocales: []string{"en", "zh-CN"}}, policy)
	if e != nil {
		t.Fatal(e)
	}
	s.Locales = registry
	portal := httptest.NewTLSServer(identityhttp.New(s))
	defer portal.Close()
	admin := httptest.NewTLSServer(identityhttp.New(s))
	defer admin.Close()
	s.Config.Server.PortalOrigin = portal.URL
	s.Config.Server.AdminOrigin = admin.URL
	raw, e := os.ReadFile(filepath.Join(directory, "inventory.json"))
	if e != nil {
		t.Fatal(e)
	}
	var inventory localization.Inventory
	if json.Unmarshal(raw, &inventory) != nil {
		t.Fatal("independent original package manifest")
	}
	packages := map[string]string{}
	for _, item := range inventory.Locales {
		packages[item.Locale] = item.SHA256
	}
	if len(packages) != 2 {
		t.Fatal("actual English/Chinese package inventory required")
	}
	parent := filepath.Join(evidence, "target-client-scope")
	if os.Mkdir(parent, 0700) != nil {
		t.Fatal("new private evidence scope")
	}
	ca := filepath.Join(parent, "ca.pem")
	if os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: portal.Certificate().Raw}), 0600) != nil {
		t.Fatal("test CA")
	}
	scope := uuid.NewString()
	marker := map[string]any{"schema_version": 1, "kind": "projectone-localization-test-scope-v1", "scope_id": scope, "purpose": "isolated_test_only", "portal_origin": portal.URL, "admin_origin": admin.URL}
	profile := map[string]any{"schema_version": 1, "kind": "projectone-isolated-localization-journey-v1", "scope_id": scope, "mount_root": mount, "portal_origin": portal.URL, "admin_origin": admin.URL, "ca_file": ca, "expected_source_sha256": inventory.SourceSHA256, "expected_locales": []string{"en", "zh-CN"}, "expected_locale_packages": packages, "required_reviewed_locales": []string{}, "request_timeout_seconds": 5, "maximum_seconds": 60, "maximum_response_bytes": 32 * 1024 * 1024, "maximum_total_bytes": 64 * 1024 * 1024, "maximum_requests": 128, "expected_cache_seconds": 3600}
	for name, value := range map[string]any{".projectone-localization-test-scope.json": marker, "profile.json": profile} {
		body, err := json.Marshal(value)
		if err != nil || os.WriteFile(filepath.Join(parent, name), body, 0600) != nil {
			t.Fatal("private exact qualification input")
		}
	}
	environment := filepath.Join(parent, "component.env")
	if os.WriteFile(environment, []byte("PROJECT_ONE_LOCALE_QUALIFICATION_EXPECTED_CACHE_SECONDS=0\n"), 0600) != nil {
		t.Fatal("nondefault env")
	}
	root, e := filepath.Abs("../../../../../")
	if e != nil {
		t.Fatal(e)
	}
	output := filepath.Join(parent, "result")
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, python, "-B", filepath.Join(root, "tests/validation/localization/journey.py"), "--config", filepath.Join(parent, "profile.json"), "--env-file", environment, "--output-directory", output, "--local-protocol-check")
	response, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("actual read-only client: %v %s; inspect private target-client receipt", err, response)
	}
	raw, e = os.ReadFile(filepath.Join(output, "manifest.json"))
	if e != nil {
		t.Fatal(e)
	}
	var record struct {
		Status   string   `json:"status"`
		Mode     string   `json:"mode"`
		Requests int      `json:"requests"`
		Checks   []any    `json:"checks"`
		Reviewed []string `json:"required_reviewed_locales"`
	}
	if json.Unmarshal(raw, &record) != nil || record.Status != "passed_local_protocol_only" || record.Mode != "local_protocol_only" || record.Requests < 30 || len(record.Checks) != 2 || len(record.Reviewed) != 0 {
		t.Fatal("invalid local receipt or false target/review qualification")
	}
	// A real seed can never satisfy a declared Google/human-reviewed gate.
	profile["required_reviewed_locales"] = []string{"zh-CN"}
	negative, e := json.Marshal(profile)
	if e != nil || os.WriteFile(filepath.Join(parent, "review-profile.json"), negative, 0600) != nil {
		t.Fatal("explicit seed review refusal profile")
	}
	command = exec.CommandContext(ctx, python, "-B", filepath.Join(root, "tests/validation/localization/journey.py"), "--config", filepath.Join(parent, "review-profile.json"), "--env-file", environment, "--output-directory", filepath.Join(parent, "review-refusal"), "--local-protocol-check")
	response, err = command.CombinedOutput()
	if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 1 {
		t.Fatalf("seed review unexpectedly accepted: %v %s", err, response)
	}
	negative, e = os.ReadFile(filepath.Join(parent, "review-refusal", "manifest.json"))
	var refused struct {
		Status string `json:"status"`
		Code   string `json:"error_code"`
	}
	if e != nil || json.Unmarshal(negative, &refused) != nil || refused.Status != "failed" || refused.Code != "LOCALIZATION_QUALIFICATION_GENUINE_REVIEW_REQUIRED" {
		t.Fatal("seed refusal failed before actual provenance check")
	}
}
