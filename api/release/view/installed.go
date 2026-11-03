package view

import (
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
	"regexp"
)

// InstalledRelease is a completed journal outcome, never live readiness. The
// original Snapshot bindings still identify the original installation scope.
type InstalledRelease struct {
	ReleaseID          string `json:"release_id"`
	Profile            string `json:"profile"`
	ArchiveSHA256      string `json:"archive_sha256"`
	ManifestSHA256     string `json:"manifest_sha256"`
	SourceCommit       string `json:"source_commit"`
	SchemaVersion      int    `json:"schema_version"`
	ActivationSequence int    `json:"activation_sequence"`
}

func (s Snapshot) validInstalledRelease() bool {
	i := s.InstalledRelease
	if i == nil {
		return true
	}
	profileValid := i.Profile == wire.CumulativeCPUProfile && i.SchemaVersion == 22
	if i.Profile == wire.MVPPostgreSQLCPUProfile {
		profileValid = s.Profile == wire.CumulativeCPUProfile && i.SchemaVersion == wire.MVPPostgreSQLCPUV1SchemaVersion
	}
	if !releaseID.MatchString(i.ReleaseID) || i.ReleaseID == s.ReleaseID || !profileValid || !wire.ValidSHA256(i.ArchiveSHA256) || !wire.ValidSHA256(i.ManifestSHA256) || i.ManifestSHA256 == s.ManifestSHA256 || !regexp.MustCompile(`^[a-f0-9]{40}$`).MatchString(i.SourceCommit) || i.ActivationSequence < 1 || i.ActivationSequence > s.Sequence {
		return false
	}
	if i.ActivationSequence >= s.FirstSequence {
		index := i.ActivationSequence - s.FirstSequence
		if index >= len(s.Events) || s.Events[index].Action != "upgrade_activate" || s.Events[index].Outcome != "completed" {
			return false
		}
	}
	return true
}
