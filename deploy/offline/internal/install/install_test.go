package install

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"
)

func validYAML(t *testing.T, root string) string {
	t.Helper()
	inputs := t.TempDir()
	trust := filepath.Join(inputs, "trust.json")
	compose := filepath.Join(inputs, "compose.yaml")
	if e := os.WriteFile(trust, []byte(`{"format_version":1}`), 0o600); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(compose, []byte("name: x\n"), 0o600); e != nil {
		t.Fatal(e)
	}
	return `format_version: 1
install_root: ` + root + `
compose_file: ` + compose + `
runtime: {uid: ` + itoa(os.Getuid()) + `, gid: ` + itoa(os.Getgid()) + `}
images:
  control: ownsolid/control:1.2.3
  postgres: pgvector/pgvector@sha256:` + strings.Repeat("a", 64) + `
  metrics: victoriametrics/victoria-metrics:v1.155.0
  logs: victoriametrics/victoria-logs:v1.53.0
origins:
  portal: https://portal.ownsolid.test:8443
  admin: https://admin.ownsolid.test:8443
license: {trust_file: ` + trust + `}
smtp: {host: smtp.ownsolid.test, from_address: platform@ownsolid.test}
administrator:
  email: admin@ownsolid.test
  display_name: Administrator
  installation_name: Test
  workspace_name: Default
`
}

func itoa(v int) string {
	raw, _ := json.Marshal(v)
	return string(raw)
}

func validConfig(t *testing.T) Config {
	t.Helper()
	c, e := Decode([]byte(validYAML(t, filepath.Join(t.TempDir(), "install"))))
	if e != nil {
		t.Fatal(e)
	}
	return c
}

func TestDecodeAppliesDefaults(t *testing.T) {
	c := validConfig(t)
	if c.Topology.Size != SingleNode || c.TLS.Source != "generate" || c.Network.HTTPSPort != 8443 || c.Network.BindAddress != "127.0.0.1" || c.Project != "ownsolid" {
		t.Fatalf("defaults not applied: %+v", c)
	}
}

func TestStateSubnetFixesTheDatabaseAddressOutsideTheDynamicRange(t *testing.T) {
	for subnet, want := range map[string][2]string{
		"172.31.254.0/26": {"172.31.254.2", "172.31.254.32/27"},
		"10.20.0.0/16":    {"10.20.0.2", "10.20.128.0/17"},
		"192.168.7.16/28": {"192.168.7.18", "192.168.7.24/29"},
	} {
		a := NetworkConfig{StateSubnet: subnet}.stateAddresses()
		if a.Postgres.String() != want[0] || a.Dynamic.String() != want[1] {
			t.Errorf("%s: got %s and %s, want %v", subnet, a.Postgres, a.Dynamic, want)
		}
		if a.Metrics != a.Postgres.Next() || a.Logs != a.Metrics.Next() {
			t.Errorf("%s: store addresses are not consecutive: %+v", subnet, a)
		}
		for _, store := range []netip.Addr{a.Postgres, a.Metrics, a.Logs} {
			if a.Dynamic.Contains(store) {
				t.Errorf("%s: the dynamic range holds store address %s", subnet, store)
			}
		}
	}
	rendered := validConfig(t).ComposeVariables()
	if rendered["OWNSOLID_STATE_SUBNET"] != "172.31.254.0/26" || rendered["OWNSOLID_POSTGRES_ADDRESS"] != "172.31.254.2" || rendered["OWNSOLID_STATE_DYNAMIC_RANGE"] != "172.31.254.32/27" {
		t.Fatalf("default network variables: %v", rendered)
	}
}

