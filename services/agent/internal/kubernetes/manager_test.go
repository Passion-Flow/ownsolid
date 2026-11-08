package kubernetes

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
)

const (
	clusterID = "6f1c2a8e-4b3d-4c5e-9f7a-1b2c3d4e5f60"
	nodeID    = "0d9e8f7a-6b5c-4d3e-8f2a-1b0c9d8e7f6a"
	caContent = "-----BEGIN CERTIFICATE-----\nsynthetic\n-----END CERTIFICATE-----\n"
)

func caHash() string {
	sum := sha256.Sum256([]byte(caContent))
	return hex.EncodeToString(sum[:])
}

type fakeHost struct {
	mu          sync.Mutex
	privileged  bool
	systemd     bool
	route       bool
	local       bool
	free        int64
	ready       bool
	activeState string
	journal     string
	tokenOutput string
	tokenError  error
	calls       []string
}

func (h *fakeHost) Privileged() bool                      { return h.privileged }
func (h *fakeHost) SystemdRunning() bool                  { return h.systemd }
func (h *fakeHost) DefaultRoute(netip.Addr) (bool, error) { return h.route, nil }
func (h *fakeHost) LocalAddress(netip.Addr) (bool, error) { return h.local, nil }
func (h *fakeHost) FreeBytes(string) (int64, error)       { return h.free, nil }
func (h *fakeHost) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.calls = append(h.calls, filepath.Base(name)+" "+strings.Join(args, " "))
	switch filepath.Base(name) {
	case "kubectl":
		if h.ready {
			return []byte("True"), nil
		}
		return []byte("False"), nil
	case "systemctl":
		if args[0] == "show" {
			return []byte(h.activeState + "\n"), nil
		}
	case "journalctl":
		return []byte(h.journal), nil
	case "rke2":
		return []byte(h.tokenOutput), h.tokenError
	}
	return nil, nil
}

func (h *fakeHost) called(prefix string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	count := 0
	for _, call := range h.calls {
		if strings.HasPrefix(call, prefix) {
			count++
		}
	}
	return count
}

type fixture struct {
	t         *testing.T
	manager   *Manager
	host      *fakeHost
	files     map[string][]byte
	downloads map[string]int
}

func tarball(t *testing.T, files map[string]string, extra func(*tar.Writer)) []byte {
	t.Helper()
	var buffer bytes.Buffer
	compressed := gzip.NewWriter(&buffer)
	archive := tar.NewWriter(compressed)
	for name, content := range files {
		if e := archive.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(content)), Typeflag: tar.TypeReg}); e != nil {
			t.Fatal(e)
		}
		archive.Write([]byte(content))
	}
	if extra != nil {
		extra(archive)
	}
	archive.Close()
	compressed.Close()
	return buffer.Bytes()
}

func binaryContents() map[string]string {
	files := map[string]string{}
	for name := range binaryFiles {
		files[name] = "content of " + name
	}
	return files
}

func artifact(name string, content []byte) wire.KubernetesArtifact {
	sum := sha256.Sum256(content)
	return wire.KubernetesArtifact{Name: name, SizeBytes: int64(len(content)), SHA256: hex.EncodeToString(sum[:])}
}

func newFixture(t *testing.T, binary []byte) *fixture {
	t.Helper()
	root, state := t.TempDir(), t.TempDir()
	files := map[string][]byte{"rke2.linux-amd64.tar.gz": binary, "images-core.tar.zst": []byte("core images"), "images-canal.tar.zst": []byte("canal images")}
	release := wire.KubernetesRelease{Version: wire.ApprovedKubernetesRelease().Version, Binary: artifact("rke2.linux-amd64.tar.gz", binary), Images: []wire.KubernetesArtifact{artifact("images-core.tar.zst", files["images-core.tar.zst"]), artifact("images-canal.tar.zst", files["images-canal.tar.zst"])}}
	for path, content := range map[string]string{"/etc/resolv.conf": "nameserver 10.0.0.53\n"} {
		os.MkdirAll(filepath.Dir(root+path), 0o755)
		os.WriteFile(root+path, []byte(content), 0o644)
	}
	host := &fakeHost{privileged: true, systemd: true, route: true, local: true, free: 32 << 30, ready: true}
	m, e := New(Config{ReadySeconds: 60}, state)
	if e != nil {
		t.Fatal(e)
	}
	m.root, m.host, m.release, m.poll = root, host, release, time.Millisecond
	return &fixture{t: t, manager: m, host: host, files: files, downloads: map[string]int{}}
}

