package scope

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/compose"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// Only the filesystem, PKI, production controllers and journals are real here.
// This explicit Engine double retains the full synthetic mounted bytes by owner;
// it does not establish Docker, process, SQL, browser or network qualification.
type selectedGenerationEngineDouble struct {
	*replacementEngineDouble
	contents map[string][]engine.ConfigurationFile
	mounted  map[string]string
	command  []string
	reads    int
}

func selectedGenerationClone(files []engine.ConfigurationFile) []engine.ConfigurationFile {
	result := make([]engine.ConfigurationFile, len(files))
	for index, file := range files {
		result[index] = engine.ConfigurationFile{Name: file.Name, Contents: bytes.Clone(file.Contents)}
	}
	return result
}

func selectedGenerationCopyInputs(input preparedInputs) map[string][]engine.ConfigurationFile {
	copy := map[string][]engine.ConfigurationFile{}
	for role, files := range input.files {
		if files == nil {
			copy[role] = nil
		} else {
			copy[role] = selectedGenerationClone(files)
		}
	}
	return copy
}

func (d *selectedGenerationEngineDouble) InspectRuntime(ctx context.Context, scope, name string) (engine.RuntimeContainer, bool, error) {
	object, found, e := d.replacementEngineDouble.InspectRuntime(ctx, scope, name)
	if e == nil && found && name == "control" {
		object.Config.Entrypoint = []string{"/usr/local/bin/control"}
		object.Config.Cmd = slices.Clone(d.command)
	}
	return object, found, e
}

func (d *selectedGenerationEngineDouble) PutConfiguration(ctx context.Context, owner engine.Ownership, image string, uid, gid int, files []engine.ConfigurationFile) error {
	e := d.tlsControllerDouble.PutConfiguration(ctx, owner, image, uid, gid, files)
	if d.payloads[owner.Role] {
		d.contents[owner.Role] = selectedGenerationClone(files)
	}
	return e
}

func (d *selectedGenerationEngineDouble) InspectConfiguration(ctx context.Context, owner engine.Ownership, image string, uid, gid int, files []engine.ConfigurationFile) (engine.ConfigurationObservation, error) {
	if d.payloads[owner.Role] && !reflect.DeepEqual(d.contents[owner.Role], files) {
		return engine.ConfigurationObservation{}, errors.New("SYNTHETIC_SELECTED_PAYLOAD_CHANGED")
	}
	return d.tlsControllerDouble.InspectConfiguration(ctx, owner, image, uid, gid, files)
}

func (d *selectedGenerationEngineDouble) InspectStoppedControlConfiguration(ctx context.Context, owner engine.Ownership, id, destination string, uid, gid int, files []engine.ConfigurationFile) (engine.ConfigurationObservation, error) {
	if d.mounted[id] != owner.Role || !reflect.DeepEqual(d.contents[owner.Role], files) {
		return engine.ConfigurationObservation{}, errors.New("SYNTHETIC_SELECTED_MOUNT_CHANGED")
	}
	return d.replacementEngineDouble.InspectStoppedControlConfiguration(ctx, owner, id, destination, uid, gid, files)
}

func (d *selectedGenerationEngineDouble) RemoveTLSGenerationVolume(ctx context.Context, owner engine.Ownership) error {
	e := d.tlsControllerDouble.RemoveTLSGenerationVolume(ctx, owner)
	if !d.volumes[owner.Role] {
		delete(d.contents, owner.Role)
	}
	return e
}

func (d *selectedGenerationEngineDouble) ReadCurrentControlConfiguration(ctx context.Context, owner engine.Ownership, id, destination string, uid, gid int, index []engine.ConfigurationFileDigest) ([]engine.ConfigurationFile, error) {
	d.reads++
	object, found, e := d.InspectRuntime(ctx, owner.ScopeID, "control")
	files, present := d.contents[owner.Role]
	if ctx.Err() != nil || e != nil || !found || !present || owner.ScopeID != d.scope || object.ID != id || d.mounted[id] != owner.Role || !d.generated(owner) || destination != engine.DatabaseTaskDestination || uid != 10033 || gid != 10034 || len(index) != len(files) || object.State.Status != "created" || !d.volumes[owner.Role] || d.utilities[owner.Role] {
		return nil, errors.New("SYNTHETIC_SELECTED_READBACK_CONTEXT_CHANGED")
	}
	for position, file := range files {
		if index[position].Name != file.Name || index[position].Bytes != int64(len(file.Contents)) || index[position].SHA256 != wire.Digest(file.Contents) {
			return nil, errors.New("SYNTHETIC_SELECTED_READBACK_INDEX_CHANGED")
		}
	}
	return selectedGenerationClone(files), nil
}