func TestDecodeRefusals(t *testing.T) {
	base := validYAML(t, "/srv/ownsolid")
	for name, test := range map[string]struct{ old, new, want string }{
		"unknown key":        {"format_version: 1\n", "format_version: 1\nsurprise: true\n", "INSTALL_CONFIG_INVALID"},
		"format":             {"format_version: 1\n", "format_version: 2\n", "format_version"},
		"size unknown":       {"format_version: 1\n", "format_version: 1\ntopology: {size: huge}\n", "topology.size"},
		"root relative":      {"install_root: /srv/ownsolid", "install_root: srv/ownsolid", "install_root"},
		"root is slash":      {"install_root: /srv/ownsolid", "install_root: /", "install_root"},
		"root unclean":       {"install_root: /srv/ownsolid", "install_root: /srv/../ownsolid", "install_root"},
		"root user":          {"runtime: {uid: ", "runtime: {uid: 0, ignored: ", "INSTALL_CONFIG_INVALID"},
		"image latest":       {"ownsolid/control:1.2.3", "ownsolid/control:latest", "images.control"},
		"image untagged":     {"ownsolid/control:1.2.3", "ownsolid/control", "images.control"},
		"registry port only": {"ownsolid/control:1.2.3", "registry:5000/control", "images.control"},
		"short digest":       {strings.Repeat("a", 64), "abc", "images.postgres"},
		"http origin":        {"https://portal.ownsolid.test:8443", "http://portal.ownsolid.test:8443", "origins.portal"},
		"origin path":        {"https://admin.ownsolid.test:8443", "https://admin.ownsolid.test:8443/x", "origins.admin"},
		"same origins":       {"https://admin.ownsolid.test:8443", "https://portal.ownsolid.test:8443", "origins"},
		"tls source":         {"format_version: 1\n", "format_version: 1\ntls: {source: none}\n", "tls.source"},
		"tls stray file":     {"format_version: 1\n", "format_version: 1\ntls: {certificate_file: /a}\n", "tls"},
		"tls files partial":  {"format_version: 1\n", "format_version: 1\ntls: {source: files, certificate_file: /a}\n", "tls"},
		"smtp mode":          {"smtp: {host: smtp.ownsolid.test,", "smtp: {tls_mode: none, host: smtp.ownsolid.test,", "smtp.tls_mode"},
		"smtp credential":    {"smtp: {host: smtp.ownsolid.test,", "smtp: {username: a, host: smtp.ownsolid.test,", "smtp"},
		"admin email":        {"email: admin@ownsolid.test", "email: nobody", "administrator.email"},
		"owned setting":      {"format_version: 1\n", "format_version: 1\ncontrol_settings: {server: {}}\n", "control_settings.server"},
		"memory":             {"format_version: 1\n", "format_version: 1\nresources: {control_memory: lots}\n", "resources"},
		"metrics image":      {"victoriametrics/victoria-metrics:v1.155.0", "victoriametrics/victoria-metrics:latest", "images.metrics"},
		"logs image":         {"victoriametrics/victoria-logs:v1.53.0", "victoriametrics/victoria-logs", "images.logs"},
		"retention":          {"format_version: 1\n", "format_version: 1\ntelemetry: {logs_retention: 1y}\n", "telemetry"},
		"store memory":       {"format_version: 1\n", "format_version: 1\nresources: {metrics_memory: lots}\n", "resources"},
		"store cpus":         {"format_version: 1\n", "format_version: 1\nresources: {logs_cpus: many}\n", "resources"},
		"subnet not cidr":    {"format_version: 1\n", "format_version: 1\nnetwork: {state_subnet: 172.31.254.0}\n", "network.state_subnet"},
		"subnet host bits":   {"format_version: 1\n", "format_version: 1\nnetwork: {state_subnet: 172.31.254.1/26}\n", "network.state_subnet"},
		"subnet public":      {"format_version: 1\n", "format_version: 1\nnetwork: {state_subnet: 8.8.8.0/24}\n", "network.state_subnet"},
		"subnet ipv6":        {"format_version: 1\n", "format_version: 1\nnetwork: {state_subnet: \"fd00::/64\"}\n", "network.state_subnet"},
		"subnet too small":   {"format_version: 1\n", "format_version: 1\nnetwork: {state_subnet: 10.0.0.0/29}\n", "network.state_subnet"},
		"subnet too large":   {"format_version: 1\n", "format_version: 1\nnetwork: {state_subnet: 10.0.0.0/8}\n", "network.state_subnet"},
	} {
		t.Run(name, func(t *testing.T) {
			if !strings.Contains(base, test.old) {
				t.Fatalf("fixture lacks %q", test.old)
			}
			_, e := Decode([]byte(strings.Replace(base, test.old, test.new, 1)))
			if e == nil || !strings.Contains(e.Error(), test.want) {
				t.Fatalf("got %v, want mention of %q", e, test.want)
			}
		})
	}
}

