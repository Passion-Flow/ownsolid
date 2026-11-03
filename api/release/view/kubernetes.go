package view

import (
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
	"regexp"
)

// KubernetesFormat is an additive recorded-source view of one actual ordered
// K18 index. It does not merge independently numbered original journals.
const KubernetesFormat = "projectone.kubernetes-release-view.v1"
const KubernetesIndexFormat = "projectone.kubernetes-operation-index.v1"

type KubernetesSourceReference struct {
	Kind        string `json:"kind"`
	EventKind   string `json:"event_kind"`
	OperationID string `json:"operation_id"`
	PlanSHA256  string `json:"plan_sha256"`
	Sequence    int    `json:"sequence"`
	SHA256      string `json:"sha256"`
}

type KubernetesInstalledRelease struct {
	ReleaseID          string `json:"release_id"`
	Profile            string `json:"profile"`
	ArchiveSHA256      string `json:"archive_sha256"`
	ManifestSHA256     string `json:"manifest_sha256"`
	SourceCommit       string `json:"source_commit"`
	SchemaVersion      int    `json:"schema_version"`
	ActivationSequence int    `json:"activation_sequence"`
}

type KubernetesJournalBinding struct {
	IndexFormat        string                      `json:"index_format"`
	ApplicationProfile string                      `json:"application_profile"`
	SchemaVersion      int                         `json:"schema_version"`
	PreparedPlanSHA256 string                      `json:"prepared_plan_sha256"`
	LastSource         KubernetesSourceReference   `json:"last_source"`
	InstalledRelease   *KubernetesInstalledRelease `json:"installed_release,omitempty"`
}

func KubernetesSourceKind(kind string) bool {
	switch kind {
	case "preparation", "installation", "maintenance", "drain", "schema", "postgres_backup", "postgres_restore", "restore_observer", "restored_readiness", "restored_serving", "upgrade_jobs", "upgrade_serving", "uninstall":
		return true
	}
	return false
}

func (s Snapshot) validKubernetesBinding() bool {
	b := s.Kubernetes
	if b == nil || s.InstalledRelease != nil || b.IndexFormat != KubernetesIndexFormat || b.ApplicationProfile != wire.MVPPostgreSQLCPUApplicationProfile || b.SchemaVersion != wire.MVPPostgreSQLCPUV1SchemaVersion || !wire.ValidSHA256(b.PreparedPlanSHA256) {
		return false
	}
	source := b.LastSource
	if !KubernetesSourceKind(source.Kind) || !token.MatchString(source.EventKind) || !ValidScopeID(source.OperationID) || !wire.ValidSHA256(source.PlanSHA256) || source.Sequence < 1 || source.Sequence > s.Sequence || !wire.ValidSHA256(source.SHA256) || len(s.Events) == 0 {
		return false
	}
	last := s.Events[len(s.Events)-1]
	if last.OperationID != source.OperationID || last.Action != source.Kind+"_"+source.EventKind {
		return false
	}
	active := b.InstalledRelease
	if active == nil {
		return true
	}
	if !releaseID.MatchString(active.ReleaseID) || active.Profile != s.Profile || !wire.ValidSHA256(active.ArchiveSHA256) || !wire.ValidSHA256(active.ManifestSHA256) || !regexp.MustCompile(`^[a-f0-9]{40}$`).MatchString(active.SourceCommit) || active.SchemaVersion != b.SchemaVersion || active.ActivationSequence < 1 || active.ActivationSequence > s.Sequence {
		return false
	}
	if active.ActivationSequence >= s.FirstSequence {
		event := s.Events[active.ActivationSequence-s.FirstSequence]
		if event.Outcome != "serving_verified" {
			return false
		}
		switch event.Action {
		case "installation_terminal", "restored_serving_terminal", "upgrade_serving_terminal":
		default:
			return false
		}
	}
	return true
}
