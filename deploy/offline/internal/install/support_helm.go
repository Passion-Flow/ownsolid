package install

import "context"

type deploymentSupportState struct {
	State             string `json:"state"`
	ObservedCurrent   bool   `json:"observed_current_generation"`
	DesiredReplicas   int32  `json:"desired_replicas"`
	UpdatedReplicas   int32  `json:"updated_replicas"`
	ReadyReplicas     int32  `json:"ready_replicas"`
	AvailableReplicas int32  `json:"available_replicas"`
}

type helmServiceSupportState struct {
	State string `json:"state"`
}

type helmResourceIdentity struct {
	Version string `json:"apiVersion"`
	Kind    string `json:"kind"`
	Meta    struct {
		Name       string            `json:"name"`
		Namespace  string            `json:"namespace"`
		Generation int64             `json:"generation"`
		Labels     map[string]string `json:"labels"`
	} `json:"metadata"`
}

func (h *helmSupportInput) validResource(r helmResourceIdentity, version, kind string) bool {
	return r.Version == version && r.Kind == kind && r.Meta.Name == h.Release+"-control" &&
		r.Meta.Namespace == h.Namespace && h.controlLabels(r.Meta.Labels)
}

func (h *helmSupportInput) controlLabels(labels map[string]string) bool {
	return labels["app.kubernetes.io/name"] == "ownsolid-control" &&
		labels["app.kubernetes.io/instance"] == h.Release
}

func (h *helmSupportInput) deployment(ctx context.Context, client *clusterSupportClient) (*deploymentSupportState, string) {
	var obj struct {
		helmResourceIdentity
		Spec struct {
			Replicas *int32 `json:"replicas"`
		} `json:"spec"`
		Status struct {
			ObservedGeneration int64 `json:"observedGeneration"`
			Replicas           int32 `json:"replicas"`
			Updated            int32 `json:"updatedReplicas"`
			Ready              int32 `json:"readyReplicas"`
			Available          int32 `json:"availableReplicas"`
			Conditions         []struct {
				Type   string `json:"type"`
				Status string `json:"status"`
				Reason string `json:"reason"`
			} `json:"conditions"`
		} `json:"status"`
	}
	path := "/apis/apps/v1/namespaces/" + h.Namespace + "/deployments/" + h.Release + "-control"
	if issue := client.get(ctx, path, &obj); issue != "" {
		return nil, issue
	}
	s := obj.Status
	if !h.validResource(obj.helmResourceIdentity, "apps/v1", "Deployment") || obj.Spec.Replicas == nil ||
		*obj.Spec.Replicas < 0 || obj.Meta.Generation < 1 || s.ObservedGeneration < 0 ||
		s.ObservedGeneration > obj.Meta.Generation || s.Available < 0 || s.Ready < s.Available || s.Replicas < s.Ready ||
		s.Updated < 0 || s.Updated > s.Replicas {
		return nil, "INVALID_RESPONSE"
	}
	report := &deploymentSupportState{
		State: "progressing", ObservedCurrent: s.ObservedGeneration == obj.Meta.Generation,
		DesiredReplicas: *obj.Spec.Replicas, UpdatedReplicas: s.Updated, ReadyReplicas: s.Ready, AvailableReplicas: s.Available,
	}
	if !report.ObservedCurrent {
		return report, ""
	}
	for _, c := range s.Conditions {
		if (c.Type == "ReplicaFailure" && c.Status == "True") ||
			(c.Type == "Progressing" && c.Status == "False" && c.Reason == "ProgressDeadlineExceeded") {
			report.State = "degraded"
			return report, ""
		}
	}
	switch {
	case report.DesiredReplicas == 0 && s.Replicas == 0:
		report.State = "scaled_down"
	case report.DesiredReplicas > 0 && s.Updated == report.DesiredReplicas && s.Replicas == s.Updated && s.Available == s.Updated:
		report.State = "ready"
	}
	return report, ""
}

func (h *helmSupportInput) service(ctx context.Context, client *clusterSupportClient) (*helmServiceSupportState, string) {
	var obj struct {
		helmResourceIdentity
		Spec struct {
			Type      string            `json:"type"`
			ClusterIP string            `json:"clusterIP"`
			Selector  map[string]string `json:"selector"`
			Ports     []struct {
				Name       string `json:"name"`
				Port       int32  `json:"port"`
				TargetPort any    `json:"targetPort"`
				Protocol   string `json:"protocol"`
			} `json:"ports"`
		} `json:"spec"`
	}
	path := "/api/v1/namespaces/" + h.Namespace + "/services/" + h.Release + "-control"
	if issue := client.get(ctx, path, &obj); issue != "" {
		return nil, issue
	}
	if !h.validResource(obj.helmResourceIdentity, "v1", "Service") {
		return nil, "INVALID_RESPONSE"
	}
	s := obj.Spec
	if s.Type == "ClusterIP" && s.ClusterIP != "" && s.ClusterIP != "None" &&
		len(s.Selector) == 2 && h.controlLabels(s.Selector) {
		for _, p := range s.Ports {
			if p.Name == "https" && p.Port == 443 && p.TargetPort == "https" && p.Protocol == "TCP" {
				return &helmServiceSupportState{State: "configured"}, ""
			}
		}
	}
	return &helmServiceSupportState{State: "unexpected"}, "CHART_MISMATCH"
}
