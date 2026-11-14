package v1

import (
	"errors"
	"net/netip"
	"regexp"
	"time"
)

// KubernetesNodeProfile is the execution profile of a node that belongs to its cell's
// Kubernetes cluster (ARC-104, ADR 0051). The agent installs and starts RKE2 on it.
const KubernetesNodeProfile = "kubernetes_node"

// KubernetesVersion is the protocol version an agent of the Kubernetes node profile speaks.
const KubernetesVersion = "1.0"

// The operations of the Kubernetes node profile.
const (
	// KubernetesJoinOperation installs RKE2 and starts the node as the cluster's server or
	// as a worker, then waits for the node's own Node object to be Ready.
	KubernetesJoinOperation = "kubernetes.join.v1"
	// KubernetesTokenOperation creates a bootstrap token on the server that workers join with.
	KubernetesTokenOperation = "kubernetes.token.v1"
	// KubernetesObserveOperation reports the node's RKE2 service and Node readiness.
	KubernetesObserveOperation = "kubernetes.observe.v1"
)

// KubernetesArtifactsPath is the task path leaf an agent downloads RKE2 artifacts from.
const KubernetesArtifactsPath = "kubernetes-artifacts"

// KubernetesTransferChunkBytes is the range one artifact download call returns.
const KubernetesTransferChunkBytes int64 = 8 * 1024 * 1024

// Roles a node takes in its cell's cluster.
const (
	KubernetesServerRole = "server"
	KubernetesWorkerRole = "worker"
)

// States of a node in its cell's cluster, as its agent observes them.
const (
	KubernetesReady        = "ready"
	KubernetesNotReady     = "not_ready"
	KubernetesNotInstalled = "not_installed"
)

// Join token lifetimes control may ask the server for. The token is needed only while a
// batch of workers joins, so it should expire soon after.
const (
	KubernetesTokenMinimumSeconds = 300
	KubernetesTokenMaximumSeconds = 86400
)

// KubernetesArtifact is one pinned file of the approved RKE2 release.
type KubernetesArtifact struct {
	Name      string `json:"name"`
	SizeBytes int64  `json:"size_bytes"`
	SHA256    string `json:"sha256"`
}

// KubernetesRelease is the RKE2 release ownsolid installs, with every file it needs.
type KubernetesRelease struct {
	Version string
	// Binary is the tarball holding the rke2 binary and its systemd units.
	Binary KubernetesArtifact
	// Images are the image archives RKE2 imports at start, offline.
	Images []KubernetesArtifact
}

// approvedKubernetesRelease is RKE2 v1.36.6+rke2r1, the stable channel on 2026-11-08.
// Sizes and digests are those of the release's sha256sum-amd64.txt.
var approvedKubernetesRelease = KubernetesRelease{
	Version: "v1.36.6+rke2r1",
	Binary:  KubernetesArtifact{Name: "rke2.linux-amd64.tar.gz", SizeBytes: 41496182, SHA256: "e111ac6333ddb2b819d133a413914d4456dd2195a3d694865cb7acda5bf81ae4"},
	Images: []KubernetesArtifact{
		{Name: "rke2-images-core.linux-amd64.tar.zst", SizeBytes: 567518406, SHA256: "58c153e94944039fc05b8e23de9e8f489df049519e55dd33e5790bf5ae19a217"},
		{Name: "rke2-images-canal.linux-amd64.tar.zst", SizeBytes: 241211711, SHA256: "40a09eb8460e27e79dc13247517445c9d94461ef5078ef9e4d7fe1f95aced8b0"},
	},
}

// ApprovedKubernetesRelease returns the RKE2 release this protocol version installs.
func ApprovedKubernetesRelease() KubernetesRelease {
	release := approvedKubernetesRelease
	release.Images = append([]KubernetesArtifact(nil), approvedKubernetesRelease.Images...)
	return release
}

// Artifacts lists every file of the release, the binary first.
func (r KubernetesRelease) Artifacts() []KubernetesArtifact {
	return append([]KubernetesArtifact{r.Binary}, r.Images...)
}

// KubernetesArtifactForName returns the approved artifact with that file name.
func KubernetesArtifactForName(name string) (KubernetesArtifact, bool) {
	for _, artifact := range approvedKubernetesRelease.Artifacts() {
		if artifact.Name == name {
			return artifact, true
		}
	}
	return KubernetesArtifact{}, false
}

// IsKubernetesOperation reports whether operation belongs to the Kubernetes node profile.
func IsKubernetesOperation(operation string) bool {
	return operation == KubernetesJoinOperation || operation == KubernetesTokenOperation || operation == KubernetesObserveOperation
}

