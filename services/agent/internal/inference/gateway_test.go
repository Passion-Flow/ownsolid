package inference

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/agent/internal/settings"
)

const (
	testGatewayID = "12345678-1234-1234-1234-123456789abc"
	testNodeID    = "22345678-1234-1234-1234-123456789abc"
	testImage     = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
)

func testGatewayConfig(root string) *GatewayConfig {
	return &GatewayConfig{ListenAddress: "0.0.0.0:18443", Origin: "https://gw.rack7.example:18443", Image: testImage, RootDirectory: root, MemoryBytes: 512 << 20}
}

func TestGatewayConfigurationRefusesWhatCannotServe(t *testing.T) {
	if e := testGatewayConfig("/var/lib/ownsolid-gateway").Validate(); e != nil {
		t.Fatal(e)
	}
	for name, change := range map[string]func(*GatewayConfig){
		"IPv6 listener":      func(c *GatewayConfig) { c.ListenAddress = "[::]:18443" },
		"port zero":          func(c *GatewayConfig) { c.ListenAddress = "0.0.0.0:0" },
		"broadcast":          func(c *GatewayConfig) { c.ListenAddress = "255.255.255.255:18443" },
		"multicast":          func(c *GatewayConfig) { c.ListenAddress = "224.0.0.1:18443" },
		"engine port":        func(c *GatewayConfig) { c.ListenAddress = "127.0.0.1:18000" },
		"engine port on all": func(c *GatewayConfig) { c.ListenAddress = "0.0.0.0:18000" },
		"plain origin":       func(c *GatewayConfig) { c.Origin = "http://gw.rack7.example:18443" },
		"image by name":      func(c *GatewayConfig) { c.Image = "ownsolid/gateway:latest" },
		"relative root":      func(c *GatewayConfig) { c.RootDirectory = "var/lib/gateway" },
		"filesystem root":    func(c *GatewayConfig) { c.RootDirectory = "/" },
		"unclean root":       func(c *GatewayConfig) { c.RootDirectory = "/var/lib/../gateway" },
		"too little memory":  func(c *GatewayConfig) { c.MemoryBytes = 1 << 20 },
		"too much memory":    func(c *GatewayConfig) { c.MemoryBytes = 32 << 30 },
		"padded listener":    func(c *GatewayConfig) { c.ListenAddress = "0.0.0.0:018443" },
	} {
		c := testGatewayConfig("/var/lib/ownsolid-gateway")
		change(c)
		if c.Validate() == nil {
			t.Error(name)
		}
	}
	if got := testGatewayConfig("/x").probeAddress(); got != "127.0.0.1:18443" {
		t.Fatal(got)
	}
	specific := testGatewayConfig("/x")
	specific.ListenAddress = "192.0.2.10:18443"
	if got := specific.probeAddress(); got != "192.0.2.10:18443" {
		t.Fatal(got)
	}
}

// fakeGatewayDocker records what the manager asks of Docker.
type fakeGatewayDocker struct {
	image      Image
	imageError error
	listed     []GatewayContainer
	created    []ContainerConfig
	names      []string
	started    []string
	stopped    []string
	removed    []string
	running    bool
	createErr  error
}

func (f *fakeGatewayDocker) InspectImage(_ context.Context, id string) (Image, error) {
	return f.image, f.imageError
}
func (f *fakeGatewayDocker) GatewayContainers(context.Context) ([]GatewayContainer, error) {
	return f.listed, nil
}
func (f *fakeGatewayDocker) CreateGatewayContainer(_ context.Context, name string, c ContainerConfig) (string, error) {
	if f.createErr != nil {
		return "", f.createErr
	}
	f.names, f.created = append(f.names, name), append(f.created, c)
	return strings.Repeat("c", 64), nil
}
func (f *fakeGatewayDocker) InspectContainer(_ context.Context, id string) (Container, error) {
	var c Container
	c.ID = id
	c.State.Running = f.running
	return c, nil
}
func (f *fakeGatewayDocker) StartContainer(_ context.Context, id string) error {
	f.started = append(f.started, id)
	return nil
}
func (f *fakeGatewayDocker) StopContainer(_ context.Context, id string) error {
	f.stopped = append(f.stopped, id)
	return nil
}
func (f *fakeGatewayDocker) RemoveContainer(_ context.Context, id string) error {
	f.removed = append(f.removed, id)
	return nil
}

// testAuthority signs serving certificates the way control's gateway CA does.
type testAuthority struct {
	certificate *x509.Certificate
	key         *ecdsa.PrivateKey
	bundle      string
}

