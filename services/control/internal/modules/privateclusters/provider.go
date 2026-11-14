package privateclusters

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"time"

	proof "github.com/Passion-Flow/ownsolid/services/control/internal/platform/privateclusterproof"
)

// providerRun is an immutable server-created request, never an HTTP override.
type providerRun struct {
	ImageDigest  string          `json:"image_digest"`
	ConfigDigest string          `json:"config_digest"`
	ManifestSHA  string          `json:"manifest_sha256"`
	Profile      string          `json:"profile"`
	ID           string          `json:"id"`
	JobName      string          `json:"job_name"`
	Challenge    proof.Challenge `json:"challenge"`
	SpecSHA      string          `json:"spec_sha256"`
	LeaseUID     string          `json:"lease_uid"`
	JobUID       string          `json:"job_uid"`
	PodUID       string          `json:"pod_uid"`
}
type remoteRef struct {
	UID             string `json:"uid"`
	ResourceVersion string `json:"resource_version"`
}
type remoteState struct {
	Lease       remoteRef    `json:"lease"`
	Job         remoteRef    `json:"job"`
	PodUID      string       `json:"pod_uid"`
	State       string       `json:"state"`
	Proof       *proof.Proof `json:"proof,omitempty"`
	Logs        string       `json:"logs,omitempty"`
	NodeName    string       `json:"node_name,omitempty"`
	ContainerID string       `json:"container_id,omitempty"`
	ImageID     string       `json:"image_id,omitempty"`
}

