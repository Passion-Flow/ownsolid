//go:build repository_integration

package models_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	datawire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	lifecycle "github.com/Passion-Flow/ownsolid/api/model-lifecycle/v1"
	trainingwire "github.com/Passion-Flow/ownsolid/api/training/v1"
	transport "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/modelpreparations/snapshot"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/training"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/httpfixture"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/repositoryfixture"
)

const modelContract = "../../../../../api/openapi/v1/models/openapi.json"

func adminModelDetailSchema(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(modelContract)
	if err != nil {
		t.Fatal(err)
	}
	var value any
	if err = json.Unmarshal(raw, &value); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"paths", "/api/v1/sites/{site_id}/cells/{cell_id}/admin/models/{model_id}", "get", "responses", "200", "content", "application/json", "schema", "$ref"} {
		object, _ := value.(map[string]any)
		value = object[key]
	}
	ref, _ := value.(string)
	schema, ok := strings.CutPrefix(ref, "#/components/schemas/")
	if !ok || schema == "" || schema == "ModelVersionResponse" {
		t.Fatal("Admin model detail needs its own response schema")
	}
	return schema
}

// These are retained version 1 publication inputs for metadata readback. The
// existing relational fixture creates the scoped graph under normal constraints.
func seedModelHTTPLineage(t *testing.T, x modelReadFixture, source *accessDerivedSource) (lifecycle.DerivedCatalog, snapshot.DatasetLineage) {
	t.Helper()
	p := retirementTraining(t, x, x.model)
	holdout := retirementTraining(t, x, x.model)
	var holdoutAttempt string
	if err := x.f.Store.QueryRow(x.ctx, x.f.Statement(
		"SELECT attempt_id::text FROM dataset_versions WHERE id=$1", "", holdout.version)).Scan(&holdoutAttempt); err != nil {
		t.Fatal(err)
	}
	digest := strings.Repeat("1", 64)
	configuration := trainingwire.DefaultConfiguration()
	configurationSHA, err := configuration.Digest()
	if err != nil {
		t.Fatal(err)
	}
	imageID := "sha256:" + digest
	command := trainingwire.Command{
		SchemaVersion: 1, Scope: trainingwire.Scope{TenantID: x.f.Tenant, WorkspaceID: x.f.Workspace, JobID: p.job},
		AttemptID: p.attempt, AttemptNumber: 1, ModelID: x.model, ModelCatalogSHA256: wire.ModelCatalogDigest(),
		DatasetID: p.dataset, DatasetVersionID: p.version, DatasetManifestSHA256: digest,
		GPUUUID: "GPU-" + uuid.NewString(), Configuration: configuration, ConfigurationSHA256: configurationSHA,
		Recipe: trainingwire.ExecutionRecipe{
			SchemaVersion: 1, WorkerVersion: trainingwire.WorkerVersion, ImageReference: "projectone/training@" + imageID,
			Identity:   trainingwire.ExecutionIdentity{Profile: trainingwire.ProductionProfile, Python: trainingwire.PythonVersion, WorkerSourceSHA256: digest, ImageID: &imageID},
			Resources:  trainingwire.DefaultResources(),
			DataLimits: datawire.Limits{MaximumBytes: 1048576, MaximumRows: 256, MaximumLineBytes: 16384, MaximumTextBytes: 4096, MaximumSourceBytes: 256, MaximumErrors: 10},
		},
		Inputs: trainingwire.InputInventory{
			Train:      datawire.Asset{Path: "train.jsonl", SizeBytes: 128, SHA256: digest},
			Validation: datawire.Asset{Path: "validation.jsonl", SizeBytes: 128, SHA256: strings.Repeat("2", 64)},
		},
	}
	adapter := training.UploadDescriptor{
		SchemaVersion: 1, Scope: command.Scope, UploadID: p.artifact, AttemptID: p.attempt,
		Kind: "artifact", GlobalStep: 1, ManifestSHA256: digest,
		Files: []datawire.Asset{
			{Path: "README.md", SizeBytes: 128, SHA256: digest},
			{Path: "adapter_config.json", SizeBytes: 128, SHA256: digest},
			{Path: "adapter_model.safetensors", SizeBytes: 128, SHA256: digest},
		},
	}
	inputs := snapshot.Inputs{
		SchemaVersion: 1, TrainingCommand: command, Adapter: snapshot.Adapter(adapter),
		TrainingIdentitySHA256: (training.AcceptedAdapter{Command: command, Descriptor: adapter}).IdentitySHA256(),
		Heldout: snapshot.Holdout{
			ID: p.holdout, WorkspaceID: x.f.Workspace, DatasetID: holdout.dataset, DatasetVersionID: holdout.version,
			Name: "Synthetic holdout", ManifestSHA256: digest, EffectiveSourceSHA256: digest, RowMappingSHA256: digest,
			SourceBytes: 3, RowCount: 3, Labels: snapshot.LabelCounts{Account: 1, Billing: 1, Technical: 1},
			RecipeID: lifecycle.RecipeID, RecipeSHA256: lifecycle.RecipeDigest(), InstructionSHA256: lifecycle.InstructionDigest(),
			FingerprintAlgorithm: lifecycle.PromptFingerprintAlgorithm, CreatedBy: x.f.User, Audience: "admin",
			Reason: "Synthetic retained holdout", CreatedAt: time.Now().UTC().Truncate(time.Microsecond),
		},
		DatasetAttemptID: holdoutAttempt,
	}
	if err := inputs.Validate(); err != nil {
		t.Fatal(err)
	}
	retirementPreparationWithSnapshot(t, x, p, x.model, &inputs)
	return accessDerivedFromPreparation(t, x, p, source, ""), inputs.Lineage()
}

