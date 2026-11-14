package privateclusters

// ScopeObjects renders operator-reviewed preprovisioning JSON only. The runtime
// never applies this list. Actual namespace/SA UIDs and protected token files
// must be measured/provisioned by the operator before binding the profile.
func ScopeObjects(c Config, pid string) (map[string]any, error) {
	if e := c.Validate(); e != nil {
		return nil, e
	}
	p, e := c.profile(pid)
	if e != nil {
		return nil, e
	}
	remote := &client{profile: p, policy: c.Policy}
	ns := p.Namespace
	metadata := func(name string, namespaced bool) map[string]any {
		m := map[string]any{"name": name}
		if namespaced {
			m["namespace"] = ns
		}
		return m
	}
	labels := map[string]string{PurposeLabel: Purpose, InstallationLabel: p.InstallationID, WorkspaceLabel: p.WorkspaceID, "pod-security.kubernetes.io/enforce": "restricted", "pod-security.kubernetes.io/enforce-version": PodSecurityVersion, "pod-security.kubernetes.io/audit": "restricted", "pod-security.kubernetes.io/audit-version": PodSecurityVersion, "pod-security.kubernetes.io/warn": "restricted", "pod-security.kubernetes.io/warn-version": PodSecurityVersion}
	namespace := map[string]any{"apiVersion": "v1", "kind": "Namespace", "metadata": map[string]any{"name": ns, "labels": labels}}
	object := func(api, kind, name string, spec map[string]any) map[string]any {
		o := map[string]any{"apiVersion": api, "kind": kind, "metadata": metadata(name, true)}
		for k, v := range spec {
			o[k] = v
		}
		return o
	}
	rule := func(group string, resources, verbs []string) map[string]any {
		return map[string]any{"apiGroups": []string{group}, "resources": resources, "verbs": verbs}
	}
	rules := []any{rule("batch", []string{"jobs"}, []string{"create", "get", "list", "delete"}), rule("", []string{"pods"}, []string{"list"}), rule("", []string{"pods/log"}, []string{"get"}), rule("networking.k8s.io", []string{"networkpolicies"}, []string{"list"})}
	for _, v := range []struct {
		group, resource, name string
		verbs                 []string
	}{{"", "resourcequotas", QuotaName, []string{"get"}}, {"coordination.k8s.io", "leases", LeaseName, []string{"get", "delete"}}} {
		r := rule(v.group, []string{v.resource}, v.verbs)
		r["resourceNames"] = []string{v.name}
		rules = append(rules, r)
	}
	rules = append(rules, rule("coordination.k8s.io", []string{"leases"}, []string{"create"}))
	clusterName := ns + "-read"
	clusterRules := []any{map[string]any{"nonResourceURLs": []string{"/version", "/readyz"}, "verbs": []string{"get"}}, rule("", []string{"nodes"}, []string{"list"})}
	namespaceRead := rule("", []string{"namespaces"}, []string{"get"})
	namespaceRead["resourceNames"] = []string{ns, "kube-system"}
	clusterRules = append(clusterRules, namespaceRead)
	// Standard authenticated review permissions are explicitly requested here;
	// no secret, namespace mutation, exec, pod creation or wildcard grant exists.
	clusterRules = append(clusterRules, rule("authentication.k8s.io", []string{"selfsubjectreviews"}, []string{"create"}), rule("authorization.k8s.io", []string{"selfsubjectaccessreviews"}, []string{"create"}))
	subject := []any{map[string]any{"kind": "ServiceAccount", "name": ControlServiceAccount, "namespace": ns}}
	list := []any{namespace,
		object("v1", "ServiceAccount", ControlServiceAccount, map[string]any{"automountServiceAccountToken": false}), object("v1", "ServiceAccount", ProbeServiceAccount, map[string]any{"automountServiceAccountToken": false}),
		object("v1", "ResourceQuota", QuotaName, map[string]any{"spec": map[string]any{"hard": remote.quota()}}),
		object("networking.k8s.io/v1", "NetworkPolicy", PolicyName, map[string]any{"spec": map[string]any{"podSelector": map[string]any{}, "policyTypes": []string{"Ingress", "Egress"}, "ingress": []any{}, "egress": []any{}}}),
		object("rbac.authorization.k8s.io/v1", "Role", ControlServiceAccount, map[string]any{"rules": rules}),
		object("rbac.authorization.k8s.io/v1", "RoleBinding", ControlServiceAccount, map[string]any{"roleRef": map[string]string{"apiGroup": "rbac.authorization.k8s.io", "kind": "Role", "name": ControlServiceAccount}, "subjects": subject}),
		map[string]any{"apiVersion": "rbac.authorization.k8s.io/v1", "kind": "ClusterRole", "metadata": metadata(clusterName, false), "rules": clusterRules},
		map[string]any{"apiVersion": "rbac.authorization.k8s.io/v1", "kind": "ClusterRoleBinding", "metadata": metadata(clusterName, false), "roleRef": map[string]string{"apiGroup": "rbac.authorization.k8s.io", "kind": "ClusterRole", "name": clusterName}, "subjects": subject},
	}
	return map[string]any{"apiVersion": "v1", "kind": "List", "items": list}, nil
}