func missing(e error) bool {
	var a *apiError
	return errors.As(e, &a) && a.code == "PRIVATE_CLUSTER_RESOURCE_MISSING"
}
func (c *client) labels(r providerRun) map[string]string {
	return map[string]string{RunLabel: r.ID, PurposeLabel: Purpose, InstallationLabel: c.profile.InstallationID, WorkspaceLabel: c.profile.WorkspaceID}
}
func (c *client) podSpec(r providerRun) (map[string]any, error) {
	args, e := proof.Arguments(r.Challenge)
	if e != nil {
		return nil, e
	}
	if r.ImageDigest != c.approval.Asset.ImageID || r.ConfigDigest != c.approval.Asset.ImageConfigDigest || r.ManifestSHA != c.approval.ManifestSHA256 || r.Profile != c.profile.Profile || r.JobName != JobNamePrefix+r.ID || r.Challenge.RunID != r.ID || r.Challenge.NamespaceUID != c.profile.NamespaceUID || r.Challenge.InstallationID != c.profile.InstallationID || r.Challenge.Architecture != c.profile.Architecture() || r.Challenge.RunAsUser != c.policy.RunAsUser {
		return nil, fail("PRIVATE_CLUSTER_RUN_BINDING_INVALID")
	}
	resources := map[string]string{"cpu": strconv.Itoa(c.policy.CPUMilli) + "m", "memory": strconv.Itoa(c.policy.MemoryMiB) + "Mi"}
	return map[string]any{
		"restartPolicy": "Never", "serviceAccountName": ProbeServiceAccount, "automountServiceAccountToken": false,
		"enableServiceLinks": false, "hostNetwork": false, "hostPID": false, "hostIPC": false,
		"dnsPolicy": "ClusterFirst", "schedulerName": "default-scheduler", "terminationGracePeriodSeconds": 0,
		"nodeSelector":    map[string]string{"kubernetes.io/os": "linux", "kubernetes.io/arch": c.profile.Architecture()},
		"securityContext": map[string]any{"runAsNonRoot": true, "runAsUser": c.policy.RunAsUser, "runAsGroup": c.policy.RunAsUser, "seccompProfile": map[string]string{"type": "RuntimeDefault"}},
		"containers": []any{map[string]any{"name": proof.ContainerName, "image": c.profile.ImageRepository + "@" + c.approval.Asset.ImageID, "imagePullPolicy": "Never", "command": []string{proof.Command}, "args": args,
			"env":       []any{map[string]any{"name": proof.PodUIDVariable, "valueFrom": map[string]any{"fieldRef": map[string]string{"apiVersion": "v1", "fieldPath": "metadata.uid"}}}},
			"resources": map[string]any{"requests": resources, "limits": resources}, "terminationMessagePath": "/dev/termination-log", "terminationMessagePolicy": "File",
			"securityContext": map[string]any{"allowPrivilegeEscalation": false, "privileged": false, "readOnlyRootFilesystem": true, "capabilities": map[string]any{"drop": []string{"ALL"}}}}},
	}, nil
}
func (c *client) jobBody(r providerRun) (map[string]any, error) {
	pod, e := c.podSpec(r)
	if e != nil {
		return nil, e
	}
	spec := map[string]any{"parallelism": 1, "completions": 1, "backoffLimit": 0, "activeDeadlineSeconds": c.policy.JobSeconds, "manualSelector": true, "selector": map[string]any{"matchLabels": map[string]string{RunLabel: r.ID}}, "suspend": false, "completionMode": "NonIndexed", "podReplacementPolicy": "Failed", "template": map[string]any{"metadata": map[string]any{"labels": c.labels(r)}, "spec": pod}}
	sha := fingerprint(spec)
	if r.SpecSHA != "" && r.SpecSHA != sha {
		return nil, fail("PRIVATE_CLUSTER_RUN_SPEC_CHANGED")
	}
	return map[string]any{"apiVersion": "batch/v1", "kind": "Job", "metadata": map[string]any{"name": r.JobName, "namespace": c.profile.Namespace, "labels": c.labels(r), "annotations": map[string]string{SpecAnnotation: sha}}, "spec": spec}, nil
}
func (c *client) prepared(id string, challenge proof.Challenge) (providerRun, error) {
	r := providerRun{ID: id, JobName: JobNamePrefix + id, Challenge: challenge, ImageDigest: c.approval.Asset.ImageID, ConfigDigest: c.approval.Asset.ImageConfigDigest, ManifestSHA: c.approval.ManifestSHA256, Profile: c.profile.Profile}
	b, e := c.jobBody(r)
	if e != nil {
		return providerRun{}, e
	}
	r.SpecSHA = fingerprint(b["spec"])
	return r, nil
}
func (c *client) leaseBody(r providerRun) map[string]any {
	return map[string]any{"apiVersion": "coordination.k8s.io/v1", "kind": "Lease", "metadata": map[string]any{"name": LeaseName, "namespace": c.profile.Namespace, "labels": c.labels(r), "annotations": map[string]string{SpecAnnotation: r.SpecSHA}}, "spec": map[string]any{"holderIdentity": r.ID}}
}

type leaseObject struct {
	object
	Spec struct {
		HolderIdentity       string  `json:"holderIdentity"`
		LeaseDurationSeconds *int64  `json:"leaseDurationSeconds"`
		RenewTime            *string `json:"renewTime"`
		AcquireTime          *string `json:"acquireTime"`
		LeaseTransitions     *int64  `json:"leaseTransitions"`
		Strategy             *string `json:"strategy"`
		PreferredHolder      *string `json:"preferredHolder"`
	} `json:"spec"`
}