func TestActualAdminModelDetailDatasetLineageHTTPS(t *testing.T) {
	owner := identityfixture.New(t)
	password := identityfixture.Bootstrap(t, owner)
	dispatcher := transport.New(owner)
	if err := dispatcher.InitializationError(); err != nil {
		t.Fatal(err)
	}
	server := httpfixture.NewTLSServer(t, dispatcher)
	t.Cleanup(server.Close)
	schemas := httpfixture.Schemas(t, modelContract)
	admin := httpfixture.New(t, server, "admin", schemas)
	portal := httpfixture.New(t, server, "portal", schemas)
	anonymous := httpfixture.New(t, server, "admin", schemas)
	login := map[string]any{"email": "admin@example.invalid", "password": password}
	admin.Write("POST", "/api/v1/auth/login", login, 200, "")
	portal.Write("POST", "/api/v1/auth/login", login, 200, "")
	store, err := owner.Repository()
	if err != nil {
		t.Fatal(err)
	}
	var source *accessDerivedSource
	timeout := 30 * time.Second
	output := os.Getenv("PROJECT_ONE_ACTUAL_PREPARATION_OUTPUT")
	if output != "" {
		if resolved, err := filepath.EvalSymlinks(output); err != nil || !filepath.IsAbs(output) || resolved != output {
			t.Fatal("retained CPU producer directory must be an exact absolute path", err)
		}
		source = readActualDerivedSource(t, filepath.Join(output, "result.json"))
		timeout = 3 * time.Minute
	}
	ctx, cancel := context.WithTimeout(t.Context(), timeout)
	defer cancel()
	x := modelReadFixture{ctx: ctx, model: uuid.NewString(), s: dispatcher.Models.Service}
	x.f = repositoryfixture.Fixture{Store: store, PostgreSQL: owner.DB}
	if err = owner.DB.QueryRow(x.ctx, "SELECT tenant_id::text,default_workspace_id::text,primary_admin_user_id::text FROM installation_state WHERE id=1").
		Scan(&x.f.Tenant, &x.f.Workspace, &x.f.User); err != nil {
		t.Fatal(err)
	}
	manifest, err := json.Marshal(wire.ApprovedModel())
	if err != nil {
		t.Fatal(err)
	}
	retirementSeed(t, x, store, "model_versions", map[string]any{
		"id": x.model, "name": "Synthetic HTTPS base model", "model_key": wire.ApprovedModel().ModelKey,
		"catalog_digest": wire.ModelCatalogDigest(), "manifest": retirementJSON(manifest), "state": "ready", "created_by": x.f.User,
	})
	catalog, want := seedModelHTTPLineage(t, x, source)
	if source != nil {
		accessRoot(t, x)
		t.Setenv("PROJECT_ONE_REPOSITORY_NATIVE_DERIVED_DIRECTORY", filepath.Join(output, "model"))
		accessPrepare(t, x, catalog, true)
	}
	adminSchema := adminModelDetailSchema(t)
	adminPath := "/api/v1/admin/models/" + catalog.ModelID
	portalList := "/api/v1/workspaces/" + x.f.Workspace + "/models"
	version := func(v snapshot.DatasetVersionLineage) map[string]any {
		return map[string]any{"dataset_id": v.DatasetID, "dataset_version_id": v.DatasetVersionID, "manifest_sha256": v.ManifestSHA256}
	}
	wanted := map[string]any{"training": version(want.Training), "holdout": version(want.Holdout)}
	read := func() map[string]any {
		t.Helper()
		data := admin.Call("GET", adminPath, nil, 200, adminSchema)
		if !reflect.DeepEqual(data["dataset_lineage"], wanted) {
			t.Fatal("Admin detail lost the distinct retained dataset references")
		}
		return data
	}
	before := read()
	if source != nil {
		verifyHTTPDerivedFiles(t, x, catalog, source, before)
	}
	x.exec(t, "UPDATE dataset_versions SET retired_at=clock_timestamp(),retire_reason=$1 WHERE id=$2", "",
		"Retire synthetic source version", want.Training.DatasetVersionID)
	after := read()
	if !reflect.DeepEqual(before, after) {
		t.Fatal("dataset retirement changed the published detail")
	}
	delete(after, "dataset_lineage")
	if data := portal.Call("GET", portalList+"/"+catalog.ModelID, nil, 200, "ModelVersionResponse"); !reflect.DeepEqual(data, after) {
		t.Fatal("Portal detail changed shape or received dataset lineage")
	}
	if base := admin.Call("GET", "/api/v1/admin/models/"+x.model, nil, 200, adminSchema); base["id"] != x.model || base["kind"] != "approved_base" {
		t.Fatal("Admin base detail changed")
	} else if _, present := base["dataset_lineage"]; present {
		t.Fatal("base model exposed a lineage field")
	}
	for _, data := range []map[string]any{
		admin.Call("GET", "/api/v1/admin/models?workspace_id="+x.f.Workspace, nil, 200, "ModelListResponse"),
		portal.Call("GET", portalList, nil, 200, "ModelListResponse"),
	} {
		found := false
		for _, item := range data["items"].([]any) {
			model := item.(map[string]any)
			if model["id"] == catalog.ModelID {
				found = true
			}
			if _, present := model["dataset_lineage"]; present {
				t.Fatal("model list exposed dataset lineage")
			}
		}
		if !found {
			t.Fatal("model list omitted the published derivative")
		}
	}

	anonymous.Call("GET", adminPath, nil, 401, "Problem")
	portal.Call("GET", adminPath, nil, 404, "Problem")
	other := uuid.NewString()
	x.exec(t, "INSERT INTO workspaces(id,tenant_id,name,status) VALUES($1,$2,'Other lineage workspace','active')", "", other, x.f.Tenant)
	portal.Call("GET", "/api/v1/workspaces/"+other+"/models/"+catalog.ModelID, nil, 404, "Problem")

	// Simulate damaged retained metadata only in the disposable fixture schema.
	x.exec(t, "ALTER TABLE model_preparation_artifacts DISABLE TRIGGER model_preparation_artifact_immutable", "")
	x.exec(t, "UPDATE model_preparation_artifacts SET manifest_sha256=$1 WHERE id=$2", "", strings.Repeat("f", 64), catalog.ArtifactID)
	x.exec(t, "ALTER TABLE model_preparation_artifacts ENABLE TRIGGER model_preparation_artifact_immutable", "")
	problem := admin.Call("GET", adminPath, nil, 409, "Problem")
	if problem["code"] != "MODEL_DERIVED_CATALOG_INVALID" {
		t.Fatal("corrupt publication did not return the existing problem code")
	}
}
