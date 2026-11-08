package install

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	maximumDetailBytes    = 2000
	readinessPollInterval = 500 * time.Millisecond
)

// Runner executes one external command and returns its combined output.
type Runner interface {
	Run(ctx context.Context, name string, args ...string) (string, error)
}

// ExecRunner runs real commands with a closed environment, so an ambient COMPOSE_*
// or registry setting cannot change what gets installed.
type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, name string, args ...string) (string, error) {
	command := exec.CommandContext(ctx, name, args...)
	command.Env = closedEnvironment()
	output, e := command.CombinedOutput()
	return string(output), e
}

func closedEnvironment() []string {
	environment := []string{}
	for _, key := range []string{"PATH", "HOME", "DOCKER_HOST", "DOCKER_CONFIG", "XDG_RUNTIME_DIR"} {
		if value, present := os.LookupEnv(key); present {
			environment = append(environment, key+"="+value)
		}
	}
	return environment
}

// StepError names the failed step, so the operator can fix it and apply again.
type StepError struct {
	Step   string
	Code   string
	Detail string
	// Command is what to run again after fixing the cause; empty means apply.
	Command string
}

func (e *StepError) Error() string {
	message := fmt.Sprintf("%s: step %s failed", e.Code, e.Step)
	if e.Detail != "" {
		message += ": " + e.Detail
	}
	command := e.Command
	if command == "" {
		command = "apply"
	}
	return message + ". Fix the cause and run " + command + " again; finished work is kept."
}

// retryWith names the command to run again in every step error inside e.
func retryWith(e error, command string) error {
	var step *StepError
	if errors.As(e, &step) {
		step.Command = command
	}
	return e
}

// Probe checks that a browser origin answers ready through the published port.
type Probe func(ctx context.Context, c Config, origin string) error

type installer struct {
	config       Config
	runner       Runner
	probe        Probe
	out          io.Writer
	verifySchema func(context.Context) error
	complete     func() error
}

// Apply brings the installation to the configured state. It is safe to repeat: each
// step either finds its work done or finishes it, so a failed run is retried by
// running it again.
func Apply(ctx context.Context, c Config, runner Runner, out io.Writer) error {
	if os.Geteuid() != 0 {
		return errors.New("INSTALL_ROOT_REQUIRED: apply creates files owned by the service users; run it as root")
	}
	if c.Preflight != nil {
		if e := Preflight(ctx, c, runner, out); e != nil {
			return retryWith(e, "apply")
		}
	}
	unlock, e := lockOperation(c)
	if e != nil {
		return e
	}
	defer unlock()
	binding, e := readBundleBinding(c)
	if e != nil {
		return e
	}
	if binding != nil {
		return errors.New("INSTALL_BUNDLE_REQUIRED: resume this installation with apply-bundle and its signed release")
	}
	return (&installer{config: c, runner: runner, probe: ProbeOrigin, out: out}).apply(ctx)
}

func (i *installer) apply(ctx context.Context) error {
	steps := []struct {
		name string
		run  func(context.Context) error
	}{
		{"preflight", i.preflight},
		{"configuration", func(context.Context) error { return Materialize(i.config) }},
		{"images", i.images},
		{"stop-control", i.stopControl},
		{"database", i.database},
		{"telemetry", i.telemetry},
		{"vector-extension", i.vectorExtension},
		{"schema", i.schema},
		{"runtime-access", i.runtimeAccess},
		{"check-schema", i.checkSchema},
		{"administrator", i.administrator},
		{"control", i.control},
		{"health", i.health},
	}
	for _, step := range steps {
		stepContext, cancel := context.WithTimeout(ctx, time.Duration(i.config.StepSeconds)*time.Second)
		e := step.run(stepContext)
		cancel()
		if e != nil {
			return wrapStep(step.name, e)
		}
		fmt.Fprintf(i.out, "step %s: ok\n", step.name)
	}
	if i.complete != nil {
		if err := i.complete(); err != nil {
			return wrapStep("finalize-release", err)
		}
	}
	fmt.Fprintf(i.out, "INSTALL_COMPLETE portal=%s admin=%s administrator_credential=%s\n", i.config.Origins.Portal, i.config.Origins.Admin, i.config.CredentialPath())
	return nil
}