func (c *client) checkLabels(m metadata, r providerRun) bool {
	for k, v := range c.labels(r) {
		if m.Labels[k] != v {
			return false
		}
	}
	return m.Annotations[SpecAnnotation] == r.SpecSHA && len(m.OwnerReferences) == 0
}
func (c *client) checkLease(v leaseObject, r providerRun) (remoteRef, error) {
	if v.APIVersion != "coordination.k8s.io/v1" || v.Kind != "Lease" || !validMetadata(v.Metadata, LeaseName, c.profile.Namespace) || !c.checkLabels(v.Metadata, r) || r.LeaseUID != "" && v.Metadata.UID != r.LeaseUID || v.Spec.HolderIdentity != r.ID || v.Spec.LeaseDurationSeconds != nil || v.Spec.RenewTime != nil || v.Spec.AcquireTime != nil || v.Spec.Strategy != nil || v.Spec.PreferredHolder != nil || v.Spec.LeaseTransitions != nil {
		return remoteRef{}, fail("PRIVATE_CLUSTER_LEASE_CONFLICT")
	}
	return remoteRef{v.Metadata.UID, v.Metadata.ResourceVersion}, nil
}
func (c *client) lease(ctx context.Context, r providerRun) (remoteRef, error) {
	var v leaseObject
	e := c.get(ctx, c.leases()+"/"+LeaseName, &v)
	if e != nil {
		return remoteRef{}, e
	}
	return c.checkLease(v, r)
}
func (c *client) createLease(ctx context.Context, r providerRun) (remoteRef, error) {
	if e := c.empty(ctx, r, false); e != nil {
		return remoteRef{}, e
	}
	var v leaseObject
	if e := c.json(ctx, http.MethodPost, c.leases(), c.leaseBody(r), &v); e != nil {
		return remoteRef{}, e
	}
	return c.checkLease(v, r)
}

type jobObject struct {
	object
	Spec   map[string]any `json:"spec"`
	Status struct {
		Active     int `json:"active"`
		Succeeded  int `json:"succeeded"`
		Failed     int `json:"failed"`
		Conditions []struct {
			Type   string `json:"type"`
			Status string `json:"status"`
		} `json:"conditions"`
	} `json:"status"`
}

