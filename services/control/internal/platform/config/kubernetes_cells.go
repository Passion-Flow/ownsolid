package config

import (
	"errors"

	agentwire "github.com/Passion-Flow/ownsolid/api/agent/v1"
)

// Bounds and defaults of KubernetesCellsConfiguration (ADR 0051).
const (
	defaultKubernetesJoinSeconds      = 1800
	minimumKubernetesJoinSeconds      = 300
	maximumKubernetesJoinSeconds      = 7200
	defaultKubernetesTokenTTLSeconds  = 3600
	defaultKubernetesJoinBatch        = 50
	maximumKubernetesJoinBatch        = 50
	defaultKubernetesReconcileSeconds = 10
	maximumKubernetesReconcileSeconds = 300
)

// KubernetesCellsConfiguration lets control install one Kubernetes cluster per cell on its
// Kubernetes nodes. Without it, control refuses to create cell clusters.
type KubernetesCellsConfiguration struct {
	// ArtifactDirectory holds the files of the approved RKE2 release under their release
	// names, as the offline bundle places them. Agents download them from control.
	ArtifactDirectory string `yaml:"artifact_directory"`
	// JoinTokenKeyFile holds at least 32 random bytes that seal join tokens at rest. Control
	// and every agent gateway read it; nothing else needs it.
	JoinTokenKeyFile string `yaml:"join_token_key_file"`
	// JoinSeconds bounds one join: downloading about 850 MB, installing, and waiting for the
	// node to be Ready.
	JoinSeconds int `yaml:"join_seconds"`
	// TokenTTLSeconds is the lifetime of the bootstrap token workers join with. Control asks
	// for a new one when less than JoinSeconds remain.
	TokenTTLSeconds int `yaml:"token_ttl_seconds"`
	// JoinBatch is how many workers of one cluster join at once. Each join loads the server,
	// and RKE2's sizing guidance is batches of 50 or fewer.
	JoinBatch int `yaml:"join_batch"`
	// ReconcileSeconds is how often control looks for cluster work that no task result
	// moved on, such as a task that expired.
	ReconcileSeconds int `yaml:"reconcile_seconds"`
}

// WithDefaults fills every unset bound.
func (c KubernetesCellsConfiguration) WithDefaults() KubernetesCellsConfiguration {
	if c.JoinSeconds == 0 {
		c.JoinSeconds = defaultKubernetesJoinSeconds
	}
	if c.TokenTTLSeconds == 0 {
		c.TokenTTLSeconds = defaultKubernetesTokenTTLSeconds
	}
	if c.JoinBatch == 0 {
		c.JoinBatch = defaultKubernetesJoinBatch
	}
	if c.ReconcileSeconds == 0 {
		c.ReconcileSeconds = defaultKubernetesReconcileSeconds
	}
	return c
}

// Validate checks the configuration after defaults are filled.
func (c KubernetesCellsConfiguration) Validate() error {
	if !canonicalAbsolutePath(c.ArtifactDirectory) || !canonicalAbsolutePath(c.JoinTokenKeyFile) || c.JoinSeconds < minimumKubernetesJoinSeconds || c.JoinSeconds > maximumKubernetesJoinSeconds ||
		c.TokenTTLSeconds < agentwire.KubernetesTokenMinimumSeconds || c.TokenTTLSeconds > agentwire.KubernetesTokenMaximumSeconds ||
		// A token must outlive the join that uses it.
		c.TokenTTLSeconds <= c.JoinSeconds ||
		c.JoinBatch < 1 || c.JoinBatch > maximumKubernetesJoinBatch || c.ReconcileSeconds < 1 || c.ReconcileSeconds > maximumKubernetesReconcileSeconds {
		return errors.New("CONFIG_KUBERNETES_CELLS_INVALID")
	}
	return nil
}
