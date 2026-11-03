//go:build training_artifact_integration && model_lifecycle_admission_integration && training_operator_integration

package training_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
)

func init() { preparationPairedRestoreProfile = actualPreparationPairedRestore }

func configureNativePreparation(t *testing.T, h nativeTrainingHarness, release config.ModelPreparationConfiguration) {
	t.Helper()
	raw, e := os.ReadFile(h.Configuration)
	if e != nil {
		t.Fatal(e)
	}
	var values map[string]any
	if json.Unmarshal(raw, &values) != nil {
		t.Fatal("native operator configuration invalid")
	}
	values["model_preparation"] = map[string]any{"artifact_root": release.ArtifactRoot, "image_reference": release.ImageReference, "image_id": release.ImageID, "worker_source_sha256": release.WorkerSourceSHA256}
	raw, e = json.Marshal(values)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(h.Configuration, raw, 0600); e != nil {
		t.Fatal(e)
	}
}

// This is an actual native/PG/custom-dump/four-store isolated recovery profile.
// Source acceptance uses real files; physical node/production observations remain explicit doubles.
func actualPreparationPairedRestore(t *testing.T, g preparationScenario) {
	t.Helper()
	owned := fixture.OwnedPostgres(t, g.f.s.DB)
	var e error
	ctx, cancel := context.WithTimeout(context.Background(), 280*time.Second)
	defer cancel()
	upgradePopulatedM10ToSSO(t, g)
	// A separately delivered uncertainty must remain fenced after restore.
	active := preparationDispatched(t, g)
	oldLease := preparationClaimed(t, g)
	ssoState := prepareCumulativeSSORecovery(t, g)
	upgradePopulatedM11ToKnowledge(t, g)
	knowledgeState := prepareCumulativeKnowledgeRecovery(t, g)
	upgradePopulatedM12ToWorkflows(t, g)
	workflowState := prepareCumulativeWorkflowRecovery(t, g)
	upgradePopulatedM13ToPlugins(t, g)
	pluginState := prepareCumulativePluginRecovery(t, g)
	upgradePopulatedM14ToPortal(t, g)
	upgradePopulatedM15ToLocalization(t, g)
	portalState := prepareCumulativePortalRecovery(t, g)
	localeState := prepareCumulativeLocalizationRecovery(t, g)
	h := newNativeTrainingOperator(t, g.f)
	h.SetPair("", g.f.s.Models.RootDirectory, g.f.s.Identity.Config.Datasets.ArtifactRoot)
	configureNativePreparation(t, h, g.f.s.Identity.Config.ModelPreparation)
	for _, store := range []interface{ Close() error }{g.store, g.f.s.Store, g.f.s.Datasets.Store} {
		if e = store.Close(); e != nil {
			t.Fatal(e)
		}
	}
	// The actual operator must apply an explicit environment file before selecting
	// its accepted store. A deliberately wrong protected JSON root fails closed.
	configured, e := os.ReadFile(h.Configuration)
	if e != nil {
		t.Fatal(e)
	}
	var alternate map[string]any
	if json.Unmarshal(configured, &alternate) != nil {
		t.Fatal("operator configuration fixture")
	}
	alternate["model_preparation"].(map[string]any)["artifact_root"] = snapshotTestDirectory(t)
	alternateRaw, e := json.Marshal(alternate)
	if e != nil {
		t.Fatal(e)
	}
	configuration := filepath.Join(t.TempDir(), "alternate-operator.json")
	if os.WriteFile(configuration, alternateRaw, 0600) != nil {
		t.Fatal("private alternate operator configuration")
	}
	environment := filepath.Join(t.TempDir(), "preparation.env")
	selectedRoot, _ := json.Marshal(g.f.s.Identity.Config.ModelPreparation.ArtifactRoot)
	if os.WriteFile(environment, []byte("PROJECT_ONE_MODEL_PREPARATION_ARTIFACT_ROOT="+string(selectedRoot)+"\n"), 0600) != nil {
		t.Fatal("private explicit preparation environment")
	}
	command := func(extra ...string) ([]byte, error) {
		bounded, done := context.WithTimeout(ctx, 20*time.Second)
		defer done()
		return exec.CommandContext(bounded, os.Getenv("PROJECT_ONE_CONTROL_BINARY"), append([]string{"verify-preparation-files", "--config", configuration, "--reason", "Actual explicit environment selects accepted preparation store"}, extra...)...).CombinedOutput()
	}
	if raw, err := command(); err == nil || bytes.Contains(raw, []byte("POLICY_ENVIRONMENT_FLAG_INVALID")) {
		t.Fatal("wrong configured accepted store did not fail at storage boundary", err)
	}
	if raw, err := command("--env-file", environment); err != nil {
		t.Fatal("native preparation operator explicit environment rejected", err, string(raw))
	}
	t.Log("Actual native preparation operator wrong YAML store refusal and explicit protected environment override verified accepted files")
	backup := snapshotTestDirectory(t)
	// Derived snapshot directories remain immutable until this newly owned test tree is removed.
	t.Cleanup(func() {
		filepath.WalkDir(backup, func(path string, entry os.DirEntry, e error) error {
			if e == nil && entry.IsDir() {
				os.Chmod(path, 0700)
			}
			return nil
		})
	})
	for _, kind := range []string{"model", "dataset", "training", "preparation"} {
		if e = h.RunWithin(ctx, "export-"+kind+"-files", "--backup-root", filepath.Join(backup, kind)); e != nil {
			t.Fatal("actual paired file export", kind, e)
		}
	}
	copyCumulativeSSOKeyring(t, ssoState, backup)
	schema := owned.Schema
	archive := owned.DumpSchema(t, ctx)
	if e = os.WriteFile(filepath.Join(backup, "database.dump"), archive, 0600); e != nil {
		t.Fatal(e)
	}
	if workflowState != nil {
		retainMatchedWorkflowEvidence(t, backup, archive)
	}
	restored := owned.RestoreFresh(t, ctx, g.f.s.DB, archive, g.f.s.Identity.Config.AccountRuntime())
	target := restored.Config().ConnConfig.Database
	rawDSN := os.Getenv("PROJECT_ONE_TEST_DSN")
	if rawDSN == "" {
		b, e := config.Secret(os.Getenv("PROJECT_ONE_TEST_DSN_FILE"))
		if e != nil {
			t.Fatal(e)
		}
		rawDSN = string(b)
	}
	dsn, e := url.Parse(strings.TrimSpace(rawDSN))
	if e != nil {
		t.Fatal(e)
	}
	dsn.Path = "/" + target
	q := dsn.Query()
	q.Set("search_path", schema)
	dsn.RawQuery = q.Encode()
	modelRoot, datasetRoot, trainingRoot, preparationRoot := snapshotTestDirectory(t), snapshotTestDirectory(t), snapshotTestDirectory(t), snapshotTestDirectory(t)
	t.Cleanup(func() {
		filepath.WalkDir(modelRoot, func(path string, entry os.DirEntry, e error) error {
			if e == nil && entry.IsDir() {
				os.Chmod(path, 0700)
			}
			return nil
		})
	})
	h.SetTrainingRoot(trainingRoot)
	h.SetPair(dsn.String(), modelRoot, datasetRoot)
	release := g.f.s.Identity.Config.ModelPreparation
	release.ArtifactRoot = preparationRoot
	configureNativePreparation(t, h, release)
	restoreCumulativeSSOKeyring(t, ssoState, h, backup)
	if workflowState != nil {
		before := cumulativeWorkflowRows(t, restored)
		var clearFault func()
		if g.f.s.Identity.Config.LocalizationOnly() {
			clearFault = fixture.ArmLateLocalizationRestoreFailure(t, restored)
		} else {
			clearFault = fixture.ArmLateWorkflowRestoreFailure(t, restored)
		}
		if h.RunWithin(ctx, "invalidate-restored-auth") == nil {
			t.Fatal("matched restore accepted actual late workflow failure")
		}
		assertCumulativeWorkflowRows(t, restored, before)
		clearFault()
	}
	if e = h.RunWithin(ctx, "invalidate-restored-auth"); e != nil {
		t.Fatal(e)
	}
	for _, kind := range []string{"model", "dataset", "training", "preparation"} {
		args := []string{"--backup-root", filepath.Join(backup, kind)}
		if kind == "training" {
			args = append(args, "--training-store-root", trainingRoot, "--confirm-empty-training-store")
		}
		if kind == "preparation" {
			args = append(args, "--preparation-store-root", preparationRoot, "--confirm-empty-preparation-store")
		}
		if e = h.RunWithin(ctx, "import-"+kind+"-files", args...); e != nil {
			t.Fatal("actual paired file import", kind, e)
		}
	}
	for _, name := range []string{"verify-model-store", "verify-dataset-store", "verify-training-files", "verify-preparation-files"} {
		if e = h.RunWithin(ctx, name); e != nil {
			t.Fatal("actual paired restored integrity", name, e)
		}
	}
	verifyCumulativeSSORecovery(t, ssoState, g, restored)
	verifyCumulativeKnowledgeRecovery(t, knowledgeState, g, restored)
	verifyCumulativeWorkflowRecovery(t, workflowState, g, restored)
	verifyCumulativePluginRecovery(t, pluginState, g, restored)
	verifyCumulativePortalRecovery(t, portalState, g, restored)
	verifyCumulativeLocalizationRecovery(t, localeState, g, restored)
	var state, attempt, lease string
	var revoked bool
	if e = restored.QueryRow(ctx, `SELECT p.state,a.state,g.state,n.credential_hash IS NULL FROM model_preparations p JOIN model_preparation_attempts a ON a.id=p.current_attempt_id JOIN gpu_leases g ON g.model_preparation_id=p.id JOIN managed_nodes n ON n.id=p.current_node_id WHERE p.id=$1`, active.ID).Scan(&state, &attempt, &lease, &revoked); e != nil {
		t.Fatal(e)
	}
	if state != "recovery_required" || attempt != "recovery_required" || lease != "recovery_required" || !revoked {
		t.Fatal("restored uncertainty or node authority escaped fence", state, attempt, lease, revoked)
	}
	var artifacts, models, reviews int
	if e = restored.QueryRow(ctx, `SELECT (SELECT count(*) FROM model_preparation_artifacts),(SELECT count(*) FROM model_versions WHERE kind='merged_lora'),(SELECT count(*) FROM model_preparation_reviews WHERE decision='approve')`).Scan(&artifacts, &models, &reviews); e != nil || artifacts != 1 || models != 1 || reviews != 1 {
		t.Fatal("accepted derivation graph not restored", e, artifacts, models, reviews)
	}
	service := *g.service
	service.DB = restored
	identity := *g.f.s.Identity
	identity.DB = restored
	service.Identity = &identity
	node := *g.f.s.Nodes
	node.DB = restored
	node.Identity = &identity
	service.Nodes = &node
	if _, e = service.RecordStop(ctx, g.node.NodeID, g.node.Credential, oldLease.TaskID, oldLease.LeaseToken, preparationStopReceipt(t, g, oldLease), nil); e == nil {
		t.Fatal("restored old node credential completed a prior owner")
	}
	ca, e := os.ReadFile(h.CAFile)
	if e != nil {
		t.Fatal(e)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca) {
		t.Fatal("fixture CA invalid")
	}
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}, Timeout: 10 * time.Second}
	defer client.CloseIdleConnections()
	startStop := func() {
		t.Helper()
		process := exec.CommandContext(ctx, os.Getenv("PROJECT_ONE_CONTROL_BINARY"), "serve", "--config", h.Configuration)
		log, e := os.CreateTemp(backup, "native-service-")
		if e != nil {
			t.Fatal(e)
		}
		defer log.Close()
		process.Stdout = log
		process.Stderr = log
		if e = process.Start(); e != nil {
			t.Fatal(e)
		}
		done := make(chan error, 1)
		go func() { done <- process.Wait() }()
		defer func() {
			process.Process.Signal(syscall.SIGTERM)
			select {
			case e := <-done:
				if e != nil {
					t.Error("native restored shutdown", e)
				}
			case <-time.After(10 * time.Second):
				process.Process.Kill()
				<-done
				t.Error("native restored shutdown timeout")
			}

			raw, readError := os.ReadFile(log.Name())
			if readError != nil {
				t.Error(readError)
			} else if output := os.Getenv("PROJECT_ONE_EVIDENCE_ROOT"); output != "" {
				for _, secret := range []string{dsn.String(), rawDSN, string(g.f.s.Identity.HMACKey)} {
					raw = bytes.ReplaceAll(raw, []byte(secret), []byte("[redacted]"))
				}
				if e := os.WriteFile(filepath.Join(output, filepath.Base(log.Name())+".log"), raw, 0600); e != nil {
					t.Error(e)
				}
			}
		}()
		deadline := time.Now().Add(30 * time.Second)
		for time.Now().Before(deadline) {
			response, e := client.Get(h.Origin + "/health/ready")
			if e == nil {
				response.Body.Close()
				if response.StatusCode == 200 {
					return
				}
			}
			time.Sleep(100 * time.Millisecond)
		}
		t.Fatal("actual restored preparation native readiness timeout")
	}
	startStop()
	startStop()
	var relative string
	if e = restored.QueryRow(ctx, "SELECT storage_path FROM model_preparation_artifacts LIMIT 1").Scan(&relative); e != nil {
		t.Fatal(e)
	}
	protected := filepath.Join(preparationRoot, relative, "model.safetensors")
	preserved := protected + ".preserved"
	if e = os.Rename(protected, preserved); e != nil {
		t.Fatal(e)
	}
	invalid := exec.CommandContext(ctx, os.Getenv("PROJECT_ONE_CONTROL_BINARY"), "serve", "--config", h.Configuration)
	raw, e := invalid.CombinedOutput()
	if e == nil || !bytes.Contains(raw, []byte("MODEL_PREPARATION_STORAGE_DATA_INVALID")) {
		t.Fatal("missing accepted preparation did not fail startup")
	}
	if e = os.Rename(preserved, protected); e != nil {
		t.Fatal(e)
	}
	startStop()
	t.Log("Actual PostgreSQL custom dump/restore into a new isolated test DB, full base/derived model+dataset+training+preparation exports/imports, native four-store verify, three bounded TLS start-stop cycles, missing accepted model refusal/repair, retained approved graph and uncertain GPU with old credentials fenced. No physical NVIDIA qualification.")
}