// exactSubset rejects admission-added executable fields. Only named API defaults
// with no extra executable behavior are normalized; arbitrary rewrites fail.
func normalizedJSON(v any) any {
	b, _ := json.Marshal(v)
	var out any
	_ = json.Unmarshal(b, &out)
	return out
}
func sameSpec(actual map[string]any, expected map[string]any, pod bool) bool {
	a := normalizedJSON(actual).(map[string]any)
	e := normalizedJSON(expected).(map[string]any)
	if pod {

		for _, key := range []string{"hostNetwork", "hostPID", "hostIPC"} {
			if a[key] == false {
				delete(a, key)
			}
			if e[key] == false {
				delete(e, key)
			}
		}
		ac, aok := a["containers"].([]any)
		ec, eok := e["containers"].([]any)
		if !aok || !eok || len(ac) != 1 || len(ec) != 1 {
			return false
		}
		am, aok := ac[0].(map[string]any)
		em, eok := ec[0].(map[string]any)
		if !aok || !eok {
			return false
		}
		ar, aok := am["resources"].(map[string]any)
		er, eok := em["resources"].(map[string]any)
		if !aok || !eok || len(ar) != 2 {
			return false
		}
		for _, name := range []string{"requests", "limits"} {
			av, aok := ar[name].(map[string]any)
			ev, eok := er[name].(map[string]any)
			if !aok || !eok || len(av) != 2 {
				return false
			}
			for _, key := range []string{"cpu", "memory"} {
				as, aok := av[key].(string)
				es, eok := ev[key].(string)
				if !aok || !eok {
					return false
				}
				an, aok := quantity(as, key == "memory")
				en, eok := quantity(es, key == "memory")
				if !aok || !eok || an != en {
					return false
				}
				av[key] = es
			}
		}
		// API/kubelet placement and admission's two standard unreachable tolerations
		// do not grant workload capabilities. All other extra fields remain rejected.
		if name, ok := a["nodeName"]; ok {
			s, valid := name.(string)
			if !valid || s == "" || len(s) > 253 {
				return false
			}
			delete(a, "nodeName")
		}
		if t, ok := a["tolerations"]; ok {
			items, valid := t.([]any)
			if !valid || len(items) > 2 {
				return false
			}
			seen := map[string]bool{}
			for _, item := range items {
				m, valid := item.(map[string]any)
				if !valid || len(m) != 4 || m["operator"] != "Exists" || m["effect"] != "NoExecute" || m["tolerationSeconds"] != float64(300) {
					return false
				}
				k, _ := m["key"].(string)
				if k != "node.kubernetes.io/not-ready" && k != "node.kubernetes.io/unreachable" || seen[k] {
					return false
				}
				seen[k] = true
			}
			delete(a, "tolerations")
		}
		if v, ok := a["serviceAccount"]; ok {
			if v != ProbeServiceAccount {
				return false
			}
			delete(a, "serviceAccount")
		}
		if v, ok := a["preemptionPolicy"]; ok {
			if v != "PreemptLowerPriority" {
				return false
			}
			delete(a, "preemptionPolicy")
		}
		if v, ok := a["priority"]; ok {
			if v != float64(0) {
				return false
			}
			delete(a, "priority")
		}
		if sc, ok := a["securityContext"].(map[string]any); ok {
			if v, exists := sc["fsGroupChangePolicy"]; exists {
				if v != "Always" {
					return false
				}
				delete(sc, "fsGroupChangePolicy")
			}
		}
	} else {
		at, ok := a["template"].(map[string]any)
		et, _ := e["template"].(map[string]any)
		if !ok || len(at) != 2 {
			return false
		}
		am, ok := at["metadata"].(map[string]any)
		em, _ := et["metadata"].(map[string]any)
		if !ok {
			return false
		}
		for _, key := range []string{"creationTimestamp", "name", "namespace"} {
			if v, ok := am[key]; ok {
				if v != nil && v != "" {
					return false
				}
				delete(am, key)
			}
		}
		if !reflect.DeepEqual(am, em) {
			return false
		}
		aps, ok := at["spec"].(map[string]any)
		eps, _ := et["spec"].(map[string]any)
		if !ok || !sameSpec(aps, eps, true) {
			return false
		}
		delete(a, "template")
		delete(e, "template")
	}
	return reflect.DeepEqual(a, e)
}
func (c *client) checkJob(v jobObject, r providerRun) (remoteRef, error) {
	b, e := c.jobBody(r)
	if e != nil {
		return remoteRef{}, e
	}
	if v.APIVersion != "batch/v1" || v.Kind != "Job" || !validMetadata(v.Metadata, r.JobName, c.profile.Namespace) || !c.checkLabels(v.Metadata, r) || r.JobUID != "" && v.Metadata.UID != r.JobUID || !sameSpec(v.Spec, b["spec"].(map[string]any), false) {
		return remoteRef{}, fail("PRIVATE_CLUSTER_JOB_CHANGED")
	}
	return remoteRef{v.Metadata.UID, v.Metadata.ResourceVersion}, nil
}
func (c *client) job(ctx context.Context, r providerRun) (jobObject, error) {
	var v jobObject
	if e := c.get(ctx, c.jobs()+"/"+url.PathEscape(r.JobName), &v); e != nil {
		return v, e
	}
	_, e := c.checkJob(v, r)
	return v, e
}
func (c *client) createJob(ctx context.Context, r providerRun) (remoteRef, error) {
	if _, e := c.lease(ctx, r); e != nil {
		return remoteRef{}, e
	}
	if e := c.empty(ctx, r, true); e != nil {
		return remoteRef{}, e
	}
	b, e := c.jobBody(r)
	if e != nil {
		return remoteRef{}, e
	}
	var v jobObject
	if e = c.json(ctx, http.MethodPost, c.jobs(), b, &v); e != nil {
		return remoteRef{}, e
	}
	return c.checkJob(v, r)
}

type podObject struct {
	object
	Spec   map[string]any `json:"spec"`
	Status struct {
		Phase             string `json:"phase"`
		ContainerStatuses []struct {
			Name         string `json:"name"`
			Image        string `json:"image"`
			ImageID      string `json:"imageID"`
			RestartCount int    `json:"restartCount"`
			State        struct {
				Terminated *struct {
					ExitCode    int    `json:"exitCode"`
					Reason      string `json:"reason"`
					ContainerID string `json:"containerID"`
				} `json:"terminated"`
			} `json:"state"`
		} `json:"containerStatuses"`
		InitContainerStatuses      []any `json:"initContainerStatuses"`
		EphemeralContainerStatuses []any `json:"ephemeralContainerStatuses"`
	} `json:"status"`
}