func wrapStep(step string, e error) error {
	var existing *StepError
	if errors.As(e, &existing) {
		existing.Step = step
		return existing
	}
	code, detail, _ := strings.Cut(e.Error(), ": ")
	return &StepError{Step: step, Code: code, Detail: detail}
}

func (i *installer) preflight(ctx context.Context) error {
	if output, e := i.runner.Run(ctx, "docker", "version", "--format", "{{.Server.Version}}"); e != nil {
		return &StepError{Code: "INSTALL_DOCKER_UNAVAILABLE", Detail: lastLines(output)}
	}
	if output, e := i.runner.Run(ctx, "docker", "compose", "version", "--short"); e != nil {
		return &StepError{Code: "INSTALL_COMPOSE_UNAVAILABLE", Detail: lastLines(output)}
	}
	return nil
}

// images loads the optional archive and confirms every image is local. Nothing is pulled.
func (i *installer) images(ctx context.Context) error {
	if i.config.Images.ArchiveFile != "" {
		if output, e := i.runner.Run(ctx, "docker", "load", "--input", i.config.Images.ArchiveFile); e != nil {
			return &StepError{Code: "INSTALL_IMAGE_LOAD_FAILED", Detail: lastLines(output)}
		}
	}
	for _, image := range []string{i.config.Images.Control, i.config.Images.Postgres, i.config.Images.Metrics, i.config.Images.Logs} {
		if _, e := i.runner.Run(ctx, "docker", "image", "inspect", "--format", "{{.Id}}", image); e != nil {
			return &StepError{Code: "INSTALL_IMAGE_MISSING", Detail: image + " is not loaded on this host"}
		}
	}
	return nil
}

func (i *installer) compose(ctx context.Context, args ...string) (string, error) {
	l := i.config.Layout()
	return i.runner.Run(ctx, "docker", append([]string{"compose", "--env-file", l.ComposeEnv, "-f", l.ComposeFile}, args...)...)
}

// The installation lock excludes the serving process from operator tasks.
func (i *installer) stopControl(ctx context.Context) error {
	if output, e := i.compose(ctx, "stop", "control"); e != nil {
		return &StepError{Code: "INSTALL_COMPOSE_FAILED", Detail: lastLines(output)}
	}
	return nil
}

func (i *installer) database(ctx context.Context) error {
	return i.start(ctx, "postgres", "INSTALL_DATABASE_START_FAILED")
}

// telemetry starts both stores. The logs image has no shell, so Compose cannot health
// check it; readiness is read from the metrics container, which shares its network.
func (i *installer) telemetry(ctx context.Context) error {
	if e := i.start(ctx, "metrics", "INSTALL_METRICS_START_FAILED"); e != nil {
		return e
	}
	if e := i.start(ctx, "logs", "INSTALL_LOGS_START_FAILED"); e != nil {
		return e
	}
	for {
		output, e := logsReady(ctx, i.config, i.runner)
		if e == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return &StepError{Code: "INSTALL_LOGS_START_FAILED", Detail: lastLines(output)}
		case <-time.After(readinessPollInterval):
		}
	}
}

// logsReady asks the logs store for its health from inside the state network.
func logsReady(ctx context.Context, c Config, runner Runner) (string, error) {
	l := c.Layout()
	address := net.JoinHostPort(c.Network.stateAddresses().Logs.String(), logsPort)
	return runner.Run(ctx, "docker", "compose", "--env-file", l.ComposeEnv, "-f", l.ComposeFile, "exec", "--no-TTY", "metrics", "wget", "-q", "-T", "3", "-O", "/dev/null", "http://"+address+"/health")
}

// The migrations verify the vector extension but leave installing it to the operator.
func (i *installer) vectorExtension(ctx context.Context) error {
	output, e := i.compose(ctx, "exec", "--no-TTY", "postgres", "psql", "-X", "-U", migratorRole, "-d", databaseName, "-v", "ON_ERROR_STOP=1", "-c", "CREATE EXTENSION IF NOT EXISTS vector VERSION '0.8.7'")
	if e != nil {
		return &StepError{Code: "INSTALL_VECTOR_EXTENSION_FAILED", Detail: lastLines(output)}
	}
	return nil
}

