package inference

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/agent/internal/settings"
)

// Paths inside the gateway container. The configuration and secrets are mounted read-only,
// the report spool read-write, and nothing else of the node is visible.
const (
	gatewayConfigMount = "/run/projectone-gateway"
	gatewayStateMount  = "/var/lib/projectone-gateway"
	gatewayLabel       = "io.ownsolid.gateway"
	gatewayNodeLabel   = "io.ownsolid.node"
	gatewayNamePrefix  = "ownsolid-gateway-"
)

// Activate binds a gateway's credential and key request to its instance and returns the
// serving certificate, leaf first, and the CA bundle that verifies it. It is the task's only
// call that carries secrets.
type Activate func(ctx context.Context, credential, certificateRequest, origin string) (wire.GatewayActivated, error)

// gatewayEngine is the part of Docker a gateway needs.
type gatewayEngine interface {
	InspectImage(context.Context, string) (Image, error)
	GatewayContainers(context.Context) ([]GatewayContainer, error)
	CreateGatewayContainer(context.Context, string, ContainerConfig) (string, error)
	InspectContainer(context.Context, string) (Container, error)
	StartContainer(context.Context, string) error
	StopContainer(context.Context, string) error
	RemoveContainer(context.Context, string) error
}

// GatewayManager starts and stops the model gateway of a serving node.
type GatewayManager struct {
	config  *GatewayConfig
	caFile  string
	runtime settings.Policy
	docker  gatewayEngine
	// own hands a path to the gateway's account; tests record it instead.
	own func(string) error
	// serving checks that the listener presents the issued certificate.
	serving func(context.Context, string, servedCertificate) error
}

// NewGatewayManager returns the manager of a node's gateway. A nil config is a node that
// declares no gateway: its tasks fail with GATEWAY_NOT_CONFIGURED, so control can say so.
func NewGatewayManager(config *GatewayConfig, caFile, dockerSocket string, policy settings.Policy) (*GatewayManager, error) {
	m := &GatewayManager{config: config, caFile: caFile, runtime: policy, own: chownGateway}
	m.serving = m.presents
	if config == nil {
		return m, nil
	}
	if e := config.Validate(); e != nil {
		return nil, e
	}
	if e := secureRoot(config.RootDirectory, true); e != nil {
		return nil, e
	}
	docker, e := NewDocker(dockerSocket, policy)
	if e != nil {
		return nil, e
	}
	m.docker = docker
	return m, nil
}

// Close releases the Docker connection.
func (m *GatewayManager) Close() {
	if docker, ok := m.docker.(*Docker); ok {
		docker.Close()
	}
}

// Start runs the gateway of command.GatewayID on this node, replacing any gateway the node
// ran before: control allows one active gateway per node, so an older one is stale.
func (m *GatewayManager) Start(ctx context.Context, node string, scope wire.Scope, command wire.GatewayCommand, activate Activate) wire.GatewayObservation {
	observation := wire.GatewayObservation{GatewayID: command.GatewayID, State: "stopped"}
	failed := func(code string) wire.GatewayObservation {
		if ctx.Err() != nil {
			code = "GATEWAY_CANCELLED"
		}
		observation.ErrorCode = code
		return observation
	}
	if command.Validate(wire.GatewayOperation("start")) != nil || !scope.Valid() {
		return failed("GATEWAY_CONFIGURATION_INVALID")
	}
	if m.config == nil {
		return failed("GATEWAY_NOT_CONFIGURED")
	}
	image, e := m.docker.InspectImage(ctx, m.config.Image)
	if e != nil {
		if e.Error() == "INFERENCE_DOCKER_NOT_FOUND" {
			return failed("GATEWAY_IMAGE_MISSING")
		}
		return failed(gatewayDockerCode(e))
	}
	if image.ID != m.config.Image || image.OS != "linux" || image.Architecture != "amd64" {
		return failed("GATEWAY_IMAGE_INVALID")
	}
	if e = m.removeGateways(ctx, ""); e != nil {
		return failed(gatewayDockerCode(e))
	}
	controlCA, e := os.ReadFile(m.caFile)
	if e != nil || len(controlCA) == 0 {
		return failed("GATEWAY_NOT_CONFIGURED")
	}
	secret := make([]byte, 32)
	if _, e = rand.Read(secret); e != nil {
		return failed("GATEWAY_IO_FAILED")
	}
	credential := wire.GatewayCredential(command.GatewayID, base64.RawURLEncoding.EncodeToString(secret))
	key, e := wire.NewCertificateKey()
	if e != nil {
		return failed("GATEWAY_IO_FAILED")
	}
	request, e := wire.NewCertificateRequest(key)
	if e != nil {
		return failed("GATEWAY_IO_FAILED")
	}
	activated, e := activate(ctx, credential, request, m.config.Origin)
	if e != nil {
		return failed("GATEWAY_ACTIVATION_REFUSED")
	}
	certificate := activated.Certificate
	leaf, roots, e := checkServingCertificate(activated, key, m.config.Origin)
	if e != nil {
		return failed("GATEWAY_ACTIVATION_REFUSED")
	}
	directory := filepath.Join(m.config.RootDirectory, command.GatewayID)
	configuration, e := json.Marshal(m.gatewayFile(node, scope, command))
	if e != nil {
		return failed("GATEWAY_IO_FAILED")
	}
	if e = m.writeFiles(directory, map[string]gatewayFile{
		"gateway.json":   {configuration, 0600},
		"credential":     {[]byte(credential), 0600},
		"tls.key":        {[]byte(key), 0600},
		"tls.pem":        {[]byte(certificate), 0644},
		"control-ca.pem": {controlCA, 0644},
	}); e != nil {
		return failed("GATEWAY_IO_FAILED")
	}
	id, e := m.docker.CreateGatewayContainer(ctx, gatewayNamePrefix+command.GatewayID, m.container(node, command.GatewayID, directory))
	if e != nil {
		return failed(gatewayDockerCode(e))
	}
	if e = m.docker.StartContainer(ctx, id); e != nil {
		return failed("GATEWAY_START_FAILED")
	}
	if code := m.awaitServing(ctx, id, servedCertificate{leaf: leaf.Raw, roots: roots, name: m.config.Origin}); code != "" {
		// A gateway that never served stays stopped for its logs; the next start removes it.
		_ = m.docker.StopContainer(context.WithoutCancel(ctx), id)
		return failed(code)
	}
	observation.State, observation.ContainerID = "running", id
	return observation
}