func (f *fixture) download(_ context.Context, a wire.KubernetesArtifact, destination string) error {
	f.downloads[a.Name]++
	content, ok := f.files[a.Name]
	if !ok {
		return errors.New("missing")
	}
	return os.WriteFile(destination, content, 0o600)
}

// startServer makes the files RKE2 writes on its first start.
func (f *fixture) startServer() {
	path := f.manager.path(serverCA)
	os.MkdirAll(filepath.Dir(path), 0o700)
	os.WriteFile(path, []byte(caContent), 0o600)
}

func server() wire.KubernetesCommand {
	return wire.KubernetesCommand{ClusterID: clusterID, Version: wire.ApprovedKubernetesRelease().Version, Role: wire.KubernetesServerRole, NodeName: nodeID, NodeAddress: "172.22.0.2"}
}

func worker() wire.KubernetesCommand {
	c := server()
	c.Role, c.NodeAddress, c.ServerAddress = wire.KubernetesWorkerRole, "172.22.0.3", "172.22.0.2"
	c.JoinToken = "K10" + caHash() + "::awcyco.0123456789abcdef"
	return c
}

func (f *fixture) execute(operation string, command wire.KubernetesCommand) (wire.KubernetesObservation, error) {
	return f.manager.Execute(context.Background(), operation, command, f.download)
}

func (f *fixture) read(path string) string {
	content, e := os.ReadFile(f.manager.path(path))
	if e != nil {
		f.t.Fatal(e)
	}
	return string(content)
}

func TestServerJoinInstallsAndReportsTheCA(t *testing.T) {
	f := newFixture(t, tarball(t, binaryContents(), nil))
	f.startServer()
	observation, e := f.execute(wire.KubernetesJoinOperation, server())
	if e != nil {
		t.Fatal(e)
	}
	if observation.State != wire.KubernetesReady || observation.CASHA256 != caHash() || observation.ErrorCode != "" {
		t.Fatalf("observation %+v", observation)
	}
	if e = observation.ValidateFor(server(), wire.KubernetesJoinOperation, "success"); e != nil {
		t.Fatal(e)
	}
	if got := f.read(rke2Binary); got != "content of bin/rke2" {
		t.Fatalf("binary %q", got)
	}
	info, _ := os.Stat(f.manager.path(rke2Binary))
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("binary mode %v", info.Mode())
	}
	if got := f.read(filepath.Join(imagesDirectory, "images-core.tar.zst")); got != "core images" {
		t.Fatalf("images %q", got)
	}
	config := f.read(configFile)
	for _, line := range []string{`node-name: "` + nodeID + `"`, `node-ip: "172.22.0.2"`, `resolv-conf: "/etc/resolv.conf"`, `ingress-controller: "none"`, `write-kubeconfig-mode: "0600"`, `private-registry: "/etc/rancher/rke2/ownsolid-registries.yaml"`, `disable-default-registry-endpoint: true`} {
		if !strings.Contains(config, line+"\n") {
			t.Errorf("config lacks %s:\n%s", line, config)
		}
	}
	if strings.Contains(config, "token") || strings.Contains(config, "server:") {
		t.Errorf("server config names a token or server:\n%s", config)
	}
	info, _ = os.Stat(f.manager.path(configFile))
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("config mode %v", info.Mode())
	}
	if got := f.read(offlineRegistryFile); !strings.Contains(got, "  docker.io:\n    endpoint:\n      - \"https://127.0.0.1:0\"\n") {
		t.Fatalf("offline registry has no loopback mirror: %q", got)
	}
	if f.host.called("systemctl start --no-block rke2-server.service") != 1 || f.host.called("systemctl enable rke2-server.service") != 1 {
		t.Fatalf("calls %v", f.host.calls)
	}
	if _, e = os.Stat(f.manager.staging); !os.IsNotExist(e) {
		t.Fatal("staging kept after install")
	}
}

