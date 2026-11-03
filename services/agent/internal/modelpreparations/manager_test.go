package modelpreparations

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/agent/internal/settings"
	agentwire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	datawire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	wire "github.com/Passion-Flow/ownsolid/api/model-lifecycle/v1"
)

// transferFixture supplies synthetic bytes/reports only; real TLS is tested by the client package.
type transferFixture struct {
	content      map[string][]byte
	events       []int64
	stops        []wire.StopReceipt
	failStop     bool
	chunks       int
	upload       wire.Upload
	stopOutcomes [][]byte
	foreignAck   bool
}

func (d *transferFixture) PreparationFile(_ context.Context, _, _, kind string, asset datawire.Asset, destination string) error {
	body, ok := d.content[kind+":"+asset.Path]
	if !ok {
		return fail("MODEL_PREPARATION_INPUT_INVALID")
	}
	return os.WriteFile(destination, body, 0600)
}
func (d *transferFixture) PreparationEvents(_ context.Context, _, _ string, _ wire.Command, _ int64, events [][]byte) ([]wire.EventAcceptance, error) {
	for _, raw := range events {
		var e wire.Event
		json.Unmarshal(raw, &e)
		d.events = append(d.events, e.Sequence)
	}
	return nil, nil
}
func (d *transferFixture) BeginPreparationUpload(context.Context, string, string, wire.Command, []byte) (wire.Upload, error) {
	return d.upload, nil
}
func (d *transferFixture) PreparationChunk(context.Context, string, string, string, wire.ChunkRequest, []byte) (wire.ChunkAcceptance, error) {
	d.chunks++
	return wire.ChunkAcceptance{}, nil
}
func (d *transferFixture) FinishPreparationUpload(context.Context, string, string, wire.Upload) (wire.OutputAcceptance, error) {
	return wire.OutputAcceptance{}, nil
}
func (d *transferFixture) PreparationStop(_ context.Context, _, _ string, _ wire.Command, r wire.StopReceipt, outcome []byte) (wire.StopAcceptance, error) {
	d.stops = append(d.stops, r)
	d.stopOutcomes = append(d.stopOutcomes, append([]byte(nil), outcome...))
	if d.foreignAck {
		return wire.StopAcceptance{}, nil
	}
	if d.failStop {
		return wire.StopAcceptance{}, fail("CONTROL_CONNECTION_FAILED")
	}
	return wire.StopAcceptance{PreparationID: r.Scope.PreparationID, AttemptID: r.AttemptID, ReceiptID: r.ReceiptID, GPUReleased: true, State: "failed"}, nil
}

