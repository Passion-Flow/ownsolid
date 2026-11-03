//go:build offlineintegration

package scope

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"reflect"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/tools/release/offline/pkg/bundle"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/compose"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// Test-only real transport response loss, after actual SDK copy/readback succeeds.
type actualTLSCopyLoss struct {
	*engine.Client
	calls int
	lost  bool
}

func (c *actualTLSCopyLoss) PutConfiguration(ctx context.Context, o engine.Ownership, image string, uid, gid int, files []engine.ConfigurationFile) error {
	e := c.Client.PutConfiguration(ctx, o, image, uid, gid, files)
	if o.Role == engine.TLSConfigurationRole+"-"+o.OperationID {
		c.calls++
		if e == nil && !c.lost {
			c.lost = true
			return errors.New("SYNTHETIC_REAL_TLS_COPY_RESPONSE_LOST")
		}
	}
	return e
}
func TestActualRetainedTLSPreparationCopyLoss(t *testing.T) {
	retention := os.Getenv("PROJECT_ONE_TEST_TLS_PROJECTION_RETENTION")
	if retention == "" {
		t.Skip("explicit isolated selected-store target required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	read := func(name string, out any) {
		raw, e := bundle.ReadProtectedConfiguration(name)
		if e != nil || wire.UniqueJSON(raw, out) != nil {
			t.Fatal("protected isolated fixture required", e)
		}
	}
	var retained struct {
		ScopeID     string `json:"scope_id"`
		Config      string `json:"operator_config"`
		Qualified   bool   `json:"qualified_stores"`
		Observation Status `json:"status_observation"`
	}
	read(retention, &retained)
	if !retained.Qualified || retained.Observation.RestoreStores == nil || retained.Observation.RestoreDatabase == nil || retained.Observation.RestoreSQL == nil {
		t.Fatal("selected-store-qualified target required")
	}
	raw, e := bundle.ReadProtectedConfiguration(retained.Config)
	if e != nil {
		t.Fatal(e)
	}
	c, e := configuration.Load(raw, nil)
	if e != nil || !c.Verification.AllowTestFixture || c.Engine == nil || c.Engine.Utility == nil || c.Compose == nil {
		t.Fatal("explicit isolated configuration", e)
	}
	requestName := os.Getenv("PROJECT_ONE_TEST_TLS_PROJECTION_REQUEST")
	raw, e = bundle.ReadProtectedConfiguration(requestName)
	if e != nil {
		t.Fatal(e)
	}
	request, e := configuration.LoadTLSGenerationRequest(raw, nil)
	if e != nil {
		t.Fatal(e)
	}
	out, binary := os.Getenv("PROJECT_ONE_TEST_TLS_PROJECTION_EVIDENCE"), os.Getenv("PROJECT_ONE_TEST_TLS_PROJECTION_OPERATOR")
	if !privateDirectory(out) || !canonical(binary) {
		t.Fatal("private external evidence/current native required")
	}
	put := func(name string, value any) {
		root, e := os.OpenRoot(out)
		if e != nil {
			t.Fatal(e)
		}
		defer root.Close()
		if e = writeBackupFile(root, name, encode(value)); e != nil {
			t.Fatal(e)
		}
	}
	operator := func(c context.Context, action string, operation bool) ([]byte, error) {
		args := []string{action, "--config", retained.Config}
		if operation {
			args = append(args, "--operation-file", requestName)
		}
		return exec.CommandContext(c, binary, args...).CombinedOutput()
	}
	status := func(c context.Context) Status {
		raw, e := operator(c, "status", false)
		var s Status
		if e != nil || wire.StrictJSON(raw, &s) != nil {
			t.Fatal("current native status", e)
		}
		return s
	}
	beforeStatus := status(ctx)
	if beforeStatus.ScopeID != retained.ScopeID || beforeStatus.Purpose != purpose || beforeStatus.RestoreStores == nil || beforeStatus.RestoreDatabase == nil || beforeStatus.RestoreSQL == nil || beforeStatus.RestoreStores.ProofSHA256 != retained.Observation.RestoreStores.ProofSHA256 || beforeStatus.RestoreDatabase.ProofSHA256 != retained.Observation.RestoreDatabase.ProofSHA256 || beforeStatus.RestoreSQL.ProofSHA256 != retained.Observation.RestoreSQL.ProofSHA256 {
		t.Fatal("retained restore lineage changed")
	}
	if beforeStatus.TLSProjection != nil && (beforeStatus.TLSProjection.Pending || beforeStatus.TLSProjection.Status != "TLS_projection_aborted_original_configuration_preserved") {
		t.Fatal("only absent or cleaned prior TLS generation allowed")
	}
	client, e := engine.New(*c.Engine)
	if e != nil {
		t.Fatal(e)
	}
	defer client.Close()
	worker, e := compose.New(c)
	if e != nil {
		t.Fatal(e)
	}
	l, e := open(c, false)
	if e != nil {
		t.Fatal(e)
	}
	verified, e := l.verifyRuntimeContext(ctx, client, worker, false)
	if e != nil {
		l.close()
		t.Fatal(e)
	}
	for _, s := range verified.observed.Services {
		if s.State != "created" && s.State != "exited" {
			l.close()
			t.Fatal("all core containers must remain stopped")
		}
	}
	descriptor, payload := wire.Digest(encode(verified.inputs.descriptor)), restoreConfigurationDigest(verified.inputs.files["control-config"])
	firstSequence := len(l.events)
	l.close()
	qualified, started := false, false
	defer func() {
		cleanup, done := context.WithTimeout(context.Background(), 60*time.Second)
		defer done()
		if started && !qualified {
			if _, e := operator(cleanup, "abort-restored-tls", true); e != nil {
				t.Errorf("generated TLS cleanup unresolved: %v", e)
			}
		}
		if _, e := operator(cleanup, "stop-runtime", false); e != nil {
			t.Errorf("scoped stop unresolved: %v", e)
		}
		raw, e := operator(cleanup, "status", false)
		var final Status
		if e != nil || wire.StrictJSON(raw, &final) != nil {
			t.Errorf("retained TLS status unresolved: %v", e)
			return
		}
		put("retention.json", map[string]any{"status": "retained_TLS_configuration_prepared_core_stopped_replacement_pending", "scope_id": retained.ScopeID, "operator_config": retained.Config, "TLS_request": requestName, "native_operator": binary, "selected_store_retention": retention, "qualified_TLS_preparation": qualified, "status_observation": final, "qualification": "TLS_preparation_only_not_control_replacement_activation_browser_or_native_target"})
	}()
	loss := &actualTLSCopyLoss{Client: client}
	started = true
	failed, e := runTLSProjection(ctx, c, request, "prepare-restored-tls", loss, worker)
	if e == nil || !failed.Pending || !loss.lost || loss.calls != 1 {
		t.Fatal("actual copied response loss required", e)
	}
	if _, e = operator(ctx, "prepare-restored-tls", true); e == nil {
		t.Fatal("ordinary pending prepare resubmitted")
	}
	raw, e = operator(ctx, "reconcile-restored-tls", true)
	var result TLSProjectionResult
	if e != nil || wire.StrictJSON(raw, &result) != nil || result.Pending || result.ProofSHA256 == "" || result.CompletedSteps != 6 || result.GenerationID != failed.GenerationID {
		t.Fatal("actual native copied-input observation and completion", e)
	}
	l, e = open(c, false)
	if e != nil {
		t.Fatal(e)
	}
	plan, e := l.readTLSProjectionPlan(*tlsProjectionState(l.events).record)
	if e != nil {
		l.close()
		t.Fatal(e)
	}
	after, e := l.verifyRuntimeContext(ctx, client, worker, false)
	if e != nil || !reflect.DeepEqual(verified.observed, after.observed) || descriptor != wire.Digest(encode(after.inputs.descriptor)) || payload != restoreConfigurationDigest(after.inputs.files["control-config"]) {
		l.close()
		t.Fatal("original configuration/actual runtime changed", e)
	}
	counts := map[string]int{}
	for _, event := range l.events[firstSequence:] {
		if event.Action != "tls_projection_effect" && event.Action != "tls_projection_reconcile" {
			l.close()
			t.Fatal("non-TLS work replayed")
		}
		if event.TLSProjection == nil || event.TLSProjection.GenerationID != plan.GenerationID {
			l.close()
			t.Fatal("foreign generation event")
		}
		if event.Action == "tls_projection_effect" && event.Outcome == "started" {
			counts[event.TLSProjection.Phase]++
		}
	}
	for _, phase := range tlsProjectionSteps() {
		if counts[phase] != 1 {
			l.close()
			t.Fatal("TLS intent replay", phase, counts[phase])
		}
	}
	beforeRepeat, e := regular(l.root, journalName, c.MaximumJournalBytes)
	if e != nil {
		l.close()
		t.Fatal(e)
	}
	beforeHead, e := regular(l.root, headName, c.Verification.MaximumMetadataBytes)
	if e != nil {
		l.close()
		t.Fatal(e)
	}
	l.close()
	volume, found, e := client.InspectVolume(ctx, plan.ConfigurationOwner)
	if e != nil || !found || volume.Name != plan.ConfigurationOwner.VolumeName() {
		t.Fatal("prepared exact configuration volume", e)
	}
	if e = client.VerifyVolumeConsumers(ctx, plan.ConfigurationOwner, nil); e != nil {
		t.Fatal("unused generation volume", e)
	}
	if _, found, e = client.InspectUtility(ctx, plan.ConfigurationOwner, plan.Initializer, plan.UID, plan.GID); e != nil || found {
		t.Fatal("generated never-started utility retained", e)
	}
	raw, e = operator(ctx, "prepare-restored-tls", true)
	var again TLSProjectionResult
	if e != nil || wire.StrictJSON(raw, &again) != nil || again != result {
		t.Fatal("terminal historical repeat", e)
	}
	if _, e = operator(ctx, "start-runtime", false); e == nil {
		t.Fatal("preparation opened application activation")
	}
	l, e = open(c, false)
	if e != nil {
		t.Fatal(e)
	}
	afterRepeat, e := regular(l.root, journalName, c.MaximumJournalBytes)
	if e != nil || !bytes.Equal(beforeRepeat, afterRepeat) {
		l.close()
		t.Fatal("completed repeat/refused activation changed journal", e)
	}
	afterHead, e := regular(l.root, headName, c.Verification.MaximumMetadataBytes)
	if e != nil || !bytes.Equal(beforeHead, afterHead) {
		l.close()
		t.Fatal("completed repeat changed head", e)
	}
	l.close()
	final := status(ctx)
	if final.RestoreStores == nil || final.RestoreDatabase == nil || final.RestoreSQL == nil || final.RestoreStores.ProofSHA256 != beforeStatus.RestoreStores.ProofSHA256 || final.RestoreDatabase.ProofSHA256 != beforeStatus.RestoreDatabase.ProofSHA256 || final.RestoreSQL.ProofSHA256 != beforeStatus.RestoreSQL.ProofSHA256 {
		t.Fatal("earlier restore proof changed")
	}
	var logicalBytes int64
	for _, file := range plan.ConfigurationFiles {
		logicalBytes += file.Bytes
	}
	qualified = true
	put("actual.json", map[string]any{"status": "passed_actual_retained_TLS_preparation_with_real_copy_response_loss", "scope_id": retained.ScopeID, "TLS_result": result, "plan_sha256": wire.Digest(encode(plan)), "configuration_volume": volume.Name, "logical_payload_bytes": logicalBytes, "physical_volume_disk_bytes": "not_measured", "volume_unused": true, "utility_absent": true, "copy_calls": loss.calls, "actual_successful_copy_response_loss_reconciled_without_resend": true, "effect_start_intents": counts, "original_descriptor_payload_runtime_and_restore_proofs_unchanged": true, "journal_suffix_contains_only_TLS_work": true, "terminal_repeat_journal_and_head_identical": true, "application_activation_refused": true, "control_not_replaced": true, "qualification": "actual_local_Docker_API_configuration_preparation_only_not_replacement_activation_browser_or_native_target"})
}