func TestWorkerJoinWritesTheServerAndToken(t *testing.T) {
	f := newFixture(t, tarball(t, binaryContents(), nil))
	observation, e := f.execute(wire.KubernetesJoinOperation, worker())
	if e != nil || observation.State != wire.KubernetesReady || observation.CASHA256 != "" {
		t.Fatalf("%+v %v", observation, e)
	}
	config := f.read(configFile)
	for _, line := range []string{`server: "https://172.22.0.2:9345"`, `token: "` + worker().JoinToken + `"`, `node-ip: "172.22.0.3"`, `private-registry: "/etc/rancher/rke2/ownsolid-registries.yaml"`, `disable-default-registry-endpoint: true`} {
		if !strings.Contains(config, line+"\n") {
			t.Errorf("config lacks %s:\n%s", line, config)
		}
	}
	if strings.Contains(config, "ingress-controller") {
		t.Error("worker config sets a server option")
	}
	if f.host.called("systemctl start --no-block rke2-agent.service") != 1 {
		t.Fatalf("calls %v", f.host.calls)
	}
}

func TestJoinKeepsTheSiteRegistryPolicy(t *testing.T) {
	f := newFixture(t, tarball(t, binaryContents(), nil))
	const site = "mirrors:\n  docker.io:\n    endpoint: [https://registry.example.invalid]\n"
	if e := writeBytes(f.manager.path(nativeRegistryFile), []byte(site)); e != nil {
		t.Fatal(e)
	}
	if _, e := f.execute(wire.KubernetesJoinOperation, worker()); e != nil {
		t.Fatal(e)
	}
	if got := f.read(nativeRegistryFile); got != site {
		t.Fatalf("site registry policy was rewritten: %q", got)
	}
	if got := f.read(configFile); strings.Contains(got, "private-registry:") || strings.Contains(got, "disable-default-registry-endpoint:") {
		t.Fatal("generated configuration overrides the site registry policy")
	}
	if _, e := os.Stat(f.manager.path(offlineRegistryFile)); !errors.Is(e, os.ErrNotExist) {
		t.Fatalf("unexpected generated registry file: %v", e)
	}
}

func TestWorkerJoinToAnIPv6Server(t *testing.T) {
	f := newFixture(t, tarball(t, binaryContents(), nil))
	f.host.local = true
	c := worker()
	c.NodeAddress, c.ServerAddress = "2001:db8::3", "2001:db8::2"
	if _, e := f.execute(wire.KubernetesJoinOperation, c); e != nil {
		t.Fatal(e)
	}
	if config := f.read(configFile); !strings.Contains(config, `server: "https://[2001:db8::2]:9345"`) {
		t.Fatalf("config:\n%s", config)
	}
}

func TestPreflightRefusals(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*fixture)
		code   string
	}{
		{"not root", func(f *fixture) { f.host.privileged = false }, "KUBERNETES_PRIVILEGE_REQUIRED"},
		{"no systemd", func(f *fixture) { f.host.systemd = false }, "KUBERNETES_SYSTEMD_UNAVAILABLE"},
		{"address elsewhere", func(f *fixture) { f.host.local = false }, "KUBERNETES_ADDRESS_NOT_LOCAL"},
		{"no default route", func(f *fixture) { f.host.route = false }, "KUBERNETES_DEFAULT_ROUTE_MISSING"},
		{"loopback resolver only", func(f *fixture) {
			os.WriteFile(f.manager.path("/etc/resolv.conf"), []byte("nameserver 127.0.0.53\noptions edns0\n"), 0o644)
		}, "KUBERNETES_RESOLVER_INVALID"},
		{"disk too small", func(f *fixture) { f.host.free = 1 << 30 }, "KUBERNETES_DISK_SPACE_LOW"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFixture(t, tarball(t, binaryContents(), nil))
			c.mutate(f)
			observation, e := f.execute(wire.KubernetesJoinOperation, server())
			if e == nil || observation.ErrorCode != c.code || e.Error() != c.code {
				t.Fatalf("%+v %v", observation, e)
			}
			if e = observation.ValidateFor(server(), wire.KubernetesJoinOperation, "failure"); e != nil {
				t.Fatal(e)
			}
			if _, e = os.Stat(f.manager.path(configFile)); !os.IsNotExist(e) {
				t.Fatal("a refused join wrote the config")
			}
		})
	}
}