// Stop removes the gateway's container and its secrets. Its report spool stays: calls it
// has not yet reported may still be read from it.
func (m *GatewayManager) Stop(ctx context.Context, command wire.GatewayCommand) wire.GatewayObservation {
	observation := wire.GatewayObservation{GatewayID: command.GatewayID, State: "stopped"}
	if command.Validate(wire.GatewayOperation("stop")) != nil {
		observation.ErrorCode = "GATEWAY_CONFIGURATION_INVALID"
		return observation
	}
	if m.config == nil {
		// A node that declares no gateway runs none.
		return observation
	}
	if e := m.removeGateways(ctx, command.GatewayID); e != nil {
		observation.State, observation.ErrorCode = "running", "GATEWAY_STOP_FAILED"
		return observation
	}
	if e := removePrivate(filepath.Join(m.config.RootDirectory, command.GatewayID, "config")); e != nil {
		observation.ErrorCode = "GATEWAY_IO_FAILED"
	}
	return observation
}

// removeGateways stops and removes the gateway containers this agent created: every one, or
// only those of gateway when it is set.
func (m *GatewayManager) removeGateways(ctx context.Context, gateway string) error {
	containers, e := m.docker.GatewayContainers(ctx)
	if e != nil {
		return e
	}
	for _, c := range containers {
		if gateway != "" && c.Labels[gatewayLabel] != gateway {
			continue
		}
		if c.State == "running" || c.State == "restarting" {
			if e = m.docker.StopContainer(ctx, c.ID); e != nil && e.Error() != "INFERENCE_DOCKER_NOT_FOUND" {
				return e
			}
		}
		if e = m.docker.RemoveContainer(ctx, c.ID); e != nil && e.Error() != "INFERENCE_DOCKER_NOT_FOUND" {
			return e
		}
	}
	return nil
}

// awaitServing waits until the listener presents the issued certificate, and fails early
// when the container exits.
func (m *GatewayManager) awaitServing(ctx context.Context, id string, leaf servedCertificate) string {
	deadline := time.Now().Add(settings.Seconds(m.runtime.ProbeSeconds))
	poll := time.Duration(m.runtime.ModelPollMilliseconds) * time.Millisecond
	for {
		if m.serving(ctx, m.config.probeAddress(), leaf) == nil {
			return ""
		}
		c, e := m.docker.InspectContainer(ctx, id)
		if e != nil {
			return gatewayDockerCode(e)
		}
		if !c.State.Running {
			return "GATEWAY_START_FAILED"
		}
		if !time.Now().Before(deadline) {
			return "GATEWAY_NOT_READY"
		}
		timer := time.NewTimer(poll)
		select {
		case <-ctx.Done():
			timer.Stop()
			return "GATEWAY_CANCELLED"
		case <-timer.C:
		}
	}
}

