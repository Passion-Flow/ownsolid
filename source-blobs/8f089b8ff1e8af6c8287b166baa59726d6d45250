package training_test

import (
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	agentwire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	transport "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/training"
	"github.com/google/uuid"
)

type cliTrainingJob struct {
	ID               string `json:"id"`
	WorkspaceID      string `json:"workspace_id"`
	DatasetVersionID string `json:"dataset_version_id"`
	State            string `json:"state"`
	Revision         int64  `json:"revision"`
}

func TestLaneDTrainingTLS(t *testing.T) {
	binary := os.Getenv("LANE_D_CLI_BINARY")
	if binary == "" {
		t.Fatal("set LANE_D_CLI_BINARY to the compiled CLI")
	}
	f := setup(t)
	attachStore(t, f)
	hardware(t, f)

	var endpoint http.Handler
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		endpoint.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	f.s.Identity.Config.Server.AdminOrigin = "https://" + server.Listener.Addr().String()
	handler := transport.New(f.s.Identity)
	if err := handler.LicenseInitializationError(); err != nil {
		t.Fatal(err)
	}
	if err := handler.Licensing.Service.VerifyStored(t.Context()); err != nil {
		t.Fatal(err)
	}
	handler.Training.Store = f.s.Store
	handler.Datasets.Service.Store = f.s.Datasets.Store
	handler.Datasets.Service.Processor = f.s.Datasets.Processor
	endpoint = handler
	server.StartTLS()

	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OWNSOLID_CONFIG_DIR", dir)
	t.Setenv("OWNSOLID_SERVER", server.URL)
	ca := filepath.Join(dir, "ca.pem")
	password := filepath.Join(dir, "password")
	configuration := filepath.Join(dir, "training.json")
	invalid := filepath.Join(dir, "invalid.json")
	config, err := json.Marshal(map[string]string{"ca_file": ca})
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(f.input.Configuration)
	if err != nil {
		t.Fatal(err)
	}
	for path, contents := range map[string][]byte{
		ca:       pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}),
		password: []byte(f.password), configuration: body, invalid: []byte("[]"),
		filepath.Join(dir, "config.json"): config,
	} {
		if err := os.WriteFile(path, contents, 0600); err != nil {
			t.Fatal(err)
		}
	}

	run := func(args ...string) (string, error) {
		t.Helper()
		output, err := exec.CommandContext(t.Context(), binary, args...).CombinedOutput()
		if strings.Contains(string(output), f.password) {
			t.Fatal("the CLI printed its password")
		}
		return string(output), err
	}
	ok := func(args ...string) string {
		t.Helper()
		output, err := run(args...)
		if err != nil {
			t.Fatalf("CLI %v: %v\n%s", args, err, output)
		}
		return output
	}
	refused := func(code string, args ...string) {
		t.Helper()
		output, err := run(args...)
		if err == nil || !strings.Contains(output, code) {
			t.Fatalf("CLI %v must fail with %s: %v\n%s", args, code, err, output)
		}
	}
	job := func(args ...string) cliTrainingJob {
		t.Helper()
		var result cliTrainingJob
		if err := json.Unmarshal([]byte(ok(append(args, "--output", "json")...)), &result); err != nil {
			t.Fatalf("decode CLI training output: %v", err)
		}
		if result.ID == "" || result.WorkspaceID != f.input.WorkspaceID || result.DatasetVersionID != f.input.DatasetVersionID || result.Revision < 1 {
			t.Fatal("the CLI lost the training job's scoped identifiers or revision")
		}
		return result
	}

	refused("no saved Admin session", "training", "get", uuid.NewString())
	ok("login", "--email", "admin@example.invalid", "--password-file", password)
	key := uuid.NewString()
	create := func(file, reason string) []string {
		return []string{
			"training", "create", "--node", f.input.NodeID, "--gpu", f.input.GPUUUID,
			"--model", f.input.ModelID, "--dataset", f.input.DatasetID,
			"--dataset-version", f.input.DatasetVersionID, "--configuration", file,
			"--reason", reason, "--idempotency-key", key,
		}
	}
	refused("must contain one JSON object", create(invalid, "Invalid local configuration")...)
	created := job(create(configuration, f.input.Reason)...)
	if created.State != "queued" || job(create(configuration, f.input.Reason)...) != created {
		t.Fatal("creating or replaying a queued job changed its durable result")
	}
	refused("IDEMPOTENCY_CONFLICT", create(configuration, "Different intent with same key")...)
	if got := count(t, f, "SELECT count(*) FROM training_jobs WHERE workspace_id=$1", f.input.WorkspaceID); got != 1 {
		t.Fatalf("creation replay left %d jobs", got)
	}
	if job("training", "get", created.ID) != created {
		t.Fatal("CLI get did not read the queued job")
	}

	var queuedHistory training.Page[training.Attempt]
	if err := json.Unmarshal([]byte(ok("training", "attempts", created.ID, "--output", "json")), &queuedHistory); err != nil ||
		queuedHistory.Items == nil || len(queuedHistory.Items) != 0 || queuedHistory.NextCursor != nil {
		t.Fatalf("queued attempt page was not empty: %v", err)
	}

	tenant, other := uuid.NewString(), uuid.NewString()
	if _, err := f.s.DB.Exec(t.Context(), "INSERT INTO tenants(id,name,status) VALUES($1,'Foreign training tenant','active')", tenant); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.DB.Exec(t.Context(), "INSERT INTO workspaces(id,tenant_id,name,status) VALUES($1,$2,'Foreign training workspace','active')", other, tenant); err != nil {
		t.Fatal(err)
	}
	refused("RESOURCE_NOT_FOUND", "training", "get", created.ID, "--workspace", other)

	cancel := func(revision int64) []string {
		return []string{"training", "cancel", created.ID, "--revision", strconv.FormatInt(revision, 10), "--reason", "Withdraw the queued training intent"}
	}
	refused("RESOURCE_NOT_FOUND", append(cancel(created.Revision), "--workspace", other)...)
	refused("TRAINING_REVISION_CONFLICT", cancel(created.Revision+1)...)
	cancelled := job(cancel(created.Revision)...)
	if cancelled.State != "cancelled" || cancelled.Revision != created.Revision+1 {
		t.Fatal("queued cancellation did not persist a new revision")
	}
	if job("training", "get", created.ID) != cancelled || job(cancel(cancelled.Revision)...) != cancelled {
		t.Fatal("cancelled job changed on read or repeated cancellation")
	}
	if got := count(t, f, "SELECT count(*) FROM audit_events WHERE action='training.cancel_requested' AND target_id=$1", created.ID); got != 1 {
		t.Fatalf("cancellation left %d audit events", got)
	}
	key = uuid.NewString()
	running := job(create(configuration, "Synthetic attempt metrics")...)
	ctx := t.Context()
	if worked, err := handler.Training.DispatchNext(ctx); err != nil || !worked {
		t.Fatal("dispatch the CLI-created training intent", worked, err)
	}
	lease := claimed(t, f)
	events := prelude(*lease.Training)
	events = append(events,
		eventFixture(*lease.Training, 3, 1, "metric", map[string]any{"values": map[string]any{"loss": 1.0, "learning_rate": 0.0002}}),
		eventFixture(*lease.Training, 4, 2, "metric", map[string]any{"values": map[string]any{"loss": 0.0}}),
	)
	if _, err := handler.Training.RecordEvents(ctx, f.input.NodeID, f.credential, lease.TaskID, lease.LeaseToken, events); err != nil {
		t.Fatal(err)
	}
	var history training.Page[training.Attempt]
	if err := json.Unmarshal([]byte(ok("training", "attempts", running.ID, "--output", "json")), &history); err != nil ||
		len(history.Items) != 1 || history.Items[0].ID != lease.Training.AttemptID ||
		history.Items[0].JobID != running.ID || history.NextCursor != nil {
		t.Fatalf("attempt history lost its admitted scope: %v", err)
	}
	attempt := history.Items[0].ID
	metrics := func(attempt, output string) []string {
		return []string{"training", "metrics", running.ID, "--attempt", attempt, "--limit", "1", "--output", output}
	}
	firstArgs := metrics(attempt, "json")
	first := ok(firstArgs...)
	if first != ok(firstArgs...) {
		t.Fatal("same retained metric page changed on retry")
	}
	var firstPage, nextPage training.Page[training.MetricRecord]
	if err := json.Unmarshal([]byte(first), &firstPage); err != nil || len(firstPage.Items) != 1 || firstPage.NextCursor == nil ||
		firstPage.Items[0].Sequence != 3 || firstPage.Items[0].GlobalStep != 1 || firstPage.Items[0].AttemptID != attempt {
		t.Fatalf("first metric page lost its attempt, step or cursor: %v", err)
	}
	cursor := *firstPage.NextCursor
	if table := ok(metrics(attempt, "table")...); !strings.Contains(table, "SEQUENCE") || !strings.Contains(table, cursor) {
		t.Fatal("metric table omitted its sequence or continuation")
	}
	if err := json.Unmarshal([]byte(ok(append(firstArgs, "--cursor", cursor)...)), &nextPage); err != nil ||
		len(nextPage.Items) != 1 || nextPage.Items[0].Sequence != 4 || nextPage.Items[0].GlobalStep != 2 ||
		nextPage.Items[0].Values.Loss == nil || *nextPage.Items[0].Values.Loss != 0 || nextPage.NextCursor != nil {
		t.Fatalf("next metric page lost the measured zero or its final boundary: %v", err)
	}
	unknown := uuid.NewString()
	refused("INVALID_CURSOR", append(metrics(unknown, "json"), "--cursor", cursor)...)
	refused("INVALID_CURSOR", "training", "attempts", running.ID, "--cursor", cursor)
	refused("TRAINING_NOT_FOUND", metrics(unknown, "json")...)
	refused("RESOURCE_NOT_FOUND", append(firstArgs, "--workspace", other)...)
	refused("RESOURCE_NOT_FOUND", "training", "attempts", running.ID, "--workspace", other)
	refused("--attempt must be a lowercase UUID", "training", "metrics", running.ID)

	historyArgs := func(collection string) []string {
		return []string{"training", collection, running.ID, "--workspace", f.input.WorkspaceID, "--limit", "1", "--output", "json"}
	}
	for _, collection := range []string{"checkpoints", "recoveries"} {
		var empty training.Page[json.RawMessage]
		if err := json.Unmarshal([]byte(ok(historyArgs(collection)...)), &empty); err != nil ||
			empty.Items == nil || len(empty.Items) != 0 || empty.NextCursor != nil {
			t.Fatalf("%s did not return an empty final page: %v", collection, err)
		}
		if help := ok("training", collection, "--help"); !strings.Contains(help, "--cursor") ||
			!strings.Contains(help, "--limit") || strings.Contains(help, "--attempt") {
			t.Fatal("history help does not match the job-scoped read", collection)
		}
	}

	restart := func(attemptLease agentwire.Lease) string {
		t.Helper()
		stop := stopFixture(t, f, attemptLease)
		stop.Proof = "owned_container_removed"
		container := strings.Repeat("e", 64)
		stop.ContainerID = &container
		if _, err := handler.Training.RecordStop(ctx, f.input.NodeID, f.credential,
			attemptLease.TaskID, attemptLease.LeaseToken, stop, nil); err != nil {
			t.Fatal("stop the retained attempt", err)
		}
		current, err := handler.Training.Get(ctx, f.admin, f.input.WorkspaceID, running.ID)
		if err != nil {
			t.Fatal(err)
		}
		queued, err := handler.Training.Recover(ctx, f.admin, f.input.WorkspaceID, running.ID,
			training.RecoveryRequest{ExpectedRevision: current.Revision, Reason: "Explicit restart for history readback",
				NodeID: f.input.NodeID, Mode: "restart"}, uuid.NewString(), uuid.NewString())
		if err != nil || queued.PendingRecoveryID == nil {
			t.Fatal("record a real recovery intent", err)
		}
		return *queued.PendingRecoveryID
	}
	dispatchedRecovery := restart(lease)
	if worked, err := handler.Training.DispatchNext(ctx); err != nil || !worked {
		t.Fatal("dispatch the retained recovery intent", worked, err)
	}
	second := claimed(t, f)
	if second.Training.Scope.JobID != running.ID || second.Training.AttemptID == attempt {
		t.Fatal("recovery did not allocate a new attempt of the same job")
	}
	pendingRecovery := restart(second)

	// Seed synthetic metadata only. No checkpoint bytes are used to recover a worker.
	checkpoints := make([]training.Checkpoint, 0, 2)
	for index, attemptID := range []string{attempt, second.Training.AttemptID} {
		receipt := []byte(`{"synthetic_step":` + strconv.Itoa(index+1) + "}")
		checkpoint := training.Checkpoint{ID: uuid.NewString(), JobID: running.ID, AttemptID: attemptID,
			GlobalStep: int64(index + 1), ReceiptSHA256: digest(receipt)}
		storage := "checkpoints/" + running.ID + "/" + attemptID + "/" + checkpoint.ID
		if _, err := f.s.DB.Exec(ctx, `WITH fixture_upload AS (
			INSERT INTO training_uploads
				(id,tenant_id,workspace_id,job_id,attempt_id,kind,global_step,manifest_sha256,manifest,state,manifest_bytes)
			VALUES($1,$2,$3,$4,$5,'checkpoint',$6,$7,$8::jsonb,'accepted',$9)
			RETURNING id,tenant_id,workspace_id,job_id,attempt_id,global_step,manifest_sha256,manifest
		)
		INSERT INTO training_checkpoints
			(id,tenant_id,workspace_id,job_id,attempt_id,global_step,receipt_sha256,receipt,file_inventory,storage_key,created_at)
		SELECT id,tenant_id,workspace_id,job_id,attempt_id,global_step,manifest_sha256,manifest,'{}',$10,
			TIMESTAMPTZ '2000-01-01 00:00:00+00' + global_step * INTERVAL '1 second'
		FROM fixture_upload`, checkpoint.ID, f.admin.Me.Workspace.TenantID, f.input.WorkspaceID, running.ID,
			attemptID, checkpoint.GlobalStep, checkpoint.ReceiptSHA256, string(receipt), receipt, storage); err != nil {
			t.Fatal("seed synthetic checkpoint readback metadata", err)
		}
		checkpoints = append(checkpoints, checkpoint)
	}

	firstHistory := func(collection string) string {
		t.Helper()
		args := historyArgs(collection)
		raw := ok(args...)
		if raw != ok(args...) {
			t.Fatal("a repeated history read changed its retained first page", collection)
		}
		return raw
	}
	var cpFirst, cpLast training.Page[training.Checkpoint]
	if err := json.Unmarshal([]byte(firstHistory("checkpoints")), &cpFirst); err != nil ||
		len(cpFirst.Items) != 1 || cpFirst.NextCursor == nil {
		t.Fatal("checkpoints first page omitted its record or continuation", err)
	}
	cpCursor := *cpFirst.NextCursor
	if err := json.Unmarshal([]byte(ok(append(historyArgs("checkpoints"), "--cursor", cpCursor)...)), &cpLast); err != nil ||
		len(cpLast.Items) != 1 || cpLast.NextCursor != nil {
		t.Fatal("checkpoints final page lost its boundary", err)
	}
	for index, actual := range []training.Checkpoint{cpLast.Items[0], cpFirst.Items[0]} {
		want := checkpoints[index]
		if actual.ID != want.ID || actual.JobID != want.JobID || actual.AttemptID != want.AttemptID ||
			actual.GlobalStep != want.GlobalStep || actual.ReceiptSHA256 != want.ReceiptSHA256 || actual.CreatedAt.IsZero() {
			t.Fatalf("checkpoint %d lost its job, attempt, receipt or step", index)
		}
	}

	var recFirst, recLast training.Page[training.Recovery]
	if err := json.Unmarshal([]byte(firstHistory("recoveries")), &recFirst); err != nil ||
		len(recFirst.Items) != 1 || recFirst.NextCursor == nil {
		t.Fatal("recoveries first page omitted its record or continuation", err)
	}
	recCursor := *recFirst.NextCursor
	if err := json.Unmarshal([]byte(ok(append(historyArgs("recoveries"), "--cursor", recCursor)...)), &recLast); err != nil ||
		len(recLast.Items) != 1 || recLast.NextCursor != nil {
		t.Fatal("recoveries final page lost its boundary", err)
	}
	for _, item := range []training.Recovery{recFirst.Items[0], recLast.Items[0]} {
		if item.JobID != running.ID || item.Mode != "restart" || item.CheckpointID != nil || item.PreviousAttemptID == nil {
			t.Fatal("restart history lost job scope or added a checkpoint", item.ID)
		}
	}
	pending, linked := recFirst.Items[0], recLast.Items[0]
	if pending.ID != pendingRecovery || *pending.PreviousAttemptID != second.Training.AttemptID || pending.NextAttemptID != nil ||
		linked.ID != dispatchedRecovery || *linked.PreviousAttemptID != attempt || linked.NextAttemptID == nil ||
		*linked.NextAttemptID != second.Training.AttemptID {
		t.Fatal("recovery history lost its exact previous and next attempt links")
	}

	localWorkspace := uuid.NewString()
	if _, err := f.s.DB.Exec(ctx, "INSERT INTO workspaces(id,tenant_id,name,status) VALUES($1,$2,'Second training workspace','active')",
		localWorkspace, f.admin.Me.Workspace.TenantID); err != nil {
		t.Fatal(err)
	}
	for _, collection := range []struct{ name, cursor, otherCursor, column, record string }{
		{"checkpoints", cpCursor, recCursor, "RECEIPT SHA256", checkpoints[1].ID},
		{"recoveries", recCursor, cpCursor, "PREVIOUS ATTEMPT", pendingRecovery},
	} {
		table := ok("training", collection.name, running.ID, "--limit", "1")
		if !strings.Contains(table, collection.column) || !strings.Contains(table, collection.record) ||
			!strings.Contains(table, collection.cursor) {
			t.Fatal("history table omitted its record or continuation", collection.name)
		}
		refused("INVALID_CURSOR", append(historyArgs(collection.name), "--cursor", collection.otherCursor)...)
		refused("INVALID_CURSOR", "training", collection.name, created.ID, "--cursor", collection.cursor)
		refused("INVALID_CURSOR", "training", collection.name, running.ID, "--workspace", localWorkspace, "--cursor", collection.cursor)
		refused("TRAINING_NOT_FOUND", "training", collection.name, running.ID, "--workspace", localWorkspace)
		refused("RESOURCE_NOT_FOUND", "training", collection.name, running.ID, "--workspace", other)
	}
	assertHistoryRevoked := func() {
		t.Helper()
		for collection, cursor := range map[string]string{"checkpoints": cpCursor, "recoveries": recCursor} {
			output, err := run(append(historyArgs(collection), "--cursor", cursor)...)
			if err == nil || !strings.Contains(output, "AUTH_SESSION_EXPIRED") ||
				strings.Contains(output, checkpoints[0].ID) || strings.Contains(output, dispatchedRecovery) {
				t.Fatal("revoked Admin authority read retained history", collection, err)
			}
		}
	}
	setAdmin := func(allowed bool) {
		t.Helper()
		if _, err := f.s.DB.Exec(ctx, "UPDATE users SET is_platform_admin=$1 WHERE id=$2",
			allowed, f.admin.Me.User.ID); err != nil {
			t.Fatal(err)
		}
	}
	setAdmin(false)
	assertHistoryRevoked()
	setAdmin(true)
	if _, err := f.s.DB.Exec(ctx, `UPDATE sessions SET revoked_at=clock_timestamp(),revoke_reason='logout'
		WHERE user_id=$1 AND audience='admin' AND revoked_at IS NULL`, f.admin.Me.User.ID); err != nil {
		t.Fatal(err)
	}
	assertHistoryRevoked()
	ok("login", "--email", "admin@example.invalid", "--password-file", password)
	ok(append(historyArgs("checkpoints"), "--cursor", cpCursor)...)
	ok(append(historyArgs("recoveries"), "--cursor", recCursor)...)
	ok("logout")
}
