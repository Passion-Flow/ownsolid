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
	ok("logout")
}