// servedCertificate is what a started gateway must present.
type servedCertificate struct {
	leaf  []byte
	roots *x509.CertPool
	// name is the origin the certificate is verified for.
	name string
}

// presents completes a verified TLS handshake with the listener for the declared origin and
// accepts only the certificate this start was issued: the gateway serving it is the one the
// agent just configured.
func (m *GatewayManager) presents(ctx context.Context, address string, want servedCertificate) error {
	origin, e := wire.ParseGatewayOrigin(want.name)
	if e != nil {
		return e
	}
	name := origin.Host
	if origin.IP != nil {
		name = origin.IP.String()
	}
	dialer := &tls.Dialer{NetDialer: &net.Dialer{Timeout: settings.Seconds(m.runtime.ProbeDialSeconds)}, Config: &tls.Config{
		MinVersion: tls.VersionTLS12,
		NextProtos: []string{"http/1.1"},
		RootCAs:    want.roots,
		ServerName: name,
		VerifyConnection: func(state tls.ConnectionState) error {
			if len(state.PeerCertificates) == 0 || !bytes.Equal(state.PeerCertificates[0].Raw, want.leaf) {
				return errors.New("GATEWAY_NOT_READY")
			}
			return nil
		},
	}}
	bounded, cancel := context.WithTimeout(ctx, settings.Seconds(m.runtime.TLSHandshakeSeconds))
	defer cancel()
	connection, e := dialer.DialContext(bounded, "tcp", address)
	if e != nil {
		return e
	}
	return connection.Close()
}

// gatewayConfiguration is the gateway's own configuration file
// (services/gateway/src/configuration.rs). Limits keep the gateway's defaults.
type gatewayConfiguration struct {
	GatewayID       string `json:"gateway_id"`
	NodeID          string `json:"node_id"`
	ListenAddress   string `json:"listen_address"`
	BackendAddress  string `json:"backend_address"`
	ControlOrigin   string `json:"control_origin"`
	SiteID          string `json:"site_id"`
	CellID          string `json:"cell_id"`
	ControlCAFile   string `json:"control_ca_file"`
	CredentialFile  string `json:"gateway_credential_file"`
	CertificateFile string `json:"tls_certificate_file"`
	PrivateKeyFile  string `json:"tls_private_key_file"`
	StateDirectory  string `json:"state_directory"`
}

func (m *GatewayManager) gatewayFile(node string, scope wire.Scope, command wire.GatewayCommand) gatewayConfiguration {
	return gatewayConfiguration{
		GatewayID:       command.GatewayID,
		NodeID:          node,
		ListenAddress:   m.config.ListenAddress,
		BackendAddress:  "127.0.0.1:" + strconv.Itoa(wire.ApprovedInferenceRecipe().HostPort),
		ControlOrigin:   command.ControlOrigin,
		SiteID:          scope.SiteID,
		CellID:          scope.CellID,
		ControlCAFile:   gatewayConfigMount + "/control-ca.pem",
		CredentialFile:  gatewayConfigMount + "/credential",
		CertificateFile: gatewayConfigMount + "/tls.pem",
		PrivateKeyFile:  gatewayConfigMount + "/tls.key",
		StateDirectory:  gatewayStateMount,
	}
}

// container is the gateway's container: the host's network, because the engine listens on
// the node's loopback; no capabilities, a read-only root and the image's own account.
func (m *GatewayManager) container(node, gateway, directory string) ContainerConfig {
	c := ContainerConfig{
		Image:  m.config.Image,
		User:   gatewayUser,
		Labels: map[string]string{gatewayLabel: gateway, gatewayNodeLabel: node},
		HostConfig: HostConfig{
			NetworkMode:    "host",
			ReadonlyRootfs: true,
			CapDrop:        []string{"ALL"},
			SecurityOpt:    []string{"no-new-privileges"},
			Memory:         m.config.MemoryBytes,
			MemorySwap:     m.config.MemoryBytes,
			PidsLimit:      256,
			Mounts: []BindMount{
				{Type: "bind", Source: filepath.Join(directory, "config"), Target: gatewayConfigMount, ReadOnly: true},
				{Type: "bind", Source: filepath.Join(directory, "state"), Target: gatewayStateMount},
			},
			RestartPolicy: &RestartPolicy{Name: "unless-stopped"},
		},
	}
	c.HostConfig.LogConfig.Type = "json-file"
	c.HostConfig.LogConfig.Config = map[string]string{"max-size": "10m", "max-file": "3"}
	return c
}

type gatewayFile struct {
	body []byte
	mode os.FileMode
}

