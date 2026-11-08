package kubernetes

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/agent/internal/progress"
)

// Paths RKE2 uses, relative to the machine's root. They are RKE2's own defaults, which its
// units and tools assume; the root is configurable only so tests can run in a directory.
const (
	installDirectory  = "/usr/local"
	rke2Binary        = "/usr/local/bin/rke2"
	configFile        = "/etc/rancher/rke2/config.yaml"
	dataDirectory     = "/var/lib/rancher/rke2"
	imagesDirectory   = "/var/lib/rancher/rke2/agent/images"
	kubectlBinary     = "/var/lib/rancher/rke2/bin/kubectl"
	kubeletKubeconfig = "/var/lib/rancher/rke2/agent/kubelet.kubeconfig"
	adminKubeconfig   = "/etc/rancher/rke2/rke2.yaml"
	serverCA          = "/var/lib/rancher/rke2/server/tls/server-ca.crt"
)

// journalName is the agent's record of the cluster membership it installed.
const journalName = "kubernetes.json"

// pollInterval is how often a join looks at the node's readiness.
const pollInterval = 5 * time.Second

// membership is what the agent installed. A later join must agree with it: a server
// whose address changes after its first start loses its etcd membership, so a different
// configuration is refused, never rewritten.
type membership struct {
	ClusterID     string `json:"cluster_id"`
	Version       string `json:"version"`
	Role          string `json:"role"`
	NodeName      string `json:"node_name"`
	NodeAddress   string `json:"node_address"`
	ServerAddress string `json:"server_address,omitempty"`
	Resolver      string `json:"resolver"`
	// Installed is false while files are being placed, so a crash mid-install resumes.
	Installed bool `json:"installed"`
}

// Download writes one approved artifact to destination.
type Download func(ctx context.Context, artifact wire.KubernetesArtifact, destination string) error

// Manager installs RKE2 and reports the node's place in its cell's cluster.
type Manager struct {
	config  Config
	root    string
	journal string
	staging string
	host    host
	release wire.KubernetesRelease
	now     func() time.Time
	poll    time.Duration
}

// New returns a manager whose journal and staging live in the agent's state directory.
func New(config Config, stateDirectory string) (*Manager, error) {
	config = config.WithDefaults()
	if e := config.Validate(); e != nil {
		return nil, e
	}
	return &Manager{config: config, root: "/", journal: filepath.Join(stateDirectory, journalName), staging: filepath.Join(stateDirectory, "kubernetes-staging"), host: realHost{}, release: wire.ApprovedKubernetesRelease(), now: time.Now, poll: pollInterval}, nil
}

func (m *Manager) path(name string) string { return filepath.Join(m.root, name) }

func (m *Manager) unit(role string) string {
	if role == wire.KubernetesServerRole {
		return "rke2-server.service"
	}
	return "rke2-agent.service"
}

// Execute runs one Kubernetes operation. A command that does not fit the operation is
// refused with AGENT_OPERATION_UNSUPPORTED and no observation. Otherwise the observation is
// filled; on failure its error code says why and the returned error carries the same code.
func (m *Manager) Execute(ctx context.Context, operation string, command wire.KubernetesCommand, download Download) (wire.KubernetesObservation, error) {
	observation := wire.KubernetesObservation{ClusterID: command.ClusterID, Version: command.Version, Role: command.Role, State: wire.KubernetesNotInstalled}
	if e := command.Validate(operation); e != nil {
		return observation, e
	}
	var e error
	switch operation {
	case wire.KubernetesJoinOperation:
		e = m.join(ctx, command, download, &observation)
	case wire.KubernetesTokenOperation:
		e = m.token(ctx, command, &observation)
	default:
		e = m.observe(ctx, command, &observation)
	}
	observation.ObservedAt = m.now().UTC()
	if e != nil {
		observation.ErrorCode = errorCode(ctx, e)
		observation.JoinToken, observation.JoinTokenExpiresAt = "", time.Time{}
		return observation, errors.New(observation.ErrorCode)
	}
	return observation, nil
}

func errorCode(ctx context.Context, e error) string {
	if ctx.Err() != nil {
		return "KUBERNETES_CANCELLED"
	}
	if wire.KubernetesErrors[e.Error()] {
		return e.Error()
	}
	return "KUBERNETES_IO_FAILED"
}