type selectedGenerationWorkerDouble struct {
	*replacementWorkerDouble
	selected *selectedGenerationEngineDouble
}

func (w *selectedGenerationWorkerDouble) CreateReplacementControl(ctx context.Context, reader compose.RuntimeReader, scope compose.RenderContext, model compose.Model, before compose.RuntimeStatus) (compose.RuntimeStatus, error) {
	result, e := w.replacementWorkerDouble.CreateReplacementControl(ctx, reader, scope, model, before)
	if control, found := result.Services["control"]; found && control.ID == w.selected.newID {
		for _, mount := range model.Services["control"].Mounts {
			if strings.HasPrefix(mount.Source, engine.TLSConfigurationRole+"-") {
				w.selected.mounted[control.ID] = mount.Source
			}
		}
	}
	return result, e
}

func selectedGenerationFixture(t *testing.T) (configuration.Config, configuration.TLSGenerationRequest, *selectedGenerationEngineDouble, *selectedGenerationWorkerDouble) {
	t.Helper()
	now := time.Now()
	machineCertificate, machineKey, _ := selectedTLSCertificate(t, []string{"machine.example.test"}, now, 71)
	l, first, base, worker := tlsProjectionPrivatePlanRuntimeConfiguredFixture(t, func(input *preparedInputs) {
		document := selectedTLSDocument(t, *input)
		server := document["server"].(map[string]any)
		server["listen_address"], server["transport_mode"] = "0.0.0.0:8443", "direct_https"
		prefix := engine.DatabaseTaskDestination + "/private/"
		document["machine_server"] = map[string]any{"listen_address": "0.0.0.0:9443", "origin": "https://machine.example.test:19443", "tls_certificate_file": prefix + "machine-certificate.pem", "tls_private_key_file": prefix + "machine-private-key.pem"}
		selectedTLSSetRuntime(t, input, document)
		input.files["control-config"] = append(input.files["control-config"], engine.ConfigurationFile{Name: "machine-certificate.pem", Contents: machineCertificate}, engine.ConfigurationFile{Name: "machine-private-key.pem", Contents: machineKey})
		runtime, e := restoreNamedFile(input.files["control-config"], "runtime.yaml")
		if e != nil {
			t.Fatal(e)
		}
		for index := range input.files["operator-config"] {
			if input.files["operator-config"][index].Name == "runtime.yaml" {
				input.files["operator-config"][index].Contents = bytes.Clone(runtime)
			}
		}
		// Exercise replacement of existing operator copies, not only unioning
		// names that happen to be absent from the operator's original snapshot.
		for _, file := range input.files["control-config"] {
			if file.Name == "certificate.pem" || file.Name == "private-key.pem" || file.Name == "machine-certificate.pem" || file.Name == "machine-private-key.pem" {
				input.files["operator-config"] = append(input.files["operator-config"], engine.ConfigurationFile{Name: file.Name, Contents: bytes.Clone(file.Contents)})
			}
		}
	}, func(model *compose.Model) {
		control := model.Services["control"]
		control.Command = []string{"serve", "--config", engine.DatabaseTaskDestination + "/private/runtime.yaml"}
		model.Services["control"] = control
	})
	c, scope := l.config, l.marker.ScopeID
	l.close()
	postgres := worker.state.Services["postgres"]
	postgres.State, postgres.ExitCode = "exited", 0
	worker.state.Services["postgres"] = postgres
	tls := &tlsControllerDouble{databaseEngineDouble: base, volumes: map[string]bool{}, utilities: map[string]bool{}, payloads: map[string]bool{}}
	replacement := &replacementEngineDouble{tlsControllerDouble: tls, scope: scope, newID: strings.Repeat("f", 64)}
	d := &selectedGenerationEngineDouble{replacementEngineDouble: replacement, contents: map[string][]engine.ConfigurationFile{}, mounted: map[string]string{}, command: []string{"serve", "--config", engine.DatabaseTaskDestination + "/private/runtime.yaml"}}
	w := &selectedGenerationWorkerDouble{replacementWorkerDouble: &replacementWorkerDouble{databaseWorkerDouble: worker, engine: replacement}, selected: d}
	if out, e := runTLSProjection(context.Background(), c, first.Request, "prepare-restored-tls", d, w); e != nil || out.Pending {
		t.Fatal("synthetic V1 predecessor preparation", e)
	}
	if out, e := replaceRestoredControl(context.Background(), c, d, w, false); e != nil || out.Pending {
		t.Fatal("synthetic V1 predecessor replacement", e)
	}
	return c, first.Request, d, w
}