// writeFiles replaces the gateway's configuration directory and keeps its state directory.
// Every path belongs to the gateway's account; the secrets are readable by it alone.
func (m *GatewayManager) writeFiles(directory string, files map[string]gatewayFile) error {
	for _, path := range []string{directory, filepath.Join(directory, "state")} {
		if e := os.Mkdir(path, 0700); e != nil && !os.IsExist(e) {
			return e
		}
		if e := privateDirectory(path); e != nil {
			return e
		}
		if e := m.own(path); e != nil {
			return e
		}
	}
	configuration := filepath.Join(directory, "config")
	if e := removePrivate(configuration); e != nil {
		return e
	}
	if e := os.Mkdir(configuration, 0700); e != nil {
		return e
	}
	for name, file := range files {
		path := filepath.Join(configuration, name)
		if e := writeNew(path, file.body, file.mode); e != nil {
			return e
		}
		if e := m.own(path); e != nil {
			return e
		}
	}
	if e := m.own(configuration); e != nil {
		return e
	}
	return syncDir(directory)
}

// privateDirectory refuses a link or a directory others can enter.
func privateDirectory(path string) error {
	info, e := os.Lstat(path)
	if e != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0077 != 0 {
		return fail("GATEWAY_IO_FAILED")
	}
	return nil
}

// removePrivate removes a configuration directory the agent wrote, and nothing behind a link.
func removePrivate(path string) error {
	info, e := os.Lstat(path)
	if os.IsNotExist(e) {
		return nil
	}
	if e != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fail("GATEWAY_IO_FAILED")
	}
	if os.RemoveAll(path) != nil {
		return fail("GATEWAY_IO_FAILED")
	}
	return nil
}

func chownGateway(path string) error {
	if e := os.Lchown(path, gatewayUID, gatewayGID); e != nil {
		return fail("GATEWAY_IO_FAILED")
	}
	return nil
}

// checkServingCertificate accepts the certificate only for the key this start generated, for
// serving, for the origin the node declares, and verified by the bundle that came with it.
func checkServingCertificate(activated wire.GatewayActivated, key, origin string) (*x509.Certificate, *x509.CertPool, error) {
	bad := errors.New("GATEWAY_ACTIVATION_REFUSED")
	if activated.Certificate == "" || len(activated.Certificate) > wire.MaxCertificateBytes || activated.CertificateAuthority == "" || len(activated.CertificateAuthority) > wire.MaxGatewayAuthorityBytes {
		return nil, nil, bad
	}
	block, rest := pem.Decode([]byte(activated.Certificate))
	if block == nil || block.Type != wire.CertificatePEM {
		return nil, nil, bad
	}
	leaf, e := x509.ParseCertificate(block.Bytes)
	if e != nil {
		return nil, nil, bad
	}
	keyBlock, _ := pem.Decode([]byte(key))
	if keyBlock == nil {
		return nil, nil, bad
	}
	parsed, e := x509.ParsePKCS8PrivateKey(keyBlock.Bytes)
	private, ok := parsed.(*ecdsa.PrivateKey)
	public, matches := leaf.PublicKey.(*ecdsa.PublicKey)
	if e != nil || !ok || !matches || !public.Equal(&private.PublicKey) {
		return nil, nil, bad
	}
	served, e := wire.ParseGatewayOrigin(origin)
	if e != nil {
		return nil, nil, bad
	}
	host := served.Host
	if served.IP != nil {
		host = served.IP.String()
	}
	roots := x509.NewCertPool()
	intermediates := x509.NewCertPool()
	if !roots.AppendCertsFromPEM([]byte(activated.CertificateAuthority)) {
		return nil, nil, bad
	}
	intermediates.AppendCertsFromPEM(rest)
	if _, e = leaf.Verify(x509.VerifyOptions{DNSName: host, Roots: roots, Intermediates: intermediates, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}); e != nil {
		return nil, nil, bad
	}
	return leaf, roots, nil
}

// gatewayDockerCode turns a Docker failure into the gateway's own code.
func gatewayDockerCode(e error) string {
	switch {
	case errors.Is(e, context.Canceled), errors.Is(e, context.DeadlineExceeded):
		return "GATEWAY_CANCELLED"
	case e.Error() == "INFERENCE_DOCKER_UNAVAILABLE":
		return "GATEWAY_DOCKER_UNAVAILABLE"
	case e.Error() == "INFERENCE_CONTAINER_CONFLICT":
		return "GATEWAY_CONTAINER_CONFLICT"
	case errors.Is(e, syscall.ENOSPC):
		return "GATEWAY_IO_FAILED"
	}
	return "GATEWAY_DOCKER_REJECTED"
}
