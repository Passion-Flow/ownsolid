//go:build repository_integration && machine_ingress_integration

package distribution_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/distribution"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
)

type machineOriginBinding struct {
	originHash, intent string
	credential, input  []byte
	created            time.Time
}

func machineOriginHash(origin string) string {
	digest := sha256.Sum256([]byte(origin))
	return hex.EncodeToString(digest[:])
}

func machineOriginReadBinding(t *testing.T, g pairedDistributionStory, id string) machineOriginBinding {
	t.Helper()
	var out machineOriginBinding
	err := g.f.Store.QueryRow(g.ctx, g.sql(
		"SELECT control_origin_sha256,idempotency_key::text,credential_hash,fingerprint,created_at FROM gateway_instances WHERE id=$1",
		"SELECT control_origin_sha256,idempotency_key,credential_hash,fingerprint,created_at FROM gateway_instances WHERE id=?", id)).Scan(&out.originHash, &out.intent, &out.credential, &out.input, &out.created)
	if err != nil {
		t.Fatal("actual immutable gateway binding readback failed", err)
	}
	out.created = out.created.UTC()
	return out
}

func machineOriginReadFence(t *testing.T, g pairedDistributionStory, id string) ([]byte, string) {
	t.Helper()
	var raw []byte
	var digest string
	if err := g.f.Store.QueryRow(g.ctx, g.sql("SELECT fence_receipt,fence_receipt_sha256 FROM gateway_recoveries WHERE id=$1", "SELECT fence_receipt,fence_receipt_sha256 FROM gateway_recoveries WHERE id=?", id)).Scan(&raw, &digest); err != nil {
		t.Fatal("actual retained fence readback failed", err)
	}
	return raw, digest
}