func selectedGenerationRequest(t *testing.T, target string, at time.Time) configuration.TLSGenerationRequest {
	t.Helper()
	hosts := []string{"portal.example.test", "admin.example.test"}
	if target == "machine" {
		hosts = []string{"machine.example.test"}
	}
	certificate, key, trust := selectedTLSCertificate(t, hosts, at, 81)
	directory := t.TempDir()
	if e := os.Chmod(directory, 0700); e != nil {
		t.Fatal(e)
	}
	request := configuration.TLSGenerationRequest{FormatVersion: 2, Target: target, CertificateFile: filepath.Join(directory, "chain.pem"), PrivateKeyFile: filepath.Join(directory, "key.pem"), TrustBundleFile: filepath.Join(directory, "trust.pem"), Reason: "Synthetic selected generation recovery control"}
	for name, raw := range map[string][]byte{request.CertificateFile: certificate, request.PrivateKeyFile: key, request.TrustBundleFile: trust} {
		if e := os.WriteFile(name, raw, 0600); e != nil {
			t.Fatal(e)
		}
	}
	return request
}

func selectedGenerationDeleteSources(t *testing.T, request configuration.TLSGenerationRequest) {
	t.Helper()
	for _, name := range []string{request.CertificateFile, request.PrivateKeyFile, request.TrustBundleFile} {
		if e := os.Remove(name); e != nil {
			t.Fatal("remove owned synthetic source", e)
		}
	}
}

func selectedGenerationEffects(d *selectedGenerationEngineDouble, w *selectedGenerationWorkerDouble) []int {
	return []int{d.creates, d.utilityCreates, d.puts, d.utilityRemoves, d.volumeRemoves, d.removed, w.creates, w.starts, w.databaseStarts}
}

func selectedGenerationPlan(t *testing.T, c configuration.Config) RestoredTLSPlan {
	t.Helper()
	l, e := open(c, false)
	if e != nil {
		t.Fatal(e)
	}
	defer l.close()
	s := tlsProjectionState(l.events)
	if s.record == nil {
		t.Fatal("recorded TLS generation required")
	}
	p, e := l.readTLSProjectionPlan(*s.record)
	if e != nil {
		t.Fatal(e)
	}
	return p
}

func selectedGenerationStage(t *testing.T, c configuration.Config, d *selectedGenerationEngineDouble, w *selectedGenerationWorkerDouble, request configuration.TLSGenerationRequest, at time.Time) (*locked, RestoredTLSPlan, []engine.ConfigurationFile, []byte) {
	t.Helper()
	l, e := open(c, true)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(l.close)
	generation, e := nonce()
	if e != nil {
		l.close()
		t.Fatal(e)
	}
	verified, e := l.verifyRuntimeContext(context.Background(), d, w, false)
	if e == nil {
		verified.previousTLSFiles, e = l.readSelectedTLSPredecessor(context.Background(), d, generation, request.Target, verified)
	}
	if e != nil {
		l.close()
		t.Fatal("synthetic predecessor readback", e)
	}
	p, files, e := l.makeRestoredTLSPlan(context.Background(), request, generation, verified, at)
	if e != nil {
		l.close()
		t.Fatal(e)
	}
	trust, e := protectedInput(c, request.TrustBundleFile, c.Verification.MaximumMetadataBytes)
	if e != nil {
		l.close()
		t.Fatal(e)
	}
	return l, p, files, trust
}