// operator runs one control plane task and requires its exact success line.
func (i *installer) operator(ctx context.Context, want string, args ...string) (string, error) {
	output, e := i.compose(ctx, append([]string{"--profile", "operator", "run", "--rm", "--no-deps", "--no-TTY", "control-operator"}, args...)...)
	if e != nil || !containsLine(output, want) {
		return output, &StepError{Code: "INSTALL_OPERATOR_TASK_FAILED", Detail: lastLines(output)}
	}
	return output, nil
}

func (i *installer) operatorConfig() string { return containerOperatorDir + "/operator.yaml" }
func (i *installer) runtimeConfig() string  { return containerConfigDir + "/control.yaml" }

func (i *installer) schema(ctx context.Context) error {
	_, e := i.operator(ctx, "MIGRATION_COMPLETE", "migrate", "--config", i.operatorConfig())
	return e
}

// runtimeAccess grants the runtime role what the schema needs and verifies it. A new
// installation gets its role from prepare-runtime-access. On an existing one, a migration
// that added a table leaves the role without access to it, which prepare-runtime-access
// refuses to repair; upgrade-schema grants exactly the application tables to the role that
// is already there, and only when inspection says grants are pending.
func (i *installer) runtimeAccess(ctx context.Context) error {
	args := []string{"--config", i.operatorConfig(), "--runtime-config", i.runtimeConfig()}
	// A new installation has no accepted role yet, so inspection fails there by design.
	inspection, _ := i.operator(ctx, grantsPending, append([]string{"inspect-schema-upgrade"}, args...)...)
	if containsLine(inspection, grantsPending) {
		if _, e := i.operator(ctx, "SCHEMA_UPGRADE_COMPLETE", append([]string{"upgrade-schema"}, args...)...); e != nil {
			return e
		}
	}
	_, e := i.operator(ctx, "RUNTIME_ACCESS_VERIFIED", append([]string{"prepare-runtime-access"}, args...)...)
	return e
}

// grantsPending is what inspect-schema-upgrade prints when the schema is current and the
// runtime role lacks access to some of its tables.
const grantsPending = "SCHEMA_COMMITTED_GRANTS_PENDING"

func (i *installer) checkSchema(ctx context.Context) error {
	_, e := i.operator(ctx, "SCHEMA_CURRENT", "check-schema", "--config", i.operatorConfig())
	if e == nil && i.verifySchema != nil {
		return i.verifySchema(ctx)
	}
	return e
}

// administrator creates the first administrator once. A run that stopped after the
// credential file was written reconciles against it and never issues a second one.
func (i *installer) administrator(ctx context.Context) error {
	const verified = "BOOTSTRAP_FILE_VERIFIED"
	arguments := func(command string) []string {
		return []string{command, "--config", i.operatorConfig(), "--bootstrap-input", containerOperatorDir + "/bootstrap.json", "--credential-output", containerOperatorDir + "/" + credentialFile}
	}
	if !exists(i.config.CredentialPath()) {
		_, e := i.operator(ctx, verified, arguments("bootstrap-admin-file")...)
		return e
	}
	output, e := i.operator(ctx, verified, arguments("reconcile-bootstrap-file")...)
	if e != nil && strings.Contains(output, "BOOTSTRAP_FILE_NOT_COMMITTED") {
		_, e = i.operator(ctx, verified, arguments("resume-bootstrap-file")...)
	}
	return e
}

func (i *installer) control(ctx context.Context) error {
	return i.start(ctx, "control", "INSTALL_CONTROL_START_FAILED")
}

// start waits for a service to report healthy, and on failure adds the end of its log.
func (i *installer) start(ctx context.Context, service, code string) error {
	output, e := i.compose(ctx, "up", "--detach", "--wait", "--no-build", service)
	if e == nil {
		return nil
	}
	detail := lastLines(output)
	if logs, logError := i.compose(ctx, "logs", "--no-color", "--tail", "10", service); logError == nil {
		detail += " | log: " + lastLines(logs)
	}
	return &StepError{Code: code, Detail: detail}
}

func (i *installer) health(ctx context.Context) error {
	for _, origin := range []string{i.config.Origins.Portal, i.config.Origins.Admin} {
		if e := i.probe(ctx, i.config, origin); e != nil {
			return &StepError{Code: "INSTALL_HEALTH_FAILED", Detail: origin + ": " + e.Error()}
		}
	}
	return nil
}

