package v1

import (
	"errors"
	"time"
)

// InferenceRemovalReceipt is an explicit local operator attestation, not remote hardware attestation.
// Control accepts it only for an already fenced, credential-revoked node and matching retained ownership.
type InferenceRemovalReceipt struct {
	Derived              *DerivedInferenceIdentity `json:"derived,omitempty"`
	SchemaVersion        int                       `json:"schema_version"`
	ReceiptID            string                    `json:"receipt_id"`
	NodeID               string                    `json:"node_id"`
	DeploymentID         string                    `json:"deployment_id"`
	ModelID              string                    `json:"model_id"`
	CatalogDigest        string                    `json:"catalog_digest"`
	GPUUUID              string                    `json:"gpu_uuid"`
	LocalRevision        int64                     `json:"local_revision"`
	OwnedContainerAbsent bool                      `json:"owned_container_absent"`
	RuntimeCacheAbsent   bool                      `json:"runtime_cache_absent"`
	ModelCachePresent    bool                      `json:"model_cache_present"`
	CompletedAt          time.Time                 `json:"completed_at"`
}

func (r InferenceRemovalReceipt) Validate() error {
	c := InferenceCommand{Action: "remove", DeploymentID: r.DeploymentID, ModelID: r.ModelID, CatalogDigest: r.CatalogDigest, GPUUUID: r.GPUUUID, Revision: r.LocalRevision, Derived: r.Derived}
	if r.SchemaVersion != 1 || !inferenceUUID.MatchString(r.ReceiptID) || !inferenceUUID.MatchString(r.NodeID) || !r.OwnedContainerAbsent || !r.RuntimeCacheAbsent || !inferenceTime(r.CompletedAt) || c.Validate(c.Operation()) != nil {
		return errors.New("VALIDATION_FAILED")
	}
	return nil
}
