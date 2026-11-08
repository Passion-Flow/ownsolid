package v1

import (
	"strings"
	"testing"
	"time"
)

const (
	testCluster = "6f1c2a8e-4b3d-4c5e-9f7a-1b2c3d4e5f60"
	testNode    = "0d9e8f7a-6b5c-4d3e-8f2a-1b0c9d8e7f6a"
	testCAHash  = "2cd644cf7855ba56fe02245ba8c22a1f015f75ba25be5e89fc32502efc95f50d"
	testToken   = "K10" + testCAHash + "::awcyco.0123456789abcdef"
)

func serverCommand() KubernetesCommand {
	return KubernetesCommand{ClusterID: testCluster, Version: ApprovedKubernetesRelease().Version, Role: KubernetesServerRole, NodeName: testNode, NodeAddress: "172.22.0.2"}
}

func workerCommand() KubernetesCommand {
	c := serverCommand()
	c.Role, c.ServerAddress, c.JoinToken = KubernetesWorkerRole, "172.22.0.2", testToken
	c.NodeAddress = "172.22.0.3"
	return c
}

func TestApprovedKubernetesRelease(t *testing.T) {
	release := ApprovedKubernetesRelease()
	artifacts := release.Artifacts()
	if len(artifacts) != 3 || artifacts[0].Name != "rke2.linux-amd64.tar.gz" {
		t.Fatalf("artifacts %+v", artifacts)
	}
	for _, artifact := range artifacts {
		found, ok := KubernetesArtifactForName(artifact.Name)
		if !ok || found != artifact || !kubernetesSHA256.MatchString(artifact.SHA256) || artifact.SizeBytes < 1 {
			t.Fatalf("artifact %+v", artifact)
		}
	}
	// The copy is the caller's: changing it leaves the approved release alone.
	release.Images[0].SHA256 = strings.Repeat("0", 64)
	if ApprovedKubernetesRelease().Images[0].SHA256 == release.Images[0].SHA256 {
		t.Fatal("release shares its images")
	}
	if _, ok := KubernetesArtifactForName("install.sh"); ok {
		t.Fatal("an unapproved file is an artifact")
	}
}

func TestKubernetesJoinToken(t *testing.T) {
	if hash, ok := KubernetesTokenCAHash(testToken); !ok || hash != testCAHash {
		t.Fatalf("hash %q %v", hash, ok)
	}
	for _, token := range []string{
		"",
		"protoAgentSecret0123456789",           // short format pins no CA
		"K10" + testCAHash + "::server:secret", // the server token is never sent
		"K10" + testCAHash[1:] + "::awcyco.0123456789abcdef",
		"K10" + strings.ToUpper(testCAHash) + "::awcyco.0123456789abcdef",
		testToken + "\n",
	} {
		if ValidKubernetesJoinToken(token) {
			t.Errorf("token %q accepted", token)
		}
	}
}

func TestKubernetesNodeAddress(t *testing.T) {
	for _, address := range []string{"172.22.0.2", "10.0.0.1", "2001:db8::1"} {
		if !ValidKubernetesNodeAddress(address) {
			t.Errorf("%s refused", address)
		}
	}
	for _, address := range []string{"", "127.0.0.1", "0.0.0.0", "::1", "fe80::1", "fe80::1%eth0", "224.0.0.1", "node-1", "172.022.0.2", " 172.22.0.2"} {
		if ValidKubernetesNodeAddress(address) {
			t.Errorf("%q accepted", address)
		}
	}
}