// ProbeOrigin requests /health/ready as a browser would: through the published port,
// with the origin's host name, verifying the certificate against the configured CA.
func ProbeOrigin(ctx context.Context, c Config, origin string) error {
	u, e := originURL(origin)
	if e != nil {
		return e
	}
	authority, e := os.ReadFile(filepath.Join(c.Layout().Config, "ca.pem"))
	if e != nil {
		return errors.New("CA file unreadable")
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(authority) {
		return errors.New("CA file invalid")
	}
	host := c.Network.BindAddress
	if ip := net.ParseIP(host); ip != nil && ip.IsUnspecified() {
		host = "127.0.0.1"
	}
	address := net.JoinHostPort(host, strconv.Itoa(c.Network.HTTPSPort))
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	client := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{
		DisableKeepAlives: true, TLSClientConfig: &tls.Config{RootCAs: roots, ServerName: u.Hostname(), MinVersion: tls.VersionTLS12},
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, network, address)
		},
	}}
	request, e := http.NewRequestWithContext(ctx, http.MethodGet, origin+"/health/ready", nil)
	if e != nil {
		return e
	}
	response, e := client.Do(request)
	if e != nil {
		return e
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d", response.StatusCode)
	}
	return nil
}

// ServiceStatus is one observed container.
type ServiceStatus struct {
	Service string `json:"Service"`
	State   string `json:"State"`
	Health  string `json:"Health"`
}

// Status reports what is running now, read from Docker and the published port.
func Status(ctx context.Context, c Config, runner Runner, out io.Writer) error {
	return status(ctx, c, runner, ProbeOrigin, out)
}

func status(ctx context.Context, c Config, runner Runner, probe Probe, out io.Writer) error {
	l := c.Layout()
	if !exists(l.ComposeEnv) {
		return errors.New("INSTALL_NOT_FOUND: nothing is installed at " + l.Root)
	}
	output, e := runner.Run(ctx, "docker", "compose", "--env-file", l.ComposeEnv, "-f", l.ComposeFile, "ps", "--all", "--format", "json")
	if e != nil {
		return errors.New("INSTALL_COMPOSE_FAILED: " + lastLines(output))
	}
	healthy := map[string]bool{}
	decoder := json.NewDecoder(strings.NewReader(output))
	for {
		var service ServiceStatus
		if e = decoder.Decode(&service); errors.Is(e, io.EOF) {
			break
		}
		if e != nil {
			return errors.New("INSTALL_COMPOSE_OUTPUT_INVALID")
		}
		healthy[service.Service] = service.State == "running" && service.Health == "healthy"
		fmt.Fprintf(out, "service %s: state=%s health=%s\n", service.Service, service.State, service.Health)
	}
	problem := ""
	for _, service := range []string{"postgres", "metrics", "control"} {
		if !healthy[service] {
			problem = service + " is not healthy"
		}
	}
	if output, e := logsReady(ctx, c, runner); e != nil {
		fmt.Fprintf(out, "service logs: not ready: %s\n", lastLines(output))
		problem = "logs is not ready"
	}
	for _, origin := range []string{c.Origins.Portal, c.Origins.Admin} {
		if e = probe(ctx, c, origin); e != nil {
			fmt.Fprintf(out, "origin %s: %s\n", origin, e.Error())
			problem = origin + " is not ready"
			continue
		}
		fmt.Fprintf(out, "origin %s: ready\n", origin)
	}
	if problem != "" {
		return errors.New("INSTALL_NOT_HEALTHY: " + problem)
	}
	return nil
}

func containsLine(output, want string) bool {
	for _, line := range strings.Split(output, "\n") {
		if strings.TrimSpace(line) == want {
			return true
		}
	}
	return false
}

// lastLines keeps the end of a command's output, where the failure code is.
func lastLines(output string) string {
	trimmed := bytes.TrimSpace([]byte(output))
	if len(trimmed) > maximumDetailBytes {
		trimmed = trimmed[len(trimmed)-maximumDetailBytes:]
	}
	lines := strings.Split(string(trimmed), "\n")
	if len(lines) > 5 {
		lines = lines[len(lines)-5:]
	}
	return strings.Join(lines, " | ")
}