func TestDistributionMachineOriginBindingSurvivesTransportChange(t *testing.T) {
	// Exactly one real selected-vendor repository fixture is created by this
	// root. Its License, sessions, SQL, admission and audit are real. Node/GPU/
	// ready-model facts and the explicitly synthetic operator receipt are not
	// physical inference, a running gateway, machine TLS or fencing evidence.
	g := pairedDistributionSetup(t).access(t)
	oldInput, oldGateway := g.provisionInput(), g.gateway
	legacyOrigin := g.owner.Config.Server.AdminOrigin
	legacyBinding := machineOriginReadBinding(t, g, oldGateway.Gateway.ID)
	if oldGateway.Secret == nil || !oldGateway.SecretAvailable || !oldGateway.Created || legacyBinding.originHash != machineOriginHash(legacyOrigin) {
		t.Fatal("legacy gateway lacks its actual one-time origin binding")
	}
	admission := g.admission()
	if _, err := g.s.Admit(g.ctx, oldGateway.Gateway.ID, *oldGateway.Secret, admission, uuid.NewString()); err != nil {
		t.Fatal("actual scoped admission failed", err)
	}
	g.held(t, admission.RequestID, true)

	// The same process can select browser HTTP independently. These synthetic
	// references are never opened here: Config.MachineOrigin owns selection,
	// while actual listener/TLS validation belongs to its separate acceptance.
	g.owner.Config.Server = config.ServerConfiguration{TransportMode: config.TransportHTTP, ListenAddress: "127.0.0.1:18080", PortalOrigin: "http://portal.example.invalid:18080", AdminOrigin: "http://admin.example.invalid:18080"}
	g.owner.Config.MachineServer = nil
	replay, err := g.s.Provision(g.ctx, g.admin, oldInput, legacyBinding.intent, uuid.NewString())
	if err != nil || replay.Gateway.ID != oldGateway.Gateway.ID || replay.Secret != nil || replay.SecretAvailable || replay.Created || !reflect.DeepEqual(legacyBinding, machineOriginReadBinding(t, g, oldGateway.Gateway.ID)) {
		t.Fatal("historical replay selected unavailable transport, rewrote its binding or reissued a secret", err)
	}
	if _, err = g.s.RevokeGateway(g.ctx, g.admin, g.f.Workspace, oldGateway.Gateway.ID, "Explicit synthetic origin transition", uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	rowsBefore := g.count(t, "SELECT COUNT(*) FROM gateway_instances", "SELECT COUNT(*) FROM gateway_instances")
	auditBefore := g.count(t, "SELECT COUNT(*) FROM audit_events", "SELECT COUNT(*) FROM audit_events")
	newIntent := uuid.NewString()
	denied, err := g.s.Provision(g.ctx, g.admin, oldInput, newIntent, uuid.NewString())
	pairedDistributionCode(t, err, "CONFIG_MACHINE_TRANSPORT_REQUIRED")
	if !reflect.DeepEqual(denied, distribution.Provisioned{}) || g.count(t, "SELECT COUNT(*) FROM gateway_instances", "SELECT COUNT(*) FROM gateway_instances") != rowsBefore || g.count(t, "SELECT COUNT(*) FROM audit_events", "SELECT COUNT(*) FROM audit_events") != auditBefore || !reflect.DeepEqual(legacyBinding, machineOriginReadBinding(t, g, oldGateway.Gateway.ID)) {
		t.Fatal("browser-only provisioning published authority/audit or changed historical binding")
	}

	const newOrigin = "https://machine-a.example.invalid:19443"
	g.owner.Config.MachineServer = &config.MachineServerConfiguration{ListenAddress: "127.0.0.1:19443", Origin: newOrigin, TLSCertificateFile: "/synthetic-machine-config/certificate.pem", TLSPrivateKeyFile: "/synthetic-machine-config/private-key.pem"}
	fresh, err := g.s.Provision(g.ctx, g.admin, oldInput, newIntent, uuid.NewString())
	if err != nil || fresh.Gateway.ID == oldGateway.Gateway.ID || fresh.Secret == nil || !fresh.SecretAvailable || !fresh.Created {
		t.Fatal("explicit machine origin did not create one fresh authority", err)
	}
	freshBinding := machineOriginReadBinding(t, g, fresh.Gateway.ID)
	if freshBinding.originHash != machineOriginHash(newOrigin) || freshBinding.originHash == legacyBinding.originHash || freshBinding.originHash == machineOriginHash(g.owner.Config.Server.AdminOrigin) || freshBinding.intent != newIntent {
		t.Fatal("new gateway persisted a browser or historical origin")
	}

	challenge, err := g.s.CreateRecoveryChallenge(g.ctx, g.admin, g.f.Workspace, admission.RequestID, "Explicit synthetic retained-origin recovery", uuid.NewString(), uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	// No machine/operator operation is fabricated as executed. This explicitly
	// synthetic receipt tests the existing SQL scope gate and exact persistence.
	receipt := g.fence(challenge.ID)
	receipt.ControlOriginSHA256 = legacyBinding.originHash
	if err := receipt.Validate(); err != nil {
		t.Fatal("synthetic historical receipt violates the real wire contract", err)
	}
	wrong := receipt
	wrong.ControlOriginSHA256 = freshBinding.originHash
	fenceAuditsBefore := g.count(t, "SELECT COUNT(*) FROM audit_events WHERE action=$1", "SELECT COUNT(*) FROM audit_events WHERE action=?", "gateway.recovery_fence_accepted")
	_, err = g.s.AcceptRecoveryFence(g.ctx, g.admin, g.f.Workspace, admission.RequestID, challenge.ID, wrong, "Synthetic wrong current-origin receipt", uuid.NewString())
	pairedDistributionCode(t, err, "GATEWAY_FENCE_SCOPE_CONFLICT")
	if g.count(t, "SELECT COUNT(*) FROM gateway_recoveries WHERE id=$1 AND fence_receipt IS NOT NULL", "SELECT COUNT(*) FROM gateway_recoveries WHERE id=? AND fence_receipt IS NOT NULL", challenge.ID) != 0 || g.count(t, "SELECT COUNT(*) FROM audit_events WHERE action=$1", "SELECT COUNT(*) FROM audit_events WHERE action=?", "gateway.recovery_fence_accepted") != fenceAuditsBefore {
		t.Fatal("incorrect new-origin fence changed the historical recovery")
	}
	accepted, err := g.s.AcceptRecoveryFence(g.ctx, g.admin, g.f.Workspace, admission.RequestID, challenge.ID, receipt, "Explicit synthetic correct retained-origin receipt", uuid.NewString())
	if err != nil || accepted.FenceReceipt == nil || accepted.FenceAcceptedAt == nil || accepted.FenceReceipt.ControlOriginSHA256 != legacyBinding.originHash {
		t.Fatal("old stored origin was replaced by current machine configuration", err)
	}
	retainedFence, retainedDigest := machineOriginReadFence(t, g, challenge.ID)
	expectedDigest, err := receipt.Digest()
	if err != nil || retainedDigest != expectedDigest {
		t.Fatal("canonical historical fence digest was not persisted", err)
	}
	g.held(t, admission.RequestID, true)

	// Another deployment-origin change must not rebind either existing intent.
	g.owner.Config.Server.PortalOrigin = "http://portal-secondary.example.invalid:18080"
	g.owner.Config.Server.AdminOrigin = "http://admin-secondary.example.invalid:18080"
	g.owner.Config.MachineServer.Origin = "https://machine-b.example.invalid:19444"
	g.owner.Config.MachineServer.ListenAddress = "127.0.0.1:19444"
	for _, previous := range []struct {
		gateway distribution.Provisioned
		binding machineOriginBinding
	}{{oldGateway, legacyBinding}, {fresh, freshBinding}} {
		replayed, err := g.s.Provision(g.ctx, g.admin, oldInput, previous.binding.intent, uuid.NewString())
		if err != nil || replayed.Gateway.ID != previous.gateway.Gateway.ID || replayed.Secret != nil || replayed.SecretAvailable || replayed.Created || !reflect.DeepEqual(previous.binding, machineOriginReadBinding(t, g, previous.gateway.Gateway.ID)) {
			t.Fatal("configuration change rebound an existing identity or disclosed its one-time secret", err)
		}
	}
	if _, err = g.s.AcceptRecoveryFence(g.ctx, g.admin, g.f.Workspace, admission.RequestID, challenge.ID, receipt, "Exact retained synthetic receipt replay", uuid.NewString()); err != nil {
		t.Fatal("exact historical fence replay refused", err)
	}
	afterFence, afterDigest := machineOriginReadFence(t, g, challenge.ID)
	if !bytes.Equal(retainedFence, afterFence) || retainedDigest != afterDigest {
		t.Fatal("configuration change rewrote an accepted historical receipt")
	}
	outcome := pairedDistributionSuccess(admission.RequestID)
	ack, err := g.s.Report(g.ctx, oldGateway.Gateway.ID, *oldGateway.Secret, outcome, uuid.NewString())
	if err != nil {
		t.Fatal("revoked historical gateway could not report its committed metadata-only outcome", err)
	}
	var storedOutcome []byte
	var storedOutcomeDigest string
	if err = g.f.Store.QueryRow(g.ctx, g.sql("SELECT outcome,outcome_sha256 FROM gateway_requests WHERE id=$1", "SELECT outcome,outcome_sha256 FROM gateway_requests WHERE id=?", admission.RequestID)).Scan(&storedOutcome, &storedOutcomeDigest); err != nil || ack.CanonicalSHA256 != storedOutcomeDigest {
		t.Fatal("actual historical outcome acknowledgment mismatch", err)
	}
	again, err := g.s.Report(g.ctx, oldGateway.Gateway.ID, *oldGateway.Secret, outcome, uuid.NewString())
	if err != nil || again != ack {
		t.Fatal("historical canonical outcome replay changed", err)
	}
	var finalOutcome []byte
	var finalOutcomeDigest string
	if err = g.f.Store.QueryRow(g.ctx, g.sql("SELECT outcome,outcome_sha256 FROM gateway_requests WHERE id=$1", "SELECT outcome,outcome_sha256 FROM gateway_requests WHERE id=?", admission.RequestID)).Scan(&finalOutcome, &finalOutcomeDigest); err != nil || !bytes.Equal(storedOutcome, finalOutcome) || storedOutcomeDigest != finalOutcomeDigest {
		t.Fatal("retained outcome was rewritten", err)
	}
	g.held(t, admission.RequestID, false)
	if !reflect.DeepEqual(legacyBinding, machineOriginReadBinding(t, g, oldGateway.Gateway.ID)) || !reflect.DeepEqual(freshBinding, machineOriginReadBinding(t, g, fresh.Gateway.ID)) {
		t.Fatal("recovery or outcome delivery rewrote an origin/credential/intent binding")
	}
}
