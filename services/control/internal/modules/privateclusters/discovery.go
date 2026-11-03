package privateclusters

import (
	"context"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

type metadata struct {
	Name              string            `json:"name"`
	Namespace         string            `json:"namespace"`
	UID               string            `json:"uid"`
	ResourceVersion   string            `json:"resourceVersion"`
	Labels            map[string]string `json:"labels"`
	Annotations       map[string]string `json:"annotations"`
	DeletionTimestamp *string           `json:"deletionTimestamp"`
	OwnerReferences   []ownerReference  `json:"ownerReferences"`
}
type ownerReference struct {
	APIVersion         string `json:"apiVersion"`
	Kind               string `json:"kind"`
	Name               string `json:"name"`
	UID                string `json:"uid"`
	Controller         bool   `json:"controller"`
	BlockOwnerDeletion bool   `json:"blockOwnerDeletion"`
}
type object struct {
	APIVersion string   `json:"apiVersion"`
	Kind       string   `json:"kind"`
	Metadata   metadata `json:"metadata"`
}
type objectList[T any] struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Metadata   struct {
		Continue string `json:"continue"`
	} `json:"metadata"`
	Items []T `json:"items"`
}
type NodeCapacity struct {
	UID          string `json:"uid"`
	Name         string `json:"name"`
	CPU          string `json:"cpu"`
	Memory       string `json:"memory"`
	Architecture string `json:"architecture"`
}
type Observation struct {
	ServerVersion     string         `json:"server_version"`
	ClusterUID        string         `json:"cluster_uid"`
	NamespaceUID      string         `json:"namespace_uid"`
	Namespace         string         `json:"namespace"`
	QuotaUID          string         `json:"quota_uid"`
	PolicyUID         string         `json:"policy_uid"`
	ServiceAccountUID string         `json:"service_account_uid"`
	Nodes             []NodeCapacity `json:"nodes"`
	ObservedAt        time.Time      `json:"observed_at"`
	Qualification     string         `json:"qualification"`
}

func validMetadata(m metadata, name, namespace string) bool {
	return m.Name == name && m.Namespace == namespace && validID(m.UID) && m.ResourceVersion != "" && len(m.ResourceVersion) <= 128 && m.DeletionTimestamp == nil
}
func (c *client) scope(ctx context.Context) error {
	var version struct {
		GitVersion string `json:"gitVersion"`
	}
	if e := c.get(ctx, "/version", &version); e != nil {
		return e
	}
	if version.GitVersion != ServerVersion {
		return fail("PRIVATE_CLUSTER_VERSION_MISMATCH")
	}
	for _, v := range []struct {
		name, uid string
		selected  bool
	}{{"kube-system", c.profile.ClusterUID, false}, {c.profile.Namespace, c.profile.NamespaceUID, true}} {
		var n struct {
			object
			Status struct {
				Phase string `json:"phase"`
			} `json:"status"`
		}
		if e := c.get(ctx, "/api/v1/namespaces/"+url.PathEscape(v.name), &n); e != nil {
			return e
		}
		if n.APIVersion != "v1" || n.Kind != "Namespace" || !validMetadata(n.Metadata, v.name, "") || n.Metadata.UID != v.uid || n.Status.Phase != "Active" {
			return fail("PRIVATE_CLUSTER_SCOPE_CHANGED")
		}
		if v.selected && (n.Metadata.Labels[PurposeLabel] != Purpose || n.Metadata.Labels[InstallationLabel] != c.profile.InstallationID || n.Metadata.Labels[WorkspaceLabel] != c.profile.WorkspaceID || n.Metadata.Labels["pod-security.kubernetes.io/enforce"] != "restricted" || n.Metadata.Labels["pod-security.kubernetes.io/enforce-version"] != PodSecurityVersion) {
			return fail("PRIVATE_CLUSTER_ISOLATED_NAMESPACE_REQUIRED")
		}
	}
	return nil
}

type access struct {
	group, resource, verb, namespace, name string
	allowed                                bool
}

