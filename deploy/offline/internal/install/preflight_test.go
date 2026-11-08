package install

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	license "github.com/Passion-Flow/ownsolid/pkg/license/v1"
	"go.yaml.in/yaml/v3"
)

func TestPreflightRefusesUnsafePolicy(t *testing.T) {
	p := testPreflightPolicy()
	for _, test := range []struct {
		name string
		edit func(*PreflightPolicy)
	}{
		{"kernel", func(p *PreflightPolicy) { p.MinimumKernel = "6.8" }},
		{"budget", func(p *PreflightPolicy) { p.MinimumDataFreeBytes = 0 }},
		{"reference", func(p *PreflightPolicy) { p.MinimumUTC = "today" }},
		{"external resolver", func(p *PreflightPolicy) { p.DNSServer = "192.0.2.1:53" }},
		{"resolver name", func(p *PreflightPolicy) { p.DNSServer = "dns.example.test:53" }},
		{"relative containerd path", func(p *PreflightPolicy) { p.ContainerdRoot = "../containerd" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			p := p
			test.edit(&p)
			if err := p.validate(); err == nil {
				t.Fatal("invalid policy was accepted")
			}
		})
	}
}

func testPreflightPolicy() PreflightPolicy {
	return PreflightPolicy{
		MinimumKernel: "5.15.0", MinimumUTC: "2026-01-01T00:00:00Z",
		MinimumMemoryBytes: 1 << 30, MinimumDataFreeBytes: 1 << 28, MinimumDockerFreeBytes: 1 << 30,
	}
}

func TestPreflightChecksKernelClockAndMemory(t *testing.T) {
	for _, test := range []struct {
		release string
		want    bool
	}{{"5.15.0", true}, {"6.8.0-147-generic", true}, {"5.14.99", false}, {"latest", false}} {
		if err := checkKernel(test.release, "5.15.0"); (err == nil) != test.want {
			t.Errorf("kernel %s: got %v", test.release, err)
		}
	}
	now := time.Now()
	if err := checkClock(now, now.Add(time.Second), false); err == nil {
		t.Fatal("clock before reference was accepted")
	}
	if err := checkClock(now, now, false); err != nil {
		t.Fatal(err)
	}
	if err := checkMemory(10, 11); err == nil {
		t.Fatal("insufficient memory was accepted")
	}
}

func TestPreflightDoesNotUseTheSystemResolver(t *testing.T) {
	c := validConfig(t)
	c.Origins.Admin = "https://unresolved-ownsolid-preflight.invalid:8443"
	if err := checkOriginResolution(t.Context(), c, ""); err == nil || !strings.Contains(err.Error(), "INSTALL_PREFLIGHT_DNS") {
		t.Fatalf("got %v", err)
	}
	hosts, err := hostMappings(strings.NewReader("127.0.0.1 portal.example.test ALIAS.example.test. # local\n0.0.0.0 sink.example.test\n"))
	if err != nil || !hosts["portal.example.test"] || !hosts["alias.example.test"] || hosts["sink.example.test"] {
		t.Fatalf("got %v, %v", hosts, err)
	}
}

func TestPreflightRefusesPortOwnedByAnotherDockerProject(t *testing.T) {
	c := validConfig(t)
	// Docker can publish via NAT without reserving a kernel listener.
	runner := &fakeRunner{answer: func(command string) (string, error) {
		if strings.Contains(command, " ps ") {
			return "container-id", nil
		}
		return `{"Project":"another","Service":"control","Ports":{"8443/tcp":[{"HostIP":"0.0.0.0","HostPort":"8443"}]}}`, nil
	}}
	if err := checkPublishedPort(t.Context(), c, runner); err == nil || !strings.Contains(err.Error(), "another Docker service") {
		t.Fatalf("got %v", err)
	}
}

func TestPreflightRequiresContainerdStorageLocation(t *testing.T) {
	c := validConfig(t)
	host := dockerHostInfo{DockerRootDir: shortTempDir(t), DriverStatus: [][2]string{{"driver-type", "io.containerd.snapshotter.v1"}}}
	p := testPreflightPolicy()
	if err := checkDisk(c, host, p); err == nil || !strings.Contains(err.Error(), "preflight.containerd_root") {
		t.Fatalf("got %v", err)
	}
	p.ContainerdRoot = host.DockerRootDir
	p.MinimumDockerFreeBytes = ^uint64(0)
	if err := checkDisk(c, host, p); err == nil || !strings.Contains(err.Error(), "needs data reserve") {
		t.Fatalf("got %v", err)
	}
}

