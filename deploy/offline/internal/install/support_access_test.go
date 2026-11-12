package install

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func supportAccessEvents(t *testing.T, directory string) []supportAccessEvent {
	t.Helper()
	file, err := os.Open(filepath.Join(directory, operationLogName))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	var events []supportAccessEvent
	for {
		var event supportAccessEvent
		if err := decoder.Decode(&event); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		if event.Action == "diagnostics" {
			events = append(events, event)
		}
	}
	return events
}

func checkSupportCompletion(t *testing.T, directory, archive, kind string) []supportAccessEvent {
	t.Helper()
	events := supportAccessEvents(t, directory)
	if len(events) != 2 || events[0].Outcome != "admitted" || events[1].Outcome != "complete" ||
		events[0].Attempt == "" || events[0].Attempt != events[1].Attempt {
		t.Fatalf("wrong diagnostic lifecycle: %+v", events)
	}
	raw, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(raw)
	for _, event := range events {
		if event.Version != 1 || event.UID != os.Getuid() || event.EffectiveUID != os.Geteuid() || event.Kind != kind ||
			len(event.InputSHA256) != 64 || event.Time.IsZero() {
			t.Fatalf("wrong actual operator binding: %+v", event)
		}
	}
	if events[1].ArchiveSHA256 != hex.EncodeToString(digest[:]) || events[1].ArchiveBytes != int64(len(raw)) {
		t.Fatal("terminal record did not bind the published archive bytes")
	}
	return events
}

func TestSupportAccessPrecedesLocalCollectorAndPersistsAcrossRetry(t *testing.T) {
	c := validConfig(t)
	t.Setenv("DOCKER_HOST", "unix:///var/run/docker.sock")
	t.Setenv("USER", "untrusted-environment-actor")
	t.Setenv("SUDO_UID", "999999")
	directory := c.Layout().Operator
	for n := range 2 {
		runner := &fakeRunner{answer: func(string) (string, error) {
			events := supportAccessEvents(t, directory)
			if len(events) != n*2+1 || events[len(events)-1].Outcome != "admitted" {
				t.Fatal("collector ran without its durable admission")
			}
			return "", errors.New("daemon unavailable")
		}}
		path := filepath.Join(shortTempDir(t), "support.tar.gz")
		if err := SupportBundle(t.Context(), c, runner, path, io.Discard); err != nil {
			t.Fatal(err)
		}
		if n == 0 {
			checkSupportCompletion(t, directory, path, "local")
		}
	}
	events := supportAccessEvents(t, directory)
	if len(events) != 4 || events[0].Attempt == events[2].Attempt {
		t.Fatal("explicit retry did not preserve two distinct attempts")
	}
	raw, err := os.ReadFile(filepath.Join(directory, operationLogName))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(c.InstallRoot)) || bytes.Contains(raw, []byte("untrusted-environment")) {
		t.Fatal("operator log contained raw paths or claimed environment identity")
	}
}