func TestEveryOtherTopologySizeIsRefusedBeforeAnyChange(t *testing.T) {
	root := filepath.Join(t.TempDir(), "install")
	for _, size := range []string{"ha", "cell", "multi-cell", "multi-site"} {
		raw := strings.Replace(validYAML(t, root), "format_version: 1\n", "format_version: 1\ntopology: {size: "+size+"}\n", 1)
		_, e := Decode([]byte(raw))
		if e == nil || !strings.HasPrefix(e.Error(), "CONFIG_TOPOLOGY_SIZE_NOT_SUPPORTED") {
			t.Fatalf("%s: got %v", size, e)
		}
	}
	if exists(root) {
		t.Fatal("a refused configuration created files")
	}
}

func TestMaterializeWritesPrivateFilesAndKeepsSecrets(t *testing.T) {
	c := validConfig(t)
	if e := Materialize(c); e != nil {
		t.Fatal(e)
	}
	l := c.Layout()
	generated := []string{
		filepath.Join(l.Config, "auth-hmac"), filepath.Join(l.Config, "mail-keyring.json"), filepath.Join(l.Config, "private-key.pem"),
		filepath.Join(l.Config, "certificate.pem"), filepath.Join(l.Config, "runtime-dsn"), filepath.Join(l.Operator, "operator-dsn"),
		filepath.Join(l.Secrets, "postgres-password"), filepath.Join(l.Secrets, "runtime-password"), filepath.Join(l.Secrets, "ca-key.pem"),
	}
	before := map[string][]byte{}
	for _, path := range generated {
		st, e := os.Stat(path)
		if e != nil || st.Mode().Perm() != 0o600 {
			t.Fatalf("%s: %v %v", path, st, e)
		}
		before[path], _ = os.ReadFile(path)
	}
	for _, directory := range []string{l.Root, l.Config, l.Operator, l.Secrets, l.Postgres} {
		if st, e := os.Stat(directory); e != nil || st.Mode().Perm() != 0o700 {
			t.Fatalf("%s: %v %v", directory, st, e)
		}
	}
	if e := Materialize(c); e != nil {
		t.Fatal(e)
	}
	for path, raw := range before {
		after, _ := os.ReadFile(path)
		if !bytes.Equal(raw, after) {
			t.Fatalf("%s changed on a second run", path)
		}
	}
	entries, _ := os.ReadDir(l.Config)
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".install-") {
			t.Fatalf("temporary file left: %s", entry.Name())
		}
	}
}