func TestPreflightRefusesRemoteDockerBeforeContact(t *testing.T) {
	t.Setenv("DOCKER_HOST", "tcp://192.0.2.10:2376")
	runner := &fakeRunner{}
	err := checkLocalDocker(t.Context(), runner)
	if err == nil || !strings.Contains(err.Error(), "INSTALL_PREFLIGHT_DOCKER_ENDPOINT") || len(runner.commands) != 0 {
		t.Fatalf("got %v after commands %v", err, runner.commands)
	}
}

func TestPreflightRequiresAnInstalledNVIDIADriverAndRuntime(t *testing.T) {
	host := dockerHostInfo{}
	runner := &fakeRunner{}
	if err := checkNVIDIA(t.Context(), runner, host); err == nil || len(runner.commands) != 0 {
		t.Fatalf("missing runtime: got %v after commands %v", err, runner.commands)
	}
	host.Runtimes = map[string]json.RawMessage{"nvidia": nil}
	if err := checkNVIDIA(t.Context(), runner, host); err == nil || len(runner.commands) != 1 {
		t.Fatalf("missing GPU: got %v after commands %v", err, runner.commands)
	}
	runner.answer = func(string) (string, error) { return "580.82.07", nil }
	if err := checkNVIDIA(t.Context(), runner, host); err != nil {
		t.Fatal(err)
	}
}

