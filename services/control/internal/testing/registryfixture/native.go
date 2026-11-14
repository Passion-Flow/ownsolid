//go:build registry_integration

// Package registryfixture starts only a digest-verified explicitly supplied
// native Distribution binary. It never downloads a binary/image or fakes HTTP.
package registryfixture

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

type Native struct {
	t                                                           *testing.T
	Binary, ExpectedSHA, Root, Endpoint, CAFile, CredentialFile string
	config, log                                                 string
	client                                                      *http.Client
	command                                                     *exec.Cmd
	done                                                        chan error
	timeout                                                     time.Duration
	memoryMiB                                                   int
	mu                                                          sync.Mutex
	closed                                                      bool
	evidence                                                    map[string]any
	pids                                                        []int
	gcRuns                                                      int
	gcLogs                                                      []string
	shutdowns                                                   []nativeShutdown
}

type nativeShutdown struct {
	PID      int    `json:"pid"`
	ExitCode int    `json:"exit_code"`
	Signal   string `json:"signal,omitempty"`
	Forced   bool   `json:"forced"`
}

func (n *Native) recordShutdown(command *exec.Cmd, forced bool) {
	observed := nativeShutdown{PID: command.Process.Pid, ExitCode: -1, Forced: forced}
	if command.ProcessState != nil {
		observed.ExitCode = command.ProcessState.ExitCode()
		if status, ok := command.ProcessState.Sys().(syscall.WaitStatus); ok && status.Signaled() {
			observed.Signal = status.Signal().String()
		}
	}
	n.shutdowns = append(n.shutdowns, observed)
	if forced || observed.ExitCode != 0 && observed.Signal != syscall.SIGTERM.String() {
		n.t.Error("actual native Registry shutdown failed", observed.ExitCode, observed.Signal)
	}
}