func TestResolverPrefersSystemdResolvedUpstreams(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(root+"/etc", 0o755)
	os.MkdirAll(root+"/run/systemd/resolve", 0o755)
	os.WriteFile(root+"/etc/resolv.conf", []byte("nameserver 127.0.0.53\n"), 0o644)
	os.WriteFile(root+"/run/systemd/resolve/resolv.conf", []byte("# managed\nnameserver 10.1.2.3\n"), 0o644)
	if path, e := resolverFile(root); e != nil || path != "/run/systemd/resolve/resolv.conf" {
		t.Fatalf("%q %v", path, e)
	}
	os.WriteFile(root+"/run/systemd/resolve/resolv.conf", []byte("nameserver fe80::1\n"), 0o644)
	if _, e := resolverFile(root); e == nil {
		t.Fatal("a link-local resolver was accepted")
	}
}

func TestArchiveRefusals(t *testing.T) {
	missing := binaryContents()
	delete(missing, "bin/rke2")
	cases := map[string][]byte{
		"an unexpected file": tarball(t, binaryContents(), func(w *tar.Writer) {
			w.WriteHeader(&tar.Header{Name: "etc/cron.d/backdoor", Mode: 0o644, Size: 1, Typeflag: tar.TypeReg})
			w.Write([]byte("x"))
		}),
		"a link": tarball(t, missing, func(w *tar.Writer) {
			w.WriteHeader(&tar.Header{Name: "bin/rke2", Linkname: "/etc/shadow", Typeflag: tar.TypeSymlink})
		}),
		"a path out of the directory": tarball(t, binaryContents(), func(w *tar.Writer) {
			w.WriteHeader(&tar.Header{Name: "../../bin/rke2", Mode: 0o755, Size: 1, Typeflag: tar.TypeReg})
			w.Write([]byte("x"))
		}),
		"a missing file": tarball(t, missing, nil),
		"not gzip":       []byte("plain bytes"),
	}
	for name, archive := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t, archive)
			observation, e := f.execute(wire.KubernetesJoinOperation, server())
			if e == nil || observation.ErrorCode != "KUBERNETES_ARCHIVE_INVALID" {
				t.Fatalf("%+v %v", observation, e)
			}
			if _, e = os.Lstat(filepath.Join(f.manager.root, "etc/cron.d/backdoor")); !os.IsNotExist(e) {
				t.Fatal("unexpected file written")
			}
		})
	}
}

func TestCorruptDownloadIsRefusedAndRetried(t *testing.T) {
	f := newFixture(t, tarball(t, binaryContents(), nil))
	good := f.files["images-canal.tar.zst"]
	f.files["images-canal.tar.zst"] = []byte("canal imageX")
	observation, e := f.execute(wire.KubernetesJoinOperation, worker())
	if e == nil || observation.ErrorCode != "KUBERNETES_ASSET_INVALID" {
		t.Fatalf("%+v %v", observation, e)
	}
	if _, e = os.Stat(filepath.Join(f.manager.staging, "images-canal.tar.zst")); !os.IsNotExist(e) {
		t.Fatal("corrupt file kept")
	}
	// A retry resumes: verified files are not fetched again.
	f.files["images-canal.tar.zst"] = good
	if _, e = f.execute(wire.KubernetesJoinOperation, worker()); e != nil {
		t.Fatal(e)
	}
	if f.downloads["rke2.linux-amd64.tar.gz"] != 1 || f.downloads["images-core.tar.zst"] != 1 || f.downloads["images-canal.tar.zst"] != 2 {
		t.Fatalf("downloads %v", f.downloads)
	}
}

func TestUnavailableDownload(t *testing.T) {
	f := newFixture(t, tarball(t, binaryContents(), nil))
	delete(f.files, "images-core.tar.zst")
	if observation, e := f.execute(wire.KubernetesJoinOperation, worker()); e == nil || observation.ErrorCode != "KUBERNETES_ASSET_UNAVAILABLE" {
		t.Fatalf("%+v %v", observation, e)
	}
}

func TestForeignInstallIsNotTouched(t *testing.T) {
	f := newFixture(t, tarball(t, binaryContents(), nil))
	os.MkdirAll(filepath.Dir(f.manager.path(rke2Binary)), 0o755)
	os.WriteFile(f.manager.path(rke2Binary), []byte("someone else's"), 0o755)
	observation, e := f.execute(wire.KubernetesJoinOperation, server())
	if e == nil || observation.ErrorCode != "KUBERNETES_CONFLICT" {
		t.Fatalf("%+v %v", observation, e)
	}
	if got := f.read(rke2Binary); got != "someone else's" || len(f.downloads) != 0 {
		t.Fatalf("binary %q, downloads %v", got, f.downloads)
	}
}

