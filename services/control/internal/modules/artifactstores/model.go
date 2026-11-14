// Package artifactstores owns private S3 ingress, current exact-object authority
// and durable handoff into existing local model/dataset publication owners.
package artifactstores

import (
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/datasets"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/models"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/objectstore"
	"time"
)

type Service struct {
	lifecycle     *lifecycle
	Identity      *identity.Service
	Workspaces    *workspaces.Service
	Models        *models.Service
	Datasets      *datasets.Service
	Configuration config.ObjectStorageConfiguration
	Transport     objectstore.Transport
	Stage         *objectstore.Stage
}
type Object struct {
	ID              string    `json:"id"`
	WorkspaceID     string    `json:"workspace_id"`
	Name            string    `json:"name"`
	Purpose         string    `json:"purpose"`
	ExpectedBytes   int64     `json:"expected_bytes"`
	SHA256          string    `json:"sha256"`
	PartBytes       int64     `json:"part_bytes"`
	State           string    `json:"state"`
	Revision        int64     `json:"revision"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
	UploadExpiresAt time.Time `json:"upload_expires_at"`
	ExpiresAt       time.Time `json:"expires_at"`
	ErrorCode       *string   `json:"error_code"`
}
type CreateRequest struct {
	WorkspaceID   string `json:"workspace_id"`
	Name          string `json:"name"`
	Purpose       string `json:"purpose"`
	ExpectedBytes int64  `json:"expected_bytes"`
	SHA256        string `json:"sha256"`
	Reason        string `json:"reason"`
}
type Grant struct {
	ID        string     `json:"id"`
	ObjectID  string     `json:"object_id"`
	Recipient string     `json:"recipient_user_id"`
	Action    string     `json:"action"`
	NotBefore time.Time  `json:"not_before"`
	ExpiresAt time.Time  `json:"expires_at"`
	RevokedAt *time.Time `json:"revoked_at"`
}
type GrantRequest struct {
	Recipient string `json:"recipient_user_id"`
	Action    string `json:"action"`
	Seconds   int    `json:"seconds"`
	Reason    string `json:"reason"`
}
type Ingestion struct {
	ID        string  `json:"id"`
	ObjectID  string  `json:"object_id"`
	Kind      string  `json:"kind"`
	TargetID  *string `json:"target_id"`
	State     string  `json:"state"`
	ErrorCode *string `json:"error_code"`
}
type ModelImportRequest struct {
	Name                string `json:"name"`
	CatalogDigest       string `json:"catalog_digest"`
	LicenseAcknowledged bool   `json:"license_acknowledged"`
	Reason              string `json:"reason"`
}
type DatasetImportRequest struct {
	DatasetID  string `json:"dataset_id"`
	SourceName string `json:"source_name"`
	Reason     string `json:"reason"`
}
type authority struct {
	FormatVersion int    `json:"format_version"`
	Actor         string `json:"actor_id"`
	Session       string `json:"session_id"`
	Audience      string `json:"audience"`
	Epoch         int64  `json:"epoch"`
	TokenHash     string `json:"token_hash"`
}
type storedObject struct {
	Object
	Tenant, Creator, Audience, StoreID                         string
	Epoch                                                      int64
	Membership                                                 *string
	UploadID, ETag, OperationID, OperationKind, OperationPhase *string
	OperationExpires                                           *time.Time
	OperationAuthority                                         []byte
	ExecutionEpoch                                             int64
	ExecutionMembership                                        *string
}

func (o storedObject) ref() objectstore.Ref {
	return objectstore.Ref{Tenant: o.Tenant, Workspace: o.WorkspaceID, Object: o.ID}
}
