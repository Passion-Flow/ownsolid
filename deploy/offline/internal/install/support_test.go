package install

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSupportBundleReportsStoppedControlWithoutCollectingSecrets(t *testing.T) {
	c := validConfig(t)
	t.Setenv("DOCKER_HOST", "unix:///var/run/docker.sock")
	path := filepath.Join(shortTempDir(t), "support.tar.gz")
	const sentinel = "sensitive-fixture-value"
	c.Administrator.DisplayName = sentinel
	c.SMTP.PasswordFile = filepath.Join(c.InstallRoot, sentinel)
	c.ControlSettings = map[string]any{"custom": sentinel}
	c.Preflight = &PreflightPolicy{DNSServer: sentinel}
	writeSupportLog(t, c, `{"time":"2026-11-09T10:00:00+08:00","action":"backup","outcome":"failed","detail":"`+sentinel+`"}
{"time":"2026-11-09T10:01:00+08:00","action":"`+sentinel+`","outcome":"failed"}
{"time":"2026-11-09T10:02:00+08:00","action":"restore","outcome":"complete"}
{"time":"2026-11-09T10:03:00+08:00","action":"backup","outcome":"`+sentinel+`"}
{"time":"`+sentinel+`","action":"backup","outcome":"failed"}
{"time":"2026-11-09T10:04:00+08:00","action":"backup","outcome":"complete"}`)
	runner := &fakeRunner{answer: func(command string) (string, error) {
		switch {
		case strings.Contains(command, " ps --all "):
			return `{"Service":"control","State":"exited","Health":"","Env":"` + sentinel + `"}
{"Service":"postgres","State":"running","Health":"healthy","Command":"` + sentinel + `"}
{"Service":"metrics","State":"` + sentinel + `","Health":"` + sentinel + `"}
{"Service":"` + sentinel + `","State":"running","Health":"healthy"}`, nil
		case strings.Contains(command, "schema_migrations"):
			return "52\n", nil
		case strings.Contains(command, "docker version"):
			return sentinel, nil
		default:
			t.Fatalf("unexpected command: %s", command)
			return "", nil
		}
	}}
	var out bytes.Buffer
	if err := SupportBundle(t.Context(), c, runner, path, &out); err != nil {
		t.Fatal(err)
	}
	report, raw := readSupportArchive(t, path)
	if bytes.Contains(raw, []byte(sentinel)) || strings.Contains(out.String(), sentinel) {
		t.Fatal("archive or command output contained unapproved data")
	}
	if !report.Host.Docker || report.Topology != SingleNode || report.SchemaVersion == nil || *report.SchemaVersion != 52 ||
		report.Services[0].State != "exited" || report.Services[1].Health != "healthy" ||
		report.Services[2].State != "unknown" || report.Services[3].State != "not_found" {
		t.Fatalf("unexpected report: %+v", report)
	}
	if report.Configuration.TLSSource != c.TLS.Source || !report.Configuration.PreflightEnabled ||
		report.Configuration.PostgresMaxConnections != c.Resources.PostgresMaxConnections ||
		report.Configuration.Resources["control"].Memory != c.Resources.ControlMemory {
		t.Fatalf("unexpected support configuration: %+v", report.Configuration)
	}
	if len(report.Operations) != 2 || report.Operations[0].Action != "backup" ||
		report.Operations[0].Outcome != "failed" || report.Operations[0].Time.Format(time.RFC3339) != "2026-11-09T02:00:00Z" ||
		report.Operations[1].Action != "restore" || report.Operations[1].Outcome != "complete" {
		t.Fatalf("unexpected operations: %+v", report.Operations)
	}
}