func TestSecretsMeetControlFormatsAndStayOutOfCompose(t *testing.T) {
	c := validConfig(t)
	if e := Materialize(c); e != nil {
		t.Fatal(e)
	}
	l := c.Layout()
	password, _ := os.ReadFile(filepath.Join(l.Secrets, "runtime-password"))
	decoded, e := base64.RawURLEncoding.DecodeString(strings.TrimSpace(string(password)))
	if e != nil || len(decoded) != 32 {
		t.Fatalf("runtime password format: %v %d", e, len(decoded))
	}
	dsn, _ := os.ReadFile(filepath.Join(l.Config, "runtime-dsn"))
	if !strings.Contains(string(dsn), runtimeRole+":"+strings.TrimSpace(string(password))+"@172.31.254.2:5432/ownsolid?sslmode=disable") {
		t.Fatal("runtime DSN does not carry the runtime identity")
	}
	operator, _ := os.ReadFile(filepath.Join(l.Secrets, "postgres-password"))
	for _, name := range []string{"compose.env", "compose.yaml", "config/control.yaml", "operator/operator.yaml", "operator/bootstrap.json"} {
		raw, _ := os.ReadFile(filepath.Join(l.Root, name))
		if bytes.Contains(raw, bytes.TrimSpace(password)) || bytes.Contains(raw, bytes.TrimSpace(operator)) {
			t.Fatalf("%s contains a secret value", name)
		}
	}
	configRoot, _ := os.ReadFile(filepath.Join(l.Config, "runtime-dsn"))
	if bytes.Contains(configRoot, bytes.TrimSpace(operator)) {
		t.Fatal("the serving configuration holds the privileged database password")
	}
}

func TestControlConfigurationSeparatesDatabaseIdentities(t *testing.T) {
	c := validConfig(t)
	c.ControlSettings = map[string]any{"runtime": map[string]any{"workspaces": map[string]any{"maximum_count": 7}}}
	for dsn, want := range map[string]string{containerConfigDir + "/runtime-dsn": "/run/projectone/runtime-dsn", containerOperatorDir + "/operator-dsn": "/run/projectone-operator/operator-dsn"} {
		raw, e := c.renderControl(dsn)
		if e != nil {
			t.Fatal(e)
		}
		var document map[string]any
		if e = yaml.Unmarshal(raw, &document); e != nil {
			t.Fatal(e)
		}
		if document["database"].(map[string]any)["dsn_file"] != want || document["topology"].(map[string]any)["size"] != SingleNode || document["runtime"] == nil {
			t.Fatalf("rendered: %s", raw)
		}
	}
}

func TestMaterializeRefusesSymlinkedDirectory(t *testing.T) {
	c := validConfig(t)
	if e := os.MkdirAll(c.InstallRoot, 0o700); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink(t.TempDir(), filepath.Join(c.InstallRoot, "config")); e != nil {
		t.Fatal(e)
	}
	if e := Materialize(c); e == nil || !strings.HasPrefix(e.Error(), "INSTALL_DIRECTORY_INVALID") {
		t.Fatalf("got %v", e)
	}
}

func TestGeneratedCertificateCoversOriginsAndIsReissuedWhenTheyChange(t *testing.T) {
	c := validConfig(t)
	if e := Materialize(c); e != nil {
		t.Fatal(e)
	}
	certificatePath := filepath.Join(c.Layout().Config, "certificate.pem")
	authorityPath := filepath.Join(c.Layout().Config, "ca.pem")
	first, _ := os.ReadFile(certificatePath)
	authority, _ := os.ReadFile(authorityPath)
	if !serverCertificateCurrent(first, authority, []string{"portal.ownsolid.test", "admin.ownsolid.test"}) {
		t.Fatal("certificate does not cover the origins")
	}
	if serverCertificateCurrent(first, authority, []string{"other.ownsolid.test"}) {
		t.Fatal("certificate accepted for a foreign host")
	}
	c.Origins.Admin = "https://10.0.0.5:8443"
	if e := Materialize(c); e != nil {
		t.Fatal(e)
	}
	second, _ := os.ReadFile(certificatePath)
	if bytes.Equal(first, second) || !serverCertificateCurrent(second, authority, []string{"portal.ownsolid.test", "10.0.0.5"}) {
		t.Fatal("certificate not reissued for the new origin")
	}
	sameAuthority, _ := os.ReadFile(authorityPath)
	if !bytes.Equal(authority, sameAuthority) {
		t.Fatal("the authority changed, so existing clients would lose trust")
	}
}

