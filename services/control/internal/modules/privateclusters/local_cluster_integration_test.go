//go:build repository_integration && privatecluster_integration && privatecluster_local

package privateclusters

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/tools/release/offline/pkg/bundle"
	release "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// This separate actual gate requires a pre-created, exclusively owned local kind
// cluster and actual preloaded signed probe image. It never builds/downloads an
// image, starts Docker/kind, chooses a production context or qualifies CNI.
type localCreated struct {
	Path            string `json:"path"`
	Name            string `json:"name"`
	UID             string `json:"uid"`
	Namespace       string `json:"namespace"`
	AbsenceObserved bool   `json:"absence_observed"`
}
type localOwner struct {
	client   *client
	created  []localCreated
	t        *testing.T
	evidence string
}

func localOperator(t *testing.T, c Config, p Profile) *localOwner {
	t.Helper()
	binary := os.Getenv("PROJECT_ONE_PRIVATE_CLUSTER_KUBECTL")
	expected := os.Getenv("PROJECT_ONE_PRIVATE_CLUSTER_KUBECTL_SHA256")
	config := os.Getenv("PROJECT_ONE_PRIVATE_CLUSTER_KUBECONFIG")
	contextName := os.Getenv("PROJECT_ONE_PRIVATE_CLUSTER_CONTEXT")
	evidence := os.Getenv("PROJECT_ONE_PRIVATE_CLUSTER_EVIDENCE_DIRECTORY")
	if !filepath.IsAbs(binary) || !filepath.IsAbs(config) || !filepath.IsAbs(evidence) || len(contextName) != len("kind-projectone-m28-")+12 || !strings.HasPrefix(contextName, "kind-projectone-m28-") || strings.TrimPrefix(contextName, "kind-projectone-m28-") == "" {
		t.Fatal("explicit owned local kind operator inputs required")
	}
	if suffix := strings.TrimPrefix(contextName, "kind-projectone-m28-"); strings.Trim(suffix, "0123456789abcdef") != "" {
		t.Fatal("exact owned local context required")
	}
	if _, e := bundle.ReadProtectedConfiguration(config); e != nil {
		t.Fatal("protected local operator kubeconfig required")
	}
	file, e := os.Open(binary)
	if e != nil {
		t.Fatal(e)
	}
	h := sha256.New()
	_, e = io.Copy(h, file)
	_ = file.Close()
	if e != nil || expected != hex.EncodeToString(h.Sum(nil)) {
		t.Fatal("actual pinned existing kubectl required")
	}
	// config view reads configuration but never executes an authentication plugin.
	// Reject all exec/auth-provider/impersonation/remote references before any API.
	viewContext, viewCancel := context.WithTimeout(t.Context(), c.timeout())
	defer viewCancel()
	command := exec.CommandContext(viewContext, binary, "--kubeconfig", config, "--context", contextName, "config", "view", "--raw", "--minify", "-o", "json")
	var output bytes.Buffer
	command.Stdout = &output
	command.Stderr = io.Discard
	if e = command.Run(); e != nil || output.Len() > 1<<20 {
		t.Fatal("closed local operator configuration view required")
	}
	var raw struct {
		APIVersion string `json:"apiVersion"`
		Kind       string `json:"kind"`
		Clusters   []struct {
			Name    string                     `json:"name"`
			Cluster map[string]json.RawMessage `json:"cluster"`
		} `json:"clusters"`
		Users []struct {
			Name string                     `json:"name"`
			User map[string]json.RawMessage `json:"user"`
		} `json:"users"`
		Contexts []struct {
			Name    string            `json:"name"`
			Context map[string]string `json:"context"`
		} `json:"contexts"`
		CurrentContext string `json:"current-context"`
	}
	if release.UniqueJSON(output.Bytes(), &raw) != nil || len(raw.Clusters) != 1 || len(raw.Users) != 1 || len(raw.Contexts) != 1 || raw.Contexts[0].Name != contextName || raw.CurrentContext != contextName || raw.Contexts[0].Context["cluster"] != raw.Clusters[0].Name || raw.Contexts[0].Context["user"] != raw.Users[0].Name {
		t.Fatal("exact local operator identity required")
	}
	cluster, user := raw.Clusters[0].Cluster, raw.Users[0].User
	for k := range cluster {
		if k != "server" && k != "certificate-authority-data" {
			t.Fatal("external/unsafe cluster configuration refused")
		}
	}
	for k := range user {
		if k != "client-certificate-data" && k != "client-key-data" {
			t.Fatal("exec/token/ambient operator authentication refused")
		}
	}
	decode := func(m map[string]json.RawMessage, k string) []byte {
		var s string
		if json.Unmarshal(m[k], &s) != nil {
			t.Fatal("local operator field required")
		}
		b, e := base64.StdEncoding.Strict().DecodeString(s)
		if e != nil {
			t.Fatal("closed embedded local operator identity required")
		}
		return b
	}
	var origin string
	if json.Unmarshal(cluster["server"], &origin) != nil || origin != p.ServerOrigin {
		t.Fatal("local configured origin differs from explicit kind context")
	}
	u, e := url.Parse(origin)
	if e != nil {
		t.Fatal(e)
	}
	ip := net.ParseIP(u.Hostname())
	if ip == nil || !ip.IsLoopback() {
		t.Fatal("local gate cannot access a production/routable cluster")
	}
	ca := decode(cluster, "certificate-authority-data")
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca) {
		t.Fatal("actual local CA required")
	}
	pair, e := tls.X509KeyPair(decode(user, "client-certificate-data"), decode(user, "client-key-data"))
	if e != nil {
		t.Fatal("actual local mTLS identity required")
	}
	transport := &http.Transport{Proxy: nil, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots, Certificates: []tls.Certificate{pair}, ServerName: p.TLSServerName}, MaxConnsPerHost: 1}
	owner := &localOwner{client: &client{profile: p, policy: c.Policy, transport: transport, http: &http.Client{Transport: transport, Timeout: time.Duration(c.Policy.RequestSeconds) * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, t: t, evidence: evidence}
	t.Cleanup(owner.client.close)
	var system object
	if e = owner.client.get(t.Context(), "/api/v1/namespaces/kube-system", &system); e != nil || system.Metadata.UID != p.ClusterUID {
		t.Fatal("actual pinned local kind cluster UID required", e)
	}
	return owner
}
func (o *localOwner) create(ctx context.Context, body map[string]any) object {
	o.t.Helper()
	kind, _ := body["kind"].(string)
	meta := body["metadata"].(map[string]any)
	name := meta["name"].(string)
	namespace, _ := meta["namespace"].(string)
	base := ""
	switch kind {
	case "Namespace":
		base = "/api/v1/namespaces"
	case "ServiceAccount":
		base = "/api/v1/namespaces/" + namespace + "/serviceaccounts"
	case "ResourceQuota":
		base = "/api/v1/namespaces/" + namespace + "/resourcequotas"
	case "NetworkPolicy":
		base = "/apis/networking.k8s.io/v1/namespaces/" + namespace + "/networkpolicies"
	case "Role":
		base = "/apis/rbac.authorization.k8s.io/v1/namespaces/" + namespace + "/roles"
	case "RoleBinding":
		base = "/apis/rbac.authorization.k8s.io/v1/namespaces/" + namespace + "/rolebindings"
	case "ClusterRole":
		base = "/apis/rbac.authorization.k8s.io/v1/clusterroles"
	case "ClusterRoleBinding":
		base = "/apis/rbac.authorization.k8s.io/v1/clusterrolebindings"
	default:
		o.t.Fatal("closed local scope object required")
	}
	var current object
	if e := o.client.get(ctx, base+"/"+name, &current); !missing(e) {
		o.t.Fatal("local preprovisioning must not adopt/modify an existing object")
	}
	o.created = append(o.created, localCreated{Path: base + "/" + name, Name: name, Namespace: namespace})
	var created object
	if e := o.client.json(ctx, http.MethodPost, base, body, &created); e != nil {
		o.t.Fatal("actual local scope create; retain uncertain resource for exact operator recovery", e)
	}
	if !validMetadata(created.Metadata, name, namespace) {
		o.t.Fatal("actual owned scope UID required")
	}
	o.created[len(o.created)-1].UID = created.Metadata.UID
	return created
}
func (o *localOwner) cleanup() {
	ctx, cancel := context.WithTimeout(context.Background(), o.client.policyTimeout())
	defer cancel()
	remaining := []localCreated{}
	for n := len(o.created) - 1; n >= 0; n-- {
		v := o.created[n]
		var current object
		e := o.client.get(ctx, v.Path, &current)
		if missing(e) {
			o.created[n].AbsenceObserved = true
			continue
		}
		if e != nil || current.Metadata.UID != v.UID {
			remaining = append(remaining, v)
			continue
		}
		if e = o.client.deleteExact(ctx, v.Path, remoteRef{v.UID, current.Metadata.ResourceVersion}); e != nil {
			remaining = append(remaining, v)
			continue
		}
		for {
			var observed object
			e = o.client.get(ctx, v.Path, &observed)
			if missing(e) {
				o.created[n].AbsenceObserved = true
				break
			}
			if e != nil || observed.Metadata.UID != v.UID || ctx.Err() != nil {
				remaining = append(remaining, v)
				break
			}
			select {
			case <-ctx.Done():
			case <-time.After(250 * time.Millisecond):
			}
		}
	}
	receipt := struct {
		Format        string         `json:"format"`
		Created       []localCreated `json:"created"`
		Remaining     []localCreated `json:"remaining"`
		Qualification string         `json:"qualification"`
	}{"projectone.private-cluster.local-scope.v1", o.created, remaining, "Actual isolated local scope cleanup only; does not qualify effective CNI, production or target architecture."}
	b, _ := json.MarshalIndent(receipt, "", "  ")
	path := filepath.Join(o.evidence, "private-cluster-local-scope.json")
	if e := localEvidence(path, append(b, '\n')); e != nil {
		o.t.Error("actual local resource evidence retention failed")
	}
	if len(remaining) > 0 {
		o.t.Error("actual local resources remain; receipt retains exact ownership; no broad cleanup")
	}
}
func (c *client) policyTimeout() time.Duration {
	return time.Duration(c.policy.OperationSeconds) * time.Second
}
func TestActualPrivateClusterLocal(t *testing.T) {
	actualPrivateClusterGate(t, LocalARM64Profile)
}

// A separate root preserves the original ARM64 gate and evidence. The native
// controller and actual selected remote probe must both be Linux AMD64 here.
func TestActualPrivateClusterLinuxAMD64(t *testing.T) {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		t.Fatal("actual Linux amd64 controller required; cross compilation is not execution")
	}
	actualPrivateClusterGate(t, AMD64Profile)
}