func (c *client) inventory(ctx context.Context) ([]jobObject, []podObject, error) {
	var jobs objectList[jobObject]
	var pods objectList[podObject]
	suffix := "?limit=" + strconv.Itoa(c.policy.MaximumObjects)
	if e := c.get(ctx, c.jobs()+suffix, &jobs); e != nil {
		return nil, nil, e
	}
	if e := c.get(ctx, c.namespaced("pods")+suffix, &pods); e != nil {
		return nil, nil, e
	}
	if jobs.APIVersion != "batch/v1" || jobs.Kind != "JobList" || pods.APIVersion != "v1" || pods.Kind != "PodList" || jobs.Metadata.Continue != "" || pods.Metadata.Continue != "" || len(jobs.Items) > c.policy.MaximumObjects || len(pods.Items) > c.policy.MaximumObjects {
		return nil, nil, fail("PRIVATE_CLUSTER_INVENTORY_LIMIT_OR_SHAPE")
	}
	return jobs.Items, pods.Items, nil
}
func (c *client) empty(ctx context.Context, r providerRun, leaseRequired bool) error {
	jobs, pods, e := c.inventory(ctx)
	if e != nil {
		return e
	}
	if len(jobs) != 0 || len(pods) != 0 {
		return fail("PRIVATE_CLUSTER_NAMESPACE_OCCUPIED")
	}
	_, e = c.lease(ctx, r)
	if leaseRequired {
		return e
	}
	if !missing(e) {
		if e == nil {
			return fail("PRIVATE_CLUSTER_LEASE_CONFLICT")
		}
		return e
	}
	return nil
}
func (c *client) checkPod(p podObject, r providerRun) error {
	expected, e := c.podSpec(r)
	if e != nil {
		return e
	}
	if p.Kind != "" && p.Kind != "Pod" || p.APIVersion != "" && p.APIVersion != "v1" || !validMetadata(p.Metadata, p.Metadata.Name, c.profile.Namespace) || !namespaceName.MatchString(p.Metadata.Name) || r.PodUID != "" && r.PodUID != p.Metadata.UID || len(p.Metadata.OwnerReferences) != 1 || p.Metadata.Labels[RunLabel] != r.ID || !sameSpec(p.Spec, expected, true) {
		return fail("PRIVATE_CLUSTER_POD_CHANGED")
	}
	owner := p.Metadata.OwnerReferences[0]
	if owner.APIVersion != "batch/v1" || owner.Kind != "Job" || owner.UID != r.JobUID || owner.Name != r.JobName || !owner.Controller || !owner.BlockOwnerDeletion {
		return fail("PRIVATE_CLUSTER_POD_OWNER_CHANGED")
	}
	return nil
}
func (c *client) state(ctx context.Context, r providerRun) (remoteState, error) {
	lease, e := c.lease(ctx, r)
	if e != nil {
		return remoteState{}, e
	}
	job, e := c.job(ctx, r)
	if e != nil {
		return remoteState{}, e
	}
	r.JobUID = job.Metadata.UID
	jobs, pods, e := c.inventory(ctx)
	if e != nil {
		return remoteState{}, e
	}
	if len(jobs) != 1 || len(pods) > 1 {
		return remoteState{}, fail("PRIVATE_CLUSTER_EXECUTION_AMBIGUOUS")
	}
	// List entries may omit type metadata; the GET is the authoritative Job shape.
	if jobs[0].Metadata.UID != r.JobUID {
		return remoteState{}, fail("PRIVATE_CLUSTER_FOREIGN_RESOURCE")
	}
	out := remoteState{Lease: lease, Job: remoteRef{job.Metadata.UID, job.Metadata.ResourceVersion}, State: "running"}
	if len(pods) == 0 {
		if job.Status.Succeeded != 0 || job.Status.Failed != 0 {
			return remoteState{}, fail("PRIVATE_CLUSTER_TERMINAL_PROVENANCE_MISSING")
		}
		return out, nil
	}
	pod := pods[0]
	if e = c.checkPod(pod, r); e != nil {
		return remoteState{}, e
	}
	out.PodUID = pod.Metadata.UID
	complete, failed := false, false
	for _, v := range job.Status.Conditions {
		if v.Status == "True" && v.Type == "Complete" {
			complete = true
		}
		if v.Status == "True" && v.Type == "Failed" {
			failed = true
		}
	}
	if complete && failed || job.Status.Active > 1 || job.Status.Succeeded > 1 || job.Status.Failed > 1 {
		return remoteState{}, fail("PRIVATE_CLUSTER_EXECUTION_AMBIGUOUS")
	}
	if failed {
		out.State = "failed"
		return out, nil
	}
	if !complete {
		return out, nil
	}
	if job.Status.Active != 0 || job.Status.Succeeded != 1 || job.Status.Failed != 0 || pod.Status.Phase != "Succeeded" || len(pod.Status.ContainerStatuses) != 1 || len(pod.Status.InitContainerStatuses) != 0 || len(pod.Status.EphemeralContainerStatuses) != 0 {
		return remoteState{}, fail("PRIVATE_CLUSTER_TERMINAL_PROVENANCE_MISSING")
	}
	status := pod.Status.ContainerStatuses[0]
	if status.Name != proof.ContainerName || status.Image != c.profile.ImageRepository+"@"+c.approval.Asset.ImageID || status.RestartCount != 0 || status.State.Terminated == nil || status.State.Terminated.ExitCode != 0 || status.State.Terminated.Reason != "Completed" || !strings.HasPrefix(status.State.Terminated.ContainerID, "containerd://") {
		return remoteState{}, fail("PRIVATE_CLUSTER_CONTAINER_PROVENANCE_INVALID")
	}
	// containerd CRI reports the content-addressed image config. A manifest-only or
	// unknown imageID is not substituted for that observed runtime identity.
	imageID := strings.TrimPrefix(status.ImageID, "containerd://")
	if imageID != c.approval.Asset.ImageConfigDigest {
		return remoteState{}, fail("PRIVATE_CLUSTER_RUNTIME_IMAGE_MISMATCH")
	}
	logs, e := c.raw(ctx, http.MethodGet, c.namespaced("pods/")+url.PathEscape(pod.Metadata.Name)+"/log?container="+proof.ContainerName+"&timestamps=false&follow=false&tailLines=2&limitBytes="+strconv.FormatInt(c.policy.MaximumLogBytes, 10), nil, true)
	if e != nil {
		return remoteState{}, e
	}
	verified, e := proof.Verify(logs, r.Challenge, pod.Metadata.UID)
	if e != nil {
		return remoteState{}, fail("PRIVATE_CLUSTER_EXECUTION_PROOF_INVALID")
	}
	out.NodeName, _ = pod.Spec["nodeName"].(string)
	if out.NodeName == "" {
		return remoteState{}, fail("PRIVATE_CLUSTER_RUNTIME_NODE_MISSING")
	}
	out.ContainerID = status.State.Terminated.ContainerID
	out.ImageID = imageID
	out.State = "succeeded"
	out.Proof = &verified
	out.Logs = string(logs)
	return out, nil
}
func (c *client) deleteExact(ctx context.Context, path string, ref remoteRef) error {
	if !validID(ref.UID) || ref.ResourceVersion == "" {
		return fail("PRIVATE_CLUSTER_DELETE_PRECONDITION_REQUIRED")
	}
	var response map[string]any
	return c.json(ctx, http.MethodDelete, path, map[string]any{"apiVersion": "v1", "kind": "DeleteOptions", "propagationPolicy": "Foreground", "preconditions": map[string]string{"uid": ref.UID, "resourceVersion": ref.ResourceVersion}}, &response)
}
func (c *client) deleteJob(ctx context.Context, r providerRun) error {
	jobs, pods, e := c.inventory(ctx)
	if e != nil {
		return e
	}
	if len(jobs) > 1 || len(pods) > 1 {
		return fail("PRIVATE_CLUSTER_FOREIGN_RESOURCE")
	}
	for _, p := range pods {
		if e = c.checkPod(p, r); e != nil {
			return e
		}
	}
	if len(jobs) == 0 {
		if len(pods) != 0 {
			return fail("PRIVATE_CLUSTER_PODS_STILL_PRESENT")
		}
		return nil
	}
	j, e := c.job(ctx, r)
	if e != nil {
		return e
	}
	if jobs[0].Metadata.UID != j.Metadata.UID {
		return fail("PRIVATE_CLUSTER_FOREIGN_RESOURCE")
	}
	return c.deleteExact(ctx, c.jobs()+"/"+url.PathEscape(r.JobName), remoteRef{j.Metadata.UID, j.Metadata.ResourceVersion})
}
func (c *client) deleteLease(ctx context.Context, r providerRun) error {
	if e := c.empty(ctx, r, true); e != nil {
		return e
	}
	ref, e := c.lease(ctx, r)
	if e != nil {
		return e
	}
	return c.deleteExact(ctx, c.leases()+"/"+LeaseName, ref)
}
func (c *client) absent(ctx context.Context, r providerRun) (bool, error) {
	jobs, pods, e := c.inventory(ctx)
	if e != nil {
		return false, e
	}
	if len(jobs) != 0 || len(pods) != 0 {
		return false, fail("PRIVATE_CLUSTER_NAMESPACE_OCCUPIED")
	}
	var lease leaseObject
	e = c.get(ctx, c.leases()+"/"+LeaseName, &lease)
	if missing(e) {
		return true, nil
	}
	if e != nil {
		return false, e
	}
	deleting, e := deletionMarker(&lease.Metadata)
	if e != nil {
		return false, e
	}
	if _, e = c.checkLease(lease, r); e != nil {
		return false, e
	}
	if deleting {
		return false, fail("PRIVATE_CLUSTER_DELETION_IN_PROGRESS")
	}
	return false, nil
}