func selectedGenerationAssertMerge(t *testing.T, l *locked, p RestoredTLSPlan, current []engine.ConfigurationFile) {
	t.Helper()
	ctx := context.Background()
	original, _, e := l.replacementModels(ctx)
	if e != nil {
		t.Fatal(e)
	}
	before := selectedGenerationCopyInputs(original)
	restore, e := l.readBoundRestorePlan()
	if e != nil {
		t.Fatal(e)
	}
	_, source, e := l.makeRestoreDatabaseRecord(ctx, restore)
	if e != nil {
		t.Fatal(e)
	}
	raw, e := restoreNamedFile(source, restoredRuntimeContextName)
	var restored wire.RestoredRuntimeContext
	if e != nil || wire.StrictJSON(raw, &restored) != nil {
		t.Fatal("original restore context")
	}
	merged, e := mergeCurrentRestoredLiveInput(original, p, current, p.PreviousPayloadSHA256, restored, l.config.Verification.MaximumMetadataBytes)
	if e != nil {
		t.Fatal("full current operator configuration merge", e)
	}
	if len(p.ConfigurationFiles) != len(current) {
		t.Fatal("full current runtime index length changed")
	}
	currentNames := map[string]bool{}
	for index, file := range current {
		currentNames[file.Name] = true
		actual, e := restoreNamedFile(merged, file.Name)
		if e != nil || !bytes.Equal(actual, file.Contents) || p.ConfigurationFiles[index] != (InputFile{Name: file.Name, Bytes: int64(len(actual)), SHA256: wire.Digest(actual)}) {
			t.Fatal("current runtime/operator file or index diverged")
		}
	}
	for _, file := range original.files["operator-config"] {
		if !currentNames[file.Name] {
			actual, e := restoreNamedFile(merged, file.Name)
			if e != nil || !bytes.Equal(actual, file.Contents) {
				t.Fatal("unrelated original operator input changed")
			}
		}
	}
	actualContext, e := restoreNamedFile(merged, restoredRuntimeContextName)
	if e != nil || !bytes.Equal(actualContext, raw) {
		t.Fatal("original restore context changed")
	}
	if !reflect.DeepEqual(before, original.files) {
		t.Fatal("operator merge changed immutable original inputs")
	}
}

