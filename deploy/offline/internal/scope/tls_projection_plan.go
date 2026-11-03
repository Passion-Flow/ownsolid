package scope

import (
	"context"
	"os"
	"path"
	"reflect"
	"strings"
	"time"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/compose"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/tlsidentity"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

func tlsProjectionPlanFile(generation string) string { return "tls-generation-" + generation + ".json" }
func validTLSProjectionPlanName(name string) bool {
	generation := strings.TrimSuffix(strings.TrimPrefix(name, "tls-generation-"), ".json")
	return identityPattern.MatchString(generation) && name == tlsProjectionPlanFile(generation)
}
func tlsProjectionPlanRecord(p RestoredTLSPlan) TLSProjectionRecord {
	return TLSProjectionRecord{GenerationID: p.GenerationID, PlanSHA256: wire.Digest(encode(p)), StoreProofSHA256: p.StoreProofSHA256, DescriptorSHA256: p.DescriptorSHA256, RuntimeSHA256: p.RuntimeSHA256, Phase: tlsProjectionSteps()[0], PayloadCustody: p.FormatVersion == 2}
}

// This reopens immutable facts without rereading new credential references or
// requiring an unexpired leaf. Expiry must not prevent status or safe cleanup.
func (l *locked) validateTLSProjectionPlan(ctx context.Context, p RestoredTLSPlan) error {
	view := *l
	view.events = generationPrefix(l.events, p.GenerationID, false)
	return view.validateTLSProjectionPlanAtAdmission(ctx, p)
}
func (l *locked) validateTLSProjectionPlanAtAdmission(ctx context.Context, p RestoredTLSPlan) error {
	bad := func() error { return failure("OFFLINE_SCOPE_TLS_PROJECTION_PLAN_CONTEXT_CHANGED") }
	r := tlsProjectionPlanRecord(p)
	if ctx.Err() != nil {
		return failure("OFFLINE_SCOPE_OPERATION_INTERRUPTED")
	}
	if !validTLSProjectionRecord(&r, l.events) || (p.FormatVersion != 1 && p.FormatVersion != 2) || p.FormatVersion != p.Request.FormatVersion || p.ScopeID != l.marker.ScopeID || p.Request.Validate() != nil {
		return bad()
	}
	restore, e := l.readBoundRestorePlan()
	if e != nil {
		return e
	}
	sql, sqlOK := restoreSQLProof(l.events)
	database, dbOK := restoreDatabaseProof(l.events)
	if !sqlOK || !dbOK || p.SQLProofSHA256 != sql || p.DatabaseProofSHA256 != database || p.RestoreAttemptID != restore.AttemptID || p.RestorePlanSHA256 != wire.Digest(encode(restore)) {
		return bad()
	}
	input, outcome, _ := inputsState(l.events)
	if input == nil || outcome != "completed" {
		return bad()
	}
	snapshot, e := l.readInputs(ctx, *input)
	if e != nil {
		return e
	}
	model, e := compose.DecodeModel(snapshot.model, l.config.Verification.MaximumMetadataBytes)
	if e != nil {
		return e
	}
	role, ok := volumeState(l.events).roles["control-config"]
	uid, gid, e := roleUser(model, "control-config")
	previousControl, previousOwner, previousPayload, previousPlan, priorError := l.previousTLSConfigurationWithPlan(ctx, p.GenerationID)
	if !ok || role.Phase != phasePrepared || e != nil || priorError != nil || p.UID != uid || p.GID != gid || role.UID != uid || role.GID != gid || p.PreviousOwner != previousOwner || p.Initializer != snapshot.descriptor.ImageID || p.PreviousControlID != previousControl || p.ConfigurationOwner != (engine.Ownership{ScopeID: l.marker.ScopeID, OperationID: p.GenerationID, Role: engine.TLSConfigurationRole + "-" + p.GenerationID}) || p.PreviousPayloadSHA256 != previousPayload {
		return bad()
	}
	if p.FormatVersion == 2 {
		if previousPlan == nil {
			return bad()
		}
		return validateSelectedTLSMetadata(p, snapshot, model, *previousPlan, l.config.Verification.MaximumMetadataBytes)
	}
	var envelope struct {
		Server struct {
			PortalOrigin    string `json:"portal_origin"`
			AdminOrigin     string `json:"admin_origin"`
			CertificateFile string `json:"tls_certificate_file"`
			PrivateKeyFile  string `json:"tls_private_key_file"`
		} `json:"server"`
	}
	raw, e := restoreNamedFile(snapshot.files["control-config"], "runtime.yaml")
	if e != nil || wire.UniqueJSON(raw, &envelope) != nil {
		return bad()
	}
	certRef, keyRef := envelope.Server.CertificateFile, envelope.Server.PrivateKeyFile
	if certRef == keyRef || p.CertificateName != path.Base(certRef) || p.PrivateKeyName != path.Base(keyRef) || p.CertificateName == "runtime.yaml" || p.PrivateKeyName == "runtime.yaml" {
		return bad()
	}
	for _, ref := range []string{certRef, keyRef} {
		if _, e = restorePayloadAt(snapshot, model, "control-config", ref); e != nil {
			return bad()
		}
	}
	var other restoreConfigurationEnvelope
	if wire.UniqueJSON(raw, &other) != nil {
		return bad()
	}
	for _, ref := range []string{other.Database.DSNFile, other.Security.AuthHMACKeyFile, other.Security.MailKeyringFile, other.SSO.KeyringFile} {
		if ref != "" && (ref == certRef || ref == keyRef) {
			return bad()
		}
	}
	hosts, e := tlsidentity.OriginHosts([]string{envelope.Server.PortalOrigin, envelope.Server.AdminOrigin})
	if e != nil || !reflect.DeepEqual(hosts, p.Identity.Hosts) {
		return bad()
	}
	before, be := time.Parse(time.RFC3339, p.Identity.NotBefore)
	after, ae := time.Parse(time.RFC3339, p.Identity.NotAfter)
	if be != nil || ae != nil || !after.After(before) {
		return bad()
	}
	for _, hash := range []string{p.Identity.LeafSHA256, p.Identity.CertificateSHA256, p.Identity.PrivateKeySHA256, p.Identity.TrustBundleSHA256, p.PayloadSHA256} {
		if !wire.ValidSHA256(hash) {
			return bad()
		}
	}
	oldCertificate, _ := restoreNamedFile(snapshot.files["control-config"], p.CertificateName)
	if wire.Digest(oldCertificate) == p.Identity.CertificateSHA256 {
		return bad()
	}
	if len(p.ConfigurationFiles) != len(snapshot.files["control-config"]) || p.PayloadSHA256 == p.PreviousPayloadSHA256 || wire.Digest(encode(p.ConfigurationFiles)) != p.PayloadSHA256 {
		return bad()
	}
	original := map[string]engine.ConfigurationFile{}
	for _, f := range snapshot.files["control-config"] {
		original[f.Name] = f
	}
	previous := ""
	for _, f := range p.ConfigurationFiles {
		old, found := original[f.Name]
		if !found || f.Name <= previous || f.Bytes < 1 || f.Bytes > l.config.Verification.MaximumMetadataBytes || !wire.ValidSHA256(f.SHA256) {
			return bad()
		}
		previous = f.Name
		delete(original, f.Name)
		switch f.Name {
		case p.CertificateName:
			if f.SHA256 != p.Identity.CertificateSHA256 {
				return bad()
			}
		case p.PrivateKeyName:
			if f.SHA256 != p.Identity.PrivateKeySHA256 {
				return bad()
			}
		default:
			if f.Bytes != int64(len(old.Contents)) || f.SHA256 != wire.Digest(old.Contents) {
				return bad()
			}
		}
	}
	if len(original) != 0 {
		return bad()
	}
	return nil
}
func (l *locked) readTLSProjectionPlan(r TLSProjectionRecord) (RestoredTLSPlan, error) {
	var p RestoredTLSPlan
	raw, e := regular(l.root, tlsProjectionPlanFile(r.GenerationID), l.config.Verification.MaximumMetadataBytes)
	if e != nil || wire.Digest(raw) != r.PlanSHA256 || wire.StrictJSON(raw, &p) != nil || !tlsProjectionIdentity(tlsProjectionPlanRecord(p), r) {
		return p, failure("OFFLINE_SCOPE_TLS_PROJECTION_PLAN_UNAVAILABLE_OR_CHANGED")
	}
	if e = l.validateTLSProjectionPlan(context.Background(), p); e != nil {
		return p, e
	}
	return p, nil
}
func (l *locked) verifyTLSProjectionPlans() error {
	records := map[string]TLSProjectionRecord{}
	for _, e := range l.events {
		if e.TLSProjection != nil {
			records[e.TLSProjection.GenerationID] = *e.TLSProjection
		}
	}
	for generation, r := range records {
		if _, e := l.readTLSProjectionPlan(r); e != nil {
			// No SDK actor is admitted before the first publication completes. Keep a
			// missing/partial plan observable for explicit reconstruction or abort.
			completed := false
			actor := false
			for _, event := range l.events {
				if event.TLSProjection != nil && event.TLSProjection.GenerationID == generation && event.Action != "tls_projection_abort" {
					if event.TLSProjection.Step > 0 {
						actor = true
					}
					if event.TLSProjection.Step == 0 && event.Outcome == "completed" {
						completed = true
					}
				}
			}
			if completed || actor {
				return e
			}
		}
	}
	return nil
}
func (l *locked) publishTLSProjectionPlan(ctx context.Context, p RestoredTLSPlan, reconcile bool, publish func(RestoredTLSPlan) error) error {
	return l.publishTLSProjectionPlanWithCustody(ctx, p, reconcile, publish, nil, nil)
}

func (l *locked) publishTLSProjectionPlanWithCustody(ctx context.Context, p RestoredTLSPlan, reconcile bool, publish func(RestoredTLSPlan) error, files []engine.ConfigurationFile, trust []byte) error {
	if p.FormatVersion == 2 {
		if reconcile {
			if _, e := l.readTLSProjectionPlan(tlsProjectionPlanRecord(p)); e != nil {
				return e
			}
		} else if len(files) == 0 || len(trust) == 0 {
			return failure("OFFLINE_SCOPE_GENERATION_PAYLOAD_CONTEXT_INVALID")
		}
	}
	if e := l.validateTLSProjectionPlan(ctx, p); e != nil {
		return e
	}
	r := tlsProjectionPlanRecord(p)
	s := tlsProjectionState(l.events)
	if (s.aborted || rotationReady(l.events)) && s.record != nil && s.record.GenerationID != p.GenerationID {
		s = tlsProjectionRecords{completed: map[int]TLSProjectionRecord{}}
	}
	if s.record != nil && !tlsProjectionIdentity(*s.record, r) {
		return failure("OFFLINE_SCOPE_TLS_PROJECTION_CONTEXT_CHANGED")
	}
	if s.aborted || s.aborting {
		return failure("OFFLINE_SCOPE_TLS_PROJECTION_ABORTED")
	}
	if _, ok := s.completed[0]; ok {
		_, e := l.readTLSProjectionPlan(r)
		if e == nil && p.FormatVersion == 2 {
			_, _, e = l.readGenerationPayload(ctx, p)
		}
		return e
	}
	if s.record != nil && !reconcile {
		return failure("OFFLINE_SCOPE_TLS_PROJECTION_RECONCILIATION_REQUIRED")
	}
	if s.record == nil && reconcile {
		return failure("OFFLINE_SCOPE_TLS_PROJECTION_NOT_RECORDED")
	}
	if e := admitProcessJournal(l, 3); e != nil {
		return e
	}
	id, e := nonce()
	if e != nil {
		return e
	}
	action, reference := "tls_projection_effect", ""
	if reconcile {
		action = "tls_projection_reconcile"
		reference = s.pending
	} else if e = l.appendEvent(Event{OperationID: id, Action: action, Outcome: "started", TLSProjection: &r}); e != nil {
		return e
	}
	_, exists := l.root.Lstat(tlsProjectionPlanFile(p.GenerationID))
	if os.IsNotExist(exists) {
		if publish == nil {
			e = failure("OFFLINE_SCOPE_TLS_PROJECTION_PLAN_PUBLISHER_REQUIRED")
		} else {
			e = publish(p)
		}
	} else if exists != nil {
		e = failure("OFFLINE_SCOPE_TLS_PROJECTION_PLAN_UNAVAILABLE")
	}
	if e == nil {
		e = l.syncNamedRestorePlan(tlsProjectionPlanFile(p.GenerationID), encode(p))
	}
	if e == nil {
		_, e = l.readTLSProjectionPlan(r)
	}
	if e == nil && p.FormatVersion == 2 {
		if reconcile {
			e = l.syncGenerationPayload(ctx, p)
		} else {
			e = l.writeGenerationPayload(ctx, p, files, trust)
		}
	}
	outcome, code := "completed", ""
	if e != nil {
		outcome = "uncertain"
		if reconcile {
			outcome = "unresolved"
		}
		code = "OFFLINE_SCOPE_TLS_PROJECTION_PLAN_UNRESOLVED"
	}
	if journalError := l.appendEvent(Event{OperationID: id, ReferenceOperationID: reference, Action: action, Outcome: outcome, Code: code, TLSProjection: &r}); journalError != nil {
		return journalError
	}
	return e
}