// Retain minimal synthetic recovery evidence externally before disposable test
// stores are removed. Accepted large files remain at their original producers.
func retainMatchedWorkflowEvidence(t *testing.T, backup string, archive []byte) {
	t.Helper()
	output := os.Getenv("PROJECT_ONE_EVIDENCE_ROOT")
	if output == "" {
		t.Fatal("matched workflow evidence owner required")
	}
	root := filepath.Join(output, "matched-snapshot")
	if e := os.Mkdir(root, 0700); e != nil {
		t.Fatal(e)
	}
	facts := map[string]any{}
	write := func(name string, raw []byte) {
		path := filepath.Join(root, name)
		file, e := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			t.Fatal(e)
		}
		_, e = file.Write(raw)
		syncError := file.Sync()
		closeError := file.Close()
		if e != nil || syncError != nil || closeError != nil {
			t.Fatal("retained matched evidence durability", e, syncError, closeError)
		}
		digest := sha256.Sum256(raw)
		facts[name] = map[string]any{"bytes": len(raw), "sha256": hex.EncodeToString(digest[:])}
	}
	write("database.dump", archive)
	keyring, e := os.ReadFile(filepath.Join(backup, "sso-keyring.json"))
	if e != nil || len(keyring) > 65536 {
		t.Fatal("bounded matching synthetic keyring required", e)
	}
	write("sso-keyring.private.json", keyring)
	for _, kind := range []string{"model", "dataset", "training", "preparation"} {
		found := false
		for _, name := range []string{"model-files.json", "dataset-files.json", "snapshot.json"} {
			raw, e := os.ReadFile(filepath.Join(backup, kind, name))
			if os.IsNotExist(e) {
				continue
			}
			if e != nil || len(raw) > 4<<20 {
				t.Fatal("bounded actual snapshot inventory", e)
			}
			if found {
				t.Fatal("ambiguous actual snapshot inventory", kind)
			}
			found = true
			write(kind+"-"+name, raw)
		}
		if !found {
			t.Fatal("actual snapshot inventory missing", kind)
		}
	}
	raw, e := json.Marshal(map[string]any{"format": "projectone.m13.matched-snapshot.v1", "files": facts, "scope": "Private synthetic SQL and exact four-store manifests; actual import/readback and keyring checks remain in the raw test log. Original accepted large files are retained separately. No customer data or hardware qualification."})
	if e != nil {
		t.Fatal(e)
	}
	write("receipt.json", raw)
	directory, e := os.Open(root)
	if e != nil {
		t.Fatal(e)
	}
	e = directory.Sync()
	directory.Close()
	if e != nil {
		t.Fatal(e)
	}
}
