package install

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	testIsolatedDocker(t, false, false)
}

// TestSignedBundleIsolatedDocker uses the same real zero-egress runner with a
// signed fixture manufactured from real Linux amd64 image archives.
func TestSignedBundleIsolatedDocker(t *testing.T) {
	testIsolatedDocker(t, true, false)
}

// TestSignedUpgradeIsolatedDocker uses real signed releases and PostgreSQL.
func TestSignedUpgradeIsolatedDocker(t *testing.T) {
	testIsolatedDocker(t, true, true)
}

func testIsolatedDocker(t *testing.T, signed, upgrade bool) {
	t.Helper()
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
	var signedArchive, verification string
	if signed {
		c.ComposeFile = ""
		c.Images = ImagesConfig{}
		signedArchive, verification = stageSignedFixture(t, run, upgrade)
	}
	command := func(t *testing.T, action, want string, fail bool) {
		t.Helper()
		raw, err := yaml.Marshal(c)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(config, raw, 0o600); err != nil {
			t.Fatal(err)
		}
		args := []string{action, "--config", config}
		if signed && action == "apply" {
			args[0] = "apply-bundle"
			args = append(args, "--bundle", signedArchive, "--verification", verification)
		}
		out, err := run(installer, args...)
		t.Logf("%s:\n%s", action, out)
		if (err != nil) != fail || !strings.Contains(out, want) {
			t.Fatalf("want %s (fail=%t), got %v", want, fail, err)
		}
	}

	if signed {
		t.Run("refuses a truncated signed archive without installing", func(t *testing.T) {
			original := signedArchive
			signedArchive = filepath.Join(filepath.Dir(signedArchive), "truncated.tar.gz")
			source, err := os.Open(original)
			if err != nil {
				t.Fatal(err)
			}
			bad, err := os.OpenFile(signedArchive, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
			if err != nil {
				source.Close()
				t.Fatal(err)
			}
			_, err = io.CopyN(bad, source, 512)
			if err := errors.Join(err, source.Close(), bad.Close()); err != nil {
				t.Fatal(err)
			}
			command(t, "apply", "INSTALL_BUNDLE_VERIFICATION_FAILED", true)
			signedArchive = original
			if _, err := os.Stat(c.Layout().ComposeFile); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("unverified bundle prepared an installation: %v", err)
			}
			if images, err := run("docker", "image", "ls", "--quiet"); err != nil || strings.TrimSpace(images) != "" {
				t.Fatalf("unverified bundle loaded Docker images: %v: %s", err, images)
			}
		})
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
	t.Run("uses an explicitly configured DNS server on the isolated site", func(t *testing.T) {
		original := c.Origins.Admin
		t.Cleanup(func() {
			c.Origins.Admin, policy.DNSServer = original, ""
		})
		c.Origins.Admin = "https://resolver-only.lane-h.test:8443"
		command(t, "preflight", "INSTALL_PREFLIGHT_DNS", true)
		policy.DNSServer = servePreflightDNS(t)
		command(t, "preflight", "INSTALL_PREFLIGHT_PASSED", false)
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
		if signed {
			images, err := run("docker", "image", "ls", "--format", "{{.Repository}}:{{.Tag}}")
			if err != nil {
				t.Fatal(err)
			}
			for _, image := range strings.Fields(images) {
				if image != "<none>:<none>" {
					t.Fatalf("signed bundle restored an image tag: %s", image)
				}
			}
		}
		// Prove a packet from a running application container also reaches the recorder.
		probe := "import socket,sys; s=socket.socket(); s.settimeout(3); sys.exit(0 if s.connect_ex(('192.0.2.2',9)) else 1)"
		layout := c.Layout()
		if out, err := run("docker", "compose", "--env-file", layout.ComposeEnv, "-f", layout.ComposeFile, "exec", "--no-TTY", "control", "python3", "-c", probe); err != nil {
			t.Fatalf("workload isolation probe: %v: %s", err, out)
		}
	})
	if upgrade {
		t.Run("upgrades the real database after a verified backup", func(t *testing.T) {
			backupRoot := t.TempDir()
			if err := os.Chmod(backupRoot, 0o700); err != nil {
				t.Fatal(err)
			}
			newArchive := filepath.Join(filepath.Dir(signedArchive), "upgrade.tar.gz")
			runBundle := func(action, archive string, args ...string) (string, error) {
				command := []string{action, "--config", config, "--verification", verification, "--bundle", archive}
				return run(installer, append(command, args...)...)
			}
			out, err := runBundle("upgrade-plan", newArchive)
			t.Logf("upgrade-plan: %s", out)
			var plan upgradePlan
			if err != nil || json.Unmarshal([]byte(out), &plan) != nil || !plan.MigrationRequired || plan.CurrentSchema >= plan.TargetSchema {
				t.Fatalf("upgrade plan: %v: %s", err, out)
			}
			if out, err := runBundle("upgrade-bundle", newArchive, "--backup-output", c.InstallRoot); err == nil || !strings.Contains(out, "INSTALL_BACKUP_OUTPUT_INVALID") {
				t.Fatalf("backup path inside the installation was accepted: %v: %s", err, out)
			}
			command(t, "status", c.Origins.Portal+": ready", false)
			out, err = runBundle("upgrade-bundle", newArchive, "--backup-output", backupRoot)
			t.Logf("upgrade-bundle:\n%s", out)
			if err != nil || !strings.Contains(out, "UPGRADE_BACKUP_VERIFIED") || !strings.Contains(out, "UPGRADE_COMPLETE") {
				t.Fatalf("upgrade: %v: %s", err, out)
			}
			backups, err := filepath.Glob(filepath.Join(backupRoot, "ownsolid-upgrade-*", "ownsolid-backup-*"))
			if err != nil || len(backups) != 1 {
				t.Fatalf("expected one verified backup, got %v: %v", backups, err)
			}
			recovery, err := readBackup(backups[0])
			if err != nil || recovery.SchemaVersion != plan.CurrentSchema {
				t.Fatalf("pre-upgrade backup: %v, schema %d", err, recovery.SchemaVersion)
			}
			out, err = runBundle("upgrade-bundle", newArchive, "--backup-output", backupRoot)
			if err != nil || !strings.Contains(out, "UPGRADE_ALREADY_ACTIVE") {
				t.Fatalf("repeat upgrade: %v: %s", err, out)
			}
			out, err = runBundle("rollback-bundle", signedArchive)
			t.Logf("rollback-bundle with advanced schema: %s", out)
			if err == nil || !strings.Contains(out, "INSTALL_ROLLBACK_DATABASE_RESTORE_REQUIRED") {
				t.Fatalf("schema-unsafe rollback was not refused: %v: %s", err, out)
			}
			command(t, "status", c.Origins.Portal+": ready", false)

			prior, err := readBundleBinding(c)
			if err != nil || prior == nil {
				t.Fatalf("missing verified upgrade binding: %v", err)
			}
			t.Run("recovers a same-schema candidate startup failure", func(t *testing.T) {
				completeCandidate := filepath.Join(filepath.Dir(signedArchive), "patch.tar.gz")
				policy, err := LoadBundleVerification(verification)
				if err != nil {
					t.Fatal(err)
				}
				var output bytes.Buffer
				err = UpgradeBundle(t.Context(), config, completeCandidate, backupRoot, policy, failCandidateStart{}, &output)
				t.Logf("failed candidate:\n%s\n%v", output.String(), err)
				if err == nil || !strings.Contains(err.Error(), "INSTALL_CONTROL_START_FAILED") ||
					!strings.Contains(output.String(), "UPGRADE_BACKUP_VERIFIED") {
					t.Fatalf("expected a verified recovery point before the candidate start failure: %v", err)
				}
				intent, err := readUpgradeIntent(c)
				if err != nil || intent == nil || intent.Source != *prior || intent.Target == *prior {
					t.Fatalf("expected a pending upgrade that retains the prior release: %v", err)
				}
				layout := c.Layout()
				runPostgres := func() string {
					t.Helper()
					id, err := run("docker", "compose", "--env-file", layout.ComposeEnv, "-f", layout.ComposeFile, "ps", "-q", "postgres")
					if err != nil || strings.TrimSpace(id) == "" {
						t.Fatalf("PostgreSQL did not stay running: %v: %s", err, id)
					}
					return strings.TrimSpace(id)
				}
				before := runPostgres()
				out, err := runBundle("rollback-bundle", newArchive)
				t.Logf("recover pending candidate:\n%s", out)
				if err != nil || !strings.Contains(out, "ROLLBACK_COMPLETE") {
					t.Fatalf("rollback of the failed candidate: %v: %s", err, out)
				}
				if after := runPostgres(); before != after {
					t.Fatal("application rollback replaced the database container")
				}
				if intent, err := readUpgradeIntent(c); err != nil || intent != nil {
					t.Fatalf("rollback did not resolve the pending upgrade: %v", err)
				}
				command(t, "status", c.Origins.Portal+": ready", false)
			})
			patchArchive := filepath.Join(filepath.Dir(signedArchive), "delta.tar.gz")
			if out, err := runBundle("upgrade-patch", patchArchive, "--baseline", signedArchive, "--backup-output", backupRoot); err == nil || !strings.Contains(out, "RELEASE_PATCH_BASELINE_MISMATCH") {
				t.Fatalf("wrong patch baseline was accepted: %v: %s", err, out)
			}
			out, err = runBundle("upgrade-patch", patchArchive, "--baseline", newArchive, "--backup-output", backupRoot)
			t.Logf("upgrade-patch:\n%s", out)
			if err != nil || !strings.Contains(out, "UPGRADE_COMPLETE") {
				t.Fatalf("same-schema application upgrade: %v: %s", err, out)
			}
			out, err = runBundle("upgrade-patch", patchArchive, "--baseline", newArchive, "--backup-output", backupRoot)
			if err != nil || !strings.Contains(out, "UPGRADE_ALREADY_ACTIVE") {
				t.Fatalf("repeat incremental upgrade: %v: %s", err, out)
			}
			patch, err := readBundleBinding(c)
			if err != nil || patch == nil || patch.ImageIDs[0] == prior.ImageIDs[0] {
				t.Fatalf("application upgrade did not change the running release: %v", err)
			}
			out, err = runBundle("rollback-bundle", newArchive)
			t.Logf("rollback-bundle with matching schema:\n%s", out)
			if err != nil || !strings.Contains(out, "ROLLBACK_COMPLETE") {
				t.Fatalf("application rollback: %v: %s", err, out)
			}
			restored, err := readBundleBinding(c)
			if err != nil || restored == nil || *restored != *prior {
				t.Fatalf("application rollback restored the wrong binding: %v", err)
			}
			layout := c.Layout()
			id, err := run("docker", "compose", "--env-file", layout.ComposeEnv, "-f", layout.ComposeFile, "ps", "-q", "control")
			if err != nil || strings.TrimSpace(id) == "" {
				t.Fatalf("cannot inspect restored control: %v", err)
			}
			image, err := run("docker", "inspect", "--format", "{{.Image}}", strings.TrimSpace(id))
			if err != nil || strings.TrimSpace(image) != prior.ImageIDs[0] {
				t.Fatalf("restored control image %q: %v", image, err)
			}
		})
	}

	t.Run("collects support with control stopped", func(t *testing.T) {
		layout := c.Layout()
		if out, err := run("docker", "compose", "--env-file", layout.ComposeEnv, "-f", layout.ComposeFile, "stop", "control"); err != nil {
			t.Fatalf("stop control: %v: %s", err, out)
		}
		path := filepath.Join(dir, "support.tar.gz")
		out, err := run(installer, "support-bundle", "--config", config, "--output", path)
		t.Logf("support-bundle: %s", out)
		if err != nil || !strings.Contains(out, "INSTALL_SUPPORT_COMPLETE collection_issues=0") {
			t.Fatalf("support while control is stopped: %v: %s", err, out)
		}
		report, _ := readSupportArchive(t, path)
		if !report.Host.Docker || report.SchemaVersion == nil || report.Services[0].State != "exited" {
			t.Fatalf("unexpected report: %+v", report)
		}
		t.Logf("support archive reports control %s and PostgreSQL schema %d", report.Services[0].State, *report.SchemaVersion)
	})
}

type failCandidateStart struct{ ExecRunner }

func (r failCandidateStart) Run(ctx context.Context, name string, args ...string) (string, error) {
	const start = "up --detach --wait --no-build control"
	if name == "docker" && strings.HasSuffix(strings.Join(args, " "), " "+start) {
		return "injected candidate startup failure", errors.New("candidate failed to start")
	}
	return r.ExecRunner.Run(ctx, name, args...)
}

// stageSignedFixture copies the archive and the independently passed trust file
// into a disposable real mount in the isolated host.
func stageSignedFixture(t *testing.T, run func(string, ...string) (string, error), upgrade bool) (string, string) {
	t.Helper()
	delivery := os.Getenv("OWNSOLID_E2E_MODEL_ARCHIVE")
	if delivery == "" {
		t.Fatal("the signed test needs the delivery directory mounted by the harness")
	}
	mount := t.TempDir()
	if out, err := run("mount", "-t", "tmpfs", "-o", "size=2g,mode=0700", "tmpfs", mount); err != nil {
		t.Fatalf("mount signed release staging: %v: %s", err, out)
	}
	t.Cleanup(func() {
		if out, err := exec.Command("umount", mount).CombinedOutput(); err != nil {
			t.Errorf("unmount signed release staging: %v: %s", err, out)
		}
	})
	files := []string{"ownsolid.tar.gz", "release-trust.json"}
	if upgrade {
		files = append(files, "upgrade.tar.gz", "patch.tar.gz", "delta.tar.gz")
	}
	for _, name := range files {
		input, err := os.Open(filepath.Join(delivery, name))
		if err != nil {
			t.Fatal(err)
		}
		output, err := os.OpenFile(filepath.Join(mount, name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			input.Close()
			t.Fatal(err)
		}
		_, err = io.Copy(output, input)
		if err := errors.Join(err, input.Close(), output.Close()); err != nil {
			t.Fatal(err)
		}
	}
	policy := filepath.Join(mount, "verification.json")
	raw, err := json.Marshal(map[string]any{"mount_root": mount, "trust_file": filepath.Join(mount, "release-trust.json"), "minimum_free_bytes": 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(policy, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(mount, "ownsolid.tar.gz"), policy
}