func preparedManager(t *testing.T) (*Manager, agentwire.Lease, *faultEngine, *transferFixture) {
	t.Helper()
	root := t.TempDir()
	state := t.TempDir()
	os.Chmod(root, 0700)
	os.Chmod(state, 0700)
	c := fixtureConfig()
	c.RootDirectory = root
	p := c.Policy()
	p.UserID = os.Geteuid()
	p.GroupID = os.Getegid()
	p.PollMilliseconds = 100
	c.Runtime = &p
	command := fixtureCommand()
	heldout := []byte("{\"prompt\":\"Account issue\",\"completion\":\"account\",\"source_id\":\"synthetic-1\"}\n{\"prompt\":\"Billing issue\",\"completion\":\"billing\",\"source_id\":\"synthetic-2\"}\n{\"prompt\":\"Technical issue\",\"completion\":\"technical\",\"source_id\":\"synthetic-3\"}\n")
	command.Inputs.Heldout = datawire.Asset{Path: "heldout.jsonl", SizeBytes: int64(len(heldout)), SHA256: digest(heldout)}
	content := map[string][]byte{"heldout:heldout.jsonl": heldout}
	for i, asset := range command.Inputs.AdapterFiles {
		body := []byte("synthetic-" + asset.Path)
		asset.SizeBytes = int64(len(body))
		asset.SHA256 = digest(body)
		command.Inputs.AdapterFiles[i] = asset
		content["adapter:"+asset.Path] = body
	}
	engine := &faultEngine{config: c}
	manager := &Manager{Config: c, AgentPolicy: settings.Default(), Lifecycle: Lifecycle{Config: c, Engine: engine, Store: JournalStore{Directory: state}}, preflight: func(context.Context, wire.Command) error { return nil }, prepareModel: func(context.Context, Config, Journal, FileDownload) error { return nil }, expectedResult: func(context.Context, Config, Journal) (wire.ResultExpectation, error) {
		return wire.ResultExpectation{Cases: make([]wire.ExpectedCase, 3)}, nil
	}}
	lease := agentwire.Lease{TaskID: fixtureID, LeaseToken: strings.Repeat("A", 43), Operation: wire.RunOperation, Attempt: 1, Preparation: &command}
	transport := &transferFixture{content: content}
	return manager, lease, engine, transport
}
func TestPreparedPrivateFilesAndReportedWorkerFailureStopCrashRetry(t *testing.T) {
	m, lease, d, transport := preparedManager(t)
	d.afterStart = func(c *Container) {
		root := attemptPath(m.Config, Journal{Command: *lease.Preparation})
		for _, relative := range []string{"request.json", "projectone-preparation.json", "inputs/heldout.jsonl", "inputs/adapter/README.md"} {
			info, e := os.Stat(filepath.Join(root, relative))
			if e != nil || info.Mode().Perm()&0077 != 0 {
				t.Fatal("private input publication", e)
			}
		}
		first := map[string]any{"sequence": 1, "preparation_id": fixtureID, "attempt_id": fixtureID, "kind": "phase", "phase": "evaluating_base"}
		raw, _ := json.Marshal(first)
		raw = append(raw, '\n')
		os.WriteFile(filepath.Join(root, "events.jsonl"), raw, 0600)
		outcome, _ := json.Marshal(map[string]any{"schema_version": 1, "worker_version": wire.WorkerVersion, "status": "failed", "preparation_id": fixtureID, "attempt_id": fixtureID, "error_code": "MODEL_PREPARATION_PROCESSOR_TIMEOUT"})
		os.WriteFile(filepath.Join(root, "output/result.json"), outcome, 0600)
		c.State.Running = false
		c.State.Status = "exited"
		c.State.ExitCode = 1
	}
	receipt, outcome, e := m.Execute(context.Background(), fixtureID, lease, transport)
	if e != nil || receipt.Proof != "owned_container_removed" || receipt.OutcomeSHA256 == nil || digest(outcome) != *receipt.OutcomeSHA256 {
		t.Fatal("actual file/journal and explicitly synthetic worker/engine result", e)
	}
	if !reflect.DeepEqual(transport.events, []int64{1}) || d.container != nil {
		t.Fatal("events or owned process not joined")
	}
	transport.failStop = true
	if e = m.Recover(context.Background(), fixtureID, transport); e == nil {
		t.Fatal("uncertain control result discarded receipt")
	}
	if _, e = m.Lifecycle.Store.Read(); e != nil {
		t.Fatal("pending receipt lost", e)
	}
	transport.failStop = false
	if e = m.Recover(context.Background(), fixtureID, transport); e != nil {
		t.Fatal(e)
	}
	if len(transport.stops) != 2 || !reflect.DeepEqual(transport.stops[0], transport.stops[1]) {
		t.Fatal("restart changed stop identity")
	}
	if _, e = os.Stat(m.Lifecycle.Store.Path()); !os.IsNotExist(e) {
		t.Fatal("acknowledged journal was not archived")
	}
	archive := filepath.Join(m.Lifecycle.Store.Directory, "model-preparation-history", fixtureID+".json")
	if _, e = privateRegular(archive, MaximumJournalBytes); e != nil {
		t.Fatal("private immutable recovery history missing", e)
	}
	if e = m.Recover(context.Background(), fixtureID, transport); e != nil {
		t.Fatal("empty recovery was not harmless", e)
	}
}
func TestCancelledBeforeFirstRenewalRetainsProvableNeverCreatedReceipt(t *testing.T) {
	m, lease, d, transport := preparedManager(t)
	ctx, stop := context.WithCancel(context.Background())
	stop()
	receipt, raw, e := m.Execute(ctx, fixtureID, lease, transport)
	if e == nil || receipt.Proof != "never_created" || len(raw) != 0 || len(d.calls) != 0 {
		t.Fatal("cancelled pre-start intent lost safe stop proof", e)
	}
	if _, e = os.Stat(filepath.Join(m.Config.RootDirectory, "preparations")); !os.IsNotExist(e) {
		t.Fatal("cancelled intent created input files")
	}
}