func (m *Manager) loadMembership() (membership, bool, error) {
	content, e := os.ReadFile(m.journal)
	if errors.Is(e, os.ErrNotExist) {
		return membership{}, false, nil
	}
	var recorded membership
	if e != nil || json.Unmarshal(content, &recorded) != nil {
		return membership{}, false, errors.New("KUBERNETES_IO_FAILED")
	}
	return recorded, true, nil
}

// sameMembership compares everything fixed at a node's first start. A worker's join token
// is not part of it: a retry brings a fresh one.
func sameMembership(a, b membership) bool {
	return a.ClusterID == b.ClusterID && a.Version == b.Version && a.Role == b.Role && a.NodeName == b.NodeName && a.NodeAddress == b.NodeAddress && a.ServerAddress == b.ServerAddress && a.Resolver == b.Resolver
}

func (m *Manager) preflight(ctx context.Context, command wire.KubernetesCommand) (string, error) {
	progress.Step(ctx, wire.ProgressStepPreflight)
	if !m.host.Privileged() {
		progress.Error(ctx, "The agent must run as root to install Kubernetes.")
		return "", errors.New("KUBERNETES_PRIVILEGE_REQUIRED")
	}
	if !m.host.SystemdRunning() {
		progress.Error(ctx, "This node does not run systemd, which starts Kubernetes.")
		return "", errors.New("KUBERNETES_SYSTEMD_UNAVAILABLE")
	}
	address := netip.MustParseAddr(command.NodeAddress)
	if local, e := m.host.LocalAddress(address); e != nil || !local {
		progress.Error(ctx, "No interface of this node holds %s, the address it enrolled from.", command.NodeAddress)
		return "", errors.New("KUBERNETES_ADDRESS_NOT_LOCAL")
	}
	if route, e := m.host.DefaultRoute(address); e != nil || !route {
		progress.Error(ctx, "This node has no default route. Kubernetes needs one even with no route out: add a black-hole default route.")
		return "", errors.New("KUBERNETES_DEFAULT_ROUTE_MISSING")
	}
	resolver, e := resolverFile(m.root)
	if e != nil {
		progress.Error(ctx, "Neither /run/systemd/resolve/resolv.conf nor /etc/resolv.conf names a DNS server other than this node. Configure the site's DNS server on the node.")
		return "", e
	}
	return resolver, nil
}

func (m *Manager) join(ctx context.Context, command wire.KubernetesCommand, download Download, observation *wire.KubernetesObservation) error {
	resolver, e := m.preflight(ctx, command)
	if e != nil {
		return e
	}
	wanted := membership{ClusterID: command.ClusterID, Version: command.Version, Role: command.Role, NodeName: command.NodeName, NodeAddress: command.NodeAddress, ServerAddress: command.ServerAddress, Resolver: resolver}
	recorded, found, e := m.loadMembership()
	if e != nil {
		return e
	}
	if found && !sameMembership(recorded, wanted) {
		progress.Error(ctx, "This node already belongs to cluster %s as %s at %s; it cannot join with another configuration.", recorded.ClusterID, recorded.Role, recorded.NodeAddress)
		return errors.New("KUBERNETES_CONFIG_CONFLICT")
	}
	if !found && m.foreignInstall() {
		progress.Error(ctx, "RKE2 is already on this node and ownsolid did not install it. Remove it or use another node.")
		return errors.New("KUBERNETES_CONFLICT")
	}
	if !found || !recorded.Installed {
		if e = m.install(ctx, wanted, download); e != nil {
			return e
		}
	}
	if e = m.writeConfig(command, resolver); e != nil {
		return e
	}
	wanted.Installed = true
	if e = writeJSON(m.journal, wanted); e != nil {
		return e
	}
	os.RemoveAll(m.staging)
	progress.Step(ctx, wire.ProgressStepStart)
	unit := m.unit(command.Role)
	for _, args := range [][]string{{"daemon-reload"}, {"enable", unit}, {"start", "--no-block", unit}} {
		if _, e = m.host.Run(ctx, "systemctl", args...); e != nil {
			progress.Error(ctx, "systemd could not start %s.", unit)
			return errors.New("KUBERNETES_SERVICE_FAILED")
		}
	}
	progress.Step(ctx, wire.ProgressStepVerify)
	if e = m.waitReady(ctx, command, observation); e != nil {
		return e
	}
	return m.serverCA(command, observation)
}

func (m *Manager) foreignInstall() bool {
	for _, path := range []string{rke2Binary, configFile} {
		if _, e := os.Lstat(m.path(path)); e == nil {
			return true
		}
	}
	return false
}