func TestSupportBundleProjectsOnlyTheValidatedRestorePhase(t *testing.T) {
	t.Setenv("DOCKER_HOST", "unix:///var/run/docker.sock")
	const suffix = "fedcba9876543210fedcba98"
	for _, phase := range []string{"absent", restoreLoading, restorePending, restoreServing, restoreRollback, "foreign", "symlink", "invalid"} {
		t.Run(phase, func(t *testing.T) {
			c := installedConfig(t)
			site, err := restoreSite(c)
			if err != nil {
				t.Fatal(err)
			}
			intent := restoreIntent{Version: 1, Site: site, Phase: phase, Suffix: suffix, Cluster: 7482983513342374911, Original: 97433113, Incoming: 97433114}
			want := phase
			wantIssue := false
			record := filepath.Join(c.InstallRoot, restoreIntentName)
			switch phase {
			case "absent":
				want = ""
			case "symlink":
				want, wantIssue = "", true
				if err := os.Symlink(c.License.TrustFile, record); err != nil {
					t.Fatal(err)
				}
			default:
				if phase == "foreign" {
					intent.Phase = restoreLoading
					intent.Site = strings.Repeat("a", 64)
				}
				if phase == "foreign" || phase == "invalid" {
					want, wantIssue = "", true
				}
				if err := writeReleaseRecord(c, restoreIntentName, intent); err != nil {
					t.Fatal(err)
				}
			}
			path := filepath.Join(t.TempDir(), "support.tar.gz")
			runner := &fakeRunner{answer: func(string) (string, error) { return "", errors.New("docker unavailable") }}
			if err := SupportBundle(t.Context(), c, runner, path, io.Discard); err != nil {
				t.Fatal(err)
			}
			report, raw := readSupportArchive(t, path)
			if report.Host.Docker || report.RestorePhase != want || slices.Contains(report.Issues, "RESTORE_STATE_UNAVAILABLE") != wantIssue {
				t.Fatalf("restore phase=%q, collection issues=%v", report.RestorePhase, report.Issues)
			}
			for _, excluded := range []string{suffix, site, c.InstallRoot, "7482983513342374911", "97433113", "97433114"} {
				if bytes.Contains(raw, []byte(excluded)) {
					t.Fatal("archive included protected restore metadata")
				}
			}
		})
	}
}

func TestSupportBundleProjectsOnlyCoordinatedBackupRecovery(t *testing.T) {
	t.Setenv("DOCKER_HOST", "unix:///run/docker.sock")
	for _, tc := range []struct {
		name, state string
		issue       bool
	}{
		{"absent", "", false},
		{"valid", "recovery_required", false},
		{"unconfirmed", "fence_outcome_unconfirmed", false},
		{"foreign", "", true},
		{"unknown version", "", true},
		{"malformed", "", true},
		{"symlink", "", true},
		{"unsafe mode", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := installedConfig(t)
			site, err := restoreSite(c)
			if err != nil {
				t.Fatal(err)
			}
			container := strings.Repeat("3", 64)
			intent := coordinatedBackupIntent{Version: 1, Site: site, ControlID: container, WasUp: true,
				Database: coordinatedDB{Cluster: 7482983513342374911, OID: 97433113, Limit: 12}}
			record := filepath.Join(c.InstallRoot, coordinatedBackupIntentName)
			switch tc.name {
			case "absent":
			case "symlink":
				if err := os.Symlink(c.License.TrustFile, record); err != nil {
					t.Fatal(err)
				}
			case "malformed":
				if err := os.WriteFile(record, []byte("private unparsed contents"), 0o600); err != nil {
					t.Fatal(err)
				}
			default:
				switch tc.name {
				case "unconfirmed":
					until := time.Now().UTC().Add(-time.Second)
					intent.FenceUntil = &until
				case "foreign":
					intent.Site = strings.Repeat("a", 64)
				case "unknown version":
					intent.Version = 2
				}
				if err := writeReleaseRecord(c, coordinatedBackupIntentName, intent); err != nil {
					t.Fatal(err)
				}
				if tc.name == "unsafe mode" {
					if err := os.Chmod(record, 0o644); err != nil {
						t.Fatal(err)
					}
				}
			}
			path := filepath.Join(t.TempDir(), "support.tar.gz")
			runner := &fakeRunner{answer: func(string) (string, error) { return "", errors.New("docker unavailable") }}
			if err := SupportBundle(t.Context(), c, runner, path, io.Discard); err != nil {
				t.Fatal(err)
			}
			report, raw := readSupportArchive(t, path)
			if report.Host.Docker || report.BackupRecovery != tc.state ||
				slices.Contains(report.Issues, "BACKUP_RECOVERY_STATE_UNAVAILABLE") != tc.issue {
				t.Fatalf("backup recovery=%q, collection issues=%v", report.BackupRecovery, report.Issues)
			}
			for _, excluded := range []string{site, intent.Site, container, c.InstallRoot, "7482983513342374911",
				"97433113", "private unparsed contents", "fence_until"} {
				if bytes.Contains(raw, []byte(excluded)) {
					t.Fatal("archive included protected backup recovery metadata")
				}
			}
		})
	}
}

