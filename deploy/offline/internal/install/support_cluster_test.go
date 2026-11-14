package install

import (
	"bytes"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const (
	supportTestSite      = "private-site"
	supportTestCell      = "private-cell"
	supportTestNamespace = "private-namespace"
	supportTestRelease   = "private-release"
	supportTestCookie    = "private-admin-session"
	supportTestBearer    = "private-kubernetes-token"
	supportTestDetails   = "private-upstream-detail"
)

const managementSupportJSON = `{"data":{"items":[{"id":"private-upstream-detail","site_id":"private-site","cell_id":"private-cell","name":"private-upstream-detail","state":"degraded","server_state":"ready","members":{"pending":0,"joining":0,"ready":2,"not_ready":1,"failed":0}}],"next_cursor":null}}`
const helmSupportMeta = `"metadata":{"name":"private-release-control","namespace":"private-namespace","generation":2,"uid":"private-upstream-detail","labels":{"app.kubernetes.io/name":"ownsolid-control","app.kubernetes.io/instance":"private-release","unrelated":"private-upstream-detail"}}`
const deploymentSupportJSON = `{"apiVersion":"apps/v1","kind":"Deployment",` + helmSupportMeta + `,"spec":{"replicas":1,"template":{"spec":{"containers":[{"image":"private-upstream-detail"}]}}},"status":{"observedGeneration":2,"replicas":1,"updatedReplicas":1,"readyReplicas":1,"availableReplicas":1,"conditions":[{"type":"Available","status":"True","message":"private-upstream-detail"}]}}`
const serviceSupportJSON = `{"apiVersion":"v1","kind":"Service",` + helmSupportMeta + `,"spec":{"type":"ClusterIP","clusterIP":"10.76.45.99","selector":{"app.kubernetes.io/name":"ownsolid-control","app.kubernetes.io/instance":"private-release"},"ports":[{"name":"https","port":443,"targetPort":"https","protocol":"TCP"}]}}`

func TestSupportClusterHTTPS(t *testing.T) {
	for _, test := range []struct {
		name, management, deployment, service       string
		wantManagement, wantDeployment, wantService string
		issues                                      []string
	}{
		{"all sources", "ok", "ok", "ok", "degraded", "ready", "configured", nil},
		{"permissions", "forbidden", "unauthorized", "ok", "", "", "configured", []string{"MANAGED_CLUSTER_ACCESS_DENIED", "HELM_DEPLOYMENT_ACCESS_DENIED"}},
		{"control offline", "offline", "ok", "missing", "", "ready", "", []string{"MANAGED_CLUSTER_UNAVAILABLE", "HELM_SERVICE_NOT_FOUND"}},
		{"timeout retains both Helm reads", "timeout", "ok", "ok", "", "ready", "configured", []string{"MANAGED_CLUSTER_TIMEOUT", "COLLECTION_TIMEOUT"}},
		{"deployment timeout retains service", "ok", "timeout", "ok", "degraded", "", "configured", []string{"HELM_DEPLOYMENT_TIMEOUT", "COLLECTION_TIMEOUT"}},
		{"bounded responses", "wrong_scope", "oversized", "wrong_selector", "", "", "unexpected", []string{"MANAGED_CLUSTER_INVALID_RESPONSE", "HELM_DEPLOYMENT_INVALID_RESPONSE", "HELM_SERVICE_CHART_MISMATCH"}},
		{"no redirect", "redirect", "wrong_name", "ok", "", "", "configured", []string{"MANAGED_CLUSTER_UNAVAILABLE", "HELM_DEPLOYMENT_INVALID_RESPONSE"}},
		{"stale rollout", "absent", "old_generation", "ok", "absent", "progressing", "configured", nil},
		{"old replica still ready", "ok", "old_replica", "ok", "degraded", "progressing", "configured", nil},
		{"known rollout failure", "ok", "degraded", "ok", "degraded", "degraded", "configured", nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("HTTPS_PROXY", "http://127.0.0.1:9")
			namespacePath := "/namespaces/" + supportTestNamespace
			deploymentPath := "/apis/apps/v1" + namespacePath + "/deployments/" + supportTestRelease + "-control"
			servicePath := "/api/v1" + namespacePath + "/services/" + supportTestRelease + "-control"
			kube := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.Header.Get("Authorization") != "Bearer "+supportTestBearer ||
					r.Header.Get("Cookie") != "" || r.URL.RawQuery != "" {
					t.Error("unexpected Kubernetes request method, scope or credentials")
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				switch r.URL.Path {
				case deploymentPath:
					writeClusterSupportResponse(w, r, test.deployment, deploymentSupportJSON)
				case servicePath:
					writeClusterSupportResponse(w, r, test.service, serviceSupportJSON)
				default:
					t.Error("unexpected Kubernetes route or followed redirect")
					w.WriteHeader(http.StatusBadRequest)
				}
			}))
			defer kube.Close()
			admin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				cookie, err := r.Cookie("__Host-admin_session")
				if r.Method != http.MethodGet || r.URL.Path != "/api/v1/sites/"+supportTestSite+"/cells/"+supportTestCell+"/admin/cell-clusters" ||
					r.URL.RawQuery != "" || r.Header.Get("Origin") != "https://"+r.Host ||
					r.Header.Get("Authorization") != "" || err != nil || cookie.Value != supportTestCookie {
					t.Error("unexpected Admin request method, scope or credentials")
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				if test.management == "redirect" {
					w.Header().Set("Location", kube.URL+"/redirected")
					w.WriteHeader(http.StatusFound)
					return
				}
				writeClusterSupportResponse(w, r, test.management, managementSupportJSON)
			}))
			defer admin.Close()
			m := writeClusterSupportEndpoint(t, admin, supportTestCookie)
			h := writeClusterSupportEndpoint(t, kube, supportTestBearer)
			c := clusterSupportInput{Version: 1, TimeoutSeconds: 1,
				Management: &managementSupportInput{m, supportTestSite, supportTestCell},
				Helm:       &helmSupportInput{h, supportTestNamespace, supportTestRelease},
			}
			report, raw, output := runClusterSupportFixture(t, c)
			if report.Scope != "managed_cluster" || !slices.Equal(report.Issues, test.issues) {
				t.Fatalf("unexpected report or issues: %+v", report)
			}
			check := func(found bool, state, want string) {
				t.Helper()
				if found != (want != "") || state != want {
					t.Fatalf("collection state=%q present=%t, want %q", state, found, want)
				}
			}
			if report.Management != nil {
				check(true, report.Management.State, test.wantManagement)
				if report.Management.State == "degraded" && (report.Management.Members["ready"] != 2 || report.Management.Members["not_ready"] != 1) {
					t.Fatal("member counts did not match the existing management API")
				}
			} else {
				check(false, "", test.wantManagement)
			}
			if report.Deployment != nil {
				check(true, report.Deployment.State, test.wantDeployment)
			} else {
				check(false, "", test.wantDeployment)
			}
			if report.Service != nil {
				check(true, report.Service.State, test.wantService)
			} else {
				check(false, "", test.wantService)
			}
			for _, sentinel := range []string{supportTestSite, supportTestCell, supportTestNamespace, supportTestRelease,
				supportTestCookie, supportTestBearer, supportTestDetails, "10.76.45.99", admin.URL, kube.URL, h.CredentialFile} {
				if bytes.Contains(raw, []byte(sentinel)) || strings.Contains(output, sentinel) {
					t.Fatal("archive or command output contained protected information")
				}
			}
		})
	}
}

