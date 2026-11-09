//go:build dataset_processor_integration

package datasets_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	agentwire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	wire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/datasets"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/models"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/httpfixture"
	"github.com/google/uuid"
)

type tokenizerPreviewProcessFixture struct {
	models        *models.Service
	configuration datasets.TokenizerPreviewConfiguration
	password      string
}

func verifyTokenizerPreviewHTTPS(t *testing.T, service *datasets.Service, auth identity.Auth, portal, admin *httpfixture.Client, path, adminPath string, process tokenizerPreviewProcessFixture) {
	t.Helper()
	approvedSource := os.Getenv("OWNSOLID_TEST_QWEN_TOKENIZER_DIRECTORY")
	if approvedSource == "" {
		t.Fatal("the pinned Qwen tokenizer files are required")
	}
	workspace, tenant := auth.Me.Workspace.ID, auth.Me.Workspace.TenantID
	model, foreignModel, foreignWorkspace := uuid.NewString(), uuid.NewString(), uuid.NewString()
	if _, e := service.DB.Exec(t.Context(), "INSERT INTO workspaces(id,tenant_id,name,status) VALUES($1,$2,'Foreign preview fixture','active')", foreignWorkspace, tenant); e != nil {
		t.Fatal(e)
	}
	manifest, e := json.Marshal(agentwire.ApprovedModel())
	if e != nil {
		t.Fatal(e)
	}
	// Seed registry metadata for the previously verified snapshot. The preview
	// must prove custody of its subset without reading the weights.
	for _, binding := range [][2]string{{model, workspace}, {foreignModel, foreignWorkspace}} {
		if _, e = service.DB.Exec(t.Context(), `INSERT INTO model_versions(id,tenant_id,workspace_id,name,model_key,catalog_digest,manifest,state,created_by)
VALUES($1,$2,$3,'Preview fixture',$4,$5,$6,'ready',$7)`,
			binding[0], tenant, binding[1], agentwire.ApprovedModel().ModelKey, agentwire.ModelCatalogDigest(), manifest, auth.Me.User.ID); e != nil {
			t.Fatal(e)
		}
	}
	modelPath := filepath.Join(service.Identity.Config.Models.ArtifactRoot, "objects", model)
	if e = os.MkdirAll(modelPath, 0700); e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{"config.json", "merges.txt", "tokenizer.json", "tokenizer_config.json", "vocab.json"} {
		raw, e := os.ReadFile(filepath.Join(approvedSource, name))
		if e != nil {
			t.Fatal(e)
		}
		writePreviewModelFixture(t, filepath.Join(modelPath, name), raw)
	}
	// The HTTPS client has a fixed 30-second deadline. Warm the exact
	// interpreter and tokenizer after cold installs on the shared test host.
	warm, stopWarmup := context.WithTimeout(t.Context(), 75*time.Second)
	defer stopWarmup()
	const load = `import sys
from transformers import AutoTokenizer
AutoTokenizer.from_pretrained(sys.argv[1], local_files_only=True, trust_remote_code=False)
`
	probe := exec.CommandContext(warm, process.configuration.PythonExecutable, "-I", "-B", "-c", load, modelPath)
	probe.Env = []string{"PATH=/usr/bin:/bin", "LANG=C.UTF-8", "LC_ALL=C.UTF-8",
		"OPENBLAS_NUM_THREADS=1", "OMP_NUM_THREADS=1", "TOKENIZERS_PARALLELISM=false"}
	if e = probe.Run(); e != nil {
		t.Fatal("warm the verified local tokenizer", e)
	}
	stopWarmup()

	record := wire.Record{SourceID: "preview", Prompt: strings.Repeat("token ", 65), Completion: "A synthetic response."}
	encoded, e := record.Canonical(service.Identity.Config.Policy().Datasets.Limits)
	if e != nil {
		t.Fatal(e)
	}
	draft := prepareHTTPSPublicationDraft(t, service, portal, path, append(encoded, '\n'))
	draftID := draft["id"].(string)
	previewPath := path + "/drafts/" + draftID + "/tokenizer-preview"
	adminPath += "/drafts/" + draftID + "/tokenizer-preview"
	input := map[string]any{"model_id": model, "sample_limit": 1, "maximum_tokens": 32}
	// Reuse the authenticated session token for the requests in this fixture.
	portal.Call("POST", previewPath, map[string]any{"model_id": model, "sample_limit": 9, "maximum_tokens": 32}, 400, "Problem")
	portal.Call("POST", previewPath, map[string]any{"model_id": model, "sample_limit": 1, "maximum_tokens": 31}, 400, "Problem")
	portal.Call("POST", previewPath, map[string]any{"model_id": foreignModel, "sample_limit": 1, "maximum_tokens": 32}, 404, "Problem")
	foreignPath := "/api/v1/workspaces/" + foreignWorkspace + "/datasets/" + draft["dataset_id"].(string) + "/drafts/" + draftID + "/tokenizer-preview"
	portal.Call("POST", foreignPath, input, 404, "Problem")
	t.Log("read a ready Qwen tokenizer through scoped HTTPS")
	result := portal.Call("POST", previewPath, input, 200, "")
	samples := result["samples"].([]any)
	if result["model_catalog_sha256"] != agentwire.ModelCatalogDigest() || result["maximum_tokens"] != float64(32) || len(samples) != 1 {
		t.Fatal("preview did not bind the requested model, budget and sample")
	}
	sample := samples[0].(map[string]any)
	digest, e := record.ContentDigest(service.Identity.Config.Policy().Datasets.Limits)
	if e != nil || sample["content_sha256"] != digest || sample["line_number"] != float64(1) ||
		sample["first_excess_token_index"] != float64(32) || sample["first_excess_section"] != "prompt" ||
		sample["completion"] != record.Completion || sample["total_tokens"] != sample["prompt_tokens"].(float64)+sample["completion_tokens"].(float64) {
		t.Fatal("preview lost sample identity or the correct overflow boundary", e)
	}
	input["maximum_tokens"] = sample["total_tokens"]
	fit := admin.Call("POST", adminPath, input, 200, "")["samples"].([]any)[0].(map[string]any)
	if fit["first_excess_token_index"] != nil || fit["first_excess_section"] != nil || fit["rendered_prompt"] != sample["rendered_prompt"] {
		t.Fatal("an exactly fitting sample was not reproduced")
	}
	input["maximum_tokens"] = 32
	file := filepath.Join(modelPath, "config.json")
	raw, e := os.ReadFile(file)
	if e != nil || len(raw) == 0 {
		t.Fatal("read local fixture", e)
	}
	if e = os.Remove(file); e != nil {
		t.Fatal(e)
	}
	if problem := portal.Call("POST", previewPath, input, 409, "Problem"); problem["code"] != "DATASET_FILE_INVALID" {
		t.Fatal("missing tokenizer asset was not rejected")
	}
	altered := bytes.Clone(raw)
	altered[0] ^= 1
	writePreviewModelFixture(t, file, altered)
	if problem := portal.Call("POST", previewPath, input, 409, "Problem"); problem["code"] != "DATASET_FILE_INVALID" {
		t.Fatal("same-size tokenizer tampering was not rejected")
	}

	if e = os.Remove(file); e != nil {
		t.Fatal(e)
	}
	writePreviewModelFixture(t, file, raw)
	portal.Call("POST", previewPath, input, 200, "")

	t.Log("verify the training-reserved token failure")
	reserved := record
	reserved.Completion = sample["eos"].(string)
	encoded, e = reserved.Canonical(service.Identity.Config.Policy().Datasets.Limits)
	if e != nil {
		t.Fatal(e)
	}
	reservedDraft := prepareHTTPSPublicationDraft(t, service, portal, path, append(encoded, '\n'))
	reservedPath := path + "/drafts/" + reservedDraft["id"].(string) + "/tokenizer-preview"
	if problem := portal.Call("POST", reservedPath, input, 422, "Problem"); problem["code"] != "DATASET_VALIDATION_FAILED" {
		t.Fatal("reserved training token was not refused by the real tokenizer")
	}

	t.Log("hold the real tokenizer before releasing prepared text")
	verifyPreviewRevocation(t, service, process, workspace, draft["dataset_id"].(string), draftID, model)
	portal.Call("POST", previewPath, input, 200, "")

	var oversized strings.Builder
	for _, id := range []string{"large-a", "large-b", "large-c"} {
		row := wire.Record{SourceID: id, Prompt: strings.Repeat("p", 12000), Completion: strings.Repeat("c", 12000)}
		line, e := row.Canonical(service.Identity.Config.Policy().Datasets.Limits)
		if e != nil {
			t.Fatal(e)
		}
		oversized.Write(line)
		oversized.WriteByte('\n')
	}
	big := prepareHTTPSPublicationDraft(t, service, portal, path, []byte(oversized.String()))
	input["sample_limit"] = 3
	bigPath := path + "/drafts/" + big["id"].(string) + "/tokenizer-preview"
	portal.Call("POST", bigPath, input, 413, "Problem")
	t.Log("real Qwen tokenizer preview validated over scoped HTTPS")
}