func writeSupportLog(t *testing.T, c Config, records string) {
	t.Helper()
	if err := os.MkdirAll(c.Layout().Operator, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(c.Layout().Operator, operationLogName), []byte(records), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestSupportOperationLogIsBounded(t *testing.T) {
	c := validConfig(t)
	const record = `{"time":"2026-11-09T02:00:00Z","action":"backup","outcome":"complete"}` + "\n"
	writeSupportLog(t, c, strings.Repeat(record, supportOperationLogBytes/len(record)+1))
	operations, err := collectSupportOperations(c)
	if err != nil || len(operations) != supportOperationLimit {
		t.Fatalf("recent operations count = %d: %v", len(operations), err)
	}
}

func TestSupportRefusesAnOperationLogSymlink(t *testing.T) {
	c := validConfig(t)
	if err := os.MkdirAll(c.Layout().Operator, 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(c.Layout().Operator, "external.log")
	if err := os.WriteFile(target, []byte(`{"time":"2026-11-09T02:00:00Z","action":"backup","outcome":"complete"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(c.Layout().Operator, operationLogName)); err != nil {
		t.Fatal(err)
	}
	operations, err := collectSupportOperations(c)
	if err == nil || len(operations) != 0 {
		t.Fatalf("a symlink was read as an operation log: %+v, %v", operations, err)
	}
}

func TestSupportBundleKeepsHostFactsWhenDockerFailsAndPreservesOutput(t *testing.T) {
	c := validConfig(t)
	t.Setenv("DOCKER_HOST", "unix:///var/run/docker.sock")
	path := filepath.Join(shortTempDir(t), "support.tar.gz")
	runner := &fakeRunner{answer: func(string) (string, error) {
		return "sensitive-diagnostic-fixture", errors.New("daemon unavailable")
	}}
	if err := SupportBundle(t.Context(), c, runner, path, io.Discard); err != nil {
		t.Fatal(err)
	}
	report, raw := readSupportArchive(t, path)
	if report.Host.Docker || report.Host.OS == "" || !bytes.Contains(raw, []byte("DOCKER_UNAVAILABLE")) ||
		bytes.Contains(raw, []byte("sensitive-diagnostic-fixture")) {
		t.Fatalf("unexpected report: %+v", report)
	}
	if err := SupportBundle(t.Context(), c, runner, path, io.Discard); err == nil || !strings.Contains(err.Error(), "INSTALL_SUPPORT_OUTPUT_EXISTS") {
		t.Fatalf("existing output was accepted: %v", err)
	}
	if _, after := readSupportArchive(t, path); !bytes.Equal(raw, after) {
		t.Fatal("existing output was changed")
	}
}

func TestSupportBundleConcurrentWritersPublishOnce(t *testing.T) {
	c := validConfig(t)
	t.Setenv("DOCKER_HOST", "unix:///var/run/docker.sock")
	path := filepath.Join(shortTempDir(t), "support.tar.gz")
	results := make(chan error, 2)
	start := make(chan struct{})
	var writers sync.WaitGroup
	for range 2 {
		writers.Go(func() {
			<-start
			runner := &fakeRunner{answer: func(string) (string, error) {
				return "", errors.New("daemon unavailable")
			}}
			results <- SupportBundle(t.Context(), c, runner, path, io.Discard)
		})
	}
	close(start)
	writers.Wait()
	close(results)
	published := 0
	for err := range results {
		if err == nil {
			published++
		} else if !strings.Contains(err.Error(), "INSTALL_SUPPORT_OUTPUT_EXISTS") {
			t.Fatal(err)
		}
	}
	if published != 1 {
		t.Fatalf("published %d archives, want one", published)
	}
	readSupportArchive(t, path)
}

func readSupportArchive(t *testing.T, path string) (supportReport, []byte) {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if info, err := file.Stat(); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("support output must be private: %v, %v", info, err)
	}
	compressed, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	defer compressed.Close()
	archive := tar.NewReader(compressed)
	header, err := archive.Next()
	if err != nil || header.Name != "support.json" {
		t.Fatalf("expected support.json: %v, %v", header, err)
	}
	raw, err := io.ReadAll(archive)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := archive.Next(); err != io.EOF {
		t.Fatalf("unexpected support bundle entry: %v", err)
	}
	var report supportReport
	if err := json.Unmarshal(raw, &report); err != nil {
		t.Fatal(err)
	}
	return report, raw
}