func TestKubernetesCommandValidate(t *testing.T) {
	server, worker := serverCommand(), workerCommand()
	token := server
	token.TokenTTLSeconds = 3600
	valid := []struct {
		operation string
		command   KubernetesCommand
	}{
		{KubernetesJoinOperation, server},
		{KubernetesJoinOperation, worker},
		{KubernetesTokenOperation, token},
		{KubernetesObserveOperation, server},
	}
	for _, c := range valid {
		if e := c.command.Validate(c.operation); e != nil {
			t.Errorf("%s %+v: %v", c.operation, c.command, e)
		}
	}
	observeWorker := worker
	observeWorker.ServerAddress, observeWorker.JoinToken = "", ""
	if observeWorker.Validate(KubernetesObserveOperation) != nil {
		t.Error("worker observe refused")
	}
	invalid := []struct {
		name      string
		operation string
		mutate    func(*KubernetesCommand)
		base      KubernetesCommand
	}{
		{"server with a token", KubernetesJoinOperation, func(c *KubernetesCommand) { c.JoinToken = testToken }, server},
		{"server with a server address", KubernetesJoinOperation, func(c *KubernetesCommand) { c.ServerAddress = "172.22.0.9" }, server},
		{"worker without a token", KubernetesJoinOperation, func(c *KubernetesCommand) { c.JoinToken = "" }, worker},
		{"worker joining itself", KubernetesJoinOperation, func(c *KubernetesCommand) { c.ServerAddress = c.NodeAddress }, worker},
		{"worker with a ttl", KubernetesJoinOperation, func(c *KubernetesCommand) { c.TokenTTLSeconds = 600 }, worker},
		{"other version", KubernetesJoinOperation, func(c *KubernetesCommand) { c.Version = "v1.37.2+rke2r1" }, server},
		{"node name not an ID", KubernetesJoinOperation, func(c *KubernetesCommand) { c.NodeName = "gpu-01" }, server},
		{"loopback address", KubernetesJoinOperation, func(c *KubernetesCommand) { c.NodeAddress = "127.0.0.1" }, server},
		{"unknown role", KubernetesJoinOperation, func(c *KubernetesCommand) { c.Role = "etcd" }, server},
		{"token from a worker", KubernetesTokenOperation, func(c *KubernetesCommand) { c.Role = KubernetesWorkerRole }, token},
		{"token that never expires", KubernetesTokenOperation, func(c *KubernetesCommand) { c.TokenTTLSeconds = 0 }, token},
		{"token too short", KubernetesTokenOperation, func(c *KubernetesCommand) { c.TokenTTLSeconds = KubernetesTokenMinimumSeconds - 1 }, token},
		{"token too long", KubernetesTokenOperation, func(c *KubernetesCommand) { c.TokenTTLSeconds = KubernetesTokenMaximumSeconds + 1 }, token},
		{"observe with a token", KubernetesObserveOperation, func(c *KubernetesCommand) { c.JoinToken = testToken }, server},
		{"unknown operation", "kubernetes.drain.v1", func(*KubernetesCommand) {}, server},
	}
	for _, c := range invalid {
		command := c.base
		c.mutate(&command)
		if command.Validate(c.operation) == nil {
			t.Errorf("%s accepted", c.name)
		}
	}
}