func TestActualPrivateAgentOOMDiagnosticSurvivesCleanupAndRestart(t *testing.T) {
	m, lease, engine, transport := preparedManager(t)
	engine.afterStart = func(c *Container) {
		c.State.Running = false
		c.State.Status = "exited"
		c.State.ExitCode = 137
		c.State.OOMKilled = true
	}
	receipt, raw, e := m.Execute(context.Background(), fixtureID, lease, transport)
	if e == nil || e.Error() != "MODEL_PREPARATION_PROCESSOR_MEMORY_EXCEEDED" || len(raw) != 0 || receipt.AgentErrorCode != "MODEL_PREPARATION_PROCESSOR_MEMORY_EXCEEDED" || engine.container != nil {
		t.Fatal("OOM diagnostic or physical join lost", e)
	}
	journal, e := m.Lifecycle.Store.Read()
	if e != nil || journal.AgentErrorCode != receipt.AgentErrorCode || journal.StopReceipt.AgentErrorCode != receipt.AgentErrorCode {
		t.Fatal("private durable OOM cause", e)
	}
	if e = m.Recover(context.Background(), fixtureID, transport); e != nil || len(transport.stops) != 1 || transport.stops[0].AgentErrorCode != receipt.AgentErrorCode {
		t.Fatal("restart changed diagnostic", e)
	}
}

func TestPreparationRestartMarksUnreportedLiveJournalInterruptedAndJoinsOwner(t *testing.T) {
	m, lease, engine, transport := preparedManager(t)
	j, e := m.Begin(fixtureID, lease)
	if e != nil {
		t.Fatal(e)
	}
	if e = m.Lifecycle.Start(context.Background(), &j); e != nil {
		t.Fatal(e)
	}
	if e = m.Recover(context.Background(), fixtureID, transport); e != nil {
		t.Fatal(e)
	}
	if engine.container != nil || len(transport.stops) != 1 || transport.stops[0].AgentErrorCode != "MODEL_PREPARATION_AGENT_INTERRUPTED" || transport.stops[0].Proof != "owned_container_removed" {
		t.Fatal("crash cleanup lost physical stop or durable interruption")
	}
}
func TestScopedInputCorruptionAndExistingAttemptPreserveOriginalFiles(t *testing.T) {
	m, lease, _, transport := preparedManager(t)
	j, e := m.Begin(fixtureID, lease)
	if e != nil {
		t.Fatal(e)
	}
	transport.content["heldout:heldout.jsonl"] = []byte("corrupt")
	download := func(ctx context.Context, kind string, asset datawire.Asset, dest string) error {
		return transport.PreparationFile(ctx, "", "", kind, asset, dest)
	}
	if e = prepareAttempt(context.Background(), m.Config, j, download); e == nil {
		t.Fatal("unverified source handed to worker")
	}
	retained := filepath.Join(attemptPath(m.Config, j), "inputs/heldout.jsonl")
	before, _ := os.ReadFile(retained)
	if e = prepareAttempt(context.Background(), m.Config, j, download); e == nil {
		t.Fatal("partial attempt overwritten")
	}
	after, _ := os.ReadFile(retained)
	if string(before) != string(after) {
		t.Fatal("retained input changed")
	}
}
func TestOutputBudgetAndUploadRejectEscapesLinksCountsAndInventories(t *testing.T) {
	for _, kind := range []string{"symlink", "hardlink", "bytes", "permissions", "depth"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			os.Chmod(root, 0700)
			resources := wire.DefaultResources()
			path := filepath.Join(root, "output")
			os.WriteFile(path, []byte("safe"), 0600)
			switch kind {
			case "symlink":
				os.Symlink(path, filepath.Join(root, "escape"))
			case "hardlink":
				os.Link(path, filepath.Join(root, "hardlink"))
			case "bytes":
				resources.MaximumOutputBytes = 3
			case "permissions":
				os.Chmod(path, 0644)
			case "depth":
				current := root
				for i := 0; i < 6; i++ {
					current = filepath.Join(current, "nested")
					os.Mkdir(current, 0700)
				}
			}
			if outputBudget(context.Background(), root, resources, os.Geteuid()) == nil {
				t.Fatal("unsafe or excessive output accepted")
			}
		})
	}
	m, lease, _, transport := preparedManager(t)
	j, e := m.Begin(fixtureID, lease)
	if e != nil {
		t.Fatal(e)
	}
	asset := datawire.Asset{Path: "config.json", SizeBytes: 1, SHA256: strings.Repeat("a", 64)}
	transport.upload = wire.Upload{ID: fixtureID, PreparationID: fixtureID, AttemptID: fixtureID, Files: []datawire.Asset{{Path: "../identity.json", SizeBytes: 1, SHA256: asset.SHA256}}}
	if e = m.upload(context.Background(), j, transport, []byte("{}"), t.TempDir(), []datawire.Asset{asset}); e == nil || transport.chunks != 0 {
		t.Fatal("server descriptor selected an arbitrary agent file")
	}
}