func (c *client) access(ctx context.Context) error {
	var identity struct {
		APIVersion string `json:"apiVersion"`
		Kind       string `json:"kind"`
		Status     struct {
			UserInfo struct {
				Username string   `json:"username"`
				UID      string   `json:"uid"`
				Groups   []string `json:"groups"`
			} `json:"userInfo"`
		} `json:"status"`
	}
	if e := c.json(ctx, http.MethodPost, "/apis/authentication.k8s.io/v1/selfsubjectreviews", map[string]any{"apiVersion": "authentication.k8s.io/v1", "kind": "SelfSubjectReview"}, &identity); e != nil {
		return e
	}
	user := identity.Status.UserInfo
	if identity.APIVersion != "authentication.k8s.io/v1" || identity.Kind != "SelfSubjectReview" || user.Username != "system:serviceaccount:"+c.profile.Namespace+":"+ControlServiceAccount || user.UID != c.profile.ControlServiceAccountUID {
		return fail("PRIVATE_CLUSTER_IDENTITY_MISMATCH")
	}
	for _, g := range user.Groups {
		if g == "system:masters" {
			return fail("PRIVATE_CLUSTER_EXCESSIVE_AUTHORITY")
		}
	}
	ns := c.profile.Namespace
	checks := []access{{"", "nodes", "list", "", "", true}, {"", "namespaces", "get", "", ns, true}, {"", "namespaces", "get", "", "kube-system", true},
		{"", "resourcequotas", "get", ns, QuotaName, true}, {"networking.k8s.io", "networkpolicies", "list", ns, "", true},
		{"batch", "jobs", "create", ns, "", true}, {"batch", "jobs", "get", ns, "", true}, {"batch", "jobs", "list", ns, "", true}, {"batch", "jobs", "delete", ns, "", true},
		{"", "pods", "list", ns, "", true}, {"", "pods/log", "get", ns, "", true},
		{"coordination.k8s.io", "leases", "get", ns, LeaseName, true}, {"coordination.k8s.io", "leases", "create", ns, "", true}, {"coordination.k8s.io", "leases", "delete", ns, LeaseName, true},
		{"", "secrets", "get", ns, "", false}, {"", "pods/exec", "create", ns, "", false}, {"", "pods", "create", ns, "", false}, {"", "pods", "delete", ns, "", false},
		{"batch", "jobs", "deletecollection", ns, "", false}, {"", "nodes", "update", "", "", false}, {"", "namespaces", "delete", "", ns, false},
		{"rbac.authorization.k8s.io", "rolebindings", "create", ns, "", false}, {"apps", "deployments", "create", ns, "", false}, {"", "pods", "list", "default", "", false}}
	for _, v := range checks {
		attributes := map[string]any{"group": v.group, "resource": v.resource, "verb": v.verb}
		if v.namespace != "" {
			attributes["namespace"] = v.namespace
		}
		if v.name != "" {
			attributes["name"] = v.name
		}
		if i := strings.IndexByte(v.resource, '/'); i >= 0 {
			attributes["resource"] = v.resource[:i]
			attributes["subresource"] = v.resource[i+1:]
		}
		var result struct {
			APIVersion string `json:"apiVersion"`
			Kind       string `json:"kind"`
			Status     struct {
				Allowed         *bool  `json:"allowed"`
				Denied          bool   `json:"denied"`
				EvaluationError string `json:"evaluationError"`
			} `json:"status"`
		}
		body := map[string]any{"apiVersion": "authorization.k8s.io/v1", "kind": "SelfSubjectAccessReview", "spec": map[string]any{"resourceAttributes": attributes}}
		if e := c.json(ctx, http.MethodPost, "/apis/authorization.k8s.io/v1/selfsubjectaccessreviews", body, &result); e != nil {
			return e
		}
		if result.APIVersion != "authorization.k8s.io/v1" || result.Kind != "SelfSubjectAccessReview" || result.Status.EvaluationError != "" || result.Status.Allowed == nil || *result.Status.Allowed != v.allowed || result.Status.Denied && v.allowed {
			return fail("PRIVATE_CLUSTER_MINIMUM_AUTHORITY_REFUSED")
		}
	}
	return nil
}
func (c *client) quota() map[string]string {
	return map[string]string{"pods": "1", "count/jobs.batch": "1", "requests.cpu": strconv.Itoa(c.policy.CPUMilli) + "m", "limits.cpu": strconv.Itoa(c.policy.CPUMilli) + "m", "requests.memory": strconv.Itoa(c.policy.MemoryMiB) + "Mi", "limits.memory": strconv.Itoa(c.policy.MemoryMiB) + "Mi"}
}
func quantity(raw string, memory bool) (int64, bool) {
	multiplier := int64(1)
	number := raw
	if memory {
		for _, u := range []struct {
			name  string
			scale int64
		}{{"Gi", 1 << 30}, {"Mi", 1 << 20}, {"Ki", 1 << 10}} {
			if strings.HasSuffix(number, u.name) {
				number = strings.TrimSuffix(number, u.name)
				multiplier = u.scale
				break
			}
		}
	} else if strings.HasSuffix(number, "m") {
		number = strings.TrimSuffix(number, "m")
	} else {
		multiplier = 1000
	}
	n, e := strconv.ParseInt(number, 10, 64)
	if e != nil || n < 0 || n > math.MaxInt64/multiplier {
		return 0, false
	}
	return n * multiplier, true
}
func (c *client) policies(ctx context.Context) (string, string, error) {
	var quota struct {
		object
		Spec struct {
			Hard          map[string]string `json:"hard"`
			Scopes        []string          `json:"scopes"`
			ScopeSelector map[string]any    `json:"scopeSelector"`
		} `json:"spec"`
		Status struct {
			Hard map[string]string `json:"hard"`
		} `json:"status"`
	}
	if e := c.get(ctx, c.namespaced("resourcequotas/")+QuotaName, &quota); e != nil {
		return "", "", e
	}
	expected := c.quota()
	if quota.APIVersion != "v1" || quota.Kind != "ResourceQuota" || !validMetadata(quota.Metadata, QuotaName, c.profile.Namespace) || len(quota.Spec.Hard) != len(expected) || len(quota.Status.Hard) != len(expected) || len(quota.Spec.Scopes) != 0 || len(quota.Spec.ScopeSelector) != 0 {
		return "", "", fail("PRIVATE_CLUSTER_QUOTA_MISMATCH")
	}
	for key, value := range expected {
		wanted, _ := quantity(value, strings.Contains(key, "memory"))
		for _, values := range []map[string]string{quota.Spec.Hard, quota.Status.Hard} {
			actual, valid := quantity(values[key], strings.Contains(key, "memory"))
			if key == "pods" || key == "count/jobs.batch" {
				actual, valid = strictCount(values[key])
				wanted = 1
			}
			if !valid || actual != wanted {
				return "", "", fail("PRIVATE_CLUSTER_QUOTA_MISMATCH")
			}
		}
	}
	var policies objectList[struct {
		object
		Spec struct {
			PodSelector map[string]any `json:"podSelector"`
			PolicyTypes []string       `json:"policyTypes"`
			Ingress     []any          `json:"ingress"`
			Egress      []any          `json:"egress"`
		} `json:"spec"`
	}]
	if e := c.get(ctx, "/apis/networking.k8s.io/v1/namespaces/"+url.PathEscape(c.profile.Namespace)+"/networkpolicies?limit="+strconv.Itoa(c.policy.MaximumObjects), &policies); e != nil {
		return "", "", e
	}
	if policies.APIVersion != "networking.k8s.io/v1" || policies.Kind != "NetworkPolicyList" || policies.Metadata.Continue != "" || len(policies.Items) != 1 {
		return "", "", fail("PRIVATE_CLUSTER_DENY_POLICY_MISMATCH")
	}
	policy := policies.Items[0]
	sort.Strings(policy.Spec.PolicyTypes)
	if policy.Kind != "" && policy.Kind != "NetworkPolicy" || policy.APIVersion != "" && policy.APIVersion != "networking.k8s.io/v1" || !validMetadata(policy.Metadata, PolicyName, c.profile.Namespace) || len(policy.Spec.PodSelector) != 0 || len(policy.Spec.Ingress) != 0 || len(policy.Spec.Egress) != 0 || strings.Join(policy.Spec.PolicyTypes, ",") != "Egress,Ingress" {
		return "", "", fail("PRIVATE_CLUSTER_DENY_POLICY_MISMATCH")
	}
	return quota.Metadata.UID, policy.Metadata.UID, nil
}
func strictCount(raw string) (int64, bool) {
	if raw != "0" && raw != "1" {
		return 0, false
	}
	n, _ := strconv.ParseInt(raw, 10, 64)
	return n, true
}
func (c *client) observe(ctx context.Context) (Observation, error) {
	if e := c.scope(ctx); e != nil {
		return Observation{}, e
	}
	ready, e := c.raw(ctx, http.MethodGet, "/readyz", nil, true)
	if e != nil {
		return Observation{}, e
	}
	if strings.TrimSpace(string(ready)) != "ok" {
		return Observation{}, fail("PRIVATE_CLUSTER_API_NOT_READY")
	}
	if e = c.access(ctx); e != nil {
		return Observation{}, e
	}
	quota, policy, e := c.policies(ctx)
	if e != nil {
		return Observation{}, e
	}
	var nodes objectList[struct {
		object
		Spec struct {
			Unschedulable bool `json:"unschedulable"`
		} `json:"spec"`
		Status struct {
			NodeInfo struct {
				OperatingSystem         string `json:"operatingSystem"`
				Architecture            string `json:"architecture"`
				ContainerRuntimeVersion string `json:"containerRuntimeVersion"`
			} `json:"nodeInfo"`
			Allocatable map[string]string `json:"allocatable"`
			Conditions  []struct {
				Type   string `json:"type"`
				Status string `json:"status"`
			} `json:"conditions"`
		} `json:"status"`
	}]
	if e = c.get(ctx, "/api/v1/nodes?limit="+strconv.Itoa(c.policy.MaximumObjects), &nodes); e != nil {
		return Observation{}, e
	}
	if nodes.APIVersion != "v1" || nodes.Kind != "NodeList" || nodes.Metadata.Continue != "" || len(nodes.Items) > c.policy.MaximumObjects {
		return Observation{}, fail("PRIVATE_CLUSTER_NODE_LIMIT_OR_SHAPE_REFUSED")
	}
	out := Observation{ServerVersion: ServerVersion, ClusterUID: c.profile.ClusterUID, NamespaceUID: c.profile.NamespaceUID, Namespace: c.profile.Namespace, QuotaUID: quota, PolicyUID: policy, ServiceAccountUID: c.profile.ControlServiceAccountUID, Nodes: []NodeCapacity{}, ObservedAt: time.Now().UTC(), Qualification: "Actual declared API/identity/policy/node observations only; no effective CNI, free capacity, GPU, training/inference or target qualification."}
	seen := map[string]bool{}
	for _, n := range nodes.Items {
		if n.Kind != "" && n.Kind != "Node" || n.APIVersion != "" && n.APIVersion != "v1" || !validMetadata(n.Metadata, n.Metadata.Name, "") || (len(n.Metadata.Name) == 0 || len(n.Metadata.Name) > 253) || seen[n.Metadata.UID] {
			return Observation{}, fail("PRIVATE_CLUSTER_NODE_RESPONSE_INVALID")
		}
		seen[n.Metadata.UID] = true
		if n.Spec.Unschedulable || n.Status.NodeInfo.Architecture != c.profile.Architecture() || n.Status.NodeInfo.OperatingSystem != "linux" || !strings.HasPrefix(n.Status.NodeInfo.ContainerRuntimeVersion, "containerd://") {
			continue
		}
		ready, count := false, 0
		for _, v := range n.Status.Conditions {
			if v.Type == "Ready" {
				ready = v.Status == "True"
				count++
			}
		}
		if count > 1 {
			return Observation{}, fail("PRIVATE_CLUSTER_NODE_RESPONSE_INVALID")
		}
		if ready && count == 1 {
			cpu, memory := n.Status.Allocatable["cpu"], n.Status.Allocatable["memory"]
			if len(cpu) < 1 || len(cpu) > 64 || len(memory) < 1 || len(memory) > 64 {
				return Observation{}, fail("PRIVATE_CLUSTER_NODE_CAPACITY_INVALID")
			}
			out.Nodes = append(out.Nodes, NodeCapacity{n.Metadata.UID, n.Metadata.Name, cpu, memory, n.Status.NodeInfo.Architecture})
		}
	}
	if len(out.Nodes) == 0 {
		return Observation{}, fail("PRIVATE_CLUSTER_READY_NODE_REQUIRED")
	}
	sort.Slice(out.Nodes, func(i, j int) bool { return out.Nodes[i].UID < out.Nodes[j].UID })
	return out, nil
}