func TestKubernetesObservationValidateFor(t *testing.T) {
	now := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	server := serverCommand()
	joined := KubernetesObservation{ClusterID: testCluster, Version: server.Version, Role: KubernetesServerRole, State: KubernetesReady, CASHA256: testCAHash, ObservedAt: now}
	if e := joined.ValidateFor(server, KubernetesJoinOperation, "success"); e != nil {
		t.Fatal(e)
	}
	token := server
	token.TokenTTLSeconds = 3600
	created := joined
	created.JoinToken, created.JoinTokenExpiresAt = testToken, now.Add(time.Hour)
	if e := created.ValidateFor(token, KubernetesTokenOperation, "success"); e != nil {
		t.Fatal(e)
	}
	failed := joined
	failed.State, failed.ErrorCode, failed.CASHA256 = KubernetesNotReady, "KUBERNETES_NOT_READY", ""
	if e := failed.ValidateFor(server, KubernetesJoinOperation, "failure"); e != nil {
		t.Fatal(e)
	}
	worker := workerCommand()
	workerJoined := KubernetesObservation{ClusterID: testCluster, Version: worker.Version, Role: KubernetesWorkerRole, State: KubernetesReady, ObservedAt: now}
	if e := workerJoined.ValidateFor(worker, KubernetesJoinOperation, "success"); e != nil {
		t.Fatal(e)
	}
	invalid := []struct {
		name        string
		observation KubernetesObservation
		command     KubernetesCommand
		operation   string
		outcome     string
	}{
		{"server success without CA", func() KubernetesObservation { o := joined; o.CASHA256 = ""; return o }(), server, KubernetesJoinOperation, "success"},
		{"success not ready", func() KubernetesObservation { o := joined; o.State = KubernetesNotReady; return o }(), server, KubernetesJoinOperation, "success"},
		{"success with an error", func() KubernetesObservation { o := joined; o.ErrorCode = "KUBERNETES_NOT_READY"; return o }(), server, KubernetesJoinOperation, "success"},
		{"failure without an error", func() KubernetesObservation { o := failed; o.ErrorCode = ""; return o }(), server, KubernetesJoinOperation, "failure"},
		{"unknown error", func() KubernetesObservation { o := failed; o.ErrorCode = "KUBERNETES_SURPRISE"; return o }(), server, KubernetesJoinOperation, "failure"},
		{"worker reporting a CA", func() KubernetesObservation { o := workerJoined; o.CASHA256 = testCAHash; return o }(), worker, KubernetesJoinOperation, "success"},
		{"join returning a token", func() KubernetesObservation { o := joined; o.JoinToken = testToken; return o }(), server, KubernetesJoinOperation, "success"},
		{"other cluster", func() KubernetesObservation { o := joined; o.ClusterID = testNode; return o }(), server, KubernetesJoinOperation, "success"},
		{"other role", func() KubernetesObservation { o := joined; o.Role = KubernetesWorkerRole; return o }(), server, KubernetesJoinOperation, "success"},
		{"no observation time", func() KubernetesObservation { o := joined; o.ObservedAt = time.Time{}; return o }(), server, KubernetesJoinOperation, "success"},
		{"token pinning another CA", func() KubernetesObservation { o := created; o.CASHA256 = strings.Repeat("a", 64); return o }(), token, KubernetesTokenOperation, "success"},
		{"token living longer than asked", func() KubernetesObservation { o := created; o.JoinTokenExpiresAt = now.Add(2 * time.Hour); return o }(), token, KubernetesTokenOperation, "success"},
		{"token already expired", func() KubernetesObservation { o := created; o.JoinTokenExpiresAt = now.Add(-time.Second); return o }(), token, KubernetesTokenOperation, "success"},
		{"token in a failure", func() KubernetesObservation { o := created; o.ErrorCode = "KUBERNETES_TOKEN_FAILED"; return o }(), token, KubernetesTokenOperation, "failure"},
		{"unknown outcome", joined, server, KubernetesJoinOperation, "partial"},
	}
	for _, c := range invalid {
		if c.observation.ValidateFor(c.command, c.operation, c.outcome) == nil {
			t.Errorf("%s accepted", c.name)
		}
	}
}

func TestKubernetesArtifactRequest(t *testing.T) {
	binary := ApprovedKubernetesRelease().Binary
	last := (binary.SizeBytes - 1) / KubernetesTransferChunkBytes * KubernetesTransferChunkBytes
	for _, r := range []KubernetesArtifactRequest{
		{Name: binary.Name, Offset: 0, Length: KubernetesTransferChunkBytes},
		{Name: binary.Name, Offset: last, Length: binary.SizeBytes - last},
	} {
		if e := r.Validate(); e != nil {
			t.Errorf("%+v: %v", r, e)
		}
	}
	for _, r := range []KubernetesArtifactRequest{
		{Name: "install.sh", Offset: 0, Length: KubernetesTransferChunkBytes},
		{Name: binary.Name, Offset: 1, Length: KubernetesTransferChunkBytes},
		{Name: binary.Name, Offset: 0, Length: 1},
		{Name: binary.Name, Offset: -KubernetesTransferChunkBytes, Length: KubernetesTransferChunkBytes},
		{Name: binary.Name, Offset: last + KubernetesTransferChunkBytes, Length: 1},
	} {
		if r.Validate() == nil {
			t.Errorf("%+v accepted", r)
		}
	}
}
