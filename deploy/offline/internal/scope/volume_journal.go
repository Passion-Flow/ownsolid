package scope

import (
	"math"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/compose"
	"regexp"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

const phaseReserved = "reserved"
const phaseVolumeCreated = "volume_created"
const phaseUtilityCreated = "utility_created"
const phaseProjectionSubmitted = "projection_submitted"
const phasePayloadVerified = "payload_verified"
const phaseUtilityRemoveSubmitted = "utility_remove_submitted"
const phasePrepared = "prepared"

var utilityIDPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

type VolumeRecord struct {
	Key                 string `json:"key"`
	DescriptorSHA256    string `json:"descriptor_sha256"`
	PayloadSHA256       string `json:"payload_sha256"`
	ImageID             string `json:"image_id"`
	UID                 int    `json:"uid"`
	GID                 int    `json:"gid"`
	ResourceOperationID string `json:"resource_operation_id"`
	Phase               string `json:"phase"`
	UtilityID           string `json:"utility_id,omitempty"`
}
type volumeRecords struct {
	roles   map[string]VolumeRecord
	pending map[string]Event
}

func phaseNumber(phase string) int {
	for i, value := range []string{phaseReserved, phaseVolumeCreated, phaseUtilityCreated, phaseProjectionSubmitted, phasePayloadVerified, phaseUtilityRemoveSubmitted, phasePrepared} {
		if phase == value {
			return i
		}
	}
	return -1
}
func volumeState(events []Event) volumeRecords {
	state := volumeRecords{roles: map[string]VolumeRecord{}, pending: map[string]Event{}}
	for _, event := range events {
		if event.Action == "volume_prepare" {
			state.roles[event.Volume.Key] = *event.Volume
			if event.Outcome == "completed" {
				delete(state.pending, event.OperationID)
			} else {
				state.pending[event.OperationID] = event
			}
		}
		if event.Action == "volume_reconcile" && event.Outcome == "completed" {
			delete(state.pending, event.ReferenceOperationID)
			state.roles[event.Volume.Key] = *event.Volume
		}
	}
	return state
}
func volumeIdentity(a, b VolumeRecord) bool {
	a.Phase = b.Phase
	a.UtilityID = b.UtilityID
	return a == b
}
func validVolumeRecord(record *VolumeRecord) bool {
	if record == nil {
		return false
	}
	if _, ok := roleServices[record.Key]; !ok && record.Key != engine.NativePostgreSQLRegistryRole && record.Key != compose.MVPRegistryReleaseRole {
		return false
	}
	phase := phaseNumber(record.Phase)
	if !wire.ValidSHA256(record.DescriptorSHA256) || !wire.ValidSHA256(record.PayloadSHA256) || !immutableDigest(record.ImageID) || record.UID < 1 || record.GID < 1 || record.UID > math.MaxInt32 || record.GID > math.MaxInt32 || !identityPattern.MatchString(record.ResourceOperationID) || phase < 0 {
		return false
	}
	if phase < phaseNumber(phaseUtilityCreated) {
		return record.UtilityID == ""
	}
	return utilityIDPattern.MatchString(record.UtilityID)
}
func validateVolumeEvent(events []Event, e Event) error {
	bad := func() error { return failure("OFFLINE_SCOPE_VOLUME_JOURNAL_TRANSITION_INVALID") }
	if !validVolumeRecord(e.Volume) || e.Inputs != nil || e.Process != nil || e.Engine != nil || e.Image != nil || e.Runtime != nil {
		return bad()
	}
	inputs, outcome, _ := inputsState(events)
	if (e.Volume.Key == engine.NativePostgreSQLRegistryRole || e.Volume.Key == compose.MVPRegistryReleaseRole) && (inputs == nil || inputs.Profile != wire.MVPPostgreSQLCPUProfile) {
		return bad()
	}
	if inputs == nil || outcome != "completed" || inputs.DescriptorSHA256 != e.Volume.DescriptorSHA256 || inputs.ImageID != e.Volume.ImageID {
		return bad()
	}
	images := imageState(events)
	if len(images.pending) > 0 {
		return bad()
	}
	state := volumeState(events)
	if e.Action == "volume_prepare" {
		if e.ReferenceOperationID != "" {
			return bad()
		}
		if e.Outcome == "started" {
			if len(state.pending) > 0 || e.Code != "" {
				return bad()
			}
			for _, prior := range events {
				if prior.OperationID == e.OperationID {
					return bad()
				}
			}
			prior, exists := state.roles[e.Volume.Key]
			if exists {
				if prior.Phase == phasePrepared || prior.Phase == phaseProjectionSubmitted || prior.Phase == phaseUtilityRemoveSubmitted || prior != *e.Volume {
					return bad()
				}
			} else {
				if e.Volume.Phase != phaseReserved {
					return bad()
				}
				for _, r := range state.roles {
					if r.ResourceOperationID == e.Volume.ResourceOperationID {
						return bad()
					}
				}
			}
			return nil
		}
		prior, ok := state.pending[e.OperationID]
		if !ok || prior.Outcome == "uncertain" || !volumeIdentity(*prior.Volume, *e.Volume) {
			return bad()
		}
		if prior.Volume.UtilityID != "" && prior.Volume.UtilityID != e.Volume.UtilityID {
			return bad()
		}
		switch e.Outcome {
		case "checkpoint":
			if e.Code != "" || phaseNumber(e.Volume.Phase) != phaseNumber(prior.Volume.Phase)+1 || e.Volume.Phase == phasePrepared {
				return bad()
			}
			return nil
		case "completed":
			if e.Code != "" || prior.Volume.Phase != phaseUtilityRemoveSubmitted || e.Volume.Phase != phasePrepared {
				return bad()
			}
			return nil
		case "uncertain":
			if e.Volume.Phase != prior.Volume.Phase || e.Volume.UtilityID != prior.Volume.UtilityID || !codePattern.MatchString(e.Code) {
				return bad()
			}
			return nil
		}
		return bad()
	}
	if e.Action == "volume_reconcile" {
		prior, ok := state.pending[e.ReferenceOperationID]
		if !ok || !volumeIdentity(*prior.Volume, *e.Volume) || phaseNumber(e.Volume.Phase) < phaseNumber(prior.Volume.Phase) || prior.Volume.UtilityID != "" && prior.Volume.UtilityID != e.Volume.UtilityID {
			return bad()
		}
		for _, p := range events {
			if p.OperationID == e.OperationID {
				return bad()
			}
		}
		if e.Outcome == "unresolved" && codePattern.MatchString(e.Code) && *prior.Volume == *e.Volume {
			return nil
		}
		if e.Outcome == "completed" && e.Code == "" {
			return nil
		}
	}
	return bad()
}