func TestSuppliedTLSFilesAreCopied(t *testing.T) {
	c := validConfig(t)
	inputs := t.TempDir()
	c.TLS = TLSConfig{Source: "files", CertificateFile: filepath.Join(inputs, "c"), PrivateKeyFile: filepath.Join(inputs, "k"), CAFile: filepath.Join(inputs, "a")}
	if e := Materialize(c); e == nil || !strings.HasPrefix(e.Error(), "INSTALL_INPUT_UNREADABLE") {
		t.Fatalf("missing input: %v", e)
	}
	for _, name := range []string{"c", "k", "a"} {
		if e := os.WriteFile(filepath.Join(inputs, name), []byte(name), 0o600); e != nil {
			t.Fatal(e)
		}
	}
	if e := Materialize(c); e != nil {
		t.Fatal(e)
	}
	for name, want := range map[string]string{"certificate.pem": "c", "private-key.pem": "k", "ca.pem": "a"} {
		raw, _ := os.ReadFile(filepath.Join(c.Layout().Config, name))
		if string(raw) != want {
			t.Fatalf("%s: %q", name, raw)
		}
	}
	if exists(filepath.Join(c.Layout().Secrets, "ca-key.pem")) {
		t.Fatal("a CA key was generated although the operator supplied certificates")
	}
}

// fakeRunner answers commands from a table keyed by a word in the command line.
type fakeRunner struct {
	commands []string
	answer   func(line string) (string, error)
}

func (r *fakeRunner) Run(_ context.Context, name string, args ...string) (string, error) {
	line := name + " " + strings.Join(args, " ")
	r.commands = append(r.commands, line)
	if r.answer != nil {
		return r.answer(line)
	}
	return successOutput(line), nil
}

func successOutput(line string) string {
	for word, output := range map[string]string{
		" migrate ": "MIGRATION_COMPLETE", "prepare-runtime-access": "RUNTIME_ACCESS_VERIFIED", "check-schema": "SCHEMA_CURRENT",
		"bootstrap-admin-file": "BOOTSTRAP_FILE_VERIFIED", "reconcile-bootstrap-file": "BOOTSTRAP_FILE_VERIFIED", "resume-bootstrap-file": "BOOTSTRAP_FILE_VERIFIED",
	} {
		if strings.Contains(line, word) {
			return "Container started\n" + output + "\n"
		}
	}
	return ""
}

func testInstaller(c Config, runner Runner, out *bytes.Buffer) *installer {
	return &installer{config: c, runner: runner, probe: func(context.Context, Config, string) error { return nil }, out: out}
}

func TestApplyRunsTheStepsInOrder(t *testing.T) {
	c := validConfig(t)
	c.Images.ArchiveFile = "/bundle/images.tar"
	runner := &fakeRunner{}
	var out bytes.Buffer
	if e := testInstaller(c, runner, &out).apply(context.Background()); e != nil {
		t.Fatal(e)
	}
	order := []string{"docker version", "compose version", "load --input /bundle/images.tar", "image inspect", "stop control", "up --detach --wait --no-build postgres", "up --detach --wait --no-build metrics", "up --detach --wait --no-build logs", "/health", "CREATE EXTENSION", " migrate ", "prepare-runtime-access", "check-schema", "bootstrap-admin-file", "up --detach --wait --no-build control"}
	position := 0
	for _, command := range runner.commands {
		if position < len(order) && strings.Contains(command, order[position]) {
			position++
		}
	}
	if position != len(order) {
		t.Fatalf("stopped at %q in:\n%s", order[position], strings.Join(runner.commands, "\n"))
	}
	for _, command := range runner.commands {
		if strings.Contains(command, " pull") || strings.Contains(command, "--build") {
			t.Fatalf("an install must never reach a registry: %s", command)
		}
		if strings.Contains(command, "control-operator") && !strings.Contains(command, "--no-deps") {
			t.Fatalf("operator task may start services: %s", command)
		}
	}
	if !strings.Contains(out.String(), "INSTALL_COMPLETE") || !strings.Contains(out.String(), c.CredentialPath()) {
		t.Fatalf("output: %s", out.String())
	}
}

