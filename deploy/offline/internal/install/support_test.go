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
	"strings"
	"sync"
	"testing"
)

func TestSupportBundleReportsStoppedControlWithoutCollectingSecrets(t *testing.T) {
	c := validConfig(t)
	t.Setenv("DOCKER_HOST", "unix:///var/run/docker.sock")
	path := filepath.Join(shortTempDir(t), "support.tar.gz")
	const sentinel = "sensitive-fixture-value"
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
