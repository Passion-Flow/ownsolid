// Package view defines the bounded, secret-free projection of an operator journal.
// It is recorded history, never a live process-health or target qualification claim.
package view

import (
	"errors"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
	"regexp"
	"time"
)

const Format = "projectone.release-view.v1"
const RelativeDirectory = "data/release-view"
const ContainerDirectory = "/run/projectone/release-view"
const SnapshotFile = "current.json"
const DirtyFile = "updating"
const PendingFile = ".next.json"

// These are wire safety bounds, not journal retention. The original journal is retained.
const MaximumEvents = 256
const MaximumBytes = 262144

var scopeID = regexp.MustCompile(`^[a-f0-9]{32}$`)
var token = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,95}$`)
var code = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,95}$`)
var releaseID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

type Event struct {
	Sequence    int    `json:"sequence"`
	OperationID string `json:"operation_id"`
	Action      string `json:"action"`
	Outcome     string `json:"outcome"`
	Code        string `json:"code"`
	Time        string `json:"time"`
}
type Snapshot struct {
	InstalledRelease    *InstalledRelease         `json:"installed_release,omitempty"`
	Format              string                    `json:"format"`
	ScopeID             string                    `json:"scope_id"`
	ReleaseID           string                    `json:"release_id"`
	Profile             string                    `json:"profile"`
	ArchiveSHA256       string                    `json:"archive_sha256"`
	ManifestSHA256      string                    `json:"manifest_sha256"`
	ConfigurationSHA256 string                    `json:"configuration_sha256"`
	Sequence            int                       `json:"sequence"`
	HeadSHA256          string                    `json:"head_sha256"`
	JournalBytes        int64                     `json:"journal_bytes"`
	RecordedAt          string                    `json:"recorded_at"`
	FirstSequence       int                       `json:"first_sequence"`
	Events              []Event                   `json:"events"`
	Kubernetes          *KubernetesJournalBinding `json:"kubernetes,omitempty"`
}

func ValidScopeID(s string) bool { return scopeID.MatchString(s) }
func (s Snapshot) Validate() error {
	bad := errors.New("RELEASE_VIEW_INVALID")
	if s.Format != Format && s.Format != KubernetesFormat || !ValidScopeID(s.ScopeID) || !releaseID.MatchString(s.ReleaseID) || !releaseID.MatchString(s.Profile) || !wire.ValidSHA256(s.ArchiveSHA256) || !wire.ValidSHA256(s.ManifestSHA256) || !wire.ValidSHA256(s.ConfigurationSHA256) || !wire.ValidSHA256(s.HeadSHA256) || s.Sequence < 1 || s.Sequence > 9007199254740991 || s.JournalBytes < 1 || s.FirstSequence < 1 || len(s.Events) < 1 || len(s.Events) > MaximumEvents || s.FirstSequence+len(s.Events)-1 != s.Sequence {
		return bad
	}
	for i, e := range s.Events {
		at, err := time.Parse(time.RFC3339Nano, e.Time)
		if err != nil || at.IsZero() || at.Location() != time.UTC || e.Sequence != s.FirstSequence+i || !ValidScopeID(e.OperationID) || !token.MatchString(e.Action) || !token.MatchString(e.Outcome) || e.Code != "" && !code.MatchString(e.Code) {
			return bad
		}
	}
	if s.Format == Format && (s.Kubernetes != nil || !s.validInstalledRelease()) || s.Format == KubernetesFormat && !s.validKubernetesBinding() || s.RecordedAt != s.Events[len(s.Events)-1].Time {
		return bad
	}
	return nil
}
