package release

// This is a prepared-cluster delivery contract, not a host/Kubernetes installer.
// Legacy Ubuntu, M16 and M17 profiles retain their exact requirements.
const CumulativeKubernetesCPUProfile = "m18-postgresql-cpu-kubernetes-linux-amd64"

const KubernetesChartAssetID = "kubernetes-chart"
const KubernetesOperatorAMD64AssetID = "kubernetes-operator-amd64"
const KubernetesOperatorARM64AssetID = "kubernetes-operator-arm64"
const KubernetesHelmAMD64AssetID = "kubernetes-helm-amd64"
const KubernetesOriginsAssetID = "kubernetes-component-origins"

func kubernetesRequiredAssets() map[string]string {
	required := cumulativeRequiredAssets()
	for _, id := range []string{KubernetesChartAssetID, KubernetesOperatorAMD64AssetID, KubernetesOperatorARM64AssetID, KubernetesHelmAMD64AssetID} {
		required[id] = "operator"
	}
	required[KubernetesOriginsAssetID] = "dependency_lock"
	return required
}
