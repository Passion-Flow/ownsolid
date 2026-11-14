//go:build repository_integration && privatecluster_integration

package privateclusters

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	proof "github.com/Passion-Flow/ownsolid/services/control/internal/platform/privateclusterproof"
)

// apiFixture is an explicit K8s API/physical-envelope double only. Real selected
// SQL, sessions, license signatures and manufacturer image approval are required;
// no test here claims real Job execution, CNI, OCI import or provider support.
type apiFixture struct {
	mu                                                                        sync.Mutex
	p                                                                         Profile
	policy                                                                    Policy
	server                                                                    *httptest.Server
	imageConfig                                                               string
	lease, job                                                                map[string]any
	leasePosts, jobPosts, deletes                                             int
	loseJobACK, omitUnknownJob, wrongScope, wrongProof, foreign, deferDeletes bool
}

func newAPIFixture(t *testing.T, c Config, p Profile) (*apiFixture, Profile) {
	t.Helper()
	f := &apiFixture{p: p, policy: c.Policy}
	f.server = httptest.NewUnstartedServer(http.HandlerFunc(f.handle))
	key, e := rsa.GenerateKey(rand.Reader, 2048)
	if e != nil {
		t.Fatal(e)
	}
	now := time.Now()
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Explicit API-double test CA"}, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature}
	caDER, e := x509.CreateCertificate(rand.Reader, ca, ca, &key.PublicKey, key)
	if e != nil {
		t.Fatal(e)
	}
	cert := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "Explicit API-double server"}, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour), IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, KeyUsage: x509.KeyUsageDigitalSignature}
	der, e := x509.CreateCertificate(rand.Reader, cert, ca, &key.PublicKey, key)
	if e != nil {
		t.Fatal(e)
	}
	pair, e := tls.X509KeyPair(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}))
	if e != nil {
		t.Fatal(e)
	}
	f.server.TLS = &tls.Config{Certificates: []tls.Certificate{pair}, MinVersion: tls.VersionTLS12}
	f.server.StartTLS()
	t.Cleanup(f.server.Close)
	root := t.TempDir()
	p.ServerOrigin = f.server.URL
	p.TLSServerName = "127.0.0.1"
	p.AllowedCIDRs = []string{"127.0.0.0/8"}
	p.CAFile = filepath.Join(root, "ca.pem")
	p.TokenFile = filepath.Join(root, "token")
	if e = os.WriteFile(p.CAFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}), 0600); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(p.TokenFile, []byte("explicit-test-token-for-api-double"), 0600); e != nil {
		t.Fatal(e)
	}
	f.p = p
	return f, p
}
func (f *apiFixture) meta(name, uid string, namespaced bool) map[string]any {
	m := map[string]any{"name": name, "uid": uid, "resourceVersion": "1"}
	if namespaced {
		m["namespace"] = f.p.Namespace
	}
	return m
}
func (f *apiFixture) handle(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	if r.Header.Get("Authorization") != "Bearer explicit-test-token-for-api-double" {
		w.WriteHeader(401)
		return
	}
	respond := func(v any) { _ = json.NewEncoder(w).Encode(v) }
	list := func(api, kind string, items []any) {
		respond(map[string]any{"apiVersion": api, "kind": kind, "metadata": map[string]any{}, "items": items})
	}
	var body map[string]any
	if r.Method == http.MethodPost {
		if e := json.NewDecoder(r.Body).Decode(&body); e != nil {
			w.WriteHeader(400)
			return
		}
	}
	switch {
	case r.URL.Path == "/version":
		respond(map[string]string{"gitVersion": ServerVersion})
	case r.URL.Path == "/readyz":
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("ok"))
	case r.URL.Path == "/api/v1/namespaces/kube-system":
		respond(map[string]any{"apiVersion": "v1", "kind": "Namespace", "metadata": f.meta("kube-system", f.p.ClusterUID, false), "status": map[string]string{"phase": "Active"}})
	case r.URL.Path == "/api/v1/namespaces/"+f.p.Namespace:
		uid := f.p.NamespaceUID
		if f.wrongScope {
			uid = uuid.NewString()
		}
		m := f.meta(f.p.Namespace, uid, false)
		m["labels"] = map[string]string{PurposeLabel: Purpose, InstallationLabel: f.p.InstallationID, WorkspaceLabel: f.p.WorkspaceID, "pod-security.kubernetes.io/enforce": "restricted", "pod-security.kubernetes.io/enforce-version": PodSecurityVersion}
		respond(map[string]any{"apiVersion": "v1", "kind": "Namespace", "metadata": m, "status": map[string]string{"phase": "Active"}})
	case r.URL.Path == "/apis/authentication.k8s.io/v1/selfsubjectreviews":
		respond(map[string]any{"apiVersion": "authentication.k8s.io/v1", "kind": "SelfSubjectReview", "status": map[string]any{"userInfo": map[string]any{"username": "system:serviceaccount:" + f.p.Namespace + ":" + ControlServiceAccount, "uid": f.p.ControlServiceAccountUID, "groups": []string{"system:authenticated"}}}})
	case r.URL.Path == "/apis/authorization.k8s.io/v1/selfsubjectaccessreviews":
		a := body["spec"].(map[string]any)["resourceAttributes"].(map[string]any)
		resource, _ := a["resource"].(string)
		verb, _ := a["verb"].(string)
		namespace, _ := a["namespace"].(string)
		sub, _ := a["subresource"].(string)
		deny := resource == "secrets" || sub == "exec" || resource == "pods" && (verb == "create" || verb == "delete") || verb == "deletecollection" || resource == "nodes" && verb == "update" || resource == "namespaces" && verb == "delete" || resource == "rolebindings" || resource == "deployments" || namespace == "default"
		respond(map[string]any{"apiVersion": "authorization.k8s.io/v1", "kind": "SelfSubjectAccessReview", "status": map[string]any{"allowed": !deny, "denied": deny}})
	case strings.Contains(r.URL.Path, "/resourcequotas/"):
		c := &client{policy: f.policy}
		respond(map[string]any{"apiVersion": "v1", "kind": "ResourceQuota", "metadata": f.meta(QuotaName, "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", true), "spec": map[string]any{"hard": c.quota()}, "status": map[string]any{"hard": c.quota()}})
	case strings.HasSuffix(r.URL.Path, "/networkpolicies"):
		list("networking.k8s.io/v1", "NetworkPolicyList", []any{map[string]any{"metadata": f.meta(PolicyName, "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", true), "spec": map[string]any{"podSelector": map[string]any{}, "policyTypes": []string{"Ingress", "Egress"}, "ingress": []any{}, "egress": []any{}}}})
	case r.URL.Path == "/api/v1/nodes":
		list("v1", "NodeList", []any{map[string]any{"metadata": f.meta("synthetic-node", "cccccccc-cccc-4ccc-8ccc-cccccccccccc", false), "status": map[string]any{"nodeInfo": map[string]any{"operatingSystem": "linux", "architecture": f.p.Architecture(), "containerRuntimeVersion": "containerd://explicit-double"}, "allocatable": map[string]string{"cpu": "2", "memory": "1Gi"}, "conditions": []any{map[string]string{"type": "Ready", "status": "True"}}}}})
	case strings.HasSuffix(r.URL.Path, "/leases") && r.Method == http.MethodPost:
		if f.lease != nil {
			w.WriteHeader(409)
			return
		}
		f.leasePosts++
		body["metadata"].(map[string]any)["uid"] = "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
		body["metadata"].(map[string]any)["resourceVersion"] = "2"
		f.lease = body
		respond(body)
	case strings.HasSuffix(r.URL.Path, "/leases/"+LeaseName):
		if f.lease == nil {
			w.WriteHeader(404)
			return
		}
		if r.Method == http.MethodDelete {
			var d map[string]any
			_ = json.NewDecoder(r.Body).Decode(&d)
			ref := d["preconditions"].(map[string]any)
			if ref["uid"] != f.lease["metadata"].(map[string]any)["uid"] {
				w.WriteHeader(409)
				return
			}
			f.deletes++
			if f.deferDeletes {
				f.lease["metadata"].(map[string]any)["deletionTimestamp"] = time.Now().UTC().Format(time.RFC3339Nano)
			} else {
				f.lease = nil
			}
			respond(map[string]string{"kind": "Status", "status": "Success"})
			return
		}
		respond(f.lease)
	case strings.HasSuffix(r.URL.Path, "/jobs") && r.Method == http.MethodPost:
		if f.job != nil {
			w.WriteHeader(409)
			return
		}
		f.jobPosts++
		body["metadata"].(map[string]any)["uid"] = "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee"
		body["metadata"].(map[string]any)["resourceVersion"] = "3"
		if !f.omitUnknownJob {
			f.job = body
		}
		if f.loseJobACK {
			f.loseJobACK = false
			conn, _, e := w.(http.Hijacker).Hijack()
			if e == nil {
				_ = conn.Close()
			}
			return
		}
		respond(body)
	case strings.HasSuffix(r.URL.Path, "/jobs"):
		items := []any{}
		if f.job != nil {
			items = append(items, f.job)
		}
		if f.foreign {
			items = append(items, map[string]any{"metadata": f.meta("foreign", uuid.NewString(), true)})
		}
		list("batch/v1", "JobList", items)
	case strings.Contains(r.URL.Path, "/jobs/"):
		if f.job == nil {
			w.WriteHeader(404)
			return
		}
		if r.Method == http.MethodDelete {
			var d map[string]any
			_ = json.NewDecoder(r.Body).Decode(&d)
			if d["preconditions"].(map[string]any)["uid"] != f.job["metadata"].(map[string]any)["uid"] {
				w.WriteHeader(409)
				return
			}
			f.deletes++
			if f.deferDeletes {
				f.job["metadata"].(map[string]any)["deletionTimestamp"] = time.Now().UTC().Format(time.RFC3339Nano)
			} else {
				f.job = nil
			}
			respond(map[string]string{"kind": "Status", "status": "Success"})
			return
		}
		f.job["status"] = map[string]any{"succeeded": 1, "conditions": []any{map[string]string{"type": "Complete", "status": "True"}}}
		respond(f.job)
	case strings.HasSuffix(r.URL.Path, "/pods"):
		if f.job == nil {
			list("v1", "PodList", []any{})
			return
		}
		pod := f.pod()
		list("v1", "PodList", []any{pod})
	case strings.HasSuffix(r.URL.Path, "/log"):
		if f.job == nil {
			w.WriteHeader(404)
			return
		}
		ps := f.job["spec"].(map[string]any)["template"].(map[string]any)["spec"].(map[string]any)
		container := ps["containers"].([]any)[0].(map[string]any)
		args := container["args"].([]any)
		data, _ := base64.RawURLEncoding.DecodeString(args[1].(string))
		challenge, _ := proof.Decode(data)
		pod := "ffffffff-ffff-4fff-8fff-ffffffffffff"
		if f.wrongProof {
			pod = uuid.NewString()
		}
		p := proof.Proof{FormatVersion: 1, RunID: challenge.RunID, NamespaceUID: challenge.NamespaceUID, InstallationID: challenge.InstallationID, PodUID: pod, Architecture: challenge.Architecture, UID: challenge.RunAsUser, ChallengeSHA256: proof.Digest(data)}
		w.Header().Set("Content-Type", "text/plain")
		_ = json.NewEncoder(w).Encode(p)
	default:
		w.WriteHeader(404)
	}
}
func (f *apiFixture) pod() map[string]any {
	spec := normalizedJSON(f.job["spec"].(map[string]any)["template"].(map[string]any)["spec"]).(map[string]any)
	spec["nodeName"] = "synthetic-node"
	meta := f.meta("projectone-proof-pod", "ffffffff-ffff-4fff-8fff-ffffffffffff", true)
	meta["labels"] = f.job["metadata"].(map[string]any)["labels"]
	if stamp, ok := f.job["metadata"].(map[string]any)["deletionTimestamp"]; ok {
		meta["deletionTimestamp"] = stamp
	}
	meta["ownerReferences"] = []any{map[string]any{"apiVersion": "batch/v1", "kind": "Job", "name": f.job["metadata"].(map[string]any)["name"], "uid": "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee", "controller": true, "blockOwnerDeletion": true}}
	image := spec["containers"].([]any)[0].(map[string]any)["image"].(string)
	return map[string]any{"metadata": meta, "spec": spec, "status": map[string]any{"phase": "Succeeded", "containerStatuses": []any{map[string]any{"name": proof.ContainerName, "image": image, "imageID": f.imageConfig, "restartCount": 0, "state": map[string]any{"terminated": map[string]any{"exitCode": 0, "reason": "Completed", "containerID": "containerd://explicit-api-double-container"}}}}}}
}

func (f *apiFixture) counts() (int, int, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.leasePosts, f.jobPosts, f.deletes
}
func (f *apiFixture) behavior(lost, absent, foreign, wrongProof bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.loseJobACK = lost
	f.omitUnknownJob = absent
	f.foreign = foreign
	f.wrongProof = wrongProof
}

func (f *apiFixture) pendingDeletes() { f.mu.Lock(); defer f.mu.Unlock(); f.deferDeletes = true }
func (f *apiFixture) physicalAbsence(job bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if job {
		f.job = nil
	} else {
		f.lease = nil
	}
}