func TestRepeatedJoinIsIdempotentAndAnotherAddressIsRefused(t *testing.T) {
	f := newFixture(t, tarball(t, binaryContents(), nil))
	f.startServer()
	if _, e := f.execute(wire.KubernetesJoinOperation, server()); e != nil {
		t.Fatal(e)
	}
	// An installed node can predate the Agent-owned registry file.
	if e := os.Remove(f.manager.path(offlineRegistryFile)); e != nil {
		t.Fatal(e)
	}
	f.host.activeState = "active"
	if _, e := f.execute(wire.KubernetesJoinOperation, server()); e != nil {
		t.Fatal(e)
	}
	if f.downloads["rke2.linux-amd64.tar.gz"] != 1 {
		t.Fatalf("downloads %v", f.downloads)
	}
	if got := f.read(offlineRegistryFile); got != offlineRegistry || f.host.called("systemctl restart") != 0 {
		t.Fatal("join replay did not restore the policy without restarting the unit")
	}
	before := f.read(configFile)
	moved := server()
	moved.NodeAddress = "172.22.0.9"
	observation, e := f.execute(wire.KubernetesJoinOperation, moved)
	if e == nil || observation.ErrorCode != "KUBERNETES_CONFIG_CONFLICT" {
		t.Fatalf("%+v %v", observation, e)
	}
	if f.read(configFile) != before {
		t.Fatal("a conflicting join rewrote the config")
	}
}

func TestWorkerRetryTakesAFreshToken(t *testing.T) {
	f := newFixture(t, tarball(t, binaryContents(), nil))
	f.host.ready = false
	f.host.journal = `level=error msg="Failed to validate connection to cluster at https://172.22.0.2:9345: 401 Unauthorized"`
	clock := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	f.manager.now = func() time.Time { clock = clock.Add(10 * time.Second); return clock }
	observation, e := f.execute(wire.KubernetesJoinOperation, worker())
	if e == nil || observation.ErrorCode != "KUBERNETES_NOT_READY" || observation.State != wire.KubernetesNotReady {
		t.Fatalf("%+v %v", observation, e)
	}
	if e = observation.ValidateFor(worker(), wire.KubernetesJoinOperation, "failure"); e != nil {
		t.Fatal(e)
	}
	if f.host.called("journalctl --unit rke2-agent.service") != 1 {
		t.Fatalf("calls %v", f.host.calls)
	}
	retry := worker()
	retry.JoinToken = "K10" + caHash() + "::bcdefg.fedcba9876543210"
	f.host.ready = true
	f.host.activeState = "activating"
	if _, e = f.execute(wire.KubernetesJoinOperation, retry); e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(f.read(configFile), retry.JoinToken) || f.downloads["rke2.linux-amd64.tar.gz"] != 1 {
		t.Fatalf("config not updated or downloaded again: %v", f.downloads)
	}
	if f.host.called("systemctl restart --no-block rke2-agent.service") != 1 {
		t.Fatalf("retry did not reload the token: %v", f.host.calls)
	}
	f.host.activeState = "active"
	if _, e = f.execute(wire.KubernetesJoinOperation, retry); e != nil {
		t.Fatal(e)
	}
	if f.host.called("systemctl restart --no-block rke2-agent.service") != 1 {
		t.Fatalf("replay restarted an active worker: %v", f.host.calls)
	}
}

