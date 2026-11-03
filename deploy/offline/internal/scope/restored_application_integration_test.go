//go:build offlineintegration

package scope

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/tools/release/offline/pkg/bundle"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/compose"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// This wrapper loses only a successful real Compose control-start response.
// Container processes, TLS, SQL, mounted stores and native recovery remain real.
type actualApplicationStartLoss struct {
	*compose.Client
	calls int
	lost  bool
}

// Preserve the concrete SDK's complete read-only capability set. Embedding the
// narrower backupEngine interface would hide current configuration inspection.
type actualEffectiveBackupStartLoss struct {
	*engine.Client
	calls int
	lost  bool
}

var _ backupEngine = (*actualEffectiveBackupStartLoss)(nil)
var _ restoredApplicationEngine = (*actualEffectiveBackupStartLoss)(nil)

func (c *actualEffectiveBackupStartLoss) StartDatabaseTask(ctx context.Context, task engine.DatabaseTask, id string, files []engine.ConfigurationFile, reserve int) (engine.ResourceAdmission, error) {
	c.calls++
	if c.calls != 1 {
		return engine.ResourceAdmission{}, errors.New("ACTUAL_BACKUP_PHYSICAL_EFFECT_REPEATED")
	}
	result, e := c.Client.StartDatabaseTask(ctx, task, id, files, reserve)
	if e == nil {
		c.lost = true
		return result, errors.New("ACTUAL_BACKUP_RESULT_LOSS_AFTER_REAL_EFFECT")
	}
	return result, e
}

// Mutation transport fault injection uses a private invalid-interpreter CLI.
// All read-only inspection, process state, durable history and native recovery
// use the actual configured Engine/Compose scope, never metadata doubles.
type actualProcessRetryTransport struct {
	*compose.Client
	fault              *compose.Client
	databaseCalls      int
	controlCalls       int
	loseControlSuccess bool
}

func (w *actualProcessRetryTransport) StartDatabaseRuntime(ctx context.Context, r compose.RuntimeReader, owner compose.RenderContext, model compose.Model, before compose.RuntimeStatus) (compose.RuntimeStatus, error) {
	w.databaseCalls++
	return w.fault.StartDatabaseRuntime(ctx, r, owner, model, before)
}

func (w *actualProcessRetryTransport) StartControlRuntime(ctx context.Context, r compose.RuntimeReader, owner compose.RenderContext, model compose.Model, before compose.RuntimeStatus) (compose.RuntimeStatus, error) {
	w.controlCalls++
	if !w.loseControlSuccess {
		return w.fault.StartControlRuntime(ctx, r, owner, model, before)
	}
	out, e := w.Client.StartControlRuntime(ctx, r, owner, model, before)
	if e == nil {
		return out, errors.New("SYNTHETIC_REAL_RETRY_SUCCESS_RESPONSE_LOST")
	}
	return out, e
}

