package e2e

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

const certificatePins = "certificate_key_sha256"

// callHeartbeat makes one heartbeat as a node would, with the credential and the given
// certificate, and returns the status and the code of any refusal. The body is never read
// by the time a connection's certificate is refused.
func (a *agentProcess) callHeartbeat(t *testing.T, e environment, credential string, certificate *tls.Certificate) (int, string) {
	t.Helper()
	b := e.newBrowser(t, adminOrigin)
	transport := b.client.Transport.(*http.Transport)
	if certificate != nil {
		transport.TLSClientConfig.Certificates = []tls.Certificate{*certificate}
	}
	defer transport.CloseIdleConnections()
	path := "/agent/v1/sites/default/cells/default/nodes/" + a.nodeID + "/heartbeat"
	request, err := http.NewRequest(http.MethodPost, adminOrigin+path, bytes.NewReader([]byte("{}")))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+credential)
	request.Header.Set("Content-Type", "application/json")
	response, err := b.client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var problem struct {
		Code string `json:"code"`
	}
	json.NewDecoder(response.Body).Decode(&problem)
	return response.StatusCode, problem.Code
}

func readJSON[T any](t *testing.T, path string) T {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out T
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// nodeCertificate is what the agent keeps of its certificate, field for field.
type nodeCertificate struct {
	PrivateKey  string    `json:"private_key"`
	Certificate string    `json:"certificate"`
	PendingKey  string    `json:"pending_key,omitempty"`
	Confirmed   bool      `json:"confirmed"`
	RenewAt     time.Time `json:"renew_at"`
}

func (c nodeCertificate) pair(t *testing.T) *tls.Certificate {
	t.Helper()
	out, err := tls.X509KeyPair([]byte(c.Certificate), []byte(c.PrivateKey))
	if err != nil {
		t.Fatal(err)
	}
	return &out
}

// rotateCertificate proves the identity rules against a real install, then moves the renewal
// time into the past so the running agent does what it does 70% through a real lifetime.
func (a *agentProcess) rotateCertificate(t *testing.T, e environment) {
	t.Helper()
	statePath := filepath.Join(a.stateDirectory, "certificate.json")
	identity := readJSON[struct {
		Credential string `json:"credential"`
	}](t, filepath.Join(a.stateDirectory, "identity.json"))
	old := readJSON[nodeCertificate](t, statePath)
	if old.Certificate == "" || !old.Confirmed || old.PendingKey != "" {
		t.Fatalf("enrollment left %+v", old)
	}
	pin := func() string {
		return e.sql(t, "SELECT encode("+certificatePins+",'hex') FROM managed_nodes WHERE id='"+a.nodeID+"'")
	}
	before := pin()
	if len(before) != 64 {
		t.Fatalf("the node's key is not pinned: %q", before)
	}
	if status, code := a.callHeartbeat(t, e, identity.Credential, nil); status != http.StatusUnauthorized || code != "AGENT_CERTIFICATE_REQUIRED" {
		t.Fatalf("no certificate: %d %s", status, code)
	}
	if status, code := a.callHeartbeat(t, e, identity.Credential, old.pair(t)); status == http.StatusUnauthorized {
		t.Fatalf("its own certificate was refused: %s", code)
	}

	a.stop(t)
	due := old
	due.RenewAt = time.Now().Add(-time.Minute)
	raw, err := json.Marshal(due)
	if err != nil {
		t.Fatal(err)
	}
	writePrivate(t, statePath, raw)
	mark := len(a.output())
	a.start(t)
	a.waitForAfter(t, mark, "agent_certificate_renewed", 2*time.Minute)

	// Control has moved to the new key, and clears the old one when the agent confirms.
	deadline := time.Now().Add(time.Minute)
	for e.sql(t, "SELECT previous_certificate_key_sha256 IS NULL FROM managed_nodes WHERE id='"+a.nodeID+"'") != "t" || pin() == before {
		if time.Now().After(deadline) {
			t.Fatalf("the rotation did not finish: pin %s\n%s", pin(), a.output())
		}
		time.Sleep(200 * time.Millisecond)
	}
	current := readJSON[nodeCertificate](t, statePath)
	if current.PrivateKey == old.PrivateKey || current.Certificate == old.Certificate || !current.Confirmed {
		t.Fatalf("after the rotation: confirmed=%v same key=%v", current.Confirmed, current.PrivateKey == old.PrivateKey)
	}
	if status, code := a.callHeartbeat(t, e, identity.Credential, old.pair(t)); status != http.StatusUnauthorized || code != "AGENT_CERTIFICATE_SUPERSEDED" {
		t.Fatalf("the old certificate after the rotation: %d %s", status, code)
	}
	if status, code := a.callHeartbeat(t, e, identity.Credential, current.pair(t)); status == http.StatusUnauthorized {
		t.Fatalf("the new certificate was refused: %s", code)
	}
	a.runTask(t, 30*time.Second)
	t.Log("rotated the certificate; " + strconv.Itoa(len(before)/2) + " byte pin moved to a new key")
}
