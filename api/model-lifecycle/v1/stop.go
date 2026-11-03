package v1

import (
	"bytes"
	"encoding/json"
	"errors"
	"time"

	datawire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	training "github.com/Passion-Flow/ownsolid/api/training/v1"
)

const MaximumStopReceiptBytes = 32768
const MaximumStopSubmissionBytes = MaximumStopReceiptBytes + MaximumResultBytes + 4096

type StopSubmission struct {
	LeaseToken string          `json:"lease_token"`
	Receipt    StopReceipt     `json:"receipt"`
	Outcome    json.RawMessage `json:"outcome"`
}

func DecodeStopSubmission(raw []byte) (StopSubmission, error) {
	var out StopSubmission
	bad := errors.New("MODEL_PREPARATION_STOP_INVALID")
	if int64(len(raw)) > MaximumStopSubmissionBytes {
		return out, bad
	}
	fields, e := training.ExactObject(raw, []string{"lease_token", "receipt", "outcome"})
	if e != nil || json.Unmarshal(fields["lease_token"], &out.LeaseToken) != nil || !machineToken.MatchString(out.LeaseToken) || len(fields["receipt"]) > MaximumStopReceiptBytes || DecodeStrict(fields["receipt"], &out.Receipt) != nil {
		return out, bad
	}
	if !bytes.Equal(bytes.TrimSpace(fields["outcome"]), []byte("null")) {
		if len(fields["outcome"]) < 2 || int64(len(fields["outcome"])) > MaximumResultBytes || fields["outcome"][0] != '{' {
			return out, bad
		}
		out.Outcome = fields["outcome"]
	}
	return out, nil
}

func ContainerName(attempt string) string { return "projectone-model-preparation-" + attempt }

type StopReceipt struct {
	SchemaVersion        int       `json:"schema_version"`
	ReceiptID            string    `json:"receipt_id"`
	JournalID            string    `json:"journal_id"`
	NodeID               string    `json:"node_id"`
	TaskID               string    `json:"task_id"`
	Scope                Scope     `json:"scope"`
	AttemptID            string    `json:"attempt_id"`
	AttemptNumber        int       `json:"attempt_number"`
	CommandSHA256        string    `json:"command_sha256"`
	ImageID              string    `json:"image_id"`
	GPUUUID              string    `json:"gpu_uuid"`
	ContainerName        string    `json:"container_name"`
	ContainerID          *string   `json:"container_id"`
	Proof                string    `json:"proof"`
	OwnedContainerAbsent bool      `json:"owned_container_absent"`
	OutcomeSHA256        *string   `json:"outcome_sha256"`
	AgentErrorCode       string    `json:"agent_error_code"`
	CompletedAt          time.Time `json:"completed_at"`
}

func (r StopReceipt) ValidateFor(c Command, node, task string) error {
	digest, e := c.Digest()
	if e != nil || r.SchemaVersion != 1 || !datawire.ValidID(r.ReceiptID) || !datawire.ValidID(r.JournalID) || !datawire.ValidID(node) || !datawire.ValidID(task) || r.NodeID != node || r.TaskID != task || r.Scope != c.Scope || r.AttemptID != c.AttemptID || r.AttemptNumber != c.AttemptNumber || r.CommandSHA256 != digest || c.Recipe.Identity.ImageID == nil || r.ImageID != *c.Recipe.Identity.ImageID || r.GPUUUID != c.GPUUUID || r.ContainerName != ContainerName(c.AttemptID) || !r.OwnedContainerAbsent || r.CompletedAt.Year() < 2020 || r.CompletedAt.Year() > 2200 || r.OutcomeSHA256 != nil && !datawire.ValidDigest(*r.OutcomeSHA256) {
		return errors.New("MODEL_PREPARATION_STOP_INVALID")
	}
	if r.Proof == "never_created" && r.ContainerID == nil || r.Proof == "owned_container_removed" && r.ContainerID != nil && datawire.ValidDigest(*r.ContainerID) {
		if ValidAgentError(r.AgentErrorCode) {
			return nil
		}
	}
	return errors.New("MODEL_PREPARATION_STOP_INVALID")
}

type StopAcceptance struct {
	PreparationID string `json:"preparation_id"`
	AttemptID     string `json:"attempt_id"`
	ReceiptID     string `json:"receipt_id"`
	State         string `json:"state"`
	GPUReleased   bool   `json:"gpu_released"`
	Duplicate     bool   `json:"duplicate"`
}