func actualPrivateClusterGate(t *testing.T, expectedProfile string) {
	t.Helper()
	t.Run("approved_probe_executes_and_exact_resources_disappear", func(t *testing.T) {
		g := clusterSetup(t)
		g.api.server.Close()
		c, e := Load(os.Getenv("PROJECT_ONE_PRIVATE_CLUSTER_TEST_CONFIGURATION"))
		if e != nil || len(c.Profiles) != 1 {
			t.Fatal("explicit actual local cluster profile required", e)
		}
		p := c.Profiles[0]
		if p.Profile != expectedProfile {
			t.Fatal("actual selected cluster profile differs from the independent gate")
		}
		p.WorkspaceID = g.f.Workspace
		var installation string
		if e = g.f.Store.QueryRow(g.ctx, g.f.Statement(`SELECT deployment_id::text FROM platform_license_state WHERE id=1`, `SELECT deployment_id FROM platform_license_state WHERE id=1`)).Scan(&installation); e != nil {
			t.Fatal(e)
		}
		p.InstallationID = installation
		p.Namespace = "projectone-test-m28-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
		c.Profiles = []Profile{p}
		owner := localOperator(t, c, p)
		t.Cleanup(owner.cleanup)
		objects, e := ScopeObjects(c, p.ID)
		if e != nil {
			t.Fatal(e)
		}
		for _, raw := range objects["items"].([]any) {
			body := raw.(map[string]any)
			made := owner.create(g.ctx, body)
			if body["kind"] == "Namespace" {
				p.NamespaceUID = made.Metadata.UID
			}
			if body["kind"] == "ServiceAccount" && made.Metadata.Name == ControlServiceAccount {
				p.ControlServiceAccountUID = made.Metadata.UID
			}
		}
		// The actual token is obtained by explicit bounded TokenRequest and retained
		// only in the owning private temp root; no token/body appears in evidence.
		var token struct {
			Status struct {
				Token string `json:"token"`
			} `json:"status"`
		}
		path := "/api/v1/namespaces/" + p.Namespace + "/serviceaccounts/" + ControlServiceAccount + "/token"
		if e = owner.client.json(g.ctx, http.MethodPost, path, map[string]any{"apiVersion": "authentication.k8s.io/v1", "kind": "TokenRequest", "spec": map[string]any{"expirationSeconds": 600}}, &token); e != nil || token.Status.Token == "" {
			t.Fatal("actual namespace credential request refused", e)
		}
		p.TokenFile = filepath.Join(t.TempDir(), "control-token")
		if e = os.WriteFile(p.TokenFile, []byte(token.Status.Token), 0600); e != nil {
			t.Fatal(e)
		}
		c.Profiles = []Profile{p}
		g.s.config = c
		// Quota controller state is observed, never assumed immediately after POST.
		var connection Connection
		until := time.Now().Add(c.timeout())
		for {
			connection, e = g.s.Bind(g.ctx, g.a, g.mutation(0), p.ID)
			if e == nil {
				break
			}
			if e.Error() != "PRIVATE_CLUSTER_QUOTA_MISMATCH" || time.Now().After(until) {
				t.Fatal("actual scope readiness not observed; no uncertain mutation retry", e)
			}
			select {
			case <-g.ctx.Done():
				t.Fatal(g.ctx.Err())
			case <-time.After(250 * time.Millisecond):
			}
		}
		run := g.start(connection)
		until = time.Now().Add(c.timeout())
		for {
			run = g.action(connection, run, "reconcile")
			if run.Result != nil {
				break
			}
			if time.Now().After(until) {
				t.Fatal("actual bounded Job terminal result absent")
			}
			select {
			case <-g.ctx.Done():
				t.Fatal(g.ctx.Err())
			case <-time.After(250 * time.Millisecond):
			}
		}
		if run.Result == nil || *run.Result != "succeeded" {
			t.Fatal("real approved probe did not produce successful execution; missing preloaded OCI image is a real failure")
		}
		result, e := g.s.Proof(g.ctx, g.a, g.f.Workspace, connection.ID, run.ID)
		if e != nil {
			t.Fatal(e)
		}
		b, _ := json.MarshalIndent(struct {
			Connection Connection     `json:"connection"`
			Run        Run            `json:"run"`
			Execution  ExecutionProof `json:"execution"`
		}{connection, run, result}, "", "  ")
		if e = localEvidence(filepath.Join(owner.evidence, "private-cluster-local-proof.json"), append(b, '\n')); e != nil {
			t.Fatal(e)
		}
		run = g.action(connection, run, "cleanup")
		until = time.Now().Add(c.timeout())
		for run.Phase != "job_absent" {
			run = g.action(connection, run, "reconcile")
			if time.Now().After(until) {
				t.Fatal("actual Job/Pods remain; exact ownership retained")
			}
			time.Sleep(250 * time.Millisecond)
		}
		run = g.action(connection, run, "cleanup")
		until = time.Now().Add(c.timeout())
		for run.Phase != "cleaned" {
			run = g.action(connection, run, "reconcile")
			if time.Now().After(until) {
				t.Fatal("actual Lease remains; ownership retained")
			}
			time.Sleep(250 * time.Millisecond)
		}
		if e = g.s.VerifyStored(g.ctx); e != nil {
			t.Fatal(e)
		}
	})
}

func localEvidence(path string, raw []byte) error {
	file, e := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	if _, e = file.Write(raw); e == nil {
		e = file.Sync()
	}
	closeErr := file.Close()
	if e != nil {
		return e
	}
	if closeErr != nil {
		return closeErr
	}
	dir, e := os.Open(filepath.Dir(path))
	if e != nil {
		return e
	}
	defer dir.Close()
	return dir.Sync()
}