func TestApplyStopsAtTheFirstFailureAndNamesIt(t *testing.T) {
	c := validConfig(t)
	runner := &fakeRunner{answer: func(line string) (string, error) {
		if strings.Contains(line, " migrate ") {
			return "MIGRATION_FAILED\n", errors.New("exit status 1")
		}
		return successOutput(line), nil
	}}
	var out bytes.Buffer
	e := testInstaller(c, runner, &out).apply(context.Background())
	var step *StepError
	if !errors.As(e, &step) || step.Step != "schema" || !strings.Contains(step.Detail, "MIGRATION_FAILED") || !strings.Contains(e.Error(), "run apply again") {
		t.Fatalf("got %v", e)
	}
	for _, command := range runner.commands {
		if strings.Contains(command, "bootstrap-admin-file") || strings.Contains(command, "no-build control") {
			t.Fatalf("ran past the failure: %s", command)
		}
	}
	if strings.Contains(out.String(), "INSTALL_COMPLETE") {
		t.Fatal("claimed success after a failure")
	}
}

func TestOperatorTaskMustPrintItsSuccessLine(t *testing.T) {
	c := validConfig(t)
	runner := &fakeRunner{answer: func(line string) (string, error) {
		if strings.Contains(line, "check-schema") {
			return "something else\n", nil
		}
		return successOutput(line), nil
	}}
	var out bytes.Buffer
	if e := testInstaller(c, runner, &out).apply(context.Background()); e == nil {
		t.Fatal("exit status zero without SCHEMA_CURRENT was accepted")
	}
}

func TestMissingImageIsReportedByName(t *testing.T) {
	c := validConfig(t)
	runner := &fakeRunner{answer: func(line string) (string, error) {
		if strings.Contains(line, "image inspect") && strings.Contains(line, c.Images.Control) {
			return "", errors.New("exit status 1")
		}
		return successOutput(line), nil
	}}
	var out bytes.Buffer
	e := testInstaller(c, runner, &out).apply(context.Background())
	var step *StepError
	if !errors.As(e, &step) || step.Code != "INSTALL_IMAGE_MISSING" || !strings.Contains(step.Detail, c.Images.Control) {
		t.Fatalf("got %v", e)
	}
}

func TestSecondApplyReconcilesTheAdministrator(t *testing.T) {
	c := validConfig(t)
	first := &fakeRunner{}
	var out bytes.Buffer
	if e := testInstaller(c, first, &out).apply(context.Background()); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(c.CredentialPath(), []byte("{}"), 0o600); e != nil {
		t.Fatal(e)
	}
	second := &fakeRunner{}
	if e := testInstaller(c, second, &out).apply(context.Background()); e != nil {
		t.Fatal(e)
	}
	joined := strings.Join(second.commands, "\n")
	if strings.Contains(joined, "bootstrap-admin-file") || !strings.Contains(joined, "reconcile-bootstrap-file") {
		t.Fatalf("second run:\n%s", joined)
	}
}

func TestInterruptedAdministratorSetupResumes(t *testing.T) {
	c := validConfig(t)
	if e := Materialize(c); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(c.CredentialPath(), []byte("{}"), 0o600); e != nil {
		t.Fatal(e)
	}
	runner := &fakeRunner{answer: func(line string) (string, error) {
		if strings.Contains(line, "reconcile-bootstrap-file") {
			return "BOOTSTRAP_FILE_NOT_COMMITTED\n", errors.New("exit status 1")
		}
		return successOutput(line), nil
	}}
	var out bytes.Buffer
	if e := testInstaller(c, runner, &out).administrator(context.Background()); e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(strings.Join(runner.commands, "\n"), "resume-bootstrap-file") {
		t.Fatal("did not resume")
	}
}

