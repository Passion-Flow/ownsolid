//go:build training_artifact_integration && model_lifecycle_admission_integration && model_lifecycle_output_acceptance_integration && training_operator_integration

package training_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	agent "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/deployments"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/distribution"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/models"
	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/databaseaccess"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
	gatewaywire "github.com/Passion-Flow/ownsolid/api/gateway/v1"
)

// The predecessor owns a genuinely accepted current CPU adapter and checkpoint.
// Only its node/GPU/production-stop envelopes are synthetic. No hardware claim.
func TestActualNativeDeliveredM09LifecycleUpgradePreservesAcceptedFiles(t *testing.T) {
	t.Setenv("PROJECT_ONE_TEST_APPLICATION_PROFILE", "training")
	f, _ := acceptedTrainingForPreparation(t)
	copyPreparationBase(t, preparationScenario{f: f}, f.input.ModelID)

	// Preserve a real resumable8MiB model upload in a separately authorized scope.
	other, e := f.s.Workspaces.Create(t.Context(), f.admin, "Retained M09 partial import", f.admin.Me.User.ID, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	archive, e := os.Open(filepath.Join(filepath.Dir(os.Getenv("PROJECT_ONE_APPROVED_MODEL_DIRECTORY")), "qwen3-0.6b.tar"))
	if e != nil {
		t.Fatal(e)
	}
	archiveInfo, e := archive.Stat()
	if e != nil {
		archive.Close()
		t.Fatal(e)
	}
	partial, e := f.s.Models.StartImport(t.Context(), f.admin, models.ImportRequest{WorkspaceID: other.ID, Name: "Actual retained partial archive", CatalogDigest: agent.ModelCatalogDigest(), ExpectedBytes: archiveInfo.Size(), LicenseAcknowledged: true, Reason: "Explicit isolated M09 upgrade preservation"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		archive.Close()
		t.Fatal(e)
	}
	chunk := make([]byte, agent.ModelTransferChunkBytes)
	_, e = io.ReadFull(archive, chunk)
	archive.Close()
	if e != nil {
		t.Fatal(e)
	}
	sum := sha256.Sum256(chunk)
	if _, e = f.s.Models.UploadChunk(t.Context(), f.admin, partial.ID, 0, int64(len(chunk)), hex.EncodeToString(sum[:]), bytes.NewReader(chunk)); e != nil {
		t.Fatal(e)
	}
	// Keep the old explicit1.4 enrollment; never grant lifecycle authority before upgrade.
	node, e := f.s.Nodes.Get(t.Context(), f.admin, "", f.input.NodeID)
	if e != nil {
		t.Fatal(e)
	}
	node.Facts.ObservedAt = time.Now().UTC()
	obs := &agent.InferenceObservation{GPUs: []agent.NVIDIAGPU{{UUID: f.input.GPUUUID, Name: "Explicit synthetic retained GPU", DriverVersion: "580.1.0", MemoryTotalMiB: 16384, MemoryFreeMiB: 16384, ComputeCapability: 8, MIGMode: "Disabled", ObservedAt: time.Now().UTC()}}, ObservedAt: time.Now().UTC()}
	if _, e = f.s.Nodes.Heartbeat(t.Context(), f.input.NodeID, f.credential, agent.Handshake{ProtocolVersion: agent.AIVersion, ExecutionProfile: agent.DockerAIProfile, Capabilities: agent.Capabilities(agent.DockerAIProfile), Facts: node.Facts, Inference: obs}); e != nil {
		t.Fatal(e)
	}
	deployment, e := f.s.Inference.Create(t.Context(), f.admin, deployments.CreateRequest{NodeID: f.input.NodeID, ModelID: f.input.ModelID, GPUUUID: f.input.GPUUUID, Reason: "Explicit synthetic retained physical owner"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	g := preparationScenario{f: f, node: agent.Enrolled{NodeID: f.input.NodeID, Credential: f.credential}}
	g.input.GPUUUID = f.input.GPUUUID
	nativeInferenceResult(t, g, deployment, "running")
	deployment, e = f.s.Inference.Get(t.Context(), f.admin, f.input.WorkspaceID, deployment.ID)
	if e != nil {
		t.Fatal(e)
	}
	distributionService := &distribution.Service{DB: f.s.DB, Identity: f.s.Identity, Workspaces: f.s.Workspaces, Nodes: f.s.Nodes, Deployments: f.s.Inference}
	gateway, e := distributionService.Provision(t.Context(), f.admin, distribution.ProvisionRequest{WorkspaceID: f.input.WorkspaceID, NodeID: f.input.NodeID, Name: "Retained M09 gateway", ClientOrigin: "https://gateway.example.invalid", Reason: "Explicit isolated upgrade"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	alias, e := distributionService.Publish(t.Context(), f.admin, distribution.PublishRequest{WorkspaceID: f.input.WorkspaceID, GatewayID: gateway.Gateway.ID, Name: "retained-model", DeploymentID: deployment.ID, DeploymentRevision: deployment.Revision, Reason: "Preserved immutable target"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	key, e := distributionService.MintKey(t.Context(), f.portal, distribution.KeyRequest{WorkspaceID: f.input.WorkspaceID, AliasID: alias.ID, Label: "Retained same key", TTLSeconds: 3600, Reason: "Explicit isolated preservation"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	if _, e = distributionService.Admit(t.Context(), gateway.Gateway.ID, *gateway.Secret, gatewaywire.AdmissionRequest{RequestID: uuid.NewString(), APIKey: *key.Secret, Model: alias.Name, MaximumOutputTokens: 16}, uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	if _, e = f.s.DB.Exec(t.Context(), "UPDATE gpu_leases SET state='recovery_required' WHERE deployment_id=$1", deployment.ID); e != nil {
		t.Fatal(e)
	}
	h := newNativeTrainingOperator(t, f)
	ctx := t.Context()
	root := filepath.Dir(h.Configuration)
	raw, e := os.ReadFile(h.Configuration)
	if e != nil {
		t.Fatal(e)
	}
	var old map[string]any
	if json.Unmarshal(raw, &old) != nil {
		t.Fatal("native predecessor config")
	}
	dsnPath := old["database"].(map[string]any)["dsn_file"].(string)
	rawDSN, e := config.Secret(dsnPath)
	if e != nil {
		t.Fatal(e)
	}
	uri, e := url.Parse(strings.TrimSpace(string(rawDSN)))
	if e != nil {
		t.Fatal("private predecessor DSN")
	}
	role := "m10_upgrade_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	credential, e := databaseaccess.NewCredential()
	if e != nil {
		t.Fatal(e)
	}
	uri.User = url.UserPassword(role, credential)
	write := func(name string, b []byte) string {
		p := filepath.Join(root, name)
		if e := os.WriteFile(p, b, 0600); e != nil {
			t.Fatal(e)
		}
		return p
	}
	runtimeDSN := write("runtime-upgrade-dsn", []byte(uri.String()))
	save := func(name string, values map[string]any, runtime bool) string {
		b, _ := json.Marshal(values)
		var v map[string]any
		json.Unmarshal(b, &v)
		if runtime {
			v["database"].(map[string]any)["dsn_file"] = runtimeDSN
		}
		b, _ = json.Marshal(v)
		return write(name, b)
	}
	oldOperator, oldRuntime := save("m09-operator.json", old, false), save("m09-runtime.json", old, true)
	prior, current := os.Getenv("PROJECT_ONE_M09_CONTROL_BINARY"), os.Getenv("PROJECT_ONE_CONTROL_BINARY")
	if !filepath.IsAbs(prior) || !filepath.IsAbs(current) {
		t.Fatal("delivered M09 and current binaries required")
	}
	run := func(binary, command, operator, runtime string) {
		args := []string{command, "--config", operator}
		if runtime != "" {
			args = append(args, "--runtime-config", runtime)
		}
		output, e := exec.CommandContext(ctx, binary, args...).CombinedOutput()
		if e != nil {
			t.Fatalf("native %s: %v %s", command, e, output)
		}
		if strings.Contains(string(output), uri.String()) || strings.Contains(string(output), string(rawDSN)) {
			t.Fatal("secret in operator output")
		}
	}

	// Simulate an already installed restricted M09 principal in this disposable
	// fixture. Product prepare-runtime-access correctly refuses a populated DB;
	// the delivered predecessor independently verifies every actual privilege.
	if _, e = f.s.DB.Exec(ctx, "CREATE ROLE "+pgx.Identifier{role}.Sanitize()+" LOGIN NOINHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS CONNECTION LIMIT 8 PASSWORD '"+credential+"'"); e != nil {
		t.Fatal("owned installed principal setup")
	}
	t.Cleanup(func() {
		cleanup := context.Background()
		if _, e := f.s.DB.Exec(cleanup, "DROP OWNED BY "+pgx.Identifier{role}.Sanitize()); e != nil {
			t.Error("owned upgrade role grants cleanup", e)
		}
		if _, e := f.s.DB.Exec(cleanup, "DROP ROLE "+pgx.Identifier{role}.Sanitize()); e != nil {
			t.Error("owned upgrade role cleanup", e)
		}
	})

	schema := f.s.DB.Config().ConnConfig.RuntimeParams["search_path"]
	if _, e = f.s.DB.Exec(ctx, "GRANT CONNECT ON DATABASE "+pgx.Identifier{f.s.DB.Config().ConnConfig.Database}.Sanitize()+" TO "+pgx.Identifier{role}.Sanitize()+";GRANT USAGE ON SCHEMA "+pgx.Identifier{schema}.Sanitize()+" TO "+pgx.Identifier{role}.Sanitize()+";GRANT SELECT ON TABLE schema_migrations TO "+pgx.Identifier{role}.Sanitize()); e != nil {
		t.Fatal(e)
	}
	catalogRows, e := f.s.DB.Query(ctx, "SELECT tablename FROM pg_tables WHERE schemaname=current_schema() AND tablename<>'schema_migrations' ORDER BY tablename")
	if e != nil {
		t.Fatal(e)
	}
	var tables []string
	for catalogRows.Next() {
		var table string
		if e = catalogRows.Scan(&table); e != nil {
			t.Fatal(e)
		}
		tables = append(tables, table)
	}
	catalogRows.Close()
	if e = catalogRows.Err(); e != nil || len(tables) != 49 {
		t.Fatal("exact installed M09 table grants", len(tables), e)
	}
	for _, table := range tables {
		if _, e = f.s.DB.Exec(ctx, "GRANT SELECT,INSERT,UPDATE,DELETE ON TABLE "+pgx.Identifier{schema, table}.Sanitize()+" TO "+pgx.Identifier{role}.Sanitize()); e != nil {
			t.Fatal(e)
		}
	}
	run(prior, "check-runtime-access", oldOperator, oldRuntime)
	var oid uint32
	var password string
	if e := f.s.DB.QueryRow(ctx, "SELECT oid,rolpassword FROM pg_authid WHERE rolname=$1", role).Scan(&oid, &password); e != nil {
		t.Fatal(e)
	}
	rows, e := f.s.DB.Query(ctx, "SELECT table_name,column_name FROM information_schema.columns WHERE table_schema=current_schema() ORDER BY table_name,ordinal_position")
	if e != nil {
		t.Fatal(e)
	}
	columns := map[string][]string{}
	for rows.Next() {
		var table, name string
		if e = rows.Scan(&table, &name); e != nil {
			t.Fatal(e)
		}
		columns[table] = append(columns[table], pgx.Identifier{name}.Sanitize())
	}
	rows.Close()
	if rows.Err() != nil || len(columns) != 50 {
		t.Fatal("exact populated M09 catalog", len(columns), rows.Err())
	}
	snapshot := func() map[string]string {
		result := map[string]string{}
		for table, names := range columns {
			where := ""
			if table == "schema_migrations" {
				where = " WHERE version<=15"
			}
			var v string
			if e := f.s.DB.QueryRow(ctx, "SELECT COALESCE(jsonb_agg(v ORDER BY v::text),'[]'::jsonb)::text FROM (SELECT to_jsonb(t) v FROM (SELECT "+strings.Join(names, ",")+" FROM "+pgx.Identifier{table}.Sanitize()+where+") t) rows").Scan(&v); e != nil {
				t.Fatal(e)
			}
			result[table] = v
		}
		return result
	}
	files := func() map[string]string {
		result := map[string]string{}
		for _, base := range []string{f.s.Models.RootDirectory, f.s.Identity.Config.Datasets.ArtifactRoot, f.s.Identity.Config.Training.ArtifactRoot} {
			e := filepath.WalkDir(base, func(path string, d os.DirEntry, e error) error {
				if e != nil {
					return e
				}
				if d.IsDir() {
					return nil
				}
				info, e := d.Info()
				if e != nil || !info.Mode().IsRegular() {
					return fmt.Errorf("ordinary preserved input required")
				}
				file, e := os.Open(path)
				if e != nil {
					return e
				}
				sum := sha256.New()
				_, e = io.Copy(sum, file)
				file.Close()
				if e != nil {
					return e
				}
				physical, ok := info.Sys().(*syscall.Stat_t)
				if !ok {
					return fmt.Errorf("file physical identity required")
				}
				result[path] = fmt.Sprintf("%d:%d:%d:%d:%d:%d:%s", physical.Dev, physical.Ino, physical.Nlink, info.Mode(), info.Size(), info.ModTime().UnixNano(), hex.EncodeToString(sum.Sum(nil)))
				return nil
			})
			if e != nil {
				t.Fatal(e)
			}
		}
		return result
	}
	before, retained := snapshot(), files()
	if e = f.s.Store.Close(); e != nil {
		t.Fatal(e)
	}
	if e = f.s.Datasets.Store.Close(); e != nil {
		t.Fatal(e)
	}
	old["server"].(map[string]any)["application_profile"] = "lifecycle"
	preparationRoot := filepath.Join(root, "new-preparations")
	if e = os.Mkdir(preparationRoot, 0700); e != nil {
		t.Fatal(e)
	}
	releaseBytes, e := os.ReadFile(os.Getenv("PROJECT_ONE_MODEL_LIFECYCLE_IMAGE_MANIFEST"))
	if e != nil {
		t.Fatal(e)
	}
	var release map[string]any
	if json.Unmarshal(releaseBytes, &release) != nil {
		t.Fatal("actual lifecycle release")
	}
	old["model_preparation"] = map[string]any{"artifact_root": preparationRoot, "image_reference": release["image_reference"], "image_id": release["image_id"], "worker_source_sha256": release["worker_source_sha256"]}
	operator, runtime := save("m10-operator.json", old, false), save("m10-runtime.json", old, true)
	for i := 0; i < 2; i++ {
		run(current, "upgrade-lifecycle", operator, runtime)
	}
	run(current, "check-runtime-access", operator, runtime)
	if e = database.Check(ctx, f.s.DB, "lifecycle"); e != nil {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(before, snapshot()) || !reflect.DeepEqual(retained, files()) {
		t.Fatal("populated M09 rows or original file identities changed")
	}
	var after uint32
	var verifier string
	if e = f.s.DB.QueryRow(ctx, "SELECT oid,rolpassword FROM pg_authid WHERE rolname=$1", role).Scan(&after, &verifier); e != nil || after != oid || verifier != password {
		t.Fatal("original runtime role changed", e)
	}
	output, e := exec.CommandContext(ctx, current, "initialize-preparation-store", "--config", operator, "--preparation-store-root", preparationRoot, "--confirm-empty-preparation-store", "--owner-uid", fmt.Sprint(os.Geteuid()), "--owner-gid", fmt.Sprint(os.Getegid()), "--reason", "Actual isolated delivered M09 upgrade").CombinedOutput()
	if e != nil {
		t.Fatalf("new preparation initialization: %v %s", e, output)
	}
	t.Log("Actual delivered M09 binary/principal, accepted CPU adapter/checkpoint/base/dataset files, all50 original table projections, retained partial8MiB upload, old1.4 enrollment, gateway/key/alias/target history, unknown request and GPU owner, unchanged device/inode/link/mode/size/mtime/hash and repeat current native upgrade; NVIDIA envelopes remain synthetic.")
}
