//go:build offlineintegration

package scope

import (
	"bytes"
	"context"
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

func TestActualRestoredTLSPlanOnRetainedSelectedStoreTarget(t *testing.T) {
	retention := os.Getenv("PROJECT_ONE_TEST_RESTORED_TLS_RETENTION")
	if retention == "" {
		t.Skip("explicit isolated selected-store-qualified target required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	raw, e := bundle.ReadProtectedConfiguration(retention)
	var retained struct {
		ScopeID     string `json:"scope_id"`
		Config      string `json:"operator_config"`
		Qualified   bool   `json:"qualified_stores"`
		Observation Status `json:"status_observation"`
	}
	if e != nil || wire.UniqueJSON(raw, &retained) != nil || !retained.Qualified || retained.Observation.RestoreStores == nil || !wire.ValidSHA256(retained.Observation.RestoreStores.ProofSHA256) {
		t.Fatal("qualified private fixture required", e)
	}
	raw, e = bundle.ReadProtectedConfiguration(retained.Config)
	if e != nil {
		t.Fatal(e)
	}
	c, e := configuration.Load(raw, nil)
	if e != nil || !c.Verification.AllowTestFixture || c.Engine == nil || c.Compose == nil {
		t.Fatal("explicit isolated config", e)
	}
	out, binary := os.Getenv("PROJECT_ONE_TEST_RESTORED_TLS_EVIDENCE"), os.Getenv("PROJECT_ONE_TEST_RESTORED_TLS_OPERATOR")
	if !privateDirectory(out) || !canonical(binary) {
		t.Fatal("private external output/current operator required")
	}
	input := filepath.Join(out, "tls-inputs")
	if os.Mkdir(input, 0700) != nil {
		t.Fatal("fresh certificate input directory required")
	}
	cert, key, ca := restoredTLSCertificate(t)
	request := configuration.TLSGenerationRequest{FormatVersion: 1, CertificateFile: filepath.Join(input, "serving-chain.pem"), PrivateKeyFile: filepath.Join(input, "serving-key.pem"), TrustBundleFile: filepath.Join(input, "trust-bundle.pem"), Reason: "Isolated restored serving certificate replacement"}
	root, e := os.OpenRoot(input)
	if e != nil {
		t.Fatal(e)
	}
	for _, file := range []struct {
		name string
		raw  []byte
	}{{"serving-chain.pem", cert}, {"serving-key.pem", key}, {"trust-bundle.pem", ca}, {"request.json", encode(request)}} {
		if e = writeBackupFile(root, file.name, file.raw); e != nil {
			root.Close()
			t.Fatal(e)
		}
	}
	root.Close()
	l, e := open(c, false)
	if e != nil {
		t.Fatal(e)
	}
	beforeJournal, e := regular(l.root, journalName, c.MaximumJournalBytes)
	if e != nil {
		l.close()
		t.Fatal(e)
	}
	beforeHead, e := regular(l.root, headName, c.Verification.MaximumMetadataBytes)
	if e != nil {
		l.close()
		t.Fatal(e)
	}
	client, e := engine.New(*c.Engine)
	if e != nil {
		l.close()
		t.Fatal(e)
	}
	defer client.Close()
	worker, e := compose.New(c)
	if e != nil {
		l.close()
		t.Fatal(e)
	}
	verified, e := l.verifyRuntimeContext(ctx, client, worker, false)
	if e != nil {
		l.close()
		t.Fatal(e)
	}
	originalDescriptor := wire.Digest(encode(verified.inputs.descriptor))
	originalPayload := restoreConfigurationDigest(verified.inputs.files["control-config"])
	generation, e := nonce()
	if e != nil {
		l.close()
		t.Fatal(e)
	}
	plan, files, e := l.makeRestoredTLSPlan(ctx, request, generation, verified, time.Now())
	if e != nil {
		l.close()
		t.Fatal("actual proposed TLS plan", e)
	}
	again, againFiles, e := l.makeRestoredTLSPlan(ctx, request, generation, verified, time.Now())
	if e != nil || !reflect.DeepEqual(plan, again) || !reflect.DeepEqual(files, againFiles) {
		l.close()
		t.Fatal("nondeterministic TLS plan", e)
	}
	if plan.ScopeID != retained.ScopeID || plan.StoreProofSHA256 != retained.Observation.RestoreStores.ProofSHA256 || plan.PreviousPayloadSHA256 != originalPayload || plan.DescriptorSHA256 != originalDescriptor || plan.ConfigurationOwner.VolumeName() == plan.PreviousOwner.VolumeName() {
		l.close()
		t.Fatal("restored proof/generation binding")
	}
	for _, fault := range []string{"running-control", "running-PG", "changed-control-ID", "changed-descriptor", "old-restore-generation", "old-store-generation", "used-generation"} {
		t.Run(fault, func(t *testing.T) {
			v := verified
			v.observed.Services = map[string]compose.RuntimeServiceStatus{}
			for k, s := range verified.observed.Services {
				v.observed.Services[k] = s
			}
			gen := generation
			switch fault {
			case "running-control":
				s := v.observed.Services["control"]
				s.State = "running"
				v.observed.Services["control"] = s
			case "running-PG":
				s := v.observed.Services["postgres"]
				s.State = "running"
				v.observed.Services["postgres"] = s
			case "changed-control-ID":
				s := v.observed.Services["control"]
				s.ID = strings.Repeat("a", 64)
				v.observed.Services["control"] = s
			case "changed-descriptor":
				v.inputs.descriptor.ScopeID = strings.Repeat("a", 32)
			case "old-restore-generation":
				gen = plan.RestoreAttemptID
			case "old-store-generation":
				gen = restoreStoresState(l.events).record.GenerationID
			case "used-generation":
				gen = l.events[0].OperationID
			}
			if _, _, e := l.makeRestoredTLSPlan(ctx, request, gen, v, time.Now()); e == nil {
				t.Fatal("changed runtime/identity admitted")
			}
		})
	}
	l.close()
	command := exec.CommandContext(ctx, binary, "inspect-restored-tls", "--config", retained.Config, "--operation-file", filepath.Join(input, "request.json"))
	raw, e = command.CombinedOutput()
	var public RestoredTLSInspection
	if e != nil || wire.StrictJSON(raw, &public) != nil || public.Status != "validated_proposed_restored_TLS_generation_no_effects" || public.Plan.PayloadSHA256 != plan.PayloadSHA256 || !reflect.DeepEqual(public.Plan.Identity, plan.Identity) {
		t.Fatal("native protected TLS inspection", e, string(raw))
	}
	if bytes.Contains(raw, key) || bytes.Contains(raw, []byte("BEGIN PRIVATE KEY")) {
		t.Fatal("private payload exposed")
	}
	for _, file := range verified.inputs.files["control-config"] {
		if len(file.Contents) > 0 && bytes.Contains(raw, file.Contents) {
			t.Fatal("private original payload exposed in plan")
		}
	}
	l, e = open(c, false)
	if e != nil {
		t.Fatal(e)
	}
	defer l.close()
	afterJournal, e := regular(l.root, journalName, c.MaximumJournalBytes)
	if e != nil || !bytes.Equal(beforeJournal, afterJournal) {
		t.Fatal("inspection changed journal", e)
	}
	afterHead, e := regular(l.root, headName, c.Verification.MaximumMetadataBytes)
	if e != nil || !bytes.Equal(beforeHead, afterHead) {
		t.Fatal("inspection changed head", e)
	}
	after, e := l.verifyRuntimeContext(ctx, client, worker, false)
	if e != nil || originalDescriptor != wire.Digest(encode(after.inputs.descriptor)) || originalPayload != restoreConfigurationDigest(after.inputs.files["control-config"]) || !reflect.DeepEqual(verified.observed, after.observed) {
		t.Fatal("inspection changed immutable inputs or actual runtime", e)
	}
	output, e := os.OpenRoot(out)
	if e != nil {
		t.Fatal(e)
	}
	defer output.Close()
	if e = writeBackupFile(output, "inspection.json", raw); e != nil {
		t.Fatal(e)
	}
	if e = writeBackupFile(output, "actual.json", encode(map[string]any{"status": "passed_actual_readonly_restored_TLS_inspection", "scope_id": plan.ScopeID, "proposed_generation_id": public.Plan.GenerationID, "store_proof_sha256": plan.StoreProofSHA256, "payload_sha256": plan.PayloadSHA256, "certificate_identity": plan.Identity, "source_descriptor_payload_journal_head_and_stopped_runtime_unchanged": true, "request_file": filepath.Join(input, "request.json"), "operator_config": retained.Config, "native_operator": binary, "qualification": "readonly_plan_only_no_projection_replacement_activation_browser_or_target"})); e != nil {
		t.Fatal(e)
	}
}