func TestHealthFailureNamesTheOrigin(t *testing.T) {
	c := validConfig(t)
	i := testInstaller(c, &fakeRunner{}, &bytes.Buffer{})
	i.probe = func(_ context.Context, _ Config, origin string) error {
		if origin == c.Origins.Admin {
			return errors.New("status 503")
		}
		return nil
	}
	e := i.health(context.Background())
	if e == nil || !strings.Contains(e.Error(), c.Origins.Admin) || !strings.Contains(e.Error(), "503") {
		t.Fatalf("got %v", e)
	}
}

func TestStatusReportsWhatIsRunning(t *testing.T) {
	c := validConfig(t)
	if e := Materialize(c); e != nil {
		t.Fatal(e)
	}
	healthy := `{"Service":"postgres","State":"running","Health":"healthy"}` + "\n" + `{"Service":"metrics","State":"running","Health":"healthy"}` + "\n" + `{"Service":"control","State":"running","Health":"healthy"}` + "\n"
	ok := func(context.Context, Config, string) error { return nil }
	var out bytes.Buffer
	if e := status(context.Background(), c, &fakeRunner{answer: func(string) (string, error) { return healthy, nil }}, ok, &out); e != nil {
		t.Fatalf("%v\n%s", e, out.String())
	}
	stopped := `{"Service":"postgres","State":"running","Health":"healthy"}` + "\n" + `{"Service":"metrics","State":"running","Health":"healthy"}` + "\n" + `{"Service":"control","State":"exited","Health":""}` + "\n"
	if e := status(context.Background(), c, &fakeRunner{answer: func(string) (string, error) { return stopped, nil }}, ok, &out); e == nil || !strings.Contains(e.Error(), "control") {
		t.Fatalf("stopped control reported healthy: %v", e)
	}
	missing := validConfig(t)
	if e := status(context.Background(), missing, &fakeRunner{}, ok, &out); e == nil || !strings.HasPrefix(e.Error(), "INSTALL_NOT_FOUND") {
		t.Fatalf("got %v", e)
	}
}

func TestProbeVerifiesTheCertificate(t *testing.T) {
	c := validConfig(t)
	if e := Materialize(c); e != nil {
		t.Fatal(e)
	}
	c.Network.HTTPSPort = 1
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if e := ProbeOrigin(ctx, c, c.Origins.Portal); e == nil {
		t.Fatal("probe succeeded with nothing listening")
	}
}

func TestComposeFileAndExampleMatchTheRenderedVariables(t *testing.T) {
	repository := filepath.Join("..", "..", "..", "compose")
	raw, e := os.ReadFile(filepath.Join(repository, "compose.yaml"))
	if e != nil {
		t.Fatal(e)
	}
	used := map[string]bool{}
	for _, match := range regexp.MustCompile(`\$\{(OWNSOLID_[A-Z_]+)`).FindAllStringSubmatch(string(raw), -1) {
		used[match[1]] = true
	}
	rendered := validConfig(t).ComposeVariables()
	example, e := os.ReadFile(filepath.Join(repository, ".env.example"))
	if e != nil {
		t.Fatal(e)
	}
	for name := range used {
		if _, present := rendered[name]; !present {
			t.Errorf("compose.yaml uses %s, which the installer does not render", name)
		}
		if !strings.Contains(string(example), "\n"+name+"=") {
			t.Errorf(".env.example does not document %s", name)
		}
	}
	for name := range rendered {
		if !used[name] {
			t.Errorf("the installer renders %s, which compose.yaml does not use", name)
		}
	}
	var document struct {
		Services map[string]struct {
			PullPolicy string   `yaml:"pull_policy"`
			ReadOnly   bool     `yaml:"read_only"`
			CapDrop    []string `yaml:"cap_drop"`
		} `yaml:"services"`
	}
	if e = yaml.Unmarshal(raw, &document); e != nil {
		t.Fatal(e)
	}
	for name, service := range document.Services {
		if service.PullPolicy != "never" {
			t.Errorf("%s may reach a registry", name)
		}
		if name != "postgres" && (!service.ReadOnly || len(service.CapDrop) == 0) {
			t.Errorf("%s is not hardened", name)
		}
	}
}