func TestSelectedTLSGenerationsPreserveMachineBrowserMachineInheritance(t *testing.T) {
	ctx := context.Background()
	c, firstRequest, d, w := selectedGenerationFixture(t)
	previous := selectedGenerationPlan(t, c)
	selectedGenerationDeleteSources(t, firstRequest)
	l, e := open(c, false)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(l.close)
	originalRuntime, originalDescriptor := encode(*runtimeState(l.events).record), previous.DescriptorSHA256
	firstPlan, e := regular(l.root, tlsProjectionPlanFile(previous.GenerationID), c.Verification.MaximumMetadataBytes)
	if e != nil {
		l.close()
		t.Fatal(e)
	}
	l.close()
	for index, target := range []string{"machine", "browser", "machine"} {
		request := selectedGenerationRequest(t, target, time.Now())
		if out, e := runTLSProjection(ctx, c, request, "rotate-restored-tls", d, w); e != nil || out.Pending {
			t.Fatal("selected rotation", target, e)
		}
		selectedGenerationDeleteSources(t, request)
		plan := selectedGenerationPlan(t, c)
		if plan.FormatVersion != 2 || plan.Request.Target != target || plan.PreviousPayloadSHA256 != previous.PayloadSHA256 || plan.DescriptorSHA256 != originalDescriptor {
			t.Fatal("generation lineage changed")
		}
		if len(plan.ConfigurationFiles) != len(previous.ConfigurationFiles) {
			t.Fatal("cross-generation inventory length changed")
		}
		for position, file := range plan.ConfigurationFiles {
			if file.Name != plan.CertificateName && file.Name != plan.PrivateKeyName && file != previous.ConfigurationFiles[position] {
				t.Fatal("earlier unselected TLS index reverted")
			}
		}
		d.newID = wire.Digest([]byte("synthetic selected control " + target + strconv.Itoa(index)))
		if out, e := replaceRestoredControl(ctx, c, d, w, false); e != nil || out.Pending {
			t.Fatal("source-independent selected replacement", e)
		}
		l, e = open(c, false)
		if e != nil {
			t.Fatal("durable selected generation reopen", e)
		}
		t.Cleanup(l.close)
		files, _, e := l.readGenerationPayload(ctx, plan)
		if e != nil || restoreConfigurationDigest(files) != plan.PayloadSHA256 || !reflect.DeepEqual(files, d.contents[plan.ConfigurationOwner.Role]) || !bytes.Equal(originalRuntime, encode(*runtimeState(l.events).record)) {
			l.close()
			t.Fatal("custody/runtime bytes or historical runtime changed", e)
		}
		selectedGenerationAssertMerge(t, l, plan, files)
		retained, e := regular(l.root, tlsProjectionPlanFile(previous.GenerationID), c.Verification.MaximumMetadataBytes)
		if e != nil || len(retained) == 0 {
			l.close()
			t.Fatal("previous plan lost", e)
		}
		original, e := regular(l.root, tlsProjectionPlanFile(selectedGenerationFirst(t, l).GenerationID), c.Verification.MaximumMetadataBytes)
		if e != nil || !bytes.Equal(original, firstPlan) || effectiveRuntimeState(l.events).record.Services["control"].ID != d.newID {
			l.close()
			t.Fatal("original plan or effective identity changed", e)
		}
		l.close()
		previous = plan
	}
	if d.reads != 3 || d.removed != 4 || w.creates != 4 || len(d.volumes) != 4 {
		t.Fatal("unexpected predecessor reads or physical effect counts")
	}
}

func selectedGenerationFirst(t *testing.T, l *locked) RestoredTLSPlan {
	t.Helper()
	for _, event := range l.events {
		if event.TLSProjection != nil {
			p, e := l.readTLSProjectionPlan(*event.TLSProjection)
			if e != nil {
				t.Fatal(e)
			}
			return p
		}
	}
	t.Fatal("first TLS generation missing")
	return RestoredTLSPlan{}
}

func TestSelectedTLSReplacementRecoversMissingControlWithoutSourceCredentials(t *testing.T) {
	for _, fault := range []string{"remove-after", "create-after"} {
		t.Run(fault, func(t *testing.T) {
			ctx := context.Background()
			c, firstRequest, d, w := selectedGenerationFixture(t)
			selectedGenerationDeleteSources(t, firstRequest)
			request := selectedGenerationRequest(t, "machine", time.Now())
			if _, e := runTLSProjection(ctx, c, request, "rotate-restored-tls", d, w); e != nil {
				t.Fatal(e)
			}
			selectedGenerationDeleteSources(t, request)
			d.newID, d.fault, d.fired = strings.Repeat("e", 64), fault, false
			removed, created := d.removed, w.creates
			if out, e := replaceRestoredControl(ctx, c, d, w, false); e == nil || !out.Pending {
				t.Fatal("lost replacement result accepted")
			}
			if _, present := w.state.Services["control"]; fault == "remove-after" && present {
				t.Fatal("missing-control recovery window not exercised")
			}
			unknown := selectedGenerationEffects(d, w)
			if _, e := replaceRestoredControl(ctx, c, d, w, false); e == nil || !reflect.DeepEqual(unknown, selectedGenerationEffects(d, w)) {
				t.Fatal("unknown replacement resent")
			}
			if out, e := replaceRestoredControl(ctx, c, d, w, true); e != nil || out.Pending || d.removed != removed+1 || w.creates != created+1 || w.state.Services["control"].ID != d.newID {
				t.Fatal("explicit replacement observation/recovery", e)
			}
			before, e := os.ReadFile(filepath.Join(c.ScopeRoot, journalName))
			if e != nil {
				t.Fatal(e)
			}
			completed := selectedGenerationEffects(d, w)
			if _, e = replaceRestoredControl(ctx, c, d, w, false); e != nil {
				t.Fatal(e)
			}
			after, e := os.ReadFile(filepath.Join(c.ScopeRoot, journalName))
			if e != nil || !bytes.Equal(before, after) || !reflect.DeepEqual(completed, selectedGenerationEffects(d, w)) {
				t.Fatal("terminal replacement replayed", e)
			}
			if _, e = runTLSProjection(ctx, c, request, "abort-restored-tls", d, w); e == nil {
				t.Fatal("consumed custody generation aborted")
			}
		})
	}
}

