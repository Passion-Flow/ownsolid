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

// The sole injected fault loses the response after the real fixed SDK delete.
// Every target observation, file readback and subsequent create is real.
type actualControlRemovalLoss struct {
	*engine.Client
	calls int
	lost  bool
}

func (c *actualControlRemovalLoss) RemoveStoppedControl(ctx context.Context, scope string, previous engine.RuntimeContainer) error {
	c.calls++
	e := c.Client.RemoveStoppedControl(ctx, scope, previous)
	if e == nil && !c.lost {
		c.lost = true
		return errors.New("SYNTHETIC_REAL_STOPPED_CONTROL_DELETE_RESPONSE_LOST")
	}
	return e
}

func TestActualRetainedControlReplacementDeleteLoss(t *testing.T) {
	retention := os.Getenv("PROJECT_ONE_TEST_CONTROL_REPLACEMENT_RETENTION")
	if retention == "" {
		t.Skip("explicit isolated TLS-prepared stopped target required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 110*time.Second)
	defer cancel()
	read := func(path string, out any) {
		raw, e := bundle.ReadProtectedConfiguration(path)
		if e != nil || wire.UniqueJSON(raw, out) != nil {
			t.Fatal("protected retained target required", e)
		}
	}
	var retained struct {
		ScopeID     string `json:"scope_id"`
		Config      string `json:"operator_config"`
		TLSRequest  string `json:"TLS_request"`
		Qualified   bool   `json:"qualified_TLS_preparation"`
		Observation Status `json:"status_observation"`
	}
	read(retention, &retained)
	if !retained.Qualified || retained.Observation.TLSProjection == nil || retained.Observation.TLSProjection.Pending || retained.Observation.TLSProjection.ProofSHA256 == "" {
		t.Fatal("completed TLS preparation required")
	}
	raw, e := bundle.ReadProtectedConfiguration(retained.Config)
	if e != nil {
		t.Fatal(e)
	}
	c, e := configuration.Load(raw, nil)
	if e != nil || !c.Verification.AllowTestFixture || c.Engine == nil || c.Engine.Utility == nil || c.Compose == nil {
		t.Fatal("isolated protected configuration required", e)
	}
	out := os.Getenv("PROJECT_ONE_TEST_CONTROL_REPLACEMENT_EVIDENCE")
	binary := os.Getenv("PROJECT_ONE_TEST_CONTROL_REPLACEMENT_OPERATOR")
	if !privateDirectory(out) || !canonical(binary) {
		t.Fatal("private evidence/current native required")
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
	operator := func(ctx context.Context, action string, request bool) ([]byte, error) {
		args := []string{action, "--config", retained.Config}
		if request {
			args = append(args, "--operation-file", retained.TLSRequest)
		}
		return exec.CommandContext(ctx, binary, args...).CombinedOutput()
	}
	status := func(ctx context.Context) Status {
		raw, e := operator(ctx, "status", false)
		var result Status
		if e != nil || wire.StrictJSON(raw, &result) != nil {
			t.Fatal("current native status", e)
		}
		return result
	}
	beforeStatus := status(ctx)
	if beforeStatus.ScopeID != retained.ScopeID || beforeStatus.Purpose != purpose || !reflect.DeepEqual(beforeStatus.RestoreSQL, retained.Observation.RestoreSQL) || !reflect.DeepEqual(beforeStatus.RestoreDatabase, retained.Observation.RestoreDatabase) || !reflect.DeepEqual(beforeStatus.RestoreStores, retained.Observation.RestoreStores) || !reflect.DeepEqual(beforeStatus.TLSProjection, retained.Observation.TLSProjection) || beforeStatus.ControlReplacement != nil {
		t.Fatal("retained restore/TLS lineage changed or replacement already begun")
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
	for _, service := range verified.observed.Services {
		if service.State != "created" && service.State != "exited" || service.ExitCode != 0 {
			l.close()
			t.Fatal("clean stopped original target required")
		}
	}
	originalRuntime := encode(*runtimeState(l.events).record)
	descriptor := wire.Digest(encode(verified.inputs.descriptor))
	payload := restoreConfigurationDigest(verified.inputs.files["control-config"])
	firstSequence := len(l.events)
	l.close()
	qualified := false
	defer func() {
		cleanup, done := context.WithTimeout(context.Background(), 60*time.Second)
		defer done()
		if _, e := operator(cleanup, "stop-runtime", false); e != nil {
			t.Errorf("scoped stopped retention unresolved: %v", e)
		}
		final := status(cleanup)
		put("retention.json", map[string]any{"status": "retained_control_replacement_checkpoint_core_stopped_activation_pending", "scope_id": retained.ScopeID, "operator_config": retained.Config, "TLS_request": retained.TLSRequest, "native_operator": binary, "TLS_preparation_retention": retention, "qualified_control_replacement": qualified, "status_observation": final, "qualification": "actual_stopped_control_replacement_only_not_activation_serving_browser_or_target"})
	}()
	loss := &actualControlRemovalLoss{Client: client}
	failed, e := replaceRestoredControl(ctx, c, loss, worker, false)
	if e == nil || !failed.Pending || !loss.lost || loss.calls != 1 {
		t.Fatal("actual successful stopped delete with lost response required", e)
	}
	if _, e = operator(ctx, "replace-restored-control", false); e == nil {
		t.Fatal("ordinary replacement resubmitted uncertain effect")
	}
	raw, e = operator(ctx, "stop-runtime", false)
	var stopped ProcessResult
	if e != nil || wire.StrictJSON(raw, &stopped) != nil || !stopped.Pending || stopped.Status != "observed_replacement_peers_stopped_no_process_effect" || len(stopped.Resources.Services) != len(verified.observed.Services)-1 || stopped.Resources.Services["postgres"] != verified.observed.Services["postgres"] {
		t.Fatal("truthful partial scoped stop required", e)
	}
	if _, present := stopped.Resources.Services["control"]; present {
		t.Fatal("missing control fabricated")
	}
	raw, e = operator(ctx, "reconcile-restored-control", false)
	var result ControlReplacementResult
	if e != nil || wire.StrictJSON(raw, &result) != nil || result.Pending || result.ProofSHA256 == "" || result.CompletedSteps != len(controlReplacementSteps()) || result.GenerationID != failed.GenerationID {
		t.Fatal("native independent deletion observation and selected create", e)
	}
	l, e = open(c, false)
	if e != nil {
		t.Fatal(e)
	}
	plan, e := l.readControlReplacementPlan()
	if e != nil {
		l.close()
		t.Fatal(e)
	}
	after, e := l.verifyRuntimeContext(ctx, client, worker, false)
	if e != nil || !bytes.Equal(originalRuntime, encode(*runtimeState(l.events).record)) || descriptor != wire.Digest(encode(after.inputs.descriptor)) || payload != restoreConfigurationDigest(after.inputs.files["control-config"]) {
		l.close()
		t.Fatal("original inputs/runtime record changed", e)
	}
	newID := after.observed.Services["control"].ID
	if newID == "" || newID == verified.observed.Services["control"].ID || after.observed.Services["control"].State != "created" || after.observed.NetworkID != verified.observed.NetworkID || after.observed.IngressNetworkID != verified.observed.IngressNetworkID {
		l.close()
		t.Fatal("new never-started control/same networks required")
	}
	for name, service := range verified.observed.Services {
		if name != "control" && after.observed.Services[name] != service {
			l.close()
			t.Fatal("untouched stopped peer changed")
		}
	}
	counts := map[string]int{}
	for _, event := range l.events[firstSequence:] {
		if event.ControlReplacement == nil || event.ControlReplacement.GenerationID != plan.GenerationID || event.Action != "control_replacement_effect" && event.Action != "control_replacement_reconcile" {
			l.close()
			t.Fatal("non-replacement work replayed")
		}
		if event.Outcome == "started" {
			counts[event.ControlReplacement.Phase]++
		}
	}
	for _, phase := range controlReplacementSteps() {
		if counts[phase] != 1 {
			l.close()
			t.Fatal("effect intent replay", phase, counts[phase])
		}
	}
	tls, e := l.readTLSProjectionPlan(*tlsProjectionState(l.events).record)
	if e != nil {
		l.close()
		t.Fatal(e)
	}
	journal, e := regular(l.root, journalName, c.MaximumJournalBytes)
	if e != nil {
		l.close()
		t.Fatal(e)
	}
	head, e := regular(l.root, headName, c.Verification.MaximumMetadataBytes)
	if e != nil {
		l.close()
		t.Fatal(e)
	}
	l.close()
	if e = client.VerifyVolumeConsumers(ctx, tls.ConfigurationOwner, []string{newID}); e != nil {
		t.Fatal("generation volume exclusive new-control consumer", e)
	}
	if _, found, e := client.InspectUtility(ctx, tls.ConfigurationOwner, tls.Initializer, tls.UID, tls.GID); e != nil || found {
		t.Fatal("replacement inspector retained", e)
	}
	for _, action := range []string{"replace-restored-control", "reconcile-restored-control"} {
		raw, e = operator(ctx, action, false)
		var repeated ControlReplacementResult
		if e != nil || wire.StrictJSON(raw, &repeated) != nil || repeated != result {
			t.Fatal("completed replacement repeat changed result", action, e)
		}
	}
	for _, action := range []string{"start-runtime", "start-database", "abort-restored-tls"} {
		if _, e = operator(ctx, action, action == "abort-restored-tls"); e == nil {
			t.Fatal("replacement opened activation or destructive TLS abort", action)
		}
	}
	l, e = open(c, false)
	if e != nil {
		t.Fatal(e)
	}
	endJournal, je := regular(l.root, journalName, c.MaximumJournalBytes)
	endHead, he := regular(l.root, headName, c.Verification.MaximumMetadataBytes)
	l.close()
	if je != nil || he != nil || !bytes.Equal(journal, endJournal) || !bytes.Equal(head, endHead) {
		t.Fatal("terminal repeat/refusal changed journal/head")
	}
	final := status(ctx)
	if !reflect.DeepEqual(final.RestoreSQL, beforeStatus.RestoreSQL) || !reflect.DeepEqual(final.RestoreDatabase, beforeStatus.RestoreDatabase) || !reflect.DeepEqual(final.RestoreStores, beforeStatus.RestoreStores) || !reflect.DeepEqual(final.TLSProjection, beforeStatus.TLSProjection) {
		t.Fatal("earlier restore/TLS proofs changed")
	}
	qualified = true
	put("actual.json", map[string]any{"status": "passed_actual_retained_control_replacement_delete_loss", "scope_id": retained.ScopeID, "replacement": result, "old_control_id": verified.observed.Services["control"].ID, "new_control_id": newID, "configuration_volume": tls.ConfigurationOwner.VolumeName(), "effect_start_intents": counts, "real_successful_delete_response_loss_reconciled_without_resend": true, "delete_calls": loss.calls, "original_input_runtime_and_restore_TLS_proofs_unchanged": true, "networks_and_stopped_peers_unchanged": true, "inspector_absent": true, "generation_volume_only_new_control_consumer": true, "terminal_repeat_and_refusal_journal_identical": true, "application_and_database_activation_refused": true, "qualification": "actual_local_Docker_API_stopped_control_replacement_only_not_activation_browser_or_native_target"})
}