func TestSupportClusterHandlesUnavailableEndpointAndUnsafeInputs(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("an untrusted peer or unsafe credential reached HTTP")
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	input := writeClusterSupportEndpoint(t, server, supportTestBearer)
	trust, _, err := generateCA("unrelated fixture authority")
	if err != nil {
		t.Fatal(err)
	}
	wrongCA := filepath.Join(t.TempDir(), "other-ca.pem")
	if err := os.WriteFile(wrongCA, trust, 0o600); err != nil {
		t.Fatal(err)
	}
	other := input
	other.CAFile = wrongCA
	closed := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	offline := writeClusterSupportEndpoint(t, closed, supportTestBearer)
	closed.Close()
	for _, test := range []struct {
		name  string
		input clusterSupportEndpoint
		want  []string
	}{
		{"wrong trust", other, []string{"HELM_DEPLOYMENT_TLS_REJECTED", "HELM_SERVICE_TLS_REJECTED"}},
		{"unsafe credential", input, []string{"HELM_CREDENTIAL_UNAVAILABLE"}},
		{"offline endpoint", offline, []string{"HELM_DEPLOYMENT_UNAVAILABLE", "HELM_SERVICE_UNAVAILABLE"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if test.name == "unsafe credential" {
				if err := os.Chmod(input.CredentialFile, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			report, _, _ := runClusterSupportFixture(t, clusterSupportInput{Version: 1, TimeoutSeconds: 1,
				Helm: &helmSupportInput{test.input, supportTestNamespace, supportTestRelease},
			})
			if !slices.Equal(report.Issues, test.want) || report.Deployment != nil || report.Service != nil {
				t.Fatalf("unexpected trust or credential result: %+v", report)
			}
		})
	}
}

func writeClusterSupportResponse(w http.ResponseWriter, r *http.Request, mode, body string) {
	w.Header().Set("Content-Type", "application/json")
	switch mode {
	case "timeout":
		<-r.Context().Done()
		return
	case "forbidden":
		w.WriteHeader(http.StatusForbidden)
	case "unauthorized":
		w.WriteHeader(http.StatusUnauthorized)
	case "offline":
		w.WriteHeader(http.StatusServiceUnavailable)
	case "missing":
		w.WriteHeader(http.StatusNotFound)
	case "absent":
		body = `{"data":{"items":[],"next_cursor":null}}`
	case "wrong_scope":
		body = strings.Replace(body, supportTestCell, "other-cell", 1)
	case "wrong_name":
		body = strings.Replace(body, "private-release-control", "another-control", 1)
	case "wrong_selector":
		body = strings.Replace(body, `"selector":{"app.kubernetes.io/name":"ownsolid-control"`, `"selector":{"app.kubernetes.io/name":"other-workload"`, 1)
	case "oversized":
		body = strings.Repeat(" ", maxClusterResponse+1)
	case "old_generation":
		body = strings.Replace(body, `"observedGeneration":2`, `"observedGeneration":1`, 1)
	case "old_replica":
		body = strings.Replace(body, `"updatedReplicas":1`, `"updatedReplicas":0`, 1)
	case "degraded":
		body = strings.Replace(body, `"type":"Available"`, `"type":"ReplicaFailure"`, 1)
	}
	fmt.Fprint(w, body)
}

func writeClusterSupportEndpoint(t *testing.T, server *httptest.Server, credential string) clusterSupportEndpoint {
	t.Helper()
	dir := t.TempDir()
	ca := filepath.Join(dir, "site-ca.pem")
	token := filepath.Join(dir, "credential")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(token, []byte(credential+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return clusterSupportEndpoint{server.URL, ca, token}
}

func runClusterSupportFixture(t *testing.T, c clusterSupportInput) (clusterSupportReport, []byte, string) {
	t.Helper()
	dir := t.TempDir()
	input, output := filepath.Join(dir, "cluster.json"), filepath.Join(dir, "support.tar.gz")
	raw, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(input, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	var log bytes.Buffer
	if binary := os.Getenv("OWNSOLID_CLUSTER_SUPPORT_INSTALLER"); binary != "" {
		cmd := exec.CommandContext(t.Context(), binary, "support-bundle", "--cluster-config", input, "--output", output)
		cmd.Stdout, cmd.Stderr = &log, &log
		err = cmd.Run()
	} else {
		err = ClusterSupportBundle(t.Context(), input, output, &log)
	}
	if err != nil || !strings.Contains(log.String(), "INSTALL_SUPPORT_COMPLETE") {
		t.Fatalf("cluster bundle failed: %v %s", err, log.String())
	}
	_, archive := readSupportArchive(t, output)
	var report clusterSupportReport
	if err := json.Unmarshal(archive, &report); err != nil {
		t.Fatal(err)
	}
	return report, archive, log.String()
}
