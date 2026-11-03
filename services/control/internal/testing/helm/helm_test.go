//go:build helm_integration

package helm_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"go.yaml.in/yaml/v3"
)

func repository(t *testing.T) string {
	t.Helper()
	root, e := filepath.Abs("../../../../../")
	if e != nil {
		t.Fatal(e)
	}
	return root
}
func values(t *testing.T) map[string]any {
	t.Helper()
	raw, e := os.ReadFile(filepath.Join(repository(t), "deploy/helm/projectone/values.yaml"))
	if e != nil {
		t.Fatal(e)
	}
	var v map[string]any
	if yaml.Unmarshal(raw, &v) != nil {
		t.Fatal("values")
	}
	set(v, "image.reference", "projectone/control@sha256:"+strings.Repeat("a", 64))
	set(v, "configuration.existingConfigMap", "synthetic-runtime-config")
	set(v, "secrets.existingSecret", "synthetic-runtime-secret")
	set(v, "probes.host", "admin.example.invalid")
	for _, name := range []string{"models", "datasets", "training", "preparations"} {
		set(v, "storage."+name+".existingClaim", "synthetic-"+name)
	}
	return v
}
func set(v map[string]any, path string, value any) {
	parts := strings.Split(path, ".")
	for _, key := range parts[:len(parts)-1] {
		v = v[key].(map[string]any)
	}
	v[parts[len(parts)-1]] = value
}
func at(t *testing.T, v any, path ...string) any {
	t.Helper()
	for _, key := range path {
		m, ok := v.(map[string]any)
		if !ok {
			t.Fatal("expected object", key)
		}
		v = m[key]
	}
	return v
}
func object(t *testing.T, v any) map[string]any {
	t.Helper()
	out, ok := v.(map[string]any)
	if !ok {
		t.Fatal("expected object")
	}
	return out
}
func array(t *testing.T, v any) []any {
	t.Helper()
	out, ok := v.([]any)
	if !ok {
		t.Fatal("expected array")
	}
	return out
}
func run(t *testing.T, v map[string]any, success bool, operation string) string {
	t.Helper()
	dir := t.TempDir()
	raw, _ := json.Marshal(v)
	input := filepath.Join(dir, "values.json")
	if os.WriteFile(input, raw, 0600) != nil {
		t.Fatal("values fixture")
	}
	binary := os.Getenv("PROJECT_ONE_HELM_BINARY")
	if binary == "" {
		t.Fatal("explicit checksum-verified native Helm required")
	}
	chart := filepath.Join(repository(t), "deploy/helm/projectone")
	args := []string{operation}
	if operation == "template" {
		args = append(args, "projectone-test", chart, "--namespace", "projectone-test-local", "--kube-version", "1.36.0", "--values", input)
	} else {
		args = append(args, chart, "--strict", "--kube-version", "1.36.0", "--values", input)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, binary, args...)
	raw, e := command.CombinedOutput()
	if (e == nil) != success {
		t.Fatalf("Helm %s expected success=%v: %v %s", operation, success, e, raw)
	}
	if !success && !strings.Contains(string(raw), "values don't meet the specifications") && !strings.Contains(string(raw), "execution error at") {
		t.Fatal("negative fixture failed before actual chart validation", string(raw))
	}
	return string(raw)
}
func documents(t *testing.T, raw string) map[string]map[string]any {
	t.Helper()
	out := map[string]map[string]any{}
	decoder := yaml.NewDecoder(strings.NewReader(raw))
	for {
		var value map[string]any
		e := decoder.Decode(&value)
		if e == io.EOF {
			break
		}
		if e != nil {
			t.Fatal("rendered YAML", e)
		}
		if value == nil {
			continue
		}
		kind, ok := value["kind"].(string)
		if !ok {
			t.Fatal("missing kind")
		}
		if out[kind] != nil {
			t.Fatal("duplicate kind", kind)
		}
		out[kind] = value
	}
	return out
}
func validateOfficial(t *testing.T, docs map[string]map[string]any) {
	t.Helper()
	raw, e := os.ReadFile(os.Getenv("PROJECT_ONE_KUBERNETES_OPENAPI"))
	if e != nil {
		t.Fatal("official pinned Kubernetes source schema required", e)
	}
	pin, e := os.ReadFile(filepath.Join(repository(t), "tools/dev/toolchain/kubernetes-openapi.json"))
	if e != nil {
		t.Fatal(e)
	}
	var metadata struct {
		SHA256 string `json:"sha256"`
	}
	json.Unmarshal(pin, &metadata)
	sum := sha256.Sum256(raw)
	if hex.EncodeToString(sum[:]) != metadata.SHA256 {
		t.Fatal("official schema drift")
	}
	var source any
	if json.Unmarshal(raw, &source) != nil {
		t.Fatal("schema JSON")
	}
	compiler := jsonschema.NewCompiler()
	url := "https://projectone.invalid/kubernetes-v1.36.0"
	if e = compiler.AddResource(url, source); e != nil {
		t.Fatal(e)
	}
	names := map[string]string{"ConfigMap": "core", "Service": "core", "Deployment": "apps", "Job": "batch"}
	for kind, value := range docs {
		schema, e := compiler.Compile(url + "#/definitions/io.k8s.api." + names[kind] + ".v1." + kind)
		if e != nil {
			t.Fatal("official schema compile", kind, e)
		}
		encoded, e := json.Marshal(value)
		if e != nil {
			t.Fatal(e)
		}
		var normalized any
		json.Unmarshal(encoded, &normalized)
		if e = schema.Validate(normalized); e != nil {
			t.Fatal("official API structural validation", kind, e)
		}
	}
}
func TestActualHelmServeLintPackageAndOfficialKubernetesSchema(t *testing.T) {
	v := values(t)
	raw := run(t, v, true, "template")
	docs := documents(t, raw)
	validateOfficial(t, docs)
	if len(docs) != 3 || docs["Job"] != nil || at(t, docs["Deployment"], "spec", "replicas") != 1 || at(t, docs["Deployment"], "spec", "strategy", "type") != "Recreate" || at(t, docs["Service"], "spec", "type") != "ClusterIP" {
		t.Fatal("single-owner/internal-only deployment drift")
	}
	pod := object(t, at(t, docs["Deployment"], "spec", "template", "spec"))
	if pod["restartPolicy"] != "Always" || pod["automountServiceAccountToken"] != false || at(t, pod, "nodeSelector", "kubernetes.io/arch") != "amd64" || at(t, pod, "securityContext", "runAsNonRoot") != true {
		t.Fatal("runtime identity drift")
	}
	init := object(t, array(t, pod["initContainers"])[0])
	if len(array(t, init["volumeMounts"])) != 4 {
		t.Fatal("projection init acquired live storage")
	}
	runtime := object(t, array(t, pod["containers"])[0])
	if at(t, runtime, "securityContext", "readOnlyRootFilesystem") != true || len(array(t, pod["volumes"])) != 9 {
		t.Fatal("runtime storage/security drift")
	}
	run(t, v, true, "lint")
	output := os.Getenv("PROJECT_ONE_EVIDENCE_ROOT")
	if output == "" {
		t.Fatal("external evidence root required")
	}
	directory := filepath.Join(output, "chart-package")
	if os.Mkdir(directory, 0700) != nil {
		t.Fatal("new package directory")
	}
	command := exec.Command(os.Getenv("PROJECT_ONE_HELM_BINARY"), "package", filepath.Join(repository(t), "deploy/helm/projectone"), "--destination", directory)
	if raw, e := command.CombinedOutput(); e != nil {
		t.Fatal("actual chart package", e, string(raw))
	}
	metadataRaw, e := os.ReadFile(filepath.Join(repository(t), "deploy/helm/projectone/Chart.yaml"))
	if e != nil {
		t.Fatal(e)
	}
	var metadata struct {
		Name    string `yaml:"name"`
		Version string `yaml:"version"`
	}
	if yaml.Unmarshal(metadataRaw, &metadata) != nil || metadata.Name != "projectone" || metadata.Version == "" || filepath.Base(metadata.Version) != metadata.Version {
		t.Fatal("current chart metadata required")
	}
	archive := filepath.Join(directory, metadata.Name+"-"+metadata.Version+".tgz")
	data, e := os.ReadFile(archive)
	if e != nil || len(data) == 0 {
		t.Fatal("actual package missing", e)
	}
	sum := sha256.Sum256(data)
	record, _ := json.Marshal(map[string]any{"status": "actual_chart_package_only", "sha256": hex.EncodeToString(sum[:]), "size_bytes": len(data), "scope": "No installed cluster, image import, CSI, operator execution or complete offline platform bundle."})
	if os.WriteFile(filepath.Join(directory, "manifest.json"), record, 0600) != nil {
		t.Fatal("package evidence")
	}
	if os.WriteFile(filepath.Join(output, "serve-render.yaml"), []byte(raw), 0600) != nil {
		t.Fatal("render evidence")
	}
}
func operatorValues(t *testing.T, command string) map[string]any {
	v := values(t)
	set(v, "mode", "operator")
	set(v, "operator.acknowledgeNamespace", "projectone-test-local")
	set(v, "operator.jobID", "synthetic1")
	set(v, "operator.existingConfigMap", "synthetic-operator-config")
	set(v, "operator.existingSecret", "synthetic-operator-secret")
	set(v, "operator.command", command)
	return v
}
func TestActualHelmOperatorModesHaveNoAutomaticMigrationOrLiveSecretAuthority(t *testing.T) {
	for _, command := range []string{"migrate", "initialize-preparation-store", "bootstrap-admin", "export-model-files"} {
		t.Run(command, func(t *testing.T) {
			v := operatorValues(t, command)
			if command == "bootstrap-admin" {
				set(v, "operator.interactive", true)
			}
			if command == "export-model-files" {
				set(v, "operator.snapshot.existingClaim", "synthetic-snapshots")
			}
			docs := documents(t, run(t, v, true, "template"))
			validateOfficial(t, docs)
			if at(t, docs["Deployment"], "spec", "replicas") != 0 || at(t, docs["Deployment"], "spec", "template", "spec", "restartPolicy") != "Always" {
				t.Fatal("operator mode did not safely scale down valid resident Deployment")
			}
			job := object(t, at(t, docs["Job"], "spec"))
			if job["backoffLimit"] != 0 {
				t.Fatal("operator automatically retried")
			}
			pod := object(t, at(t, job, "template", "spec"))
			if pod["restartPolicy"] != "Never" {
				t.Fatal("operator pod retry")
			}
			runtime := object(t, array(t, pod["containers"])[0])
			args := array(t, runtime["args"])
			if args[0] != command {
				t.Fatal("operator command changed")
			}
			root := at(t, runtime, "securityContext", "runAsUser") == 0
			if root != (command == "initialize-preparation-store") {
				t.Fatal("operator root authority widened")
			}
		})
	}
}