func (m *Manager) install(ctx context.Context, wanted membership, download Download) error {
	release := m.release
	if free, e := m.host.FreeBytes(m.existingAncestor(dataDirectory)); e != nil || free < m.config.MinimumFreeBytes {
		progress.Error(ctx, "Kubernetes needs %d GiB free under %s.", m.config.MinimumFreeBytes>>30, dataDirectory)
		return errors.New("KUBERNETES_DISK_SPACE_LOW")
	}
	wanted.Installed = false
	if e := writeJSON(m.journal, wanted); e != nil {
		return e
	}
	progress.Step(ctx, wire.ProgressStepDownload)
	if e := os.MkdirAll(m.staging, 0o700); e != nil {
		return errors.New("KUBERNETES_IO_FAILED")
	}
	for _, artifact := range release.Artifacts() {
		staged := filepath.Join(m.staging, artifact.Name)
		if verifyArtifact(staged, artifact) == nil {
			continue
		}
		os.Remove(staged)
		if e := download(ctx, artifact, staged); e != nil {
			progress.Error(ctx, "Downloading %s from control failed.", artifact.Name)
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return errors.New("KUBERNETES_ASSET_UNAVAILABLE")
		}
		if e := verifyArtifact(staged, artifact); e != nil {
			os.Remove(staged)
			progress.Error(ctx, "%s does not match the approved release.", artifact.Name)
			return e
		}
		progress.Info(ctx, "Downloaded and verified %s.", artifact.Name)
	}
	progress.Step(ctx, wire.ProgressStepInstall)
	if e := extractBinary(filepath.Join(m.staging, release.Binary.Name), m.path(installDirectory)); e != nil {
		return e
	}
	for _, image := range release.Images {
		if e := copyFile(filepath.Join(m.staging, image.Name), filepath.Join(m.path(imagesDirectory), image.Name), image.SizeBytes); e != nil {
			return e
		}
	}
	progress.Info(ctx, "Installed RKE2 %s.", release.Version)
	return nil
}

// existingAncestor is the nearest directory of path that exists, for measuring free space
// before RKE2's own directories are created.
func (m *Manager) existingAncestor(path string) string {
	for candidate := m.path(path); ; candidate = filepath.Dir(candidate) {
		if _, e := os.Stat(candidate); e == nil || candidate == filepath.Dir(candidate) {
			return candidate
		}
	}
}

// writeConfig renders RKE2's configuration. Every value was validated by the command, so
// none needs escaping; they are quoted anyway so YAML never reads one as another type.
func (m *Manager) writeConfig(command wire.KubernetesCommand, resolver string) error {
	var b strings.Builder
	if command.Role == wire.KubernetesWorkerRole {
		fmt.Fprintf(&b, "server: %q\n", "https://"+netip.AddrPortFrom(netip.MustParseAddr(command.ServerAddress), 9345).String())
		fmt.Fprintf(&b, "token: %q\n", command.JoinToken)
	}
	fmt.Fprintf(&b, "node-name: %q\n", command.NodeName)
	fmt.Fprintf(&b, "node-ip: %q\n", command.NodeAddress)
	fmt.Fprintf(&b, "resolv-conf: %q\n", resolver)
	if command.Role == wire.KubernetesServerRole {
		// No ownsolid entrance runs in the cluster yet, so no ingress controller either.
		b.WriteString("ingress-controller: \"none\"\nwrite-kubeconfig-mode: \"0600\"\n")
	}
	return writeBytes(m.path(configFile), []byte(b.String()))
}

func (m *Manager) ready(ctx context.Context, name string) bool {
	output, e := m.host.Run(ctx, m.path(kubectlBinary), "--kubeconfig", m.path(kubeletKubeconfig), "get", "node", name, "-o", `jsonpath={.status.conditions[?(@.type=="Ready")].status}`)
	return e == nil && strings.TrimSpace(string(output)) == "True"
}