func TestSupportAccessPrecedesManagedHTTPSAndPreservesSourceAuthorization(t *testing.T) {
	directory := shortTempDir(t)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		events := supportAccessEvents(t, directory)
		if len(events) != 1 || events[0].Outcome != "admitted" {
			t.Error("managed read preceded durable admission")
		}
		if r.Header.Get("Authorization") != "Bearer "+supportTestBearer {
			t.Error("source authorization was omitted")
		}
		w.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()
	c := clusterSupportInput{Version: 1, TimeoutSeconds: 2,
		Helm: &helmSupportInput{writeClusterSupportEndpoint(t, server, supportTestBearer), supportTestNamespace, supportTestRelease}}
	raw, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	input, output := filepath.Join(directory, "cluster.json"), filepath.Join(directory, "support.tar.gz")
	if err := os.WriteFile(input, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ClusterSupportBundle(t.Context(), input, output, io.Discard); err != nil {
		t.Fatal(err)
	}
	checkSupportCompletion(t, directory, output, "managed_cluster")
	_, archive := readSupportArchive(t, output)
	for _, sentinel := range []string{supportTestBearer, server.URL, directory} {
		if bytes.Contains(archive, []byte(sentinel)) {
			t.Fatal("managed archive leaked a protected value")
		}
	}
}

func TestSupportAccessUnsafeLogStopsBothCollectors(t *testing.T) {
	for _, kind := range []string{"local", "managed_cluster"} {
		for _, fault := range []string{"symlink", "directory", "read-only", "unsafe-mode", "fifo", "hardlink"} {
			t.Run(kind+"/"+fault, func(t *testing.T) {
				c := validConfig(t)
				directory := c.Layout().Operator
				if kind == "managed_cluster" {
					directory = shortTempDir(t)
				}
				if err := os.MkdirAll(directory, 0o700); err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(directory, operationLogName)
				switch fault {
				case "symlink":
					if err := os.Symlink(filepath.Join(directory, "absent"), path); err != nil {
						t.Fatal(err)
					}
				case "directory":
					if err := os.Mkdir(path, 0o700); err != nil {
						t.Fatal(err)
					}
				case "fifo":
					if err := syscall.Mkfifo(path, 0o600); err != nil {
						t.Fatal(err)
					}
				default:
					value := []byte("{}\n")
					if err := os.WriteFile(path, value, 0o600); err != nil {
						t.Fatal(err)
					}
					if fault == "read-only" {
						if err := os.Chmod(path, 0o400); err != nil {
							t.Fatal(err)
						}
					}
					if fault == "unsafe-mode" {
						if err := os.Chmod(path, 0o644); err != nil {
							t.Fatal(err)
						}
					}
					if fault == "hardlink" {
						if err := os.Link(path, path+".other"); err != nil {
							t.Fatal(err)
						}
					}
				}
				output := filepath.Join(shortTempDir(t), "support.tar.gz")
				runner := &fakeRunner{answer: func(string) (string, error) {
					t.Error("unsafe log reached a local collector")
					return "", nil
				}}
				var err error
				if kind == "local" {
					err = SupportBundle(t.Context(), c, runner, output, io.Discard)
				} else {
					server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("unsafe log reached HTTPS") }))
					defer server.Close()
					config := clusterSupportInput{Version: 1, TimeoutSeconds: 1,
						Helm: &helmSupportInput{writeClusterSupportEndpoint(t, server, supportTestBearer), supportTestNamespace, supportTestRelease}}
					raw, _ := json.Marshal(config)
					input := filepath.Join(directory, "cluster.json")
					if err := os.WriteFile(input, raw, 0o600); err != nil {
						t.Fatal(err)
					}
					err = ClusterSupportBundle(t.Context(), input, output, io.Discard)
				}
				if err == nil || !strings.Contains(err.Error(), "INSTALL_SUPPORT_ACCESS_UNAVAILABLE") {
					t.Fatalf("unsafe receipt was accepted: %v", err)
				}
				if _, err := os.Lstat(output); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("unsafe admission produced an archive")
				}
			})
		}
	}
}

func TestSupportAccessTerminalFailureWithdrawsArchive(t *testing.T) {
	c := validConfig(t)
	t.Setenv("DOCKER_HOST", "unix:///var/run/docker.sock")
	output := filepath.Join(shortTempDir(t), "support.tar.gz")
	path := filepath.Join(c.Layout().Operator, operationLogName)
	runner := &fakeRunner{answer: func(string) (string, error) {
		if err := os.Chmod(path, 0o644); err != nil {
			t.Fatal(err)
		}
		return "", errors.New("daemon unavailable")
	}}
	var out bytes.Buffer
	err := SupportBundle(t.Context(), c, runner, output, &out)
	if err == nil || !strings.Contains(err.Error(), "INSTALL_SUPPORT_ACCESS_UNAVAILABLE") ||
		strings.Contains(out.String(), "INSTALL_SUPPORT_COMPLETE") {
		t.Fatalf("uncertain terminal access released success: %v", err)
	}
	if _, err := os.Lstat(output); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("uncertain terminal access released archive")
	}
	events := supportAccessEvents(t, c.Layout().Operator)
	if len(events) != 1 || events[0].Outcome != "admitted" {
		t.Fatal("uncertain outcome was retried or fabricated")
	}
}

func TestSupportAccessCancellationAndPublicationFailureAreRecorded(t *testing.T) {
	for _, fault := range []string{"cancelled", "publication"} {
		t.Run(fault, func(t *testing.T) {
			c := validConfig(t)
			t.Setenv("DOCKER_HOST", "unix:///var/run/docker.sock")
			output := filepath.Join(shortTempDir(t), "support.tar.gz")
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			runner := &fakeRunner{answer: func(string) (string, error) {
				if fault == "cancelled" {
					cancel()
				} else if err := os.Mkdir(output, 0o700); err != nil {
					t.Fatal(err)
				}
				return "", errors.New("daemon unavailable")
			}}
			if err := SupportBundle(ctx, c, runner, output, io.Discard); err == nil {
				t.Fatal("failed collection returned success")
			}
			events := supportAccessEvents(t, c.Layout().Operator)
			if len(events) != 2 || events[1].Outcome != "failed" || events[1].Failure == "" || events[1].ArchiveSHA256 != "" {
				t.Fatalf("wrong failed attempt: %+v", events)
			}
		})
	}
}