func TestM11HelmExplicitSSOPrivateProjectionFileSelection(t *testing.T) {
	v := values(t)
	raw, e := os.ReadFile(filepath.Join(repository(t), "services/control/config/examples/control/sso.helm.values.example.json"))
	if e != nil {
		t.Fatal(e)
	}
	var overlay map[string]any
	if e = json.Unmarshal(raw, &overlay); e != nil {
		t.Fatal(e)
	}
	files := at(t, overlay, "secrets", "files")
	set(v, "secrets.files", files)
	docs := documents(t, run(t, v, true, "template"))
	validateOfficial(t, docs)
	pod := at(t, docs["Deployment"], "spec", "template", "spec")
	init := array(t, at(t, pod, "initContainers"))[0]
	args := array(t, at(t, init, "args"))
	var selected []string
	for i, arg := range args {
		if arg == "--secret-files-json" && i+1 < len(args) {
			if e = json.Unmarshal([]byte(args[i+1].(string)), &selected); e != nil {
				t.Fatal(e)
			}
		}
	}
	if len(selected) != 7 || selected[5] != "sso-keyring.json" || selected[6] != "provider-ca.pem" {
		t.Fatal("SSO private projection selection lost")
	}
	if strings.Contains(string(raw), "active_key_id") || strings.Contains(string(raw), "client_secret") {
		t.Fatal("private key or provider credential belongs in protected operator Secret, never values")
	}
}
func TestActualHelmRejectsMissingUnsafeUnknownAndCrossOwnedValues(t *testing.T) {
	cases := map[string]func(map[string]any){"image_missing": func(v map[string]any) { set(v, "image.reference", "") }, "floating_image": func(v map[string]any) { set(v, "image.reference", "projectone/control:latest") }, "missing_claim": func(v map[string]any) { set(v, "storage.preparations.existingClaim", "") }, "shared_claim": func(v map[string]any) { set(v, "storage.models.existingClaim", "synthetic-preparations") }, "overlap": func(v map[string]any) { set(v, "storage.models.mountPath", "/var/lib/projectone") }, "unknown_field": func(v map[string]any) { v["fake_success"] = true }, "two_owners": func(v map[string]any) { set(v, "replicas", 2) }, "unsupported_architecture": func(v map[string]any) { set(v, "architecture", "riscv64") }, "root_runtime": func(v map[string]any) { set(v, "runtime.uid", 0) }, "memory_overflow": func(v map[string]any) { set(v, "runtime.goMemoryMiB", 1024) }, "request_overflow": func(v map[string]any) { set(v, "resources.requestCPU", 2000) }, "projection_name_override": func(v map[string]any) { set(v, "secrets.files", []string{"control.yaml"}) }, "selector_override": func(v map[string]any) { v["nodeSelector"].(map[string]any)["kubernetes.io/arch"] = "arm64" }, "unacknowledged_operator": func(v map[string]any) { set(v, "mode", "operator") }}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) { v := values(t); change(v); run(t, v, false, "template") })
	}
	for name, change := range map[string]func(map[string]any){"wrong_namespace": func(v map[string]any) { set(v, "operator.acknowledgeNamespace", "customer-production") }, "same_secret": func(v map[string]any) { set(v, "operator.existingSecret", "synthetic-runtime-secret") }, "shell": func(v map[string]any) { set(v, "operator.command", "sh") }, "interactive_migration": func(v map[string]any) { set(v, "operator.interactive", true) }, "snapshot_missing": func(v map[string]any) { set(v, "operator.command", "export-model-files") }} {
		t.Run(name, func(t *testing.T) { v := operatorValues(t, "migrate"); change(v); run(t, v, false, "template") })
	}
}
func TestActualHelmNondefaultRuntimeAndMountValuesAreRendered(t *testing.T) {
	v := values(t)
	set(v, "runtime.uid", 10033)
	set(v, "runtime.gid", 10034)
	set(v, "runtime.goMemoryMiB", 320)
	set(v, "resources.requestMemoryMiB", 256)
	set(v, "resources.limitMemoryMiB", 448)
	set(v, "resources.requestCPU", 125)
	set(v, "resources.limitCPU", 750)
	set(v, "http.containerPort", 9443)
	set(v, "http.servicePort", 10443)
	set(v, "configuration.privateMountPath", "/runtime/projectone")
	set(v, "probes.host", "admin.example.invalid:10443")
	docs := documents(t, run(t, v, true, "template"))
	validateOfficial(t, docs)
	pod := at(t, docs["Deployment"], "spec", "template", "spec")
	if at(t, pod, "securityContext", "runAsUser") != 10033 {
		t.Fatal("runtime override ignored")
	}
	runtime := array(t, at(t, pod, "containers"))[0]
	if at(t, runtime, "resources", "limits", "memory") != "448Mi" || at(t, runtime, "resources", "limits", "cpu") != "750m" {
		t.Fatal("resource override ignored")
	}
}

