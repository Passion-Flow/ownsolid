package release

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
)

// One cumulative CPU/Compose product selection. Its schema is an immutable
// account22 prefix plus seven new migrations, never the legacy platform27 chain.
const MVPPostgreSQLCPUProfile = "mvp-postgresql-cpu-v1-linux-amd64"
const MVPPostgreSQLCPULineageAssetID = "schema-lineage"
const MVPComponentOriginsAssetID = "mvp-component-origins"
const MVPDeploymentPrerequisitesAssetID = "mvp-deployment-prerequisites"

//go:embed mvp-postgresql-cpu-v1-lineage.json
var mvpPostgreSQLCPULineage []byte

// MVPPostgreSQLCPULineage returns a copy of the versioned SQL checksum closure.
// The signed asset must contain exactly these bytes; the bundle reader checks
// its actual bytes against the signed SHA in the ordinary asset verification.
func MVPPostgreSQLCPULineage() []byte {
	return append([]byte(nil), mvpPostgreSQLCPULineage...)
}

func MVPPostgreSQLCPULineageSHA256() string {
	digest := sha256.Sum256(mvpPostgreSQLCPULineage)
	return hex.EncodeToString(digest[:])
}

func mvpPostgreSQLCPURequiredAssets() map[string]string {
	required := kubernetesRequiredAssets()
	for id, kind := range map[string]string{
		MVPPostgreSQLCPULineageAssetID:    "dependency_lock",
		MVPComponentOriginsAssetID:        "dependency_lock",
		MVPDeploymentPrerequisitesAssetID: "runbook",
		"inference-image":                 "image_archive",
		"inference-recipe":                "dependency_lock",
		"inference-terms":                 "license",
		"postgres-client-origins":         "dependency_lock",
		PrivateClusterProbeAMD64AssetID:   "image_archive",
	} {
		required[id] = kind
	}
	return required
}