func (m *Manager) waitReady(ctx context.Context, command wire.KubernetesCommand, observation *wire.KubernetesObservation) error {
	observation.State = wire.KubernetesNotReady
	deadline := m.now().Add(m.config.readyTimeout())
	for {
		if m.ready(ctx, command.NodeName) {
			observation.State = wire.KubernetesReady
			progress.Info(ctx, "Node %s is Ready.", command.NodeName)
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !m.now().Before(deadline) {
			m.diagnose(ctx, command)
			return errors.New("KUBERNETES_NOT_READY")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(m.poll):
		}
	}
}

// diagnoses maps what RKE2 logs when a join cannot finish to a message a person can act
// on. The journal itself is never copied into the task log.
var diagnoses = []struct{ pattern, message string }{
	{"CA cert validation failed", "The server's certificate authority does not match the join token. Check that %s is this cluster's server."},
	{"connection refused", "Nothing answers on %s:9345. Check that the server is running."},
	{"i/o timeout", "%s:9345 cannot be reached. Check the network and firewall between the nodes."},
	{"no route to host", "%s:9345 cannot be reached. Check the network and firewall between the nodes."},
	{"401 Unauthorized", "The server refused the join token; it may have expired. Retry to get a new one."},
}

func (m *Manager) diagnose(ctx context.Context, command wire.KubernetesCommand) {
	output, _ := m.host.Run(ctx, "journalctl", "--unit", m.unit(command.Role), "--lines", "200", "--no-pager", "--output", "cat")
	target := command.ServerAddress
	if target == "" {
		target = command.NodeAddress
	}
	for _, d := range diagnoses {
		if bytes.Contains(output, []byte(d.pattern)) {
			progress.Error(ctx, d.message, target)
			return
		}
	}
	progress.Error(ctx, "The node did not become Ready within %d seconds. Read `journalctl -u %s` on the node.", m.config.ReadySeconds, m.unit(command.Role))
}

func (m *Manager) serverCA(command wire.KubernetesCommand, observation *wire.KubernetesObservation) error {
	if command.Role != wire.KubernetesServerRole {
		return nil
	}
	content, e := os.ReadFile(m.path(serverCA))
	if e != nil {
		return errors.New("KUBERNETES_IO_FAILED")
	}
	// RKE2 pins the hash of the CA file as stored on disk.
	sum := sha256.Sum256(content)
	observation.CASHA256 = hex.EncodeToString(sum[:])
	return nil
}

func (m *Manager) installed(command wire.KubernetesCommand) (bool, error) {
	recorded, found, e := m.loadMembership()
	if e != nil {
		return false, e
	}
	if !found || !recorded.Installed {
		return false, nil
	}
	if recorded.ClusterID != command.ClusterID || recorded.Role != command.Role || recorded.NodeName != command.NodeName || recorded.NodeAddress != command.NodeAddress {
		return false, errors.New("KUBERNETES_CONFIG_CONFLICT")
	}
	return true, nil
}

func (m *Manager) token(ctx context.Context, command wire.KubernetesCommand, observation *wire.KubernetesObservation) error {
	installed, e := m.installed(command)
	if e != nil {
		return e
	}
	if !installed {
		return errors.New("KUBERNETES_NOT_INSTALLED")
	}
	if !m.ready(ctx, command.NodeName) {
		observation.State = wire.KubernetesNotReady
		return errors.New("KUBERNETES_NOT_READY")
	}
	observation.State = wire.KubernetesReady
	if e = m.serverCA(command, observation); e != nil {
		return e
	}
	issued := m.now().UTC()
	output, e := m.host.Run(ctx, m.path(rke2Binary), "token", "create", "--kubeconfig", m.path(adminKubeconfig), "--ttl", strconv.Itoa(command.TokenTTLSeconds)+"s", "--description", "ownsolid worker join")
	lines := strings.Fields(string(output))
	if e != nil || len(lines) == 0 {
		progress.Error(ctx, "RKE2 could not create a join token.")
		return errors.New("KUBERNETES_TOKEN_FAILED")
	}
	token := lines[len(lines)-1]
	if hash, ok := wire.KubernetesTokenCAHash(token); !ok || hash != observation.CASHA256 {
		progress.Error(ctx, "RKE2 created a join token that does not pin this cluster's certificate authority.")
		return errors.New("KUBERNETES_TOKEN_FAILED")
	}
	observation.JoinToken = token
	// The token was created after issued, so it expires no later than this.
	observation.JoinTokenExpiresAt = issued.Add(time.Duration(command.TokenTTLSeconds) * time.Second)
	return nil
}

func (m *Manager) observe(ctx context.Context, command wire.KubernetesCommand, observation *wire.KubernetesObservation) error {
	installed, e := m.installed(command)
	if e != nil || !installed {
		return e
	}
	observation.State = wire.KubernetesNotReady
	if _, e = m.host.Run(ctx, "systemctl", "is-active", "--quiet", m.unit(command.Role)); e == nil && m.ready(ctx, command.NodeName) {
		observation.State = wire.KubernetesReady
	}
	return m.serverCA(command, observation)
}