func TestM12ActualHelmKnowledgeScratchOwnershipResourcesAndOverlap(t *testing.T) {
	v := values(t)
	set(v, "knowledge.enabled", true)
	set(v, "knowledge.scratchMountPath", "/runtime/encoder-scratch")
	set(v, "knowledge.scratchMiB", 192)
	set(v, "resources.limitMemoryMiB", 3072)
	docs := documents(t, run(t, v, true, "template"))
	validateOfficial(t, docs)
	pod := at(t, docs["Deployment"], "spec", "template", "spec")
	inits := array(t, at(t, pod, "initContainers"))
	if len(inits) != 2 || at(t, inits[1], "name") != "knowledge-scratch" || at(t, inits[1], "securityContext", "runAsUser") != 0 {
		t.Fatal("knowledge private initializer missing")
	}
	args := array(t, at(t, inits[1], "args"))
	if args[1] != "/runtime/encoder-scratch" || args[3] != "10001" || args[5] != "10001" {
		t.Fatal("knowledge runtime owner/path ignored")
	}
	volumes := array(t, at(t, pod, "volumes"))
	found := false
	for _, volume := range volumes {
		if at(t, volume, "name") == "knowledge-scratch" {
			found = at(t, volume, "emptyDir", "sizeLimit") == "192Mi"
		}
	}
	if !found {
		t.Fatal("bounded ephemeral knowledge scratch missing")
	}
	run(t, v, true, "lint")
	set(v, "resources.limitMemoryMiB", 1024)
	run(t, v, false, "template")
	set(v, "resources.limitMemoryMiB", 3072)
	for _, path := range []string{"/run/projectone/private", "/var/lib/projectone/models", "/var/lib/projectone"} {
		set(v, "knowledge.scratchMountPath", path)
		run(t, v, false, "template")
	}
}

func TestM13HelmWorkflowPrerequisiteAndConfiguredShutdownGrace(t *testing.T) {
	v := values(t)
	set(v, "workflows.enabled", true)
	run(t, v, false, "template")
	set(v, "knowledge.enabled", true)
	set(v, "resources.limitMemoryMiB", 3072)
	set(v, "lifecycle.terminationGraceSeconds", 45)
	docs := documents(t, run(t, v, true, "template"))
	validateOfficial(t, docs)
	pod := at(t, docs["Deployment"], "spec", "template", "spec")
	if at(t, pod, "terminationGracePeriodSeconds") != 45 {
		t.Fatal("configured workflow shutdown grace ignored")
	}
	set(v, "lifecycle.terminationGraceSeconds", 0)
	run(t, v, false, "template")
}