func TestActualRetainedRestoredExplicitProcessRetry(t *testing.T) {
	configFile := os.Getenv("PROJECT_ONE_TEST_PROCESS_RETRY_CONFIG")
	if configFile == "" {
		t.Skip("explicit isolated restored scope required")
	}
	binary, out, expectedScope := os.Getenv("PROJECT_ONE_TEST_PROCESS_RETRY_OPERATOR"), os.Getenv("PROJECT_ONE_TEST_PROCESS_RETRY_EVIDENCE"), os.Getenv("PROJECT_ONE_TEST_PROCESS_RETRY_SCOPE")
	if !canonical(binary) || !privateDirectory(out) || !identityPattern.MatchString(expectedScope) {
		t.Fatal("protected operator, evidence and explicit scope required")
	}
	raw, e := bundle.ReadProtectedConfiguration(configFile)
	if e != nil {
		t.Fatal(e)
	}
	c, e := configuration.Load(raw, nil)
	if e != nil || !c.Verification.AllowTestFixture || c.Engine == nil || c.Compose == nil || c.Compose.Process == nil {
		t.Fatal("explicit test fixture configuration required", e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 420*time.Second)
	defer cancel()
	put := func(name string, value []byte) {
		root, e := os.OpenRoot(out)
		if e != nil {
			t.Fatal(e)
		}
		defer root.Close()
		if e = writeBackupFile(root, name, value); e != nil {
			t.Fatal(e)
		}
	}
	operator := func(ctx context.Context, action string, success bool) []byte {
		result, e := exec.CommandContext(ctx, binary, action, "--config", configFile).CombinedOutput()
		id, ne := nonce()
		if ne != nil {
			t.Fatal(ne)
		}
		put("command-"+action+"-"+id+".log", result)
		if success != (e == nil) {
			t.Fatalf("native %s expected success=%t: %v; inspect protected evidence", action, success, e)
		}
		return result
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
	read := func() ([]Event, compose.RuntimeStatus, []byte, []byte) {
		l, e := open(c, false)
		if e != nil {
			t.Fatal(e)
		}
		defer l.close()
		v, e := l.verifyRuntimeContext(ctx, client, worker, false)
		if e != nil || v.scope.ScopeID != expectedScope || processState(l.events).pending != "" {
			t.Fatal("current known isolated runtime required", e)
		}
		journal, je := regular(l.root, journalName, l.maximumJournalBytes())
		head, he := regular(l.root, headName, c.Verification.MaximumMetadataBytes)
		if je != nil || he != nil {
			t.Fatal(je, he)
		}
		return append([]Event{}, l.events...), v.observed, journal, head
	}
	events, before, originalJournal, _ := read()
	if restored := restoreState(events); restored.record == nil || restored.pending != "" {
		t.Fatal("completed actual restore required")
	}
	for _, process := range before.Services {
		if process.State != "exited" || process.ExitCode != 0 {
			t.Fatal("all exact retained peers must be clean stopped")
		}
	}
	qualified := false
	defer func() {
		cleanup, done := context.WithTimeout(context.Background(), 100*time.Second)
		defer done()
		statusRaw, statusError := exec.CommandContext(cleanup, binary, "status", "--config", configFile).CombinedOutput()
		var observed Status
		actions := []string{}
		if statusError != nil || wire.StrictJSON(statusRaw, &observed) != nil {
			t.Error("independent cleanup status unavailable", statusError)
		} else {
			if observed.RestoredCurrentStores != nil && observed.RestoredCurrentStores.Pending {
				actions = append(actions, "abort-restored-current-stores")
			}
			if observed.RestoredLiveDatabase != nil && observed.RestoredLiveDatabase.Pending {
				actions = append(actions, "abort-restored-live-database")
			}
		}
		actions = append(actions, "reconcile-process", "stop-runtime")
		for _, action := range actions {
			result, e := exec.CommandContext(cleanup, binary, action, "--config", configFile).CombinedOutput()
			put("cleanup-"+action+".log", result)
			if e != nil {
				t.Errorf("bounded independent cleanup %s failed: %v", action, e)
			}
		}
		stopped := true
		for name, prior := range before.Services {
			object, found, e := client.InspectRuntime(cleanup, expectedScope, name)
			if e != nil || !found || object.ID != prior.ID || object.State.Status != "exited" || object.State.ExitCode != 0 || object.State.PID != 0 || object.State.Running || object.State.OOMKilled || object.State.Dead || object.State.Restarting || object.State.Paused {
				stopped = false
			}
		}
		if !stopped {
			t.Error("same owned peers were not independently stopped0")
		}
		put("retention.json", encode(map[string]any{"status": "retained_actual_retry_scope_stopped", "scope_id": expectedScope, "operator_config": configFile, "qualified": qualified && !t.Failed() && stopped, "scope_processes_stopped": stopped}))
	}()
	fault := &actualProcessRetryTransport{Client: worker, fault: actualCreationFailureAdapter(t, c)}
	attempt := func(selected string, retry bool, mode restoredProcessMode, w processOperator) ProcessResult {
		l, e := open(c, true)
		if e != nil {
			t.Fatal(e)
		}
		defer l.close()
		result, e := l.processSelectedAttemptLocked(ctx, client, w, "running", false, []string{selected}, mode, retry)
		if e == nil || !retry && (result.Pending || result.Status != "recorded_process_unsubmitted") || retry && !result.Pending {
			t.Fatal("expected actual unsubmitted or unknown retry outcome", selected, result, e)
		}
		return result
	}
	refusal := func(action string) {
		_, _, journal, head := read()
		operator(ctx, action, false)
		_, _, after, afterHead := read()
		if !bytes.Equal(journal, after) || !bytes.Equal(head, afterHead) {
			t.Fatal("refusal changed durable history", action)
		}
	}
	checkRetry := func(selected string, afterStart []Event, previousID string) {
		latest := processState(afterStart)
		if latest.pending != "" || latest.outcome != "completed" || latest.record == nil || latest.record.AttemptVersion != processAttemptVersion || latest.record.RetryOfOperationID != previousID || !reflect.DeepEqual(latest.record.SelectedServices, []string{selected}) {
			t.Fatal("native retry lost immutable predecessor or selection", selected)
		}
	}
	attempt("postgres", false, restoredDependencyProcess, fault)
	events, _, _, _ = read()
	pgFailure := processState(events)
	if pgFailure.outcome != "unsubmitted" || pgFailure.record.Submission != processSubmissionNotSubmitted || fault.databaseCalls != 1 {
		t.Fatal("actual PostgreSQL unsubmitted proof missing")
	}
	pgFailureID := retryableStartID(events, pgFailure.record.CreationSHA256, []string{"postgres"})
	refusal("start-restored-database")
	operator(ctx, "retry-restored-database", true)
	events, afterPG, _, _ := read()
	checkRetry("postgres", events, pgFailureID)
	if afterPG.Services["postgres"].State != "running" || !reflect.DeepEqual(afterPG.Services["control"], before.Services["control"]) {
		t.Fatal("retry changed unselected peer")
	}
	for _, action := range []string{"requalify-restored-live-database", "requalify-restored-current-stores"} {
		operator(ctx, action, true)
	}
	attempt("control", false, restoredApplicationProcess, fault)
	events, _, _, _ = read()
	controlFailure := processState(events)
	controlFailureID := retryableStartID(events, controlFailure.record.CreationSHA256, []string{"control"})
	refusal("start-restored-application")
	operator(ctx, "retry-restored-application", true)
	events, afterControl, _, _ := read()
	checkRetry("control", events, controlFailureID)
	if afterControl.Services["control"].State != "running" || !reflect.DeepEqual(afterControl.Services["postgres"], afterPG.Services["postgres"]) {
		t.Fatal("actual control retry changed PostgreSQL")
	}
	operator(ctx, "stop-runtime", true)
	for _, action := range []string{"start-restored-database", "requalify-restored-live-database", "requalify-restored-current-stores"} {
		operator(ctx, action, true)
	}
	attempt("control", false, restoredApplicationProcess, fault)
	events, _, _, _ = read()
	secondFailure := processState(events)
	secondFailureID := retryableStartID(events, secondFailure.record.CreationSHA256, []string{"control"})
	fault.loseControlSuccess = true
	attempt("control", true, restoredApplicationProcess, fault)
	for _, action := range []string{"retry-restored-application", "start-restored-application", "start-runtime"} {
		operator(ctx, action, false)
	}
	operator(ctx, "reconcile-process", true)
	events, after, journal, head := read()
	checkRetry("control", events, secondFailureID)
	if fault.databaseCalls != 1 || fault.controlCalls != 3 || !bytes.HasPrefix(journal, originalJournal) || after.NetworkID != before.NetworkID || after.IngressNetworkID != before.IngressNetworkID {
		t.Fatal("effect counts, original history or network identity changed")
	}
	operator(ctx, "start-restored-application", true)
	_, _, repeated, repeatedHead := read()
	if !bytes.Equal(journal, repeated) || !bytes.Equal(head, repeatedHead) {
		t.Fatal("completed repeat resent mutation or changed journal")
	}
	// Both entrances use the already qualified current mounted TLS generation.
	l, e := open(c, false)
	if e != nil {
		t.Fatal(e)
	}
	plan, files, e := l.currentControlReplacementFiles(ctx)
	l.close()
	if e != nil {
		t.Fatal(e)
	}
	runtimeRaw, e := restoreNamedFile(files, "runtime.yaml")
	var runtime struct {
		Server struct{ Portal, Admin string }
	}
	// Runtime fields have the application's standard snake_case spelling.
	var runtimeFields struct {
		Server map[string]any `json:"server"`
	}
	if e != nil || wire.UniqueJSON(runtimeRaw, &runtimeFields) != nil {
		t.Fatal("effective runtime required", e)
	}
	runtime.Server.Portal, _ = runtimeFields.Server["portal_origin"].(string)
	runtime.Server.Admin, _ = runtimeFields.Server["admin_origin"].(string)
	trust, e := protectedInput(c, plan.Request.TrustBundleFile, c.Verification.MaximumMetadataBytes)
	roots := x509.NewCertPool()
	if e != nil || !roots.AppendCertsFromPEM(trust) {
		t.Fatal("current trust required", e)
	}
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}, Proxy: nil}
	defer transport.CloseIdleConnections()
	httpClient := &http.Client{Transport: transport, Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	for _, origin := range []string{runtime.Server.Portal, runtime.Server.Admin} {
		u, e := url.Parse(origin)
		if e != nil || u.Scheme != "https" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || net.ParseIP(u.Hostname()) == nil || !net.ParseIP(u.Hostname()).IsLoopback() {
			t.Fatal("explicit retained loopback HTTPS required")
		}
		request, e := http.NewRequestWithContext(ctx, http.MethodGet, origin+"/health/ready", nil)
		if e != nil {
			t.Fatal(e)
		}
		response, e := httpClient.Do(request)
		if e != nil {
			t.Fatal("verified current TLS readiness", e)
		}
		_, readError := io.Copy(io.Discard, io.LimitReader(response.Body, 1<<20))
		response.Body.Close()
		if response.StatusCode != http.StatusOK || readError != nil {
			t.Fatal("actual health failed", readError)
		}
	}
	for name, prior := range before.Services {
		if after.Services[name].ID != prior.ID {
			t.Fatal("actual peer replaced")
		}
	}
	put("actual.json", encode(map[string]any{"status": "passed_actual_restored_explicit_process_retry", "scope_id": expectedScope, "native_postgres_retry": true, "native_control_retry": true, "real_adapter_unsubmitted_failures": 3, "successful_retry_response_lost": true, "native_unknown_retry_refused": true, "native_readonly_reconcile_without_resend": true, "completed_repeat_journal_head_unchanged": true, "original_history_runtime_and_network_preserved": true, "both_current_HTTPS_entrances_ready": true, "fault_database_calls": fault.databaseCalls, "fault_control_calls": fault.controlCalls, "qualification": "Actual selected local Engine/Compose/journal/native recovery with explicitly injected private mutation-transport faults; not a shared Docker binary failure, actual nonzero process crash, browser or native hardware qualification", "operator_basename": filepath.Base(binary)}))
	qualified = true
}

func (w *actualApplicationStartLoss) StartControlRuntime(ctx context.Context, r compose.RuntimeReader, scope compose.RenderContext, model compose.Model, before compose.RuntimeStatus) (compose.RuntimeStatus, error) {
	w.calls++
	result, e := w.Client.StartControlRuntime(ctx, r, scope, model, before)
	if e == nil && !w.lost {
		w.lost = true
		return result, errors.New("SYNTHETIC_REAL_CONTROL_START_RESPONSE_LOST")
	}
	return result, e
}

type actualNonzeroControlStart struct {
	*compose.Client
	docker, socket, privateClient, expectedID, evidence string
	calls, kills                                        int
}

func (w *actualNonzeroControlStart) StartControlRuntime(ctx context.Context, reader compose.RuntimeReader, scope compose.RenderContext, model compose.Model, before compose.RuntimeStatus) (compose.RuntimeStatus, error) {
	w.calls++
	if w.calls != 1 || before.Services["control"].ID != w.expectedID {
		return compose.RuntimeStatus{}, errors.New("ACTUAL_NONZERO_EXACT_ONE_OWNED_START_REQUIRED")
	}
	out, e := w.Client.StartControlRuntime(ctx, reader, scope, model, before)
	if e != nil || out.Services["control"].ID != w.expectedID || out.Services["control"].State != "running" {
		return out, errors.New("ACTUAL_NONZERO_REAL_RUNNING_START_REQUIRED")
	}
	root, e := os.OpenRoot(w.evidence)
	if e != nil {
		return out, e
	}
	defer root.Close()
	if e = writeBackupFile(root, "fault-intent.json", encode(map[string]any{"scope_id": scope.ScopeID, "control_id": w.expectedID, "signal": "SIGKILL", "operation": "one_explicit_disposable_test_control_fault_after_actual_start"})); e != nil {
		return out, e
	}
	value, e := exec.CommandContext(ctx, w.docker, "--config", w.privateClient, "--host", "unix://"+w.socket, "kill", "--signal=SIGKILL", w.expectedID).CombinedOutput()
	w.kills++
	if e != nil || strings.TrimSpace(string(value)) != w.expectedID {
		return out, errors.New("ACTUAL_NONZERO_EXACT_KILL_OUTCOME_UNRESOLVED")
	}
	deadline := time.NewTimer(15 * time.Second)
	defer deadline.Stop()
	for {
		object, found, e := reader.InspectRuntime(ctx, scope.ScopeID, "control")
		if e != nil || !found || object.ID != w.expectedID {
			return out, errors.New("ACTUAL_NONZERO_ORIGINAL_RUNTIME_UNRESOLVED")
		}
		if !object.State.Running && object.State.Status == "exited" && object.State.ExitCode == 137 && object.State.PID == 0 && !object.State.OOMKilled && !object.State.Restarting && !object.State.Dead && !object.State.Paused {
			if e = writeBackupFile(root, "fault-result.json", encode(map[string]any{"scope_id": scope.ScopeID, "control_id": object.ID, "started_at": object.State.StartedAt, "exit_code": object.State.ExitCode, "oom_killed": false, "actual_kill_calls": w.kills})); e != nil {
				return out, e
			}
			return out, errors.New("ACTUAL_NONZERO_CONTROL_EXIT_OBSERVED_AFTER_REAL_START")
		}
		select {
		case <-ctx.Done():
			return out, errors.New("ACTUAL_NONZERO_OBSERVATION_CANCELLED")
		case <-deadline.C:
			return out, errors.New("ACTUAL_NONZERO_OBSERVATION_DEADLINE")
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func TestActualRetainedNonzeroControlStartAndExplicitRecovery(t *testing.T) {
	configFile := os.Getenv("PROJECT_ONE_TEST_PROCESS_NONZERO_CONFIG")
	if configFile == "" {
		t.Skip("explicit disposable accepted synthetic target required")
	}
	binary, out := os.Getenv("PROJECT_ONE_TEST_PROCESS_NONZERO_OPERATOR"), os.Getenv("PROJECT_ONE_TEST_PROCESS_NONZERO_EVIDENCE")
	retainRunning := os.Getenv("PROJECT_ONE_TEST_PROCESS_NONZERO_RETAIN_RUNNING")
	if retainRunning != "" && retainRunning != "1" {
		t.Fatal("explicit fixed browser handoff selection required")
	}
	raw, e := bundle.ReadProtectedConfiguration(configFile)
	if e != nil {
		t.Fatal("private exact configuration required", e)
	}
	c, e := configuration.Load(raw, nil)
	if e != nil || !c.Verification.AllowTestFixture || c.Engine == nil || c.Compose == nil || !privateDirectory(out) || !canonical(binary) {
		t.Fatal("explicit private synthetic target/operator/evidence required", e)
	}
	var expected struct {
		ScopeID string            `json:"scope_id"`
		Config  string            `json:"configuration_sha256"`
		Peers   map[string]string `json:"runtime_ids"`
	}
	acceptance, e := bundle.ReadProtectedConfiguration(os.Getenv("PROJECT_ONE_TEST_PROCESS_NONZERO_ACCEPTANCE"))
	if e != nil || wire.StrictJSON(acceptance, &expected) != nil || !identityPattern.MatchString(expected.ScopeID) || expected.Config != wire.Digest(raw) || len(expected.Peers) != 2 || !utilityIDPattern.MatchString(expected.Peers["control"]) || !utilityIDPattern.MatchString(expected.Peers["postgres"]) {
		t.Fatal("exact accepted disposable scope/runtime binding required", e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 360*time.Second)
	defer cancel()
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
	operator := func(ctx context.Context, action string, success bool) []byte {
		value, e := exec.CommandContext(ctx, binary, action, "--config", configFile).CombinedOutput()
		id, ne := nonce()
		if ne != nil {
			t.Fatal(ne)
		}
		put("command-"+action+"-"+id+".json", map[string]any{"exit_success": e == nil, "output": string(value)})
		if (e == nil) != success {
			t.Fatal("exact native outcome unresolved; inspect private result", action)
		}
		return value
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
	read := func() ([]Event, compose.RuntimeStatus, []byte, []byte) {
		l, e := open(c, false)
		if e != nil {
			t.Fatal(e)
		}
		defer l.close()
		if l.marker.ScopeID != expected.ScopeID || l.marker.Purpose != purpose {
			t.Fatal("exact isolated target marker required")
		}
		verified, e := l.verifyRuntimeContext(ctx, client, worker, false)
		if e != nil {
			t.Fatal("current actual runtime context", e)
		}
		journal, je := regular(l.root, journalName, l.maximumJournalBytes())
		head, he := regular(l.root, headName, c.Verification.MaximumMetadataBytes)
		if je != nil || he != nil {
			t.Fatal(je, he)
		}
		return append([]Event{}, l.events...), verified.observed, journal, head
	}
	_, before, originalJournal, _ := read()
	for name, peer := range before.Services {
		if peer.ID != expected.Peers[name] || peer.State != "exited" || peer.ExitCode != 0 {
			t.Fatal("exact clean stopped original peers required")
		}
	}
	qualified := false
	defer func() {
		if qualified && !t.Failed() && retainRunning == "1" {
			put("retention.json", map[string]any{"scope_id": expected.ScopeID, "operator_config": configFile, "qualified": true, "scope_processes_running_for_browser": true, "scope_processes_stopped": false, "qualification": "Exact same recovered peers retained only for the owning bounded browser qualifier; its finally must reconcile/stop and preserve evidence"})
			return
		}
		cleanup, done := context.WithTimeout(context.Background(), 100*time.Second)
		defer done()
		for _, action := range []string{"reconcile-process", "stop-runtime"} {
			value, e := exec.CommandContext(cleanup, binary, action, "--config", configFile).CombinedOutput()
			put("cleanup-"+action+".json", map[string]any{"exit_success": e == nil, "output": string(value)})
			if e != nil {
				t.Error("owned recovery checkpoint requires attention", action)
			}
		}
		stopped := true
		for name, id := range expected.Peers {
			object, found, e := client.InspectRuntime(cleanup, expected.ScopeID, name)
			if e != nil || !found || object.ID != id || object.State.Running || object.State.Status != "exited" || object.State.ExitCode != 0 || object.State.PID != 0 || object.State.OOMKilled {
				stopped = false
			}
		}
		put("retention.json", map[string]any{"scope_id": expected.ScopeID, "operator_config": configFile, "qualified": qualified && !t.Failed() && stopped, "scope_processes_stopped": stopped, "qualification": "Preserve original scope and any failed/nonzero/unknown intent; never recreate or automatically resend recovery"})
	}()
	for _, action := range []string{"start-restored-database", "requalify-restored-live-database", "requalify-restored-current-stores"} {
		operator(ctx, action, true)
	}
	_, admitted, _, _ := read()
	pgBefore, found, e := client.InspectRuntime(ctx, expected.ScopeID, "postgres")
	if e != nil || !found || pgBefore.ID != expected.Peers["postgres"] || pgBefore.State.Status != "running" {
		t.Fatal("original PostgreSQL admission required", e)
	}
	private := filepath.Join(out, "docker-client")
	if os.Mkdir(private, 0700) != nil || os.WriteFile(filepath.Join(private, "config.json"), []byte("{}\n"), 0600) != nil {
		t.Fatal("exclusive private Docker client required")
	}
	fault := &actualNonzeroControlStart{Client: worker, docker: c.Compose.DockerBinary, socket: c.Engine.SocketFile, privateClient: private, expectedID: expected.Peers["control"], evidence: out}
	l, e := open(c, true)
	if e != nil {
		t.Fatal(e)
	}
	partial, effectError := l.processSelectedAttemptLocked(ctx, client, fault, "running", false, []string{"control"}, restoredApplicationProcess, false)
	l.close()
	if effectError == nil || !partial.Pending || partial.Status != "recorded_process_uncertain" || fault.calls != 1 || fault.kills != 1 {
		t.Fatal("post-submission error must preserve original uncertain intent; retain checkpoint", partial, effectError)
	}
	uncertainEvents, _, _, _ := read()
	originalIntent := processState(uncertainEvents).pending
	if originalIntent == "" {
		t.Fatal("original pending start intent required")
	}
	// A lost response cannot prove the result. Reconcile the original intent from
	// live observations; a recorded failed start intentionally returns nonzero.
	if value := operator(ctx, "reconcile-process", false); strings.TrimSpace(string(value)) != "OFFLINE_SCOPE_PROCESS_START_FAILED_OBSERVED" {
		t.Fatal("exact native failed-start observation required; retain original intent")
	}
	events, crashed, failedJournal, failedHead := read()
	failed := processState(events)
	if failed.outcome != "failed" || failed.pending != "" || failed.record == nil || failed.record.After["control"].ExitCode != 137 || !reflect.DeepEqual(crashed.Services["postgres"], admitted.Services["postgres"]) {
		t.Fatal("actual failed predecessor or unchanged PostgreSQL missing")
	}
	predecessor := retryableStartID(events, failed.record.CreationSHA256, []string{"control"})
	crashedControl, found, e := client.InspectRuntime(ctx, expected.ScopeID, "control")
	if predecessor != originalIntent || e != nil || !found || crashedControl.ID != expected.Peers["control"] || crashedControl.State.ExitCode != 137 || crashedControl.State.OOMKilled || crashedControl.State.Running {
		t.Fatal("native failed reconciliation must preserve original non-OOM intent and peer")
	}
	for _, action := range []string{"start-restored-application", "retry-restored-database"} {
		operator(ctx, action, false)
	}
	_, _, afterRefusal, afterRefusalHead := read()
	if !bytes.Equal(failedJournal, afterRefusal) || !bytes.Equal(failedHead, afterRefusalHead) {
		t.Fatal("ordinary or cross-service refusal changed history")
	}
	operator(ctx, "reconcile-process", true)
	operator(ctx, "retry-restored-application", true)
	events, after, journal, head := read()
	recovered := processState(events)
	pgAfter, found, e := client.InspectRuntime(ctx, expected.ScopeID, "postgres")
	if e != nil || !found || pgAfter.ID != pgBefore.ID || pgAfter.State.PID != pgBefore.State.PID || pgAfter.State.StartedAt != pgBefore.State.StartedAt || recovered.outcome != "completed" || recovered.record.RetryOfOperationID != predecessor || predecessor == "" || after.Services["control"].ID != expected.Peers["control"] || after.Services["control"].State != "running" || !bytes.HasPrefix(journal, originalJournal) || !bytes.HasPrefix(journal, failedJournal) || after.NetworkID != before.NetworkID || after.IngressNetworkID != before.IngressNetworkID {
		t.Fatal("exact predecessor/current retry/database/runtime history not preserved", e)
	}
	retryIntents := 0
	for _, event := range events {
		if event.Action == "process_start" && event.Outcome == "started" && event.Process != nil && event.Process.RetryOfOperationID == predecessor {
			retryIntents++
		}
	}
	if retryIntents != 1 {
		t.Fatal("exactly one predecessor-bound retry start intent required")
	}
	operator(ctx, "retry-restored-application", false)
	operator(ctx, "start-restored-application", true)
	_, _, repeated, repeatedHead := read()
	if !bytes.Equal(journal, repeated) || !bytes.Equal(head, repeatedHead) || fault.calls != 1 || fault.kills != 1 {
		t.Fatal("completed repeat or consumed predecessor replayed")
	}
	put("actual.json", map[string]any{"status": "passed_actual_nonzero_control_start_and_native_explicit_recovery", "scope_id": expected.ScopeID, "exit_code": 137, "oom_killed": false, "initial_result": "recorded_process_uncertain", "native_failed_reconcile_code": "OFFLINE_SCOPE_PROCESS_START_FAILED_OBSERVED", "fault_start_calls": fault.calls, "actual_kill_calls": fault.kills, "native_retry_start_intents": retryIntents, "failed_predecessor": predecessor, "native_reconciliation_without_resend": true, "ordinary_and_cross_service_refused": true, "explicit_retry_preserved_failed_history": true, "completed_repeat_journal_identical": true, "postgres_ID_PID_start_time_unchanged": true, "runtime_network_IDs_unchanged": true, "qualification": "Actual selected local process crash/native recovery only; subsequent real browser/data and native target qualification remain separate"})
	qualified = true
}

func TestActualRetainedRestoredApplicationActivation(t *testing.T) {
	mode := os.Getenv("PROJECT_ONE_TEST_RESTORED_APPLICATION_MODE")
	if mode != "" && mode != "known-control-restart" {
		t.Fatal("explicit fixed activation case required")
	}
	restart := mode == "known-control-restart"
	retention := os.Getenv("PROJECT_ONE_TEST_RESTORED_APPLICATION_RETENTION")
	if retention == "" {
		t.Skip("explicit isolated accepted C11 target required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 240*time.Second)
	defer cancel()
	var retained struct {
		ScopeID     string `json:"scope_id"`
		Config      string `json:"operator_config"`
		TLSRequest  string `json:"TLS_request"`
		Qualified   bool   `json:"qualified_current_stores_checks"`
		Observation Status `json:"status_observation"`
	}
	raw, e := bundle.ReadProtectedConfiguration(retention)
	if e != nil || wire.UniqueJSON(raw, &retained) != nil || !retained.Qualified || retained.Observation.RestoredCurrentStores == nil || retained.Observation.RestoredCurrentStores.Pending {
		t.Fatal("accepted private C11 target required", e)
	}
	raw, e = bundle.ReadProtectedConfiguration(retained.Config)
	if e != nil {
		t.Fatal(e)
	}
	c, e := configuration.Load(raw, nil)
	if e != nil || !c.Verification.AllowTestFixture || c.Engine == nil || c.Compose == nil || c.Compose.Process == nil {
		t.Fatal("isolated protected configuration required", e)
	}
	out, binary := os.Getenv("PROJECT_ONE_TEST_RESTORED_APPLICATION_EVIDENCE"), os.Getenv("PROJECT_ONE_TEST_RESTORED_APPLICATION_OPERATOR")
	if !privateDirectory(out) || !canonical(binary) {
		t.Fatal("private evidence/current operator required")
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
	operator := func(ctx context.Context, action string) ([]byte, error) {
		result, e := exec.CommandContext(ctx, binary, action, "--config", retained.Config).CombinedOutput()
		id, nonceError := nonce()
		if nonceError != nil {
			t.Fatal(nonceError)
		}
		root, openError := os.OpenRoot(out)
		if openError != nil {
			t.Fatal(openError)
		}
		defer root.Close()
		if writeError := writeBackupFile(root, "command-"+action+"-"+id+".log", result); writeError != nil {
			t.Fatal(writeError)
		}
		return result, e
	}
	status := func(ctx context.Context) Status {
		raw, e := operator(ctx, "status")
		var s Status
		if e != nil || wire.StrictJSON(raw, &s) != nil {
			t.Fatal("current native status", e)
		}
		return s
	}
	beforeStatus := status(ctx)
	if beforeStatus.ScopeID != retained.ScopeID || beforeStatus.Purpose != purpose || !restart && !reflect.DeepEqual(beforeStatus.RestoredCurrentStores, retained.Observation.RestoredCurrentStores) {
		t.Fatal("retained target qualification changed; continue its current checkpoint")
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
	before, e := l.verifyRuntimeContext(ctx, client, worker, false)
	if e != nil {
		l.close()
		t.Fatal(e)
	}
	for _, event := range l.events {
		if !restart && event.Process != nil && event.Process.RestoredApplication != nil {
			l.close()
			t.Fatal("activation already attempted; continue retained checkpoint without replaying this loss fixture")
		}
	}
	expectedControl := "created"
	if restart {
		expectedControl = "exited"
		object, found, inspectError := client.InspectRuntime(ctx, retained.ScopeID, "control")
		if inspectError != nil || !found || !knownActivatedControl(l.events, ProcessSnapshot{ID: object.ID, State: object.State.Status, ExitCode: object.State.ExitCode, StartedAt: object.State.StartedAt}) {
			l.close()
			t.Fatal("known completed activation history required for the distinct restart case")
		}
	}
	if processState(l.events).pending != "" || before.observed.Services["control"].State != expectedControl || before.observed.Services["postgres"].State != "exited" || before.observed.Services["postgres"].ExitCode != 0 {
		l.close()
		t.Fatal("clean stopped C11 target required")
	}
	tlsPlan, files, e := l.currentControlReplacementFiles(ctx)
	if e != nil {
		l.close()
		t.Fatal(e)
	}
	var runtime struct {
		Server struct {
			Portal string `json:"portal_origin"`
			Admin  string `json:"admin_origin"`
		} `json:"server"`
	}
	runtimeRaw, e := restoreNamedFile(files, "runtime.yaml")
	if e != nil || wire.UniqueJSON(runtimeRaw, &runtime) != nil {
		l.close()
		t.Fatal("effective public origins required")
	}
	trust, e := protectedInput(c, tlsPlan.Request.TrustBundleFile, c.Verification.MaximumMetadataBytes)
	if e != nil {
		l.close()
		t.Fatal(e)
	}
	originalRuntime, effectiveRuntime := encode(*runtimeState(l.events).record), encode(*effectiveRuntimeState(l.events).record)
	originalJournal, e := regular(l.root, journalName, l.maximumJournalBytes())
	firstSequence := len(l.events)
	l.close()
	if e != nil {
		t.Fatal(e)
	}
	qualified := false
	defer func() {
		cleanup, done := context.WithTimeout(context.Background(), 90*time.Second)
		defer done()
		s := status(cleanup)
		for _, item := range []struct {
			pending bool
			action  string
		}{{s.RestoredCurrentStores != nil && s.RestoredCurrentStores.Pending, "abort-restored-current-stores"}, {s.RestoredLiveDatabase != nil && s.RestoredLiveDatabase.Pending, "abort-restored-live-database"}} {
			if item.pending {
				if _, e := operator(cleanup, item.action); e != nil {
					t.Errorf("generated verification cleanup unresolved: %v", e)
				}
			}
		}
		if _, e := operator(cleanup, "reconcile-process"); e != nil {
			t.Errorf("independent process reconciliation: %v", e)
		}
		if _, e := operator(cleanup, "stop-runtime"); e != nil {
			t.Errorf("owned scope shutdown: %v", e)
		}
		final := status(cleanup)
		stopped := true
		for name, expected := range before.observed.Services {
			object, found, e := client.InspectRuntime(cleanup, retained.ScopeID, name)
			if e != nil || !found || object.ID != expected.ID || object.State.Status != "created" && object.State.Status != "exited" || object.State.Running || object.State.PID != 0 || object.State.ExitCode != 0 || object.State.OOMKilled || object.State.Restarting || object.State.Paused || object.State.Dead {
				stopped = false
			}
		}
		if !stopped {
			t.Error("exact owned process shutdown not independently verified")
		}
		put("retention.json", map[string]any{"status": "retained_restored_application_activation_core_stopped_browser_pending", "scope_id": retained.ScopeID, "operator_config": retained.Config, "TLS_request": retained.TLSRequest, "native_operator": binary, "current_stores_retention": retention, "qualified_application_process": qualified && !t.Failed() && stopped, "scope_processes_stopped": stopped, "status_observation": final, "qualification": "actual_local_control_process_TLS_readiness_only_browser_authority_Workspace_export_restart_and_native_pending"})
	}()
	if _, e = operator(ctx, "start-restored-application"); e == nil {
		t.Fatal("stopped PostgreSQL admitted application activation")
	}
	for _, action := range []string{"start-restored-database", "requalify-restored-live-database", "requalify-restored-current-stores"} {
		if _, e = operator(ctx, action); e != nil {
			t.Fatal("current process qualification", action, e)
		}
	}
	l, e = open(c, true)
	if e != nil {
		t.Fatal(e)
	}
	admission, e := l.admitRestoredApplication(ctx, client, worker)
	l.close()
	if e != nil || admission.PostgresPID <= 0 {
		t.Fatal("current activation admission", e)
	}
	loss := &actualApplicationStartLoss{Client: worker}
	l, e = open(c, true)
	if e != nil {
		t.Fatal(e)
	}
	failed, e := l.processSelectedWithAdmissionLocked(ctx, client, loss, "running", false, []string{"control"}, restoredApplicationProcess)
	l.close()
	if e == nil || !failed.Pending || !loss.lost || loss.calls != 1 {
		t.Fatal("actual successful start response loss required", failed, e)
	}
	for _, action := range []string{"start-restored-application", "start-runtime", "start-database"} {
		if _, e = operator(ctx, action); e == nil {
			t.Fatal("unknown activation resent or generic start admitted", action)
		}
	}
	if _, e = operator(ctx, "reconcile-process"); e != nil {
		t.Fatal("native readonly recovery", e)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(trust) {
		t.Fatal("actual serving trust required")
	}
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}, Proxy: nil}
	defer transport.CloseIdleConnections()
	httpClient := &http.Client{Transport: transport, Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	for _, origin := range []string{runtime.Server.Portal, runtime.Server.Admin} {
		u, e := url.Parse(origin)
		if e != nil || u.Scheme != "https" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" || net.ParseIP(u.Hostname()) == nil || !net.ParseIP(u.Hostname()).IsLoopback() {
			t.Fatal("explicit loopback HTTPS profile required")
		}
		deadline := time.Now().Add(60 * time.Second)
		for {
			request, e := http.NewRequestWithContext(ctx, http.MethodGet, origin+"/health/ready", nil)
			if e != nil {
				t.Fatal(e)
			}
			response, e := httpClient.Do(request)
			ready := false
			if e == nil {
				_, readError := io.Copy(io.Discard, io.LimitReader(response.Body, 1<<20))
				response.Body.Close()
				ready = response.StatusCode == http.StatusOK && readError == nil
			}
			if ready {
				break
			}
			if ctx.Err() != nil || time.Now().After(deadline) {
				t.Fatal("real restored HTTPS readiness deadline")
			}
			time.Sleep(250 * time.Millisecond)
		}
	}
	l, e = open(c, false)
	if e != nil {
		t.Fatal(e)
	}
	current, e := l.verifyRuntimeContext(ctx, client, worker, false)
	if e != nil {
		l.close()
		t.Fatal(e)
	}
	process := processState(l.events)
	if process.pending != "" || process.outcome != "completed" || process.record == nil || !reflect.DeepEqual(process.record.RestoredApplication, admission) || !reflect.DeepEqual(process.record.Before["postgres"], process.record.After["postgres"]) {
		l.close()
		t.Fatal("exact process admission/unchanged dependency required")
	}
	journal, je := regular(l.root, journalName, l.maximumJournalBytes())
	head, he := regular(l.root, headName, c.Verification.MaximumMetadataBytes)
	intents, completions := 0, 0
	for _, event := range l.events[firstSequence:] {
		if event.Process != nil && event.Process.RestoredApplication != nil {
			if event.Outcome == "started" {
				intents++
			}
			if event.Outcome == "completed" {
				completions++
			}
		} else if event.Process == nil && event.RestoredLiveDatabase == nil && event.RestoredCurrentStores == nil {
			l.close()
			t.Fatal("unrelated restore/import/authority operation replayed")
		}
	}
	if je != nil || he != nil || intents != 1 || completions != 1 || !bytes.HasPrefix(journal, originalJournal) || !bytes.Equal(originalRuntime, encode(*runtimeState(l.events).record)) || !bytes.Equal(effectiveRuntime, encode(*effectiveRuntimeState(l.events).record)) || current.observed.Services["control"].ID != before.observed.Services["control"].ID || current.observed.NetworkID != before.observed.NetworkID || current.observed.IngressNetworkID != before.observed.IngressNetworkID {
		l.close()
		t.Fatal("original identity/history or one-effect facts changed")
	}
	l.close()
	if _, e = operator(ctx, "start-restored-application"); e != nil {
		t.Fatal("current running repeat", e)
	}
	l, e = open(c, false)
	if e != nil {
		t.Fatal(e)
	}
	again, re := regular(l.root, journalName, l.maximumJournalBytes())
	againHead, he := regular(l.root, headName, c.Verification.MaximumMetadataBytes)
	l.close()
	if re != nil || he != nil || !bytes.Equal(journal, again) || !bytes.Equal(head, againHead) {
		t.Fatal("completed repeat changed durable history")
	}
	afterStatus := status(ctx)
	if !reflect.DeepEqual(afterStatus.RestoreSQL, beforeStatus.RestoreSQL) || !reflect.DeepEqual(afterStatus.RestoreDatabase, beforeStatus.RestoreDatabase) || !reflect.DeepEqual(afterStatus.RestoreStores, beforeStatus.RestoreStores) || !reflect.DeepEqual(afterStatus.TLSProjection, beforeStatus.TLSProjection) || !reflect.DeepEqual(afterStatus.ControlReplacement, beforeStatus.ControlReplacement) {
		t.Fatal("original restore/authority/store/TLS/replacement facts changed")
	}
	qualified = true
	put("actual.json", map[string]any{"status": "passed_actual_restored_application_activation_process", "case": mode, "scope_id": retained.ScopeID, "admission": admission, "start_calls": loss.calls, "start_intents": intents, "start_completions": completions, "real_successful_start_response_loss_observed_without_resend": true, "dependency_process_unchanged": true, "both_effective_origins_HTTPS_ready_with_verified_trust": true, "completed_repeat_journal_head_unchanged": true, "original_history_and_runtime_network_IDs_preserved": true, "qualification": "actual_local_amd64_emulated_control_process_and_TLS_readiness_only_not_browser_authority_Workspace_export_restart_or_native_target"})
}

// Excluded tagged actual fixture, applied after the complete default gate.
// Fresh tagged vet/build and this actual operation are independently required.
func TestActualRetainedEffectiveRestoredBackup(t *testing.T) {
	configFile := os.Getenv("PROJECT_ONE_TEST_EFFECTIVE_BACKUP_CONFIG")
	if configFile == "" {
		t.Skip("explicit isolated restored backup fixture required")
	}
	binary, out, expectedScope := os.Getenv("PROJECT_ONE_TEST_EFFECTIVE_BACKUP_OPERATOR"), os.Getenv("PROJECT_ONE_TEST_EFFECTIVE_BACKUP_EVIDENCE"), os.Getenv("PROJECT_ONE_TEST_EFFECTIVE_BACKUP_SCOPE")
	requestFile := os.Getenv("PROJECT_ONE_TEST_EFFECTIVE_BACKUP_REQUEST")
	if !canonical(binary) || !privateDirectory(out) || !identityPattern.MatchString(expectedScope) {
		t.Fatal("private current operator/evidence and explicit scope required")
	}
	raw, e := bundle.ReadProtectedConfiguration(configFile)
	if e != nil {
		t.Fatal("protected installed configuration required", e)
	}
	c, e := configuration.Load(raw, nil)
	if e != nil || !c.Verification.AllowTestFixture || c.Engine == nil || c.Compose == nil || c.Compose.Process == nil {
		t.Fatal("isolated actual installed configuration required", e)
	}
	raw, e = bundle.ReadProtectedConfiguration(requestFile)
	if e != nil {
		t.Fatal("protected explicit backup operation required", e)
	}
	request, e := configuration.LoadBackupRequest(raw, nil)
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 600*time.Second)
	defer cancel()
	put := func(name string, value []byte) {
		root, e := os.OpenRoot(out)
		if e != nil {
			t.Fatal(e)
		}
		defer root.Close()
		if e = writeBackupFile(root, name, value); e != nil {
			t.Fatal(e)
		}
	}
	operator := func(ctx context.Context, action string, operation, success bool) []byte {
		args := []string{action, "--config", configFile}
		if operation {
			args = append(args, "--operation-file", requestFile)
		}
		result, e := exec.CommandContext(ctx, binary, args...).CombinedOutput()
		id, ne := nonce()
		if ne != nil {
			t.Fatal(ne)
		}
		put("command-"+action+"-"+id+".log", result)
		if success != (e == nil) {
			t.Fatalf("native %s expected success=%t: %v; inspect protected result", action, success, e)
		}
		return result
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
	before, e := l.verifyRuntimeContext(ctx, client, worker, false)
	if e != nil || l.marker.ScopeID != expectedScope || backupState(l.events).record != nil || processState(l.events).pending != "" {
		l.close()
		t.Fatal("known original current scope without prior backup required", e)
	}
	originalRuntime, effectiveRuntime := encode(*runtimeState(l.events).record), encode(*effectiveRuntimeState(l.events).record)
	originalJournal, e := regular(l.root, journalName, l.maximumJournalBytes())
	l.close()
	if e != nil {
		t.Fatal(e)
	}
	for _, peer := range before.observed.Services {
		if peer.State != "exited" || peer.ExitCode != 0 {
			t.Fatal("clean stopped installed peers required")
		}
	}
	qualified := false
	defer func() {
		cleanup, done := context.WithTimeout(context.Background(), 180*time.Second)
		defer done()
		raw, statusError := exec.CommandContext(cleanup, binary, "status", "--config", configFile).CombinedOutput()
		var status Status
		actions := []struct {
			action    string
			operation bool
		}{}
		if statusError != nil || wire.StrictJSON(raw, &status) != nil {
			t.Error("independent installed status unavailable", statusError)
		} else {
			if status.Backup != nil && status.Backup.Pending {
				actions = append(actions, struct {
					action    string
					operation bool
				}{"abort-backup", true})
			}
			if status.RestoredCurrentStores != nil && status.RestoredCurrentStores.Pending {
				actions = append(actions, struct {
					action    string
					operation bool
				}{"abort-restored-current-stores", false})
			}
			if status.RestoredLiveDatabase != nil && status.RestoredLiveDatabase.Pending {
				actions = append(actions, struct {
					action    string
					operation bool
				}{"abort-restored-live-database", false})
			}
		}
		actions = append(actions, struct {
			action    string
			operation bool
		}{"reconcile-process", false}, struct {
			action    string
			operation bool
		}{"stop-runtime", false})
		cleanupOK := true
		for _, action := range actions {
			args := []string{action.action, "--config", configFile}
			if action.operation {
				args = append(args, "--operation-file", requestFile)
			}
			result, e := exec.CommandContext(cleanup, binary, args...).CombinedOutput()
			put("cleanup-"+action.action+".log", result)
			if e != nil {
				cleanupOK = false
				t.Errorf("owned cleanup %s unresolved: %v", action.action, e)
			}
		}
		stopped := true
		for name, prior := range before.observed.Services {
			object, found, e := client.InspectRuntime(cleanup, expectedScope, name)
			if e != nil || !found || object.ID != prior.ID || object.State.Status != "exited" || object.State.ExitCode != 0 || object.State.PID != 0 || object.State.Running || object.State.OOMKilled || object.State.Paused || object.State.Restarting || object.State.Dead {
				stopped = false
			}
		}
		if !stopped {
			t.Error("exact retained peers not stopped0")
		}
		put("retention.json", encode(map[string]any{"status": "retained_actual_effective_backup_original_scope_stopped", "scope_id": expectedScope, "operator_config": configFile, "backup_request": requestFile, "backup_root": request.DestinationRoot, "qualified": qualified && !t.Failed() && stopped && cleanupOK, "scope_processes_stopped": stopped, "qualification": "new actual selected current backup; fresh restore, stale authority, full profiles, upgrade and target qualification remain separate"}))
	}()
	for _, action := range []string{"start-restored-database", "requalify-restored-live-database", "requalify-restored-current-stores"} {
		operator(ctx, action, false, true)
	}
	// Lose only the response following one successful real store export start.
	// All other physical effects use the same real Engine SDK and remain visible.
	loss := &actualEffectiveBackupStartLoss{Client: client}
	partial, e := runBackup(ctx, c, request, "backup", loss, worker)
	if e == nil || !partial.Pending || !loss.lost || loss.calls != 1 {
		t.Fatal("actual successful task response loss required", partial, e)
	}
	l, e = open(c, false)
	if e != nil {
		t.Fatal(e)
	}
	state := backupState(l.events)
	if state.record == nil || state.pending == "" || state.record.Phase != "task-execute" || state.record.RestoredAdmission == nil {
		l.close()
		t.Fatal("new backup attempt/admission not durably pending")
	}
	attempt, originalIntent := state.record.AttemptID, state.pending
	journal, e := regular(l.root, journalName, l.maximumJournalBytes())
	head, he := regular(l.root, headName, c.Verification.MaximumMetadataBytes)
	l.close()
	if e != nil || he != nil {
		t.Fatal(e, he)
	}
	operator(ctx, "backup", true, false)
	l, e = open(c, false)
	if e != nil {
		t.Fatal(e)
	}
	again, re := regular(l.root, journalName, l.maximumJournalBytes())
	againHead, he := regular(l.root, headName, c.Verification.MaximumMetadataBytes)
	l.close()
	if re != nil || he != nil || !bytes.Equal(journal, again) || !bytes.Equal(head, againHead) {
		t.Fatal("ordinary backup resent pending effect or changed journal")
	}
	operator(ctx, "reconcile-backup", true, true)
	operator(ctx, "inspect-backup", true, true)
	l, e = open(c, false)
	if e != nil {
		t.Fatal(e)
	}
	state = backupState(l.events)
	if state.record == nil || !state.terminal || state.aborted || state.record.Phase != "complete" || state.record.AttemptID != attempt || state.record.RestoredAdmission == nil {
		l.close()
		t.Fatal("native reconciliation did not complete original backup")
	}
	finalJournal, je := regular(l.root, journalName, l.maximumJournalBytes())
	finalHead, he := regular(l.root, headName, c.Verification.MaximumMetadataBytes)
	effective := effectiveRuntimeState(l.events)
	if je != nil || he != nil || !bytes.HasPrefix(finalJournal, originalJournal) || !bytes.Equal(originalRuntime, encode(*runtimeState(l.events).record)) || !bytes.Equal(effectiveRuntime, encode(*effective.record)) {
		l.close()
		t.Fatal("original history/runtime lineage changed")
	}
	reconciled := false
	for _, event := range l.events {
		if event.Backup != nil && event.Backup.AttemptID == attempt && event.Action == "backup_reconcile" && event.ReferenceOperationID == originalIntent && event.Backup.Phase == "task-execute" && event.Outcome == "completed" {
			reconciled = true
		}
	}
	if !reconciled {
		l.close()
		t.Fatal("native readonly reconciliation lost original effect reference")
	}
	generation, files, e := l.currentControlReplacementFiles(ctx)
	var plan BackupPlan
	root, e2 := openBackupDirectory(request.DestinationRoot)
	if e != nil || e2 != nil {
		l.close()
		t.Fatal(e, e2)
	}
	raw, e = regular(root, backupPlanFile, c.Verification.MaximumMetadataBytes)
	if e != nil || wire.StrictJSON(raw, &plan) != nil || plan.AttemptID != attempt || plan.RestoredAdmission == nil || plan.RuntimeSHA256 != wire.Digest(effectiveRuntime) || plan.EffectiveControlPayloadSHA256 != generation.PayloadSHA256 {
		root.Close()
		l.close()
		t.Fatal("effective backup lineage/configuration not bound", e)
	}
	copied, e := readBackupConfiguration(ctx, plan, c.Verification.MaximumMetadataBytes)
	if e != nil || !reflect.DeepEqual(copied["control-config"], files) || restoreConfigurationDigest(copied["control-config"]) != plan.EffectiveControlPayloadSHA256 {
		root.Close()
		l.close()
		t.Fatal("current mounted configuration not exported", e)
	}
	manifest, manifestFile, e := inspectBackupFiles(ctx, root, plan)
	root.Close()
	if e != nil || manifest.ScopeID != expectedScope || manifestFile.SHA256 != state.record.ArtifactSHA256 || !manifest.Source.Valid() {
		l.close()
		t.Fatal("actual complete artifact/source invalid", e)
	}
	sourceRequest := configuration.RestoreSourceDefaults()
	sourceRequest.BackupRoot = request.DestinationRoot
	sourceRequest.ManifestSHA256 = manifestFile.SHA256
	sourceRequest.MaximumBytes = request.MaximumBytes
	sourceRequest.MaximumEntries = request.MaximumEntries
	l.close()
	source, e := InspectRestoreSource(ctx, c, sourceRequest)
	if e != nil || source.SourceScopeID != expectedScope || source.ManifestSHA256 != manifestFile.SHA256 {
		t.Fatal("independent source inspection of new backup", e)
	}
	operator(ctx, "inspect-backup", true, true)
	l, e = open(c, false)
	if e != nil {
		t.Fatal(e)
	}
	repeated, je := regular(l.root, journalName, l.maximumJournalBytes())
	repeatedHead, he := regular(l.root, headName, c.Verification.MaximumMetadataBytes)
	l.close()
	if je != nil || he != nil || !bytes.Equal(finalJournal, repeated) || !bytes.Equal(finalHead, repeatedHead) {
		t.Fatal("completed artifact inspection mutated installed history")
	}
	if loss.calls != 1 {
		t.Fatal("wrapped physical effect repeated")
	}
	put("actual.json", encode(map[string]any{"status": "passed_actual_effective_restored_backup_and_native_reconciliation", "scope_id": expectedScope, "backup_root": request.DestinationRoot, "backup_manifest_sha256": manifestFile.SHA256, "backup_plan_sha256": wire.Digest(raw), "backup_files": source.Files, "backup_bytes": source.Bytes, "effective_control_payload_sha256": plan.EffectiveControlPayloadSHA256, "effective_runtime_sha256": plan.RuntimeSHA256, "TLS_generation_id": generation.GenerationID, "actual_store_export_start_response_lost": true, "pending_ordinary_backup_refused_without_journal_change": true, "native_original_task_reconciled_without_resend": true, "current_mounted_control_configuration_exported": true, "independent_complete_backup_source_inspection": true, "original_history_runtime_and_peers_preserved": true, "historical_inspection_journal_head_unchanged": true, "qualification": "actual selected models/datasets and database-aware PostgreSQL18 export on local amd64 emulation; authority source preparation, fresh restore, four stores/nonempty weights, upgrade and native target remain separate"}))
	qualified = true
}