func fileSHA(name string) (string, error) {
	f, e := os.Open(name)
	if e != nil {
		return "", e
	}
	defer f.Close()
	h := sha256.New()
	if _, e = io.Copy(h, f); e != nil {
		return "", e
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
func New(t *testing.T, binary, expected string, seconds, memoryMiB int) *Native {
	t.Helper()
	if !filepath.IsAbs(binary) || len(expected) != 64 || seconds < 1 || memoryMiB < 128 {
		t.Fatal("actual native Registry artifact and bounded fixture policy required")
	}
	actual, e := fileSHA(binary)
	if e != nil || actual != expected {
		t.Fatal("native Registry bytes differ from explicit expected artifact")
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Duration(seconds)*time.Second)
	defer cancel()
	versionCommand := exec.CommandContext(ctx, binary, "--version")
	versionCommand.Env = fixtureEnvironment(memoryMiB)
	version, e := boundedCommand(versionCommand, 4096)
	if e != nil || len(version) > 4096 || !strings.Contains(strings.ToLower(string(version)), "registry") {
		t.Fatal("actual Distribution native version required")
	}
	root := t.TempDir()
	if e = os.Chmod(root, 0700); e != nil {
		t.Fatal(e)
	}
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	address := listener.Addr().String()
	listener.Close()
	n := &Native{t: t, Binary: binary, ExpectedSHA: expected, Root: root, Endpoint: "https://" + address, CAFile: filepath.Join(root, "ca.pem"), CredentialFile: filepath.Join(root, "credential.json"), config: filepath.Join(root, "registry.yaml"), log: filepath.Join(root, "registry.log"), timeout: time.Duration(seconds) * time.Second, memoryMiB: memoryMiB}
	n.tls()
	secret := make([]byte, 32)
	if _, e = rand.Read(secret); e != nil {
		t.Fatal(e)
	}
	password := base64.RawURLEncoding.EncodeToString(secret)
	hash, e := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if e != nil {
		t.Fatal(e)
	}
	n.write("htpasswd", []byte("synthetic-registry:"+string(hash)+"\n"))
	raw, _ := json.Marshal(map[string]string{"username": "synthetic-registry", "password": password})
	n.write("credential.json", raw)
	config := fmt.Sprintf("version: 0.1\nlog:\n  level: warn\nstorage:\n  filesystem:\n    rootdirectory: %q\n  delete:\n    enabled: true\nhttp:\n  addr: %q\n  tls:\n    certificate: %q\n    key: %q\nauth:\n  htpasswd:\n    realm: registry-fixture\n    path: %q\n", filepath.Join(root, "data"), address, filepath.Join(root, "server.pem"), filepath.Join(root, "server-key.pem"), filepath.Join(root, "htpasswd"))
	n.write("registry.yaml", []byte(config))
	roots := x509.NewCertPool()
	ca, _ := os.ReadFile(n.CAFile)
	if !roots.AppendCertsFromPEM(ca) {
		t.Fatal("fixture CA invalid")
	}
	n.client = &http.Client{Timeout: n.timeout, Transport: &http.Transport{Proxy: nil, TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("REGISTRY_FIXTURE_REDIRECT") }}
	t.Cleanup(n.close)
	if directory := os.Getenv("PROJECT_ONE_REGISTRY_EVIDENCE_DIRECTORY"); directory != "" {
		if !filepath.IsAbs(directory) {
			t.Fatal("explicit external retained evidence directory required")
		}
		record := map[string]any{"binary_sha256": expected, "version": strings.TrimSpace(string(version)), "endpoint": n.Endpoint, "scope": "actual ephemeral loopback native Distribution; no GPU qualification"}
		configSHA, _ := fileSHA(n.config)
		caSHA, _ := fileSHA(n.CAFile)
		record["configuration_sha256"], record["ca_sha256"] = configSHA, caSHA
		n.evidence = record
		b, _ := json.MarshalIndent(record, "", "  ")
		name := strings.NewReplacer("/", "-", " ", "-").Replace(t.Name())
		if e = os.WriteFile(filepath.Join(directory, name+"-native-prepared.json"), append(b, '\n'), 0600); e != nil {
			t.Fatal(e)
		}
	}
	n.start()
	return n
}
func (n *Native) write(name string, raw []byte) {
	n.t.Helper()
	if e := os.WriteFile(filepath.Join(n.Root, name), raw, 0600); e != nil {
		n.t.Fatal(e)
	}
}
func (n *Native) tls() {
	n.t.Helper()
	now := time.Now().UTC()
	caPublic, caPrivate, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		n.t.Fatal(e)
	}
	serial, e := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if e != nil {
		n.t.Fatal(e)
	}
	ca := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "Explicit ephemeral registry fixture CA"}, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(12 * time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	caDER, e := x509.CreateCertificate(rand.Reader, ca, ca, caPublic, caPrivate)
	if e != nil {
		n.t.Fatal(e)
	}
	n.write("ca.pem", pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}))
	public, private, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		n.t.Fatal(e)
	}
	leafSerial := new(big.Int).Add(serial, big.NewInt(1))
	leaf := &x509.Certificate{SerialNumber: leafSerial, Subject: pkix.Name{CommonName: "Loopback registry fixture"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(12 * time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, e := x509.CreateCertificate(rand.Reader, leaf, ca, public, caPrivate)
	if e != nil {
		n.t.Fatal(e)
	}
	key, e := x509.MarshalPKCS8PrivateKey(private)
	if e != nil {
		n.t.Fatal(e)
	}
	n.write("server.pem", pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	n.write("server-key.pem", pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: key}))
}
func (n *Native) start() {
	n.t.Helper()
	actual, e := fileSHA(n.Binary)
	if e != nil || actual != n.ExpectedSHA {
		n.t.Fatal("native binary changed")
	}
	log, e := os.OpenFile(n.log, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if e != nil {
		n.t.Fatal(e)
	}
	command := exec.Command(n.Binary, "serve", n.config)
	command.Env = fixtureEnvironment(n.memoryMiB)
	command.Stdout = log
	command.Stderr = log
	n.command = command
	n.done = make(chan error, 1)
	if e = command.Start(); e != nil {
		log.Close()
		n.t.Fatal(e)
	}
	n.pids = append(n.pids, command.Process.Pid)
	go func() { n.done <- command.Wait(); log.Close() }()
	deadline := time.Now().Add(n.timeout)
	for time.Now().Before(deadline) {
		if info, e := os.Stat(n.log); e != nil || info.Size() > 4<<20 {
			n.stop()
			n.t.Fatal("native fixture log capacity exceeded")
		}
		response, e := n.client.Get(n.Endpoint + "/v2/")
		if e == nil {
			response.Body.Close()
			if response.StatusCode == 401 {
				return
			}
		}
		select {
		case e := <-n.done:
			n.recordShutdown(command, false)
			n.command = nil
			n.t.Fatal("actual native Registry failed", e)
		default:
		}
		time.Sleep(100 * time.Millisecond)
	}
	n.stop()
	n.t.Fatal("actual native Registry startup timed out")
}
func (n *Native) stop() {
	n.t.Helper()
	if n.command == nil {
		return
	}
	if e := n.command.Process.Signal(syscall.SIGTERM); e != nil && !errors.Is(e, os.ErrProcessDone) {
		n.t.Error("owned native Registry stop failed")
	}
	forced := false
	select {
	case <-n.done:
	case <-time.After(n.timeout):
		forced = true
		n.command.Process.Kill()
		<-n.done
		n.t.Error("owned native Registry required forced stop")
	}
	n.recordShutdown(n.command, forced)
	n.command = nil
}
func (n *Native) GC() {
	n.t.Helper()
	n.stop()
	ctx, cancel := context.WithTimeout(n.t.Context(), n.timeout)
	defer cancel()
	command := exec.CommandContext(ctx, n.Binary, "garbage-collect", "--delete-untagged", n.config)
	command.Env = fixtureEnvironment(n.memoryMiB)
	output, e := boundedCommand(command, 4<<20)
	logName := fmt.Sprintf("gc-%d.log", len(n.gcLogs)+1)
	if len(output) > 4<<20 {
		output = output[:4<<20]
	}
	n.write(logName, output)
	n.gcLogs = append(n.gcLogs, logName)
	aggregate, writeErr := os.OpenFile(filepath.Join(n.Root, "gc.log"), os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0600)
	if writeErr != nil {
		n.t.Fatal("actual GC aggregate log unavailable")
	}
	_, writeErr = aggregate.Write(output)
	if writeErr == nil {
		writeErr = aggregate.Sync()
	}
	closeErr := aggregate.Close()
	if writeErr != nil || closeErr != nil {
		n.t.Fatal("actual GC evidence write failed")
	}
	if e != nil {
		n.t.Fatal("actual stopped-registry GC failed", e)
	}
	n.gcRuns++
	n.start()
}
func (n *Native) close() {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.closed {
		return
	}
	n.closed = true
	n.stop()
	n.client.CloseIdleConnections()
	if n.evidence != nil {
		n.evidence["status"] = "stopped_actual_native_distribution"
		n.evidence["pids"] = n.pids
		n.evidence["gc_runs"] = n.gcRuns
		n.evidence["shutdowns"] = n.shutdowns
		n.evidence["gc_logs"] = n.gcLogs
		actual, e := fileSHA(n.Binary)
		if e != nil || actual != n.ExpectedSHA {
			n.t.Error("native Registry artifact changed before shutdown")
		}
		n.evidence["binary_sha256_after"] = actual
	}
	if directory := os.Getenv("PROJECT_ONE_REGISTRY_EVIDENCE_DIRECTORY"); directory != "" {
		name := strings.NewReplacer("/", "-", " ", "-").Replace(n.t.Name())
		if n.evidence != nil {
			raw, _ := json.MarshalIndent(n.evidence, "", "  ")
			if e := os.WriteFile(filepath.Join(directory, name+"-native.json"), append(raw, '\n'), 0600); e != nil {
				n.t.Error("native shutdown receipt failed")
			}
		}
		logs := append([]string{"registry.log", "gc.log"}, n.gcLogs...)
		for _, log := range logs {
			file, e := os.Open(filepath.Join(n.Root, log))
			if os.IsNotExist(e) {
				continue
			}
			if e != nil {
				n.t.Error("actual native log missing", e)
				continue
			}
			raw, e := io.ReadAll(io.LimitReader(file, (4<<20)+1))
			file.Close()
			if e != nil {
				n.t.Error("actual native log read failed", e)
				continue
			}
			if len(raw) > 4<<20 {
				n.t.Error("actual native log bound exceeded; retaining bounded failed diagnostic")
				raw = raw[:4<<20]
			}
			if e = os.WriteFile(filepath.Join(directory, name+"-"+log), raw, 0600); e != nil {
				n.t.Error("actual native log retention failed", e)
			}
		}
	}
}

// The isolated process must not adopt shared REGISTRY overrides, proxies,
// credentials or exporter endpoints. The official configuration guide supports
// disabling automatic trace exports with OTEL_TRACES_EXPORTER=none.
func fixtureEnvironment(memoryMiB int) []string {
	values := []string{"GOMAXPROCS=1", fmt.Sprintf("GOMEMLIMIT=%dMiB", memoryMiB), "OTEL_TRACES_EXPORTER=none"}
	for _, key := range []string{"PATH", "TMPDIR", "TZ"} {
		if value, ok := os.LookupEnv(key); ok {
			values = append(values, key+"="+value)
		}
	}
	return values
}

type boundedBuffer struct {
	mu       sync.Mutex
	data     bytes.Buffer
	maximum  int
	overflow bool
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(p) > b.maximum-b.data.Len() {
		b.overflow = true
		return 0, io.ErrShortWrite
	}
	return b.data.Write(p)
}
func boundedCommand(command *exec.Cmd, maximum int) ([]byte, error) {
	buffer := &boundedBuffer{maximum: maximum}
	command.Stdout = buffer
	command.Stderr = buffer
	e := command.Run()
	if buffer.overflow {
		return buffer.data.Bytes(), errors.New("REGISTRY_FIXTURE_OUTPUT_LIMIT")
	}
	return buffer.data.Bytes(), e
}