func TestPersistedUnacceptedPreparedDigestCrashCleanupKeepsOriginalBytesAndRequiresExactAck(t *testing.T) {
	m, lease, engine, transport := preparedManager(t)
	j, e := m.Begin(fixtureID, lease)
	if e != nil {
		t.Fatal(e)
	}
	if e = m.Lifecycle.Start(context.Background(), &j); e != nil {
		t.Fatal(e)
	}
	// Deliberately not a valid prepared report. This isolates crash journal handling;
	// actual prepared report acceptance is separately tested against native artifacts.
	raw := []byte("{\"status\":\"prepared\", \"journal_fixture_only\":\"<\u6807\u7b7e>&\"}")
	if e = m.saveOutcome(&j, raw); e != nil {
		t.Fatal(e)
	}
	transport.foreignAck = true
	if e = m.Recover(context.Background(), fixtureID, transport); e == nil {
		t.Fatal("foreign acknowledgment archived journal")
	}
	if engine.container != nil || len(transport.stops) != 1 || transport.stops[0].Proof != "owned_container_removed" || transport.stops[0].AgentErrorCode != "MODEL_PREPARATION_AGENT_INTERRUPTED" || transport.stops[0].OutcomeSHA256 == nil || *transport.stops[0].OutcomeSHA256 != digest(raw) || len(transport.stopOutcomes[0]) != 0 {
		t.Fatal("unaccepted prepared report was sent as a new result or lost digest")
	}
	retained, e := m.Lifecycle.Store.Read()
	if e != nil {
		t.Fatal(e)
	}
	actual, e := m.ReadOutcome(retained)
	if e != nil || string(actual) != string(raw) {
		t.Fatal("original protected bytes lost", e)
	}
	transport.foreignAck = false
	if e = m.Recover(context.Background(), fixtureID, transport); e != nil {
		t.Fatal(e)
	}
	if len(transport.stops) != 2 || !reflect.DeepEqual(transport.stops[0], transport.stops[1]) {
		t.Fatal("crash retry changed physical proof")
	}
}

func TestPreparedAttemptMarkerBindsEveryWorkerIdentifierAndContainerScope(t *testing.T) {
	m, lease, _, transport := preparedManager(t)
	j, e := m.Begin(fixtureID, lease)
	if e != nil {
		t.Fatal(e)
	}
	download := func(ctx context.Context, kind string, asset datawire.Asset, path string) error {
		return transport.PreparationFile(ctx, "", "", kind, asset, path)
	}
	if e = prepareAttempt(context.Background(), m.Config, j, download); e != nil {
		t.Fatal(e)
	}
	raw, e := os.ReadFile(filepath.Join(attemptPath(m.Config, j), "projectone-preparation.json"))
	if e != nil {
		t.Fatal(e)
	}
	var marker map[string]any
	if json.Unmarshal(raw, &marker) != nil || len(marker) != 8 || marker["training_job_id"] != j.Command.TrainingJobID || marker["training_attempt_id"] != j.Command.TrainingAttemptID || marker["heldout_id"] != j.Command.HeldoutID || marker["preparation_id"] != j.Command.Scope.PreparationID {
		t.Fatal("worker marker omitted immutable scope")
	}
	request, e := os.ReadFile(filepath.Join(attemptPath(m.Config, j), "request.json"))
	if e != nil {
		t.Fatal(e)
	}
	decoded, e := wire.DecodeProcessorRequest(request)
	if e != nil || decoded.ScopeDirectory != wire.ContainerScopesDirectory+"/"+j.Command.Scope.TenantID+"/"+j.Command.Scope.WorkspaceID+"/"+j.Command.Scope.PreparationID+"/"+j.Command.AttemptID {
		t.Fatal("worker/container scope mismatch", e)
	}
}
