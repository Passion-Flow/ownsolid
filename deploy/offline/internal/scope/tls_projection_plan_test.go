package scope

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/compose"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// Real private files and hash-chain journal, explicit synthetic restored/task
// metadata and Engine doubles. No SQL, volume projection or serving is executed.
func tlsProjectionPrivatePlanFixture(t *testing.T, at ...time.Time) (*locked, RestoredTLSPlan) {
	l, p, _, _ := tlsProjectionPrivatePlanRuntimeFixture(t, at...)
	return l, p
}
func tlsProjectionPrivatePlanRuntimeFixture(t *testing.T, at ...time.Time) (*locked, RestoredTLSPlan, *databaseEngineDouble, *databaseWorkerDouble) {
	return tlsProjectionPrivatePlanRuntimeConfiguredFixture(t, nil, nil, at...)
}
func tlsProjectionPrivatePlanRuntimeConfiguredFixture(t *testing.T, configure func(*preparedInputs), configureModel func(*compose.Model), at ...time.Time) (*locked, RestoredTLSPlan, *databaseEngineDouble, *databaseWorkerDouble) {
	t.Helper()
	oldCertificate, oldKey, _ := restoredTLSCertificate(t)
	l, storePlan, d, w := restoreStoresPlanRuntimeWithInputFixture(t, func(p *preparedInputs) {
		for i, f := range p.files["control-config"] {
			if f.Name == "runtime.yaml" {
				var value map[string]any
				if wire.UniqueJSON(f.Contents, &value) != nil {
					t.Fatal("synthetic runtime")
				}
				prefix := engine.DatabaseTaskDestination + "/private/"
				value["server"] = map[string]string{"portal_origin": "https://portal.example.test:18443", "admin_origin": "https://admin.example.test:18444", "tls_certificate_file": prefix + "certificate.pem", "tls_private_key_file": prefix + "private-key.pem"}
				p.files["control-config"][i].Contents = encode(value)
				for j, other := range p.files["operator-config"] {
					if other.Name == "runtime.yaml" {
						p.files["operator-config"][j].Contents = encode(value)
					}
				}
			}
		}
		p.files["control-config"] = append(p.files["control-config"], engine.ConfigurationFile{Name: "certificate.pem", Contents: oldCertificate}, engine.ConfigurationFile{Name: "private-key.pem", Contents: oldKey})
		if configure != nil {
			configure(p)
		}
	}, configureModel)
	if e := writeNew(l.root, restoreStoresPlanFile, encode(storePlan)); e != nil {
		l.close()
		t.Fatal(e)
	}
	base := restoreStoresPlanRecord(storePlan)
	for i := range restoreStoresSteps(base.Stores) {
		events := appendSyntheticRestoreStoresStep(t, l.events, base, i, l.marker.ScopeID)
		for _, e := range events[len(events)-2:] {
			if err := l.appendEvent(e); err != nil {
				l.close()
				t.Fatal(err)
			}
		}
	}
	verified, e := l.verifyRuntimeContext(context.Background(), d, w, false)
	if e != nil {
		l.close()
		t.Fatal(e)
	}
	// Constructor metadata is test-only; the tagged path independently inspects
	// genuinely stopped containers. Match IDs while declaring stopped metadata.
	for name, status := range verified.observed.Services {
		status.State = "created"
		status.ExitCode = 0
		verified.observed.Services[name] = status
	}
	now := time.Now()
	if len(at) > 0 {
		now = at[0]
	}
	cert, key, ca := restoredTLSCertificateAt(t, now)
	directory := t.TempDir()
	if e = os.Chmod(directory, 0700); e != nil {
		l.close()
		t.Fatal(e)
	}
	request := configuration.TLSGenerationRequest{FormatVersion: 1, CertificateFile: filepath.Join(directory, "chain.pem"), PrivateKeyFile: filepath.Join(directory, "key.pem"), TrustBundleFile: filepath.Join(directory, "trust.pem"), Reason: "Synthetic private TLS publication"}
	for _, item := range []struct {
		name string
		raw  []byte
	}{{request.CertificateFile, cert}, {request.PrivateKeyFile, key}, {request.TrustBundleFile, ca}} {
		if e = os.WriteFile(item.name, item.raw, 0600); e != nil {
			l.close()
			t.Fatal(e)
		}
	}
	generation, e := nonce()
	if e != nil {
		l.close()
		t.Fatal(e)
	}
	plan, _, e := l.makeRestoredTLSPlan(context.Background(), request, generation, verified, now)
	if e != nil {
		l.close()
		t.Fatal(e)
	}
	if e = l.validateTLSProjectionPlan(context.Background(), plan); e != nil {
		l.close()
		t.Fatal("original private context", e)
	}
	return l, plan, d, w
}
func TestTLSProjectionPrivatePublicationDurableReopenAndNoReplay(t *testing.T) {
	l, plan := tlsProjectionPrivatePlanFixture(t)
	publish := func(p RestoredTLSPlan) error {
		return writeNew(l.root, tlsProjectionPlanFile(p.GenerationID), encode(p))
	}
	if e := l.publishTLSProjectionPlan(context.Background(), plan, false, publish); e != nil {
		l.close()
		t.Fatal(e)
	}
	c := l.config
	before, e := regular(l.root, journalName, c.MaximumJournalBytes)
	if e != nil {
		l.close()
		t.Fatal(e)
	}
	l.close()
	l, e = open(c, true)
	if e != nil {
		t.Fatal("durable private plan reopen", e)
	}
	defer l.close()
	// Cleanup/status must not depend on source credentials or current validity.
	for _, name := range []string{plan.Request.CertificateFile, plan.Request.PrivateKeyFile, plan.Request.TrustBundleFile} {
		if e = os.Remove(name); e != nil {
			t.Fatal(e)
		}
	}
	if e = l.publishTLSProjectionPlan(context.Background(), plan, false, nil); e != nil {
		t.Fatal("completed publication replay", e)
	}
	after, e := regular(l.root, journalName, c.MaximumJournalBytes)
	if e != nil || !bytes.Equal(before, after) {
		t.Fatal("completed publication changed journal", e)
	}
	actual, e := l.readTLSProjectionPlan(tlsProjectionPlanRecord(plan))
	if e != nil || !reflect.DeepEqual(actual, plan) {
		t.Fatal("source-independent private plan", e)
	}
	if _, ok := tlsProjectionProof(l.events); ok {
		t.Fatal("publication implied volume projection")
	}
}
func TestTLSProjectionPrivatePublicationUnknownCompleteAndAbsentRecovery(t *testing.T) {
	for _, written := range []bool{false, true} {
		t.Run(map[bool]string{false: "absent", true: "written"}[written], func(t *testing.T) {
			l, plan := tlsProjectionPrivatePlanFixture(t)
			if e := l.publishTLSProjectionPlan(context.Background(), plan, false, func(p RestoredTLSPlan) error {
				if written {
					if e := writeNew(l.root, tlsProjectionPlanFile(p.GenerationID), encode(p)); e != nil {
						return e
					}
				}
				return errors.New("SYNTHETIC_PUBLICATION_RESPONSE_LOST")
			}); e == nil {
				l.close()
				t.Fatal("lost result accepted")
			}
			c := l.config
			l.close()
			var e error
			l, e = open(c, true)
			if e != nil {
				t.Fatal("pending plan not observable", e)
			}
			defer l.close()
			calls := 0
			publish := func(p RestoredTLSPlan) error {
				calls++
				return writeNew(l.root, tlsProjectionPlanFile(p.GenerationID), encode(p))
			}
			if e = l.publishTLSProjectionPlan(context.Background(), plan, false, publish); e == nil || calls != 0 {
				t.Fatal("uncertain write resubmitted", e)
			}
			if e = l.publishTLSProjectionPlan(context.Background(), plan, true, publish); e != nil || calls != map[bool]int{true: 0, false: 1}[written] {
				t.Fatal("explicit same-digest reconstruction/readback", e, calls)
			}
		})
	}
}
func TestTLSProjectionPrivatePartialChangedAndUnrecordedFilesPreserved(t *testing.T) {
	for _, fault := range []string{"partial", "changed", "unrecorded"} {
		t.Run(fault, func(t *testing.T) {
			l, plan := tlsProjectionPrivatePlanFixture(t)
			name := tlsProjectionPlanFile(plan.GenerationID)
			value := []byte("{partial")
			if fault == "changed" {
				changed := plan
				changed.Request.Reason = "Changed private reason"
				value = encode(changed)
			}
			if fault == "unrecorded" {
				if e := writeNew(l.root, name, encode(plan)); e != nil {
					l.close()
					t.Fatal(e)
				}
				c := l.config
				l.close()
				opened, e := open(c, false)
				if e == nil {
					opened.close()
					t.Fatal("unrecorded TLS plan admitted")
				}
				return
			}
			if e := l.publishTLSProjectionPlan(context.Background(), plan, false, func(RestoredTLSPlan) error { return writeNew(l.root, name, value) }); e == nil {
				l.close()
				t.Fatal("partial/changed write accepted")
			}
			c := l.config
			l.close()
			var e error
			l, e = open(c, true)
			if e != nil {
				t.Fatal("first partial plan not preserved for explicit abort", e)
			}
			defer l.close()
			if e = l.publishTLSProjectionPlan(context.Background(), plan, true, func(RestoredTLSPlan) error { t.Fatal("partial/changed write overwritten"); return nil }); e == nil {
				t.Fatal("partial/changed file qualified")
			}
			actual, e := regular(l.root, name, c.Verification.MaximumMetadataBytes)
			if e != nil || !bytes.Equal(actual, value) {
				t.Fatal("uncertain bytes changed", e)
			}
		})
	}
}
func TestTLSProjectionPrivatePlanRefusesContextFileAndPayloadDrift(t *testing.T) {
	l, plan := tlsProjectionPrivatePlanFixture(t)
	defer l.close()
	for _, fault := range []string{"scope", "attempt", "restore-plan", "SQL-proof", "DB-proof", "store-proof", "descriptor", "runtime", "control-ID", "old-owner", "new-owner", "initializer", "UID", "GID", "old-payload", "payload", "certificate-name", "key-name", "hosts", "dates", "leaf", "trust", "missing-file", "added-file", "file-hash", "file-order", "reason"} {
		t.Run(fault, func(t *testing.T) {
			raw := encode(plan)
			var p RestoredTLSPlan
			if wire.StrictJSON(raw, &p) != nil {
				t.Fatal("fixture")
			}
			switch fault {
			case "scope":
				p.ScopeID = strings.Repeat("a", 32)
			case "attempt":
				p.RestoreAttemptID = strings.Repeat("a", 32)
			case "restore-plan":
				p.RestorePlanSHA256 = strings.Repeat("a", 64)
			case "SQL-proof":
				p.SQLProofSHA256 = strings.Repeat("a", 64)
			case "DB-proof":
				p.DatabaseProofSHA256 = strings.Repeat("a", 64)
			case "store-proof":
				p.StoreProofSHA256 = strings.Repeat("a", 64)
			case "descriptor":
				p.DescriptorSHA256 = strings.Repeat("a", 64)
			case "runtime":
				p.RuntimeSHA256 = strings.Repeat("a", 64)
			case "control-ID":
				p.PreviousControlID = strings.Repeat("a", 64)
			case "old-owner":
				p.PreviousOwner.OperationID = strings.Repeat("a", 32)
			case "new-owner":
				p.ConfigurationOwner.Role = "control-config"
			case "initializer":
				p.Initializer = "sha256:" + strings.Repeat("a", 64)
			case "UID":
				p.UID++
			case "GID":
				p.GID++
			case "old-payload":
				p.PreviousPayloadSHA256 = strings.Repeat("a", 64)
			case "payload":
				p.PayloadSHA256 = strings.Repeat("a", 64)
			case "certificate-name":
				p.CertificateName = "runtime.yaml"
			case "key-name":
				p.PrivateKeyName = "auth-hmac"
			case "hosts":
				p.Identity.Hosts = []string{"other.test"}
			case "dates":
				p.Identity.NotAfter = p.Identity.NotBefore
			case "leaf":
				p.Identity.LeafSHA256 = "invalid"
			case "trust":
				p.Identity.TrustBundleSHA256 = "invalid"
			case "missing-file":
				p.ConfigurationFiles = p.ConfigurationFiles[:len(p.ConfigurationFiles)-1]
			case "added-file":
				p.ConfigurationFiles = append(p.ConfigurationFiles, p.ConfigurationFiles[0])
			case "file-hash":
				p.ConfigurationFiles[0].SHA256 = strings.Repeat("a", 64)
			case "file-order":
				p.ConfigurationFiles[0], p.ConfigurationFiles[1] = p.ConfigurationFiles[1], p.ConfigurationFiles[0]
			case "reason":
				p.Request.Reason = ""
			}
			if e := l.validateTLSProjectionPlan(context.Background(), p); e == nil {
				t.Fatal("changed private context admitted")
			}
		})
	}
}

func TestTLSProjectionPrivateHistoricalExpiryDoesNotBlockPlanReadbackOrCleanupFacts(t *testing.T) {
	l, p := tlsProjectionPrivatePlanFixture(t, time.Now().Add(-72*time.Hour))
	defer l.close()
	after, e := time.Parse(time.RFC3339, p.Identity.NotAfter)
	if e != nil || !after.Before(time.Now()) {
		t.Fatal("expired historical fixture required")
	}
	if e = l.publishTLSProjectionPlan(context.Background(), p, false, func(p RestoredTLSPlan) error {
		return writeNew(l.root, tlsProjectionPlanFile(p.GenerationID), encode(p))
	}); e != nil {
		t.Fatal("historical publication observation", e)
	}
	if _, e = l.readTLSProjectionPlan(tlsProjectionPlanRecord(p)); e != nil {
		t.Fatal("expiry blocked immutable cleanup facts", e)
	}
}