// recoveryInventory checks only recorded facts; absence does not settle an
// unknown create or release its durable capacity fence.
func (c *client) recoveryInventory(ctx context.Context, r providerRun) error {
	_, e := c.deletionInventory(ctx, r)
	return e
}
func deletionMarker(m *metadata) (bool, error) {
	if m.DeletionTimestamp == nil {
		return false, nil
	}
	if _, e := time.Parse(time.RFC3339Nano, *m.DeletionTimestamp); e != nil {
		return false, fail("PRIVATE_CLUSTER_RESPONSE_INVALID")
	}
	m.DeletionTimestamp = nil
	return true, nil
}
func (c *client) deletionInventory(ctx context.Context, r providerRun) (bool, error) {
	jobs, pods, e := c.inventory(ctx)
	if e != nil {
		return false, e
	}
	if len(jobs) > 1 || len(pods) > 1 {
		return false, fail("PRIVATE_CLUSTER_FOREIGN_RESOURCE")
	}
	deleting := false
	if len(jobs) == 1 {
		var j jobObject
		if e = c.get(ctx, c.jobs()+"/"+url.PathEscape(r.JobName), &j); e != nil {
			return false, e
		}
		gone, e := deletionMarker(&j.Metadata)
		if e != nil {
			return false, e
		}
		deleting = gone
		if _, e = c.checkJob(j, r); e != nil {
			return false, e
		}
		if jobs[0].Metadata.UID != j.Metadata.UID {
			return false, fail("PRIVATE_CLUSTER_FOREIGN_RESOURCE")
		}
		if r.JobUID == "" {
			r.JobUID = j.Metadata.UID
		}
	}
	for _, p := range pods {
		gone, e := deletionMarker(&p.Metadata)
		if e != nil {
			return false, e
		}
		deleting = deleting || gone
		if r.JobUID == "" {
			return false, fail("PRIVATE_CLUSTER_POD_OWNER_CHANGED")
		}
		if e = c.checkPod(p, r); e != nil {
			return false, e
		}
	}
	_, e = c.lease(ctx, r)
	if missing(e) {
		return deleting, nil
	}
	return deleting, e
}