func newTestAuthority(t *testing.T) testAuthority {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test gateway CA"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, e := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if e != nil {
		t.Fatal(e)
	}
	parsed, _ := x509.ParseCertificate(der)
	return testAuthority{certificate: parsed, key: key, bundle: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))}
}

func (a testAuthority) sign(t *testing.T, request, host string, usage x509.ExtKeyUsage) string {
	t.Helper()
	parsed, e := wire.ParseCertificateRequest(request)
	if e != nil {
		t.Fatal(e)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(time.Now().UnixNano()), NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{usage}}
	if ip := net.ParseIP(host); ip != nil {
		template.IPAddresses = []net.IP{ip}
	} else {
		template.DNSNames = []string{host}
	}
	der, e := x509.CreateCertificate(rand.Reader, template, a.certificate, parsed.PublicKey, a.key)
	if e != nil {
		t.Fatal(e)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}

type gatewayFixture struct {
	manager   *GatewayManager
	docker    *fakeGatewayDocker
	authority testAuthority
	root      string
	owned     []string
	served    []servedCertificate
}

func newGatewayFixture(t *testing.T) *gatewayFixture {
	t.Helper()
	root := t.TempDir()
	if e := os.Chmod(root, 0700); e != nil {
		t.Fatal(e)
	}
	ca := filepath.Join(t.TempDir(), "control-ca.pem")
	if e := os.WriteFile(ca, []byte("-----BEGIN CERTIFICATE-----\ncontrol\n-----END CERTIFICATE-----\n"), 0644); e != nil {
		t.Fatal(e)
	}
	policy := settings.Default()
	policy.ProbeSeconds, policy.ModelPollMilliseconds = 1, 100
	f := &gatewayFixture{docker: &fakeGatewayDocker{image: Image{ID: testImage, OS: "linux", Architecture: "amd64"}, running: true}, authority: newTestAuthority(t), root: root}
	f.manager = &GatewayManager{config: testGatewayConfig(root), caFile: ca, runtime: policy, docker: f.docker,
		own: func(path string) error { f.owned = append(f.owned, path); return nil },
		serving: func(_ context.Context, address string, want servedCertificate) error {
			if address != "127.0.0.1:18443" {
				return errors.New("wrong address")
			}
			f.served = append(f.served, want)
			return nil
		}}
	return f
}

func (f *gatewayFixture) activate(t *testing.T, host string) Activate {
	return func(_ context.Context, credential, request, origin string) (wire.GatewayActivated, error) {
		if !wire.ValidGatewayCredential(credential, testGatewayID) || origin != "https://gw.rack7.example:18443" {
			t.Fatalf("activation %q %q", credential, origin)
		}
		return wire.GatewayActivated{Certificate: f.authority.sign(t, request, host, x509.ExtKeyUsageServerAuth), CertificateAuthority: f.authority.bundle, NotAfter: time.Now().Add(time.Hour)}, nil
	}
}

var startCommand = wire.GatewayCommand{Action: "start", GatewayID: testGatewayID, ControlOrigin: "https://admin.example.test:8443"}

func TestGatewayStartGeneratesSecretsOnTheNodeAndRunsTheGatewayLockedDown(t *testing.T) {
	f := newGatewayFixture(t)
	stale := GatewayContainer{ID: strings.Repeat("d", 64), State: "running", Labels: map[string]string{gatewayLabel: "32345678-1234-1234-1234-123456789abc"}}
	f.docker.listed = []GatewayContainer{stale}
	var credential string
	activate := func(ctx context.Context, c, request, origin string) (wire.GatewayActivated, error) {
		credential = c
		return f.activate(t, "gw.rack7.example")(ctx, c, request, origin)
	}
	o := f.manager.Start(t.Context(), testNodeID, wire.LocalScope(), startCommand, activate)
	if o.State != "running" || o.ErrorCode != "" || o.ContainerID != strings.Repeat("c", 64) {
		t.Fatalf("%+v", o)
	}
	result := wire.Result{Outcome: "success", Gateway: &o}
	if e := result.ValidateGateway(startCommand); e != nil {
		t.Fatal(e)
	}
	// A gateway left from a revoked instance would hold the listener.
	if len(f.docker.stopped) != 1 || f.docker.stopped[0] != stale.ID || len(f.docker.removed) != 1 {
		t.Fatalf("stale gateway kept: %v %v", f.docker.stopped, f.docker.removed)
	}
	directory := filepath.Join(f.root, testGatewayID)
	config := filepath.Join(directory, "config")
	for name, mode := range map[string]os.FileMode{"gateway.json": 0600, "credential": 0600, "tls.key": 0600, "tls.pem": 0644, "control-ca.pem": 0644} {
		info, e := os.Lstat(filepath.Join(config, name))
		if e != nil || info.Mode().Perm() != mode {
			t.Fatalf("%s: %v %v", name, info, e)
		}
	}
	written, _ := os.ReadFile(filepath.Join(config, "credential"))
	if string(written) != credential {
		t.Fatal("the activated credential is not the one the gateway reads")
	}
	var file map[string]string
	raw, _ := os.ReadFile(filepath.Join(config, "gateway.json"))
	if e := json.Unmarshal(raw, &file); e != nil {
		t.Fatal(e)
	}
	want := map[string]string{"gateway_id": testGatewayID, "node_id": testNodeID, "listen_address": "0.0.0.0:18443", "backend_address": "127.0.0.1:18000", "control_origin": "https://admin.example.test:8443", "site_id": "default", "cell_id": "default", "control_ca_file": "/run/projectone-gateway/control-ca.pem", "gateway_credential_file": "/run/projectone-gateway/credential", "tls_certificate_file": "/run/projectone-gateway/tls.pem", "tls_private_key_file": "/run/projectone-gateway/tls.key", "state_directory": "/var/lib/projectone-gateway"}
	if len(file) != len(want) {
		t.Fatalf("%v", file)
	}
	for key, value := range want {
		if file[key] != value {
			t.Errorf("%s = %q, want %q", key, file[key], value)
		}
	}
	for _, path := range []string{directory, filepath.Join(directory, "state"), config, filepath.Join(config, "tls.key"), filepath.Join(config, "credential")} {
		found := false
		for _, owned := range f.owned {
			found = found || owned == path
		}
		if !found {
			t.Errorf("%s not handed to the gateway account", path)
		}
	}
	c := f.docker.created[0]
	h := c.HostConfig
	if f.docker.names[0] != "ownsolid-gateway-"+testGatewayID || c.Image != testImage || c.User != "10001:10001" || c.Labels[gatewayLabel] != testGatewayID || c.Labels[gatewayNodeLabel] != testNodeID {
		t.Fatalf("%+v", c)
	}
	if h.NetworkMode != "host" || !h.ReadonlyRootfs || h.Privileged || len(h.CapAdd) != 0 || len(h.CapDrop) != 1 || h.CapDrop[0] != "ALL" || h.SecurityOpt[0] != "no-new-privileges" || h.Memory != 512<<20 || h.MemorySwap != h.Memory || h.RestartPolicy == nil || h.RestartPolicy.Name != "unless-stopped" || len(h.Binds) != 0 || len(h.Devices) != 0 {
		t.Fatalf("%+v", h)
	}
	if len(h.Mounts) != 2 || h.Mounts[0].Source != config || !h.Mounts[0].ReadOnly || h.Mounts[1].Source != filepath.Join(directory, "state") || h.Mounts[1].ReadOnly {
		t.Fatalf("%+v", h.Mounts)
	}
	if len(f.served) != 1 || f.served[0].name != "https://gw.rack7.example:18443" || f.served[0].roots == nil {
		t.Fatal("readiness did not check the issued certificate")
	}
	// A retry of the same gateway keeps its report spool and replaces its secrets.
	marker := filepath.Join(directory, "state", "spool")
	if e := os.WriteFile(marker, []byte("unreported"), 0600); e != nil {
		t.Fatal(e)
	}
	f.docker.listed = nil
	if o = f.manager.Start(t.Context(), testNodeID, wire.LocalScope(), startCommand, f.activate(t, "gw.rack7.example")); o.State != "running" {
		t.Fatalf("%+v", o)
	}
	if _, e := os.Stat(marker); e != nil {
		t.Fatal("restarting the gateway lost its report spool")
	}
	again, _ := os.ReadFile(filepath.Join(config, "credential"))
	if string(again) == credential {
		t.Fatal("a second start reused the credential")
	}
}

func TestGatewayStartFailsWithTheGatewaysOwnCodes(t *testing.T) {
	refused := func(context.Context, string, string, string) (wire.GatewayActivated, error) {
		return wire.GatewayActivated{}, errors.New("CONTROL_RESPONSE_REJECTED")
	}
	for name, test := range map[string]struct {
		change   func(*gatewayFixture)
		activate func(*gatewayFixture, *testing.T) Activate
		code     string
	}{
		"not configured":               {func(f *gatewayFixture) { f.manager.config = nil }, nil, "GATEWAY_NOT_CONFIGURED"},
		"image missing":                {func(f *gatewayFixture) { f.docker.imageError = errors.New("INFERENCE_DOCKER_NOT_FOUND") }, nil, "GATEWAY_IMAGE_MISSING"},
		"docker down":                  {func(f *gatewayFixture) { f.docker.imageError = errors.New("INFERENCE_DOCKER_UNAVAILABLE") }, nil, "GATEWAY_DOCKER_UNAVAILABLE"},
		"image for arm":                {func(f *gatewayFixture) { f.docker.image.Architecture = "arm64" }, nil, "GATEWAY_IMAGE_INVALID"},
		"no control CA":                {func(f *gatewayFixture) { f.manager.caFile = filepath.Join(f.root, "missing.pem") }, nil, "GATEWAY_NOT_CONFIGURED"},
		"refused":                      {nil, func(*gatewayFixture, *testing.T) Activate { return refused }, "GATEWAY_ACTIVATION_REFUSED"},
		"certificate for another host": {nil, func(f *gatewayFixture, t *testing.T) Activate { return f.activate(t, "other.example") }, "GATEWAY_ACTIVATION_REFUSED"},
		"certificate for clients": {nil, func(f *gatewayFixture, t *testing.T) Activate {
			return func(ctx context.Context, c, request, origin string) (wire.GatewayActivated, error) {
				return wire.GatewayActivated{Certificate: f.authority.sign(t, request, "gw.rack7.example", x509.ExtKeyUsageClientAuth), CertificateAuthority: f.authority.bundle, NotAfter: time.Now().Add(time.Hour)}, nil
			}
		}, "GATEWAY_ACTIVATION_REFUSED"},
		"certificate for another key": {nil, func(f *gatewayFixture, t *testing.T) Activate {
			return func(ctx context.Context, c, _, origin string) (wire.GatewayActivated, error) {
				key, _ := wire.NewCertificateKey()
				request, _ := wire.NewCertificateRequest(key)
				return wire.GatewayActivated{Certificate: f.authority.sign(t, request, "gw.rack7.example", x509.ExtKeyUsageServerAuth), CertificateAuthority: f.authority.bundle, NotAfter: time.Now().Add(time.Hour)}, nil
			}
		}, "GATEWAY_ACTIVATION_REFUSED"},
		"certificate from another CA": {nil, func(f *gatewayFixture, t *testing.T) Activate {
			other := newTestAuthority(t)
			return func(ctx context.Context, c, request, origin string) (wire.GatewayActivated, error) {
				return wire.GatewayActivated{Certificate: other.sign(t, request, "gw.rack7.example", x509.ExtKeyUsageServerAuth), CertificateAuthority: f.authority.bundle, NotAfter: time.Now().Add(time.Hour)}, nil
			}
		}, "GATEWAY_ACTIVATION_REFUSED"},
		"create refused": {func(f *gatewayFixture) { f.docker.createErr = errors.New("INFERENCE_CONTAINER_CONFLICT") }, nil, "GATEWAY_CONTAINER_CONFLICT"},
		"exits at start": {func(f *gatewayFixture) {
			f.docker.running = false
			f.manager.serving = func(context.Context, string, servedCertificate) error { return errors.New("refused") }
		}, nil, "GATEWAY_START_FAILED"},
		"never serves": {func(f *gatewayFixture) {
			f.manager.serving = func(context.Context, string, servedCertificate) error { return errors.New("refused") }
		}, nil, "GATEWAY_NOT_READY"},
	} {
		t.Run(name, func(t *testing.T) {
			f := newGatewayFixture(t)
			if test.change != nil {
				test.change(f)
			}
			activate := f.activate(t, "gw.rack7.example")
			if test.activate != nil {
				activate = test.activate(f, t)
			}
			o := f.manager.Start(t.Context(), testNodeID, wire.LocalScope(), startCommand, activate)
			if o.State != "stopped" || o.ErrorCode != test.code || o.ContainerID != "" {
				t.Fatalf("%+v", o)
			}
			result := wire.Result{Outcome: "failure", ErrorCode: o.ErrorCode, Gateway: &o}
			if e := result.ValidateGateway(startCommand); e != nil {
				t.Fatal(e)
			}
			if strings.HasPrefix(name, "certificate") || name == "refused" {
				if len(f.docker.created) != 0 {
					t.Fatal("a gateway started without an accepted certificate")
				}
				if _, e := os.Stat(filepath.Join(f.root, testGatewayID, "config", "credential")); !os.IsNotExist(e) {
					t.Fatal("a refused credential was kept")
				}
			}
			if name == "exits at start" || name == "never serves" {
				if len(f.docker.stopped) != 1 {
					t.Fatal("a gateway that never served was left running")
				}
			}
		})
	}
	f := newGatewayFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	f.docker.imageError = context.Canceled
	if o := f.manager.Start(ctx, testNodeID, wire.LocalScope(), startCommand, f.activate(t, "gw.rack7.example")); o.ErrorCode != "GATEWAY_CANCELLED" {
		t.Fatalf("%+v", o)
	}
}

func TestGatewayStopRemovesOnlyItsGatewayAndItsSecrets(t *testing.T) {
	f := newGatewayFixture(t)
	if o := f.manager.Start(t.Context(), testNodeID, wire.LocalScope(), startCommand, f.activate(t, "gw.rack7.example")); o.State != "running" {
		t.Fatalf("%+v", o)
	}
	other := GatewayContainer{ID: strings.Repeat("e", 64), State: "running", Labels: map[string]string{gatewayLabel: "32345678-1234-1234-1234-123456789abc"}}
	mine := GatewayContainer{ID: strings.Repeat("c", 64), State: "exited", Labels: map[string]string{gatewayLabel: testGatewayID}}
	f.docker.listed, f.docker.stopped, f.docker.removed = []GatewayContainer{other, mine}, nil, nil
	stop := wire.GatewayCommand{Action: "stop", GatewayID: testGatewayID}
	o := f.manager.Stop(t.Context(), stop)
	if o.State != "stopped" || o.ErrorCode != "" {
		t.Fatalf("%+v", o)
	}
	if e := (wire.Result{Outcome: "success", Gateway: &o}).ValidateGateway(stop); e != nil {
		t.Fatal(e)
	}
	if len(f.docker.stopped) != 0 || len(f.docker.removed) != 1 || f.docker.removed[0] != mine.ID {
		t.Fatalf("stopped %v removed %v", f.docker.stopped, f.docker.removed)
	}
	if _, e := os.Stat(filepath.Join(f.root, testGatewayID, "config")); !os.IsNotExist(e) {
		t.Fatal("a stopped gateway kept its credential and key")
	}
	if _, e := os.Stat(filepath.Join(f.root, testGatewayID, "state")); e != nil {
		t.Fatal("stopping the gateway removed its report spool")
	}
	f.manager.config = nil
	if o = f.manager.Stop(t.Context(), stop); o.State != "stopped" || o.ErrorCode != "" {
		t.Fatalf("a node without a gateway could not stop one: %+v", o)
	}
}

func TestGatewayReadinessAcceptsOnlyTheIssuedCertificate(t *testing.T) {
	authority := newTestAuthority(t)
	issue := func() (tls.Certificate, *x509.Certificate) {
		key, _ := wire.NewCertificateKey()
		request, _ := wire.NewCertificateRequest(key)
		certificate := authority.sign(t, request, "127.0.0.1", x509.ExtKeyUsageServerAuth)
		pair, e := tls.X509KeyPair([]byte(certificate), []byte(key))
		if e != nil {
			t.Fatal(e)
		}
		leaf, _ := x509.ParseCertificate(pair.Certificate[0])
		return pair, leaf
	}
	served, servedLeaf := issue()
	_, otherLeaf := issue()
	listener, e := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{served}, MinVersion: tls.VersionTLS12})
	if e != nil {
		t.Fatal(e)
	}
	defer listener.Close()
	go func() {
		for {
			c, e := listener.Accept()
			if e != nil {
				return
			}
			_ = c.(*tls.Conn).Handshake()
			c.Close()
		}
	}()
	roots := x509.NewCertPool()
	roots.AddCert(authority.certificate)
	m := &GatewayManager{runtime: settings.Default()}
	address := listener.Addr().String()
	port := address[strings.LastIndex(address, ":")+1:]
	if e := m.presents(t.Context(), address, servedCertificate{leaf: servedLeaf.Raw, roots: roots, name: "https://127.0.0.1:" + port}); e != nil {
		t.Fatal(e)
	}
	if m.presents(t.Context(), address, servedCertificate{leaf: otherLeaf.Raw, roots: roots, name: "https://127.0.0.1:" + port}) == nil {
		t.Fatal("another gateway's certificate counted as this one serving")
	}
	if m.presents(t.Context(), address, servedCertificate{leaf: servedLeaf.Raw, roots: x509.NewCertPool(), name: "https://127.0.0.1:" + port}) == nil {
		t.Fatal("an unverified certificate counted as serving")
	}
}