// KubernetesCommand is what control asks a Kubernetes node to do. Every field is chosen
// by control; none comes from a browser.
type KubernetesCommand struct {
	ClusterID string `json:"cluster_id"`
	Version   string `json:"version"`
	Role      string `json:"role"`
	// NodeName is the Kubernetes node name: the ownsolid node ID, which never changes.
	NodeName string `json:"node_name"`
	// NodeAddress is the address the node advertises in the cluster, fixed at its first
	// start: a server whose address changes loses its etcd membership.
	NodeAddress string `json:"node_address"`
	// ServerAddress is the server's node address, for a worker's join.
	ServerAddress string `json:"server_address,omitempty"`
	// JoinToken is a bootstrap token that pins the cluster CA, for a worker's join.
	JoinToken string `json:"join_token,omitempty"`
	// TokenTTLSeconds is the lifetime of the bootstrap token a token operation creates.
	TokenTTLSeconds int `json:"token_ttl_seconds,omitempty"`
}

var (
	kubernetesUUID      = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	kubernetesJoinToken = regexp.MustCompile(`^K10[0-9a-f]{64}::[a-z0-9]{6}\.[a-z0-9]{16}$`)
	kubernetesSHA256    = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// ValidKubernetesJoinToken reports whether token is a bootstrap token in the format that
// pins the cluster CA: K10<CA SHA-256>::<id>.<secret>.
func ValidKubernetesJoinToken(token string) bool { return kubernetesJoinToken.MatchString(token) }

// KubernetesTokenCAHash returns the cluster CA hash a valid join token pins.
func KubernetesTokenCAHash(token string) (string, bool) {
	if !ValidKubernetesJoinToken(token) {
		return "", false
	}
	return token[3:67], true
}

// ValidKubernetesNodeAddress reports whether address is a unicast IP literal a node can
// advertise in the cluster.
func ValidKubernetesNodeAddress(address string) bool {
	parsed, e := netip.ParseAddr(address)
	return e == nil && parsed.String() == address && parsed.Zone() == "" && !parsed.Is4In6() && parsed.IsGlobalUnicast()
}

// Validate checks the command against the operation it travels with.
func (c KubernetesCommand) Validate(operation string) error {
	invalid := errors.New("AGENT_OPERATION_UNSUPPORTED")
	if !kubernetesUUID.MatchString(c.ClusterID) || c.Version != approvedKubernetesRelease.Version || !kubernetesUUID.MatchString(c.NodeName) || !ValidKubernetesNodeAddress(c.NodeAddress) {
		return invalid
	}
	server := c.Role == KubernetesServerRole
	worker := c.Role == KubernetesWorkerRole
	switch operation {
	case KubernetesJoinOperation:
		if server && c.ServerAddress == "" && c.JoinToken == "" && c.TokenTTLSeconds == 0 {
			return nil
		}
		if worker && ValidKubernetesNodeAddress(c.ServerAddress) && c.ServerAddress != c.NodeAddress && ValidKubernetesJoinToken(c.JoinToken) && c.TokenTTLSeconds == 0 {
			return nil
		}
	case KubernetesTokenOperation:
		if server && c.ServerAddress == "" && c.JoinToken == "" && c.TokenTTLSeconds >= KubernetesTokenMinimumSeconds && c.TokenTTLSeconds <= KubernetesTokenMaximumSeconds {
			return nil
		}
	case KubernetesObserveOperation:
		if (server || worker) && c.ServerAddress == "" && c.JoinToken == "" && c.TokenTTLSeconds == 0 {
			return nil
		}
	}
	return invalid
}

// KubernetesErrors are the error codes a Kubernetes node reports.
var KubernetesErrors = map[string]bool{
	"":                                 true,
	"KUBERNETES_PRIVILEGE_REQUIRED":    true,
	"KUBERNETES_SYSTEMD_UNAVAILABLE":   true,
	"KUBERNETES_DEFAULT_ROUTE_MISSING": true,
	"KUBERNETES_ADDRESS_NOT_LOCAL":     true,
	"KUBERNETES_RESOLVER_INVALID":      true,
	"KUBERNETES_DISK_SPACE_LOW":        true,
	"KUBERNETES_CONFLICT":              true,
	"KUBERNETES_CONFIG_CONFLICT":       true,
	"KUBERNETES_ASSET_UNAVAILABLE":     true,
	"KUBERNETES_ASSET_INVALID":         true,
	"KUBERNETES_ARCHIVE_INVALID":       true,
	"KUBERNETES_IO_FAILED":             true,
	"KUBERNETES_SERVICE_FAILED":        true,
	"KUBERNETES_NOT_READY":             true,
	"KUBERNETES_TOKEN_FAILED":          true,
	"KUBERNETES_NOT_INSTALLED":         true,
	"KUBERNETES_CANCELLED":             true,
}

// KubernetesObservation is what a Kubernetes node reports after an operation.
type KubernetesObservation struct {
	ClusterID string `json:"cluster_id"`
	Version   string `json:"version"`
	Role      string `json:"role"`
	State     string `json:"state"`
	ErrorCode string `json:"error_code"`
	// CASHA256 is the SHA-256 of the cluster's server CA certificate, which join tokens
	// pin. Only a server reports it.
	CASHA256 string `json:"ca_sha256,omitempty"`
	// JoinToken is the bootstrap token a token operation created. It is a secret: control
	// passes it to joining workers and never logs or returns it to a browser.
	JoinToken          string    `json:"join_token,omitempty"`
	JoinTokenExpiresAt time.Time `json:"join_token_expires_at,omitzero"`
	ObservedAt         time.Time `json:"observed_at"`
}

// ValidateFor checks an observation against the command and operation it answers, for
// the given task outcome.
func (o KubernetesObservation) ValidateFor(c KubernetesCommand, operation, outcome string) error {
	invalid := errors.New("VALIDATION_FAILED")
	if o.ClusterID != c.ClusterID || o.Version != c.Version || o.Role != c.Role || !KubernetesErrors[o.ErrorCode] || o.ObservedAt.IsZero() || o.ObservedAt.Year() < 2020 || o.ObservedAt.Year() > 2200 {
		return invalid
	}
	if o.State != KubernetesReady && o.State != KubernetesNotReady && o.State != KubernetesNotInstalled {
		return invalid
	}
	if (outcome == "success") != (o.ErrorCode == "") || outcome != "success" && outcome != "failure" {
		return invalid
	}
	if o.CASHA256 != "" && (!kubernetesSHA256.MatchString(o.CASHA256) || c.Role != KubernetesServerRole) {
		return invalid
	}
	token := o.JoinToken != "" || !o.JoinTokenExpiresAt.IsZero()
	switch operation {
	case KubernetesJoinOperation:
		if token || outcome == "success" && (o.State != KubernetesReady || c.Role == KubernetesServerRole && o.CASHA256 == "") {
			return invalid
		}
	case KubernetesTokenOperation:
		if outcome == "failure" {
			if token {
				return invalid
			}
			return nil
		}
		hash, ok := KubernetesTokenCAHash(o.JoinToken)
		if !ok || hash != o.CASHA256 || o.JoinTokenExpiresAt.Before(o.ObservedAt) || o.JoinTokenExpiresAt.Sub(o.ObservedAt) > time.Duration(c.TokenTTLSeconds)*time.Second {
			return invalid
		}
	case KubernetesObserveOperation:
		if token {
			return invalid
		}
	default:
		return invalid
	}
	return nil
}

// KubernetesArtifactRequest asks for one bounded range of one approved artifact.
type KubernetesArtifactRequest struct {
	LeaseToken string `json:"lease_token"`
	Name       string `json:"name"`
	Offset     int64  `json:"offset"`
	Length     int64  `json:"length"`
}

// Validate checks that the range is the next whole chunk of an approved artifact.
func (r KubernetesArtifactRequest) Validate() error {
	artifact, ok := KubernetesArtifactForName(r.Name)
	if !ok || r.Offset < 0 || r.Offset%KubernetesTransferChunkBytes != 0 || r.Offset >= artifact.SizeBytes || r.Length != min(KubernetesTransferChunkBytes, artifact.SizeBytes-r.Offset) {
		return errors.New("VALIDATION_FAILED")
	}
	return nil
}

// ValidateKubernetes checks a task result against the Kubernetes command it answers: the
// result carries only its observation, and the observation's error code is the result's.
func (r Result) ValidateKubernetes(c KubernetesCommand, operation string) error {
	invalid := errors.New("VALIDATION_FAILED")
	if !IsKubernetesOperation(operation) || r.Kubernetes == nil || r.Facts != nil || r.Component != nil || r.Logs != nil || r.Inference != nil || r.ErrorCode != r.Kubernetes.ErrorCode {
		return invalid
	}
	return r.Kubernetes.ValidateFor(c, operation, r.Outcome)
}