// TestPreflightIsolatedDocker is the lane-H payload for tests/e2e/run-single-node.sh.
// It needs an isolated Docker-in-Docker daemon, the existing egress recorder and
// the production Compose images, all mounted by that runner.
func TestPreflightIsolatedDocker(t *testing.T) {
	installer := os.Getenv("OWNSOLID_E2E_INSTALLER")
	logPath := os.Getenv("OWNSOLID_E2E_EGRESS_LOG")
	if installer == "" || logPath == "" || os.Getenv("OWNSOLID_E2E_EGRESS_INTERFACE") == "" {
		t.Skip("requires the Linux zero-egress runner; see deploy/offline/README.md")
	}
	interfaces, err := net.Interfaces()
	if err != nil {
		t.Fatal(err)
	}
	for _, iface := range interfaces {
		if iface.Name != "lo" && iface.Name != "docker0" && iface.Name != os.Getenv("OWNSOLID_E2E_EGRESS_INTERFACE") {
			t.Fatalf("unexpected interface %s on the isolated host", iface.Name)
		}
	}
	run := func(name string, args ...string) (string, error) {
		output, err := exec.CommandContext(t.Context(), name, args...).CombinedOutput()
		return string(output), err
	}
	t.Cleanup(func() {
		file, err := os.Open(logPath)
		if err != nil {
			t.Error(err)
			return
		}
		defer file.Close()
		seen := map[string]int{}
		const hostProbe, workloadProbe = "192.0.2.1:9", "192.0.2.2:9"
		lines := bufio.NewScanner(file)
		for lines.Scan() {
			var attempt struct {
				Destination string `json:"destination"`
			}
			if err := json.Unmarshal(lines.Bytes(), &attempt); err != nil || (attempt.Destination != hostProbe && attempt.Destination != workloadProbe) {
				t.Errorf("unexpected outbound attempt: %s", lines.Bytes())
			} else {
				seen[attempt.Destination]++
			}
		}
		if err := lines.Err(); err != nil {
			t.Error(err)
		}
		if seen[hostProbe] == 0 || seen[workloadProbe] == 0 {
			t.Error("recorder did not see both isolation probes")
		}
		t.Logf("egress recorder saw %d host probes, %d workload probes and zero unexpected attempts if the test passed", seen[hostProbe], seen[workloadProbe])
	})
	if conn, err := net.DialTimeout("tcp", "192.0.2.1:9", 3*time.Second); err == nil {
		conn.Close()
		t.Fatal("isolation probe escaped the test host")
	}

	// The advertised hostnames are staged in the isolated host's own hosts file.
	hosts, err := os.OpenFile("/etc/hosts", os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, err = fmt.Fprintln(hosts, "127.0.0.1 admin.lane-h.test portal.lane-h.test")
	if err = errors.Join(err, hosts.Close()); err != nil {
		t.Fatal(err)
	}

	c := defaults()
	c.FormatVersion = FormatVersion
	c.InstallRoot = os.Getenv("OWNSOLID_E2E_ROOT")
	c.ComposeFile = os.Getenv("OWNSOLID_E2E_COMPOSE_FILE")
	c.Project = "ownsolid-lane-h"
	c.Images = ImagesConfig{
		ArchiveFile: os.Getenv("OWNSOLID_E2E_IMAGE_ARCHIVE"), Control: os.Getenv("OWNSOLID_E2E_CONTROL_IMAGE"),
		Postgres: os.Getenv("OWNSOLID_E2E_POSTGRES_IMAGE"), Metrics: os.Getenv("OWNSOLID_E2E_METRICS_IMAGE"), Logs: os.Getenv("OWNSOLID_E2E_LOGS_IMAGE"),
	}
	c.Origins = OriginsConfig{Admin: "https://admin.lane-h.test:8443", Portal: "https://portal.lane-h.test:8443"}
	c.SMTP.Host, c.SMTP.FromAddress = "smtp.lane-h.test", "platform@lane-h.test"
	c.Administrator = AdminConfig{
		Email: "admin@lane-h.test", DisplayName: "Installation Administrator", InstallationName: "Lane H", WorkspaceName: "Default Workspace",
	}
	dir := t.TempDir()
	c.License.TrustFile = filepath.Join(dir, "license-trust.json")
	_, trust, err := license.GenerateKey("lane-h-test")
	if err != nil {
		t.Fatal(err)
	}
	trustJSON, err := json.Marshal(trust)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(c.License.TrustFile, trustJSON, 0o600); err != nil {
		t.Fatal(err)
	}
	policy := testPreflightPolicy()
	// This runner starts an embedded containerd with the Docker daemon. Its
	// data root is below the daemon's own root.
	storage, err := run("docker", "info", "--format", "{{.DockerRootDir}}")
	if err != nil {
		t.Fatalf("Docker storage: %v: %s", err, storage)
	}
	policy.ContainerdRoot = filepath.Join(strings.TrimSpace(storage), "containerd", "daemon")
	c.Preflight = &policy
	config := filepath.Join(dir, "install.yaml")
	command := func(t *testing.T, action, want string, fail bool) {
		t.Helper()
		raw, err := yaml.Marshal(c)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(config, raw, 0o600); err != nil {
			t.Fatal(err)
		}
		out, err := run(installer, action, "--config", config)
		t.Logf("%s:\n%s", action, out)
		if (err != nil) != fail || !strings.Contains(out, want) {
			t.Fatalf("want %s (fail=%t), got %v", want, fail, err)
		}
	}

	t.Run("refuses without preparing files", func(t *testing.T) {
		original := policy.MinimumMemoryBytes
		policy.MinimumMemoryBytes = ^uint64(0)
		command(t, "apply", "INSTALL_PREFLIGHT_MEMORY", true)
		policy.MinimumMemoryBytes = original
		if _, err := os.Stat(c.Layout().ComposeFile); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("failed preflight prepared installation files: %v", err)
		}
	})
	t.Run("reports a busy HTTPS port", func(t *testing.T) {
		listener, err := net.Listen("tcp4", net.JoinHostPort(c.Network.BindAddress, fmt.Sprint(c.Network.HTTPSPort)))
		if err != nil {
			t.Fatal(err)
		}
		defer listener.Close()
		command(t, "preflight", "INSTALL_PREFLIGHT_PORT", true)
	})
	t.Run("installs and safely repeats with its own HTTPS port in use", func(t *testing.T) {
		command(t, "preflight", "INSTALL_PREFLIGHT_PASSED", false)
		command(t, "apply", "INSTALL_COMPLETE", false)
		credential, err := os.ReadFile(c.CredentialPath())
		if err != nil {
			t.Fatal(err)
		}
		command(t, "apply", "INSTALL_COMPLETE", false)
		repeated, err := os.ReadFile(c.CredentialPath())
		if err != nil || !bytes.Equal(credential, repeated) {
			t.Fatal("repeat apply changed the administrator credential")
		}
		command(t, "status", c.Origins.Portal+": ready", false)
		// Prove a packet from a running application container also reaches the recorder.
		probe := "import socket,sys; s=socket.socket(); s.settimeout(3); sys.exit(0 if s.connect_ex(('192.0.2.2',9)) else 1)"
		layout := c.Layout()
		if out, err := run("docker", "compose", "--env-file", layout.ComposeEnv, "-f", layout.ComposeFile, "exec", "--no-TTY", "control", "python3", "-c", probe); err != nil {
			t.Fatalf("workload isolation probe: %v: %s", err, out)
		}
	})
}
