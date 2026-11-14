package release

// Only this extra external-cluster probe asset may carry an arm64 image. The
// platform profile and every original image role retain their amd64 contract.
const PrivateClusterProbeARM64AssetID = "private-cluster-probe-image-arm64"
const PrivateClusterProbeAMD64AssetID = "private-cluster-probe-image-amd64"
const PrivateClusterCapability = "M28"
const PrivateClusterSchemaVersion = 27

func validImageArchitecture(a Asset) bool {
	if a.ID == PrivateClusterProbeARM64AssetID {
		return a.Architecture == "arm64"
	}
	return a.Architecture == "amd64"
}
