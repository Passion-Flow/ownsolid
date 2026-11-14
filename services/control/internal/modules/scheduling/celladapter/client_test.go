package celladapter

import (
	"crypto/rand"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestObserveVerifiedTLSAndIdentity(t *testing.T) {
	token := rand.Text()
	var accepted atomic.Value
	accepted.Store(token)
	var mode, requests atomic.Int32
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		requests.Add(1)
		if req.Header.Get("Authorization") != "Bearer "+accepted.Load().(string) || req.Method != http.MethodGet {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch req.URL.Path {
		case "/apis/kueue.x-k8s.io/v1beta2/namespaces/team-a/workloads/job":
			switch mode.Load() {
			case 1:
				w.WriteHeader(http.StatusForbidden)
				return
			case 2:
				http.Redirect(w, req, "/redirected", http.StatusTemporaryRedirect)
				return
			case 3:
				w.WriteHeader(http.StatusNotFound)
				return
			case 4:
				fmt.Fprint(w, `{"apiVersion":"kueue.x-k8s.io/v1beta2","kind":"Workload","metadata":{"name":"job","namespace":"team-a","uid":"original","resourceVersion":"opaque-c","generation":2},"status":{"conditions":[{"type":"QuotaReserved","status":"False","reason":"TopologyPlacementFailed","message":"synthetic sibling and capacity details","observedGeneration":2}]}}`)
				return
			}
			fmt.Fprint(w, `{"apiVersion":"kueue.x-k8s.io/v1beta2","kind":"Workload","metadata":{"name":"job","namespace":"team-a","uid":"original","resourceVersion":"opaque-a","generation":2},"status":{"conditions":[{"type":"Admitted","status":"True","observedGeneration":2},{"type":"PodsReady","status":"True","observedGeneration":1}]}}`)
		case "/apis/batch.volcano.sh/v1alpha1/namespaces/team-a/jobs/job":
			fmt.Fprint(w, `{"apiVersion":"batch.volcano.sh/v1alpha1","kind":"Job","metadata":{"name":"job","namespace":"team-a","uid":"original","resourceVersion":"opaque-b"},"status":{"state":{"phase":"Running"}}}`)
		default:
			t.Errorf("unexpected cell path: %s", req.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(s.Close)
	dir := t.TempDir()
	caFile, tokenFile := filepath.Join(dir, "ca.pem"), filepath.Join(dir, "token")
	cert := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: s.Certificate().Raw})
	if err := os.WriteFile(caFile, cert, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tokenFile, []byte(token), 0o600); err != nil {
		t.Fatal(err)
	}
	config := Config{Origin: s.URL, DialAddress: netip.MustParseAddr("127.0.0.1"), CAFile: caFile, TokenFile: tokenFile, Timeout: 5 * time.Second, MaxConcurrent: 2}
	client, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.CloseIdleConnections)

	ref := Reference{Scheduler: Kueue, Namespace: "team-a", Name: "job", UID: "original"}
	got, err := client.Observe(t.Context(), ref)
	if err != nil || got != (Observation{ResourceVersion: "opaque-a", State: Admitted}) {
		t.Fatalf("Kueue stale readiness: %+v, %v", got, err)
	}
	mode.Store(4)
	got, err = client.Observe(t.Context(), ref)
	if err != nil || got != (Observation{ResourceVersion: "opaque-c", State: Pending, Blocker: TopologyPlacementFailed}) {
		t.Fatalf("verified Kueue topology blocker: %+v, %v", got, err)
	}
	mode.Store(0)
	ref.Scheduler = Volcano
	got, err = client.Observe(t.Context(), ref)
	if err != nil || got != (Observation{ResourceVersion: "opaque-b", State: Active}) {
		t.Fatalf("Volcano minimum running threshold: %+v, %v", got, err)
	}
	ref.Scheduler, ref.UID = Kueue, "expected"
	if _, err := client.Observe(t.Context(), ref); !errors.Is(err, ErrReplaced) {
		t.Fatalf("replaced object: %v", err)
	}
	ref.UID = "original"
	for i, want := range []error{ErrForbidden, ErrUnavailable, ErrMissing} {
		mode.Store(int32(i + 1))
		if _, err := client.Observe(t.Context(), ref); !errors.Is(err, want) {
			t.Errorf("response %d: got %v, want %v", i, err, want)
		}
	}
	mode.Store(0)
	rotate := func(permissions os.FileMode) {
		t.Helper()
		newToken := rand.Text()
		replacement := filepath.Join(dir, "token-replacement")
		if err := os.WriteFile(replacement, []byte(newToken), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(replacement, permissions); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(replacement, tokenFile); err != nil {
			t.Fatal(err)
		}
		accepted.Store(newToken)
	}
	rotate(0o600)
	if got, err := client.Observe(t.Context(), ref); err != nil || got.State != Admitted {
		t.Fatalf("rotated credential was not used: %+v, %v", got, err)
	}
	before := requests.Load()
	rotate(0o644)
	if _, err := client.Observe(t.Context(), ref); !errors.Is(err, ErrConfiguration) || requests.Load() != before {
		t.Fatalf("unsafe credential reached the cell: %v", err)
	}
	rotate(0o600)
	if got, err := client.Observe(t.Context(), ref); err != nil || got.State != Admitted {
		t.Fatalf("credential replacement did not restore observations: %+v, %v", got, err)
	}
	u, err := url.Parse(config.Origin)
	if err != nil {
		t.Fatal(err)
	}
	config.Origin = "https://wrong-cell.invalid:" + u.Port()
	wrongCell, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(wrongCell.CloseIdleConnections)
	if _, err := wrongCell.Observe(t.Context(), ref); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("cell TLS name mismatch: %v", err)
	}
	ref.Name = "../job"
	if _, err := client.Observe(t.Context(), ref); !errors.Is(err, ErrReference) {
		t.Fatalf("invalid object path: %v", err)
	}
}

func TestKueueReadyAndStopping(t *testing.T) {
	var obj object
	obj.Metadata.Generation = 2
	for _, kind := range []string{"Admitted", "PodsReady"} {
		obj.Status.Conditions = append(obj.Status.Conditions, condition{Type: kind, Status: "True", ObservedGeneration: 2})
	}
	if got := obj.observation(Kueue).State; got != Ready {
		t.Fatalf("current ready condition: %v", got)
	}
	obj.Status.Conditions[1].Status = "False"
	if got := obj.observation(Kueue); got.State != Admitted || got.Blocker != PodsNotReady {
		t.Fatalf("current admitted pods not ready: %+v", got)
	}
	obj.Status.Conditions[1].Status = "True"
	obj.Status.Conditions[1].Type = "Evicted"
	if got := obj.observation(Kueue).State; got != Terminating {
		t.Fatalf("evicted workload: %v", got)
	}
	obj.Status.Conditions[1].Type = "Finished"
	if got := obj.observation(Kueue).State; got != Terminal {
		t.Fatalf("finished workload: %v", got)
	}
	now := time.Now().Format(time.RFC3339)
	obj.Metadata.DeletionTimestamp = &now
	if got := obj.observation(Kueue).State; got != Terminating {
		t.Fatalf("deleted workload: %v", got)
	}
}

func TestKueueQuotaBlockerRequiresCurrentNegativeCondition(t *testing.T) {
	for _, tc := range []struct {
		name, status, reason string
		generation           int64
		want                 Blocker
	}{
		{"quota", "False", "WaitingForQuota", 2, WaitingForQuota},
		{"quota ceiling", "False", "ExceedsMaxQuota", 2, ExceedsMaxQuota},
		{"topology", "False", "TopologyPlacementFailed", 2, TopologyPlacementFailed},
		{"queue", "False", "Misconfigured", 2, QueueInadmissible},
		{"legacy queue", "False", "Inadmissible", 2, QueueInadmissible},
		{"generic pending", "False", "Pending", 2, ""},
		{"stale", "False", "WaitingForQuota", 1, ""},
		{"unknown condition status", "Unknown", "WaitingForQuota", 2, ""},
		{"quota reserved", "True", "WaitingForQuota", 2, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var obj object
			obj.Metadata.Generation = 2
			obj.Status.Conditions = []condition{{Type: "QuotaReserved", Status: tc.status, Reason: tc.reason, ObservedGeneration: tc.generation}}
			if got := obj.observation(Kueue); got.State != Pending || got.Blocker != tc.want {
				t.Fatalf("observed %+v, want pending with blocker %q", got, tc.want)
			}
			obj.Status.Conditions = append(obj.Status.Conditions, condition{Type: "Finished", Status: "True", ObservedGeneration: 2})
			if got := obj.observation(Kueue); got.State != Terminal || got.Blocker != "" {
				t.Fatalf("finished workload kept a blocker: %+v", got)
			}
		})
	}
}