func verifyPreviewRevocation(t *testing.T, service *datasets.Service, process tokenizerPreviewProcessFixture, workspace, dataset, draft, model string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 35*time.Second)
	defer cancel()
	_, token, _, err := service.Identity.Login(ctx, "admin@example.invalid", process.password, "portal", "", uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	actor, err := service.Identity.Authenticate(ctx, token, "portal")
	if err != nil {
		t.Fatal(err)
	}
	gate := t.TempDir()
	launcher := filepath.Join(gate, "tokenizer")
	// exec replaces the launcher so the normal wall and RSS monitor still
	// observes the actual interpreter and its real tokenizer allocations.
	quote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'" }
	const holdAfterPreview = `
from pathlib import Path
import sys
import time

gate = Path(sys.argv[1])
assert sys.argv[2:5] == ["-I", "-B", "-c"]
program = sys.argv[5]
sys.argv = ["-c", *sys.argv[6:]]
try:
    exec(compile(program, "<preview>", "exec"), {"__name__": "__main__"})
except SystemExit as finished:
    if finished.code != 0:
        raise
else:
    raise RuntimeError("preview did not exit")
(gate / "prepared").touch()
while not (gate / "release").exists():
    time.sleep(0.01)
`
	command := "#!/bin/sh\nexec " + quote(process.configuration.PythonExecutable) +
		" -I -B -c " + quote(holdAfterPreview) + " " + quote(gate) + " \"$@\"\n"
	if err = os.WriteFile(launcher, []byte(command), 0700); err != nil {
		t.Fatal(err)
	}
	configuration := process.configuration
	configuration.PythonExecutable = launcher
	worker, err := datasets.NewTokenizerPreviewProcessor(service.Processor, process.models, configuration)
	if err != nil {
		t.Fatal(err)
	}
	input := datasets.TokenizerPreviewRequest{ModelID: model, SampleLimit: 1, MaximumTokens: 32}
	type attempt struct {
		out datasets.TokenizerPreviewResult
		err error
	}
	completed := make(chan attempt, 1)
	var request sync.WaitGroup
	request.Go(func() {
		out, err := service.PreviewDraftTokenizer(ctx, actor, workspace, dataset, draft, input, worker)
		completed <- attempt{out, err}
	})
	release := sync.OnceFunc(func() {
		if err := os.WriteFile(filepath.Join(gate, "release"), nil, 0600); err != nil {
			t.Error(err)
		}
	})
	defer func() {
		release()
		request.Wait()
	}()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if _, err = os.Stat(filepath.Join(gate, "prepared")); err == nil {
			break
		} else if !os.IsNotExist(err) {
			t.Fatal(err)
		}
		select {
		case early := <-completed:
			t.Fatal("real tokenizer exited before the authorization barrier", early.err)
		case <-ctx.Done():
			t.Fatal("real tokenizer did not reach the authorization barrier")
		case <-ticker.C:
		}
	}
	out, err := service.PreviewDraftTokenizer(ctx, actor, workspace, dataset, draft, input, worker)
	if err == nil || err.Error() != "RATE_LIMITED" || !reflect.DeepEqual(out, datasets.TokenizerPreviewResult{}) {
		t.Fatal("one live tokenizer did not consume the preview slot", err)
	}
	if err = service.Identity.Logout(ctx, token, "portal", uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	release()
	select {
	case denied := <-completed:
		if denied.err == nil || !strings.HasPrefix(denied.err.Error(), "AUTH_") ||
			!reflect.DeepEqual(denied.out, datasets.TokenizerPreviewResult{}) {
			t.Fatal("a revoked session received prepared samples", denied.err)
		}
	case <-ctx.Done():
		t.Fatal("revoked preview did not terminate")
	}
	t.Log("revocation after real tokenization and single preview slot verified")
}

func writePreviewModelFixture(t *testing.T, path string, raw []byte) {
	t.Helper()
	if e := os.WriteFile(path, raw, 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.Chmod(path, 0444); e != nil {
		t.Fatal(e)
	}
}