func selectedGenerationLostPublication(t *testing.T, l *locked, p RestoredTLSPlan, files []engine.ConfigurationFile, trust []byte, variant string) {
	t.Helper()
	e := l.publishTLSProjectionPlanWithCustody(context.Background(), p, false, func(p RestoredTLSPlan) error {
		if variant == "absent_plan" {
			return errors.New("SYNTHETIC_PUBLICATION_RESPONSE_LOST")
		}
		if variant == "partial_plan" {
			if e := writeNew(l.root, tlsProjectionPlanFile(p.GenerationID), []byte("{")); e != nil {
				return e
			}
			return errors.New("SYNTHETIC_PUBLICATION_RESPONSE_LOST")
		}
		if e := writeNew(l.root, tlsProjectionPlanFile(p.GenerationID), encode(p)); e != nil {
			return e
		}
		if variant == "absent_payload" {
			return errors.New("SYNTHETIC_PUBLICATION_RESPONSE_LOST")
		}
		if e := l.writeGenerationPayload(context.Background(), p, files, trust); e != nil {
			return e
		}
		if variant == "partial_payload" {
			if e := l.root.Remove(filepath.Join(generationPayloadDirectory(p.GenerationID), generationPayloadConfiguration, p.PrivateKeyName)); e != nil {
				return e
			}
		}
		if variant == "unknown_payload" {
			if e := writeNew(l.root, filepath.Join(generationPayloadDirectory(p.GenerationID), "unknown.synthetic"), []byte("Synthetic owned fault injection")); e != nil {
				return e
			}
		}
		return errors.New("SYNTHETIC_PUBLICATION_RESPONSE_LOST")
	}, files, trust)
	if e == nil || tlsProjectionState(l.events).pending == "" {
		t.Fatal("lost publication was not retained as uncertain")
	}
}

func TestSelectedTLSCustodyPublicationRecoveryAndAbort(t *testing.T) {
	for _, variant := range []string{"complete_lost", "absent_plan", "partial_plan", "absent_payload", "partial_payload", "unknown_payload"} {
		t.Run(variant, func(t *testing.T) {
			ctx := context.Background()
			c, _, d, w := selectedGenerationFixture(t)
			request := selectedGenerationRequest(t, "machine", time.Now())
			l, plan, files, trust := selectedGenerationStage(t, c, d, w, request, time.Now())
			selectedGenerationLostPublication(t, l, plan, files, trust, variant)
			payload := filepath.Join(c.ScopeRoot, generationPayloadDirectory(plan.GenerationID))
			var before os.FileInfo
			if variant == "complete_lost" {
				var e error
				before, e = os.Lstat(filepath.Join(payload, generationPayloadConfiguration, plan.PrivateKeyName))
				if e != nil {
					l.close()
					t.Fatal(e)
				}
			}
			l.close()
			selectedGenerationDeleteSources(t, request)
			effects := selectedGenerationEffects(d, w)
			out, e := runTLSProjection(ctx, c, request, "reconcile-restored-tls", d, w)
			if variant == "complete_lost" {
				if e != nil || out.Pending || d.creates != effects[0]+1 || d.puts != effects[2]+1 {
					t.Fatal("complete lost publication did not resume from custody", e)
				}
				after, e := os.Lstat(filepath.Join(payload, generationPayloadConfiguration, plan.PrivateKeyName))
				if e != nil || !samePrivateSnapshot(before, after, c.Verification.MaximumMetadataBytes) {
					t.Fatal("complete custody bytes rewritten", e)
				}
			} else if e == nil || !out.Pending || !reflect.DeepEqual(effects, selectedGenerationEffects(d, w)) {
				t.Fatal("absent/partial/unknown publication replayed or accepted")
			}
			aborted, e := runTLSProjection(ctx, c, request, "abort-restored-tls", d, w)
			if variant == "unknown_payload" {
				if e == nil || !aborted.Pending {
					t.Fatal("unknown raw content deleted or cleanup debt hidden")
				}
				unknown := filepath.Join(payload, "unknown.synthetic")
				if raw, re := os.ReadFile(unknown); re != nil || string(raw) != "Synthetic owned fault injection" {
					t.Fatal("unknown content not preserved")
				}
				// Remove only this exact test-owned injected object; product cleanup
				// must have refused it and retained all generation credentials.
				if e = os.Remove(unknown); e != nil {
					t.Fatal(e)
				}
				aborted, e = runTLSProjection(ctx, c, request, "abort-restored-tls", d, w)
			}
			if e != nil || aborted.Pending || aborted.Status != "TLS_projection_aborted_original_configuration_preserved" {
				t.Fatal("precise source-independent abort", e)
			}
			if _, e = os.Lstat(payload); !os.IsNotExist(e) || len(d.volumes) != 1 || d.removed != 1 || w.creates != 1 {
				t.Fatal("generated custody or original runtime cleanup boundary changed", e)
			}
		})
	}
}

