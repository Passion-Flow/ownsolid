//go:build offlineintegration

package scope

import (
	"context"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/tools/release/offline/pkg/bundle"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

func TestActualRetainedRestoredStorePlanReadOnly(t *testing.T) {
	name := os.Getenv("PROJECT_ONE_TEST_RESTORED_STORE_RETENTION")
	if name == "" {
		t.Skip("explicit qualified synthetic SQL/database target required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	raw, e := bundle.ReadProtectedConfiguration(name)
	var retained struct {
		ScopeID           string `json:"scope_id"`
		Config            string `json:"operator_config"`
		Request           string `json:"restore_request"`
		Qualified         bool   `json:"qualified_database"`
		StatusObservation Status `json:"status_observation"`
	}
	if e != nil || wire.UniqueJSON(raw, &retained) != nil || !retained.Qualified || retained.StatusObservation.RestoreDatabase == nil || !wire.ValidSHA256(retained.StatusObservation.RestoreDatabase.ProofSHA256) {
		t.Fatal("qualified private target retention required", e)
	}
	raw, e = bundle.ReadProtectedConfiguration(retained.Config)
	if e != nil {
		t.Fatal(e)
	}
	c, e := configuration.Load(raw, nil)
	if e != nil || !c.Verification.AllowTestFixture {
		t.Fatal("explicit isolated fixture configuration required", e)
	}
	raw, e = bundle.ReadProtectedConfiguration(retained.Request)
	if e != nil {
		t.Fatal(e)
	}
	request, e := configuration.LoadRestoreRequest(raw, nil)
	if e != nil {
		t.Fatal(e)
	}
	l, e := open(c, false)
	if e != nil {
		t.Fatal("current target journal and historical payload digest", e)
	}
	defer l.close()
	if l.marker.ScopeID != retained.ScopeID {
		t.Fatal("retained scope changed")
	}
	raw, e = regular(l.root, restorePlanFile, c.Verification.MaximumMetadataBytes)
	var original RestorePlan
	if e != nil || wire.StrictJSON(raw, &original) != nil || !reflect.DeepEqual(request, original.Request) {
		t.Fatal("exact restore request required", e)
	}
	beforeJournal, e := regular(l.root, journalName, c.MaximumJournalBytes)
	if e != nil {
		t.Fatal(e)
	}
	beforeHead, e := regular(l.root, headName, c.Verification.MaximumMetadataBytes)
	if e != nil {
		t.Fatal(e)
	}
	generation, e := nonce()
	if e != nil {
		t.Fatal(e)
	}
	plan, files, e := l.makeRestoreStorePlan(ctx, original, generation)
	if e != nil {
		t.Fatal("actual protected archive/target plan", e)
	}
	again, againFiles, e := l.makeRestoreStorePlan(ctx, original, generation)
	if e != nil || !reflect.DeepEqual(plan, again) || !reflect.DeepEqual(files, againFiles) {
		t.Fatal("nondeterministic protected plan", e)
	}
	if len(plan.Jobs) != 2 || plan.Jobs[0].Role != "models" || plan.Jobs[1].Role != "datasets" || plan.DatabaseProofSHA256 != retained.StatusObservation.RestoreDatabase.ProofSHA256 {
		t.Fatal("selected accepted target facts changed")
	}
	afterJournal, e := regular(l.root, journalName, c.MaximumJournalBytes)
	if e != nil || string(beforeJournal) != string(afterJournal) {
		t.Fatal("read-only constructor changed journal", e)
	}
	afterHead, e := regular(l.root, headName, c.Verification.MaximumMetadataBytes)
	if e != nil || string(beforeHead) != string(afterHead) {
		t.Fatal("read-only constructor changed head", e)
	}
	out := os.Getenv("PROJECT_ONE_TEST_RESTORED_STORE_EVIDENCE")
	if !privateDirectory(out) {
		t.Fatal("private evidence required")
	}
	root, e := os.OpenRoot(out)
	if e != nil {
		t.Fatal(e)
	}
	defer root.Close()
	if e = writeBackupFile(root, "plan.json", encode(plan)); e != nil {
		t.Fatal(e)
	}
	if e = writeBackupFile(root, "actual.json", encode(map[string]any{"status": "passed_actual_readonly_store_plan_from_retained_SQL_database_target", "scope_id": plan.ScopeID, "generation_id": generation, "plan_sha256": wire.Digest(encode(plan)), "configuration_payload_sha256": restoreConfigurationDigest(files), "historical_database_payload_still_valid": true, "journal_and_head_unchanged": true, "stores": []string{"models", "datasets"}, "qualification": "readonly_plan_and_real_archive_description_only_no_volume_import_application_or_native_qualification"})); e != nil {
		t.Fatal(e)
	}
}