func TestSupportAccessConcurrentReadsKeepDistinctDurableAttempts(t *testing.T) {
	c := validConfig(t)
	t.Setenv("DOCKER_HOST", "unix:///var/run/docker.sock")
	directory := shortTempDir(t)
	var wg sync.WaitGroup
	for n := range 8 {
		wg.Go(func() {
			runner := &fakeRunner{answer: func(string) (string, error) { return "", errors.New("daemon unavailable") }}
			output := filepath.Join(directory, "support-"+itoa(n)+".tar.gz")
			if err := SupportBundle(t.Context(), c, runner, output, io.Discard); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	events := supportAccessEvents(t, c.Layout().Operator)
	attempts := map[string][]string{}
	for _, event := range events {
		attempts[event.Attempt] = append(attempts[event.Attempt], event.Outcome)
	}
	if len(attempts) != 8 {
		t.Fatalf("wrong concurrent attempts: %d", len(attempts))
	}
	for _, outcomes := range attempts {
		if strings.Join(outcomes, ",") != "admitted,complete" {
			t.Fatalf("wrong event order: %v", outcomes)
		}
	}
}

func TestSupportAccessHeldLogLockHonorsCancellation(t *testing.T) {
	rootPath := shortTempDir(t)
	path := filepath.Join(rootPath, operationLogName)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	defer syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if _, err := beginSupportAccess(ctx, root, "local", []byte("fixture")); err == nil {
		t.Fatal("held log lock ignored cancellation")
	}
	if info, err := file.Stat(); err != nil || info.Size() != 0 {
		t.Fatal("cancelled admission changed the log")
	}
}

func TestSupportAccessSeparatesAnIncompleteLegacyTailWithoutAdoptingIt(t *testing.T) {
	c := validConfig(t)
	t.Setenv("DOCKER_HOST", "unix:///var/run/docker.sock")
	legacy := "{\"time\":\"2026-11-09T02:00:00Z\",\"action\":\"backup\",\"outcome\":\"complete\"}"
	writeSupportLog(t, c, legacy)
	output := filepath.Join(shortTempDir(t), "support.tar.gz")
	runner := &fakeRunner{answer: func(string) (string, error) { return "", errors.New("daemon unavailable") }}
	if err := SupportBundle(t.Context(), c, runner, output, io.Discard); err != nil {
		t.Fatal(err)
	}
	events := supportAccessEvents(t, c.Layout().Operator)
	if len(events) != 2 || !events[0].PriorIncomplete || events[1].PriorIncomplete {
		t.Fatalf("legacy incompleteness was not retained: %+v", events)
	}
	raw, err := os.ReadFile(filepath.Join(c.Layout().Operator, operationLogName))
	if err != nil || !bytes.HasPrefix(raw, []byte(legacy+"\n")) {
		t.Fatal("the original legacy bytes were changed")
	}
	report, _ := readSupportArchive(t, output)
	if len(report.Operations) != 0 {
		t.Fatal("an incomplete pre-admission record was promoted into the current snapshot")
	}
}

func TestSupportAccessReplacedLogCannotReceiveCompletion(t *testing.T) {
	c := validConfig(t)
	t.Setenv("DOCKER_HOST", "unix:///var/run/docker.sock")
	path := filepath.Join(c.Layout().Operator, operationLogName)
	retained := path + ".retained"
	const sentinel = "foreign-replacement-content"
	runner := &fakeRunner{answer: func(string) (string, error) {
		if err := os.Rename(path, retained); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(sentinel), 0o600); err != nil {
			t.Fatal(err)
		}
		return "", errors.New("daemon unavailable")
	}}
	output := filepath.Join(shortTempDir(t), "support.tar.gz")
	err := SupportBundle(t.Context(), c, runner, output, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "INSTALL_SUPPORT_ACCESS_UNAVAILABLE") {
		t.Fatal("replacement log was accepted")
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != sentinel {
		t.Fatal("replacement log was modified")
	}
	raw, err := os.ReadFile(retained)
	if err != nil {
		t.Fatal(err)
	}
	var admission supportAccessEvent
	if json.Unmarshal(bytes.TrimSpace(raw), &admission) != nil || admission.Outcome != "admitted" {
		t.Fatal("original admission was changed")
	}
	if _, err := os.Lstat(output); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("replaced log released an archive")
	}
}