func TestSelectedTLSMetadataExpiryAndActivationBoundary(t *testing.T) {
	ctx := context.Background()
	c, firstRequest, d, w := selectedGenerationFixture(t)
	selectedGenerationDeleteSources(t, firstRequest)
	admittedAt := time.Now().Add(-72 * time.Hour)
	request := selectedGenerationRequest(t, "machine", admittedAt)
	l, plan, files, trust := selectedGenerationStage(t, c, d, w, request, admittedAt)
	selectedGenerationLostPublication(t, l, plan, files, trust, "complete_lost")
	l.close()
	selectedGenerationDeleteSources(t, request)
	effects := selectedGenerationEffects(d, w)
	if _, e := Read(ctx, c); e != nil {
		t.Fatal("expired metadata status unavailable", e)
	}
	// Reconcile observes the durable publication, then the first genuinely new
	// volume effect must refuse today's expired PKI without an Engine mutation.
	if out, e := runTLSProjection(ctx, c, request, "reconcile-restored-tls", d, w); e == nil || !out.Pending || e.Error() != "OFFLINE_TLS_SERVING_IDENTITY_INVALID" || !reflect.DeepEqual(effects, selectedGenerationEffects(d, w)) {
		t.Fatal("expired custody authorized a new effect", e)
	}
	l, e := open(c, false)
	if e != nil {
		t.Fatal(e)
	}
	state := tlsProjectionState(l.events)
	if _, completed := state.completed[0]; !completed || state.pending != "" {
		l.close()
		t.Fatal("expired publication could not be independently reconciled")
	}
	metadata, e := l.readTLSProjectionPlan(*state.record)
	if e != nil || !reflect.DeepEqual(metadata, plan) {
		l.close()
		t.Fatal("expired immutable plan unavailable", e)
	}
	actual, actualTrust, e := l.readGenerationPayload(ctx, plan)
	if e != nil || !reflect.DeepEqual(actual, files) || !bytes.Equal(actualTrust, trust) {
		l.close()
		t.Fatal("expired custody unavailable", e)
	}
	if e = l.admitSelectedTLSCurrent(ctx, plan, time.Now()); e == nil || e.Error() != "OFFLINE_TLS_SERVING_IDENTITY_INVALID" {
		l.close()
		t.Fatal("current activation PKI admission accepted an expired generation", e)
	}
	l.close()
	if out, e := runTLSProjection(ctx, c, request, "abort-restored-tls", d, w); e != nil || out.Pending || !reflect.DeepEqual(effects, selectedGenerationEffects(d, w)) {
		t.Fatal("expired source-independent custody cleanup", e)
	}
	if _, e := Read(ctx, c); e != nil {
		t.Fatal("expired cleanup facts unavailable", e)
	}
}