func TestToken(t *testing.T) {
	f := newFixture(t, tarball(t, binaryContents(), nil))
	command := server()
	command.TokenTTLSeconds = 1800
	if observation, e := f.execute(wire.KubernetesTokenOperation, command); e == nil || observation.ErrorCode != "KUBERNETES_NOT_INSTALLED" {
		t.Fatalf("%+v %v", observation, e)
	}
	f.startServer()
	if _, e := f.execute(wire.KubernetesJoinOperation, server()); e != nil {
		t.Fatal(e)
	}
	token := "K10" + caHash() + "::abcdef.0123456789abcdef"
	f.host.tokenOutput = "Note: token created\n" + token + "\n"
	observation, e := f.execute(wire.KubernetesTokenOperation, command)
	if e != nil || observation.JoinToken != token {
		t.Fatalf("%+v %v", observation, e)
	}
	if e = observation.ValidateFor(command, wire.KubernetesTokenOperation, "success"); e != nil {
		t.Fatal(e)
	}
	if f.host.called("rke2 token create --kubeconfig "+f.manager.path(adminKubeconfig)+" --ttl 1800s") != 1 {
		t.Fatalf("calls %v", f.host.calls)
	}
	for name, output := range map[string]string{"another CA": "K10" + strings.Repeat("a", 64) + "::abcdef.0123456789abcdef", "no token": "", "short format": "secret"} {
		f.host.tokenOutput = output
		observation, e = f.execute(wire.KubernetesTokenOperation, command)
		if e == nil || observation.ErrorCode != "KUBERNETES_TOKEN_FAILED" || observation.JoinToken != "" {
			t.Errorf("%s: %+v %v", name, observation, e)
		}
	}
	f.host.tokenOutput, f.host.tokenError = token, errors.New("exit status 1")
	if observation, e = f.execute(wire.KubernetesTokenOperation, command); e == nil || observation.JoinToken != "" {
		t.Fatalf("failed command returned %+v", observation)
	}
}

func TestObserve(t *testing.T) {
	f := newFixture(t, tarball(t, binaryContents(), nil))
	observation, e := f.execute(wire.KubernetesObserveOperation, server())
	if e != nil || observation.State != wire.KubernetesNotInstalled {
		t.Fatalf("%+v %v", observation, e)
	}
	f.startServer()
	if _, e = f.execute(wire.KubernetesJoinOperation, server()); e != nil {
		t.Fatal(e)
	}
	observation, e = f.execute(wire.KubernetesObserveOperation, server())
	if e != nil || observation.State != wire.KubernetesReady || observation.CASHA256 != caHash() {
		t.Fatalf("%+v %v", observation, e)
	}
	f.host.ready = false
	if observation, _ = f.execute(wire.KubernetesObserveOperation, server()); observation.State != wire.KubernetesNotReady {
		t.Fatalf("%+v", observation)
	}
	if e = observation.ValidateFor(server(), wire.KubernetesObserveOperation, "success"); e != nil {
		t.Fatal(e)
	}
}

func TestCommandForAnotherOperationIsRefused(t *testing.T) {
	f := newFixture(t, tarball(t, binaryContents(), nil))
	if _, e := f.execute(wire.KubernetesTokenOperation, server()); e == nil || e.Error() != "AGENT_OPERATION_UNSUPPORTED" {
		t.Fatal(e)
	}
}

func TestConfigValidate(t *testing.T) {
	if e := (Config{}).WithDefaults().Validate(); e != nil {
		t.Fatal(e)
	}
	for _, c := range []Config{{ReadySeconds: 10, MinimumFreeBytes: 4 << 30}, {ReadySeconds: 7200, MinimumFreeBytes: 4 << 30}, {ReadySeconds: 600, MinimumFreeBytes: 1 << 30}} {
		if c.Validate() == nil {
			t.Errorf("%+v accepted", c)
		}
	}
}

func TestDefaultRouteParsing(t *testing.T) {
	if !ipv4DefaultRoute(strings.Fields("dummy0\t00000000\tFF7100CB\t0003\t0\t0\t1000\t00000000\t0\t0\t0")) {
		t.Error("IPv4 default route missed")
	}
	if ipv4DefaultRoute(strings.Fields("eth0\t000016AC\t00000000\t0001\t0\t0\t0\t0000FFFF\t0\t0\t0")) {
		t.Error("IPv4 network route taken as default")
	}
	if ipv6DefaultRoute(strings.Fields("00000000000000000000000000000000 00 00000000000000000000000000000000 00 00000000000000000000000000000000 ffffffff 00000001 00000000 00200200 lo")) {
		t.Error("the kernel's unreachable placeholder taken as a route")
	}
	if !ipv6DefaultRoute(strings.Fields("00000000000000000000000000000000 00 00000000000000000000000000000000 00 20010db8000000000000000000000001 00000400 00000001 00000000 00000003 eth0")) {
		t.Error("IPv6 default route missed")
	}
}