func TestComposeNeverGivesServingThePrivilegedIdentity(t *testing.T) {
	raw, e := os.ReadFile(filepath.Join("..", "..", "..", "compose", "compose.yaml"))
	if e != nil {
		t.Fatal(e)
	}
	var document struct {
		Services map[string]struct {
			Volumes []struct {
				Source string `yaml:"source"`
			} `yaml:"volumes"`
		} `yaml:"services"`
	}
	if e = yaml.Unmarshal(raw, &document); e != nil {
		t.Fatal(e)
	}
	for _, volume := range document.Services["control"].Volumes {
		if strings.Contains(volume.Source, "OPERATOR") {
			t.Fatal("the serving container mounts the operator directory")
		}
	}
}

func TestTelemetryWaitsForTheLogsStoreAndNamesItsFailure(t *testing.T) {
	c := validConfig(t)
	probes := 0
	ready := &fakeRunner{answer: func(line string) (string, error) {
		if strings.Contains(line, "/health") {
			if probes++; probes < 3 {
				return "connection refused", errors.New("exit status 1")
			}
		}
		return "", nil
	}}
	if e := testInstaller(c, ready, &bytes.Buffer{}).telemetry(context.Background()); e != nil || probes != 3 {
		t.Fatalf("probes %d, error %v", probes, e)
	}

	never := &fakeRunner{answer: func(line string) (string, error) {
		if strings.Contains(line, "/health") {
			return "connection refused", errors.New("exit status 1")
		}
		return "", nil
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	var step *StepError
	e := testInstaller(c, never, &bytes.Buffer{}).telemetry(ctx)
	if !errors.As(e, &step) || step.Code != "INSTALL_LOGS_START_FAILED" || !strings.Contains(step.Detail, "connection refused") {
		t.Fatalf("got %v", e)
	}
}

func TestStatusReportsAStoppedLogsStore(t *testing.T) {
	c := validConfig(t)
	if e := Materialize(c); e != nil {
		t.Fatal(e)
	}
	healthy := `{"Service":"postgres","State":"running","Health":"healthy"}` + "\n" + `{"Service":"metrics","State":"running","Health":"healthy"}` + "\n" + `{"Service":"control","State":"running","Health":"healthy"}` + "\n"
	runner := &fakeRunner{answer: func(line string) (string, error) {
		if strings.Contains(line, "/health") {
			return "connection refused", errors.New("exit status 1")
		}
		return healthy, nil
	}}
	ok := func(context.Context, Config, string) error { return nil }
	var out bytes.Buffer
	if e := status(context.Background(), c, runner, ok, &out); e == nil || !strings.Contains(e.Error(), "logs is not ready") {
		t.Fatalf("got %v\n%s", e, out.String())
	}
}

func TestFailedServiceStartIncludesItsLog(t *testing.T) {
	c := validConfig(t)
	runner := &fakeRunner{answer: func(line string) (string, error) {
		switch {
		case strings.Contains(line, "up --detach --wait --no-build postgres"):
			return "container exited\n", errors.New("exit status 1")
		case strings.Contains(line, "logs --no-color --tail 10 postgres"):
			return "FATAL: data directory has wrong ownership\n", nil
		}
		return successOutput(line), nil
	}}
	e := testInstaller(c, runner, &bytes.Buffer{}).database(context.Background())
	var step *StepError
	if !errors.As(e, &step) || step.Code != "INSTALL_DATABASE_START_FAILED" || !strings.Contains(step.Detail, "wrong ownership") {
		t.Fatalf("got %v", e)
	}
}
