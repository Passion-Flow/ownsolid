package scope

import (
	"context"
	"path"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/compose"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/tlsidentity"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// RestoredTLSPlan contains references and digests only. It does not authorize serving.
type RestoredTLSPlan struct {
	ConfigurationFiles    []InputFile                        `json:"configuration_files"`
	FormatVersion         int                                `json:"format_version"`
	GenerationID          string                             `json:"generation_id"`
	ScopeID               string                             `json:"scope_id"`
	RestoreAttemptID      string                             `json:"restore_attempt_id"`
	RestorePlanSHA256     string                             `json:"restore_plan_sha256"`
	SQLProofSHA256        string                             `json:"sql_proof_sha256"`
	DatabaseProofSHA256   string                             `json:"database_proof_sha256"`
	StoreProofSHA256      string                             `json:"store_proof_sha256"`
	DescriptorSHA256      string                             `json:"descriptor_sha256"`
	RuntimeSHA256         string                             `json:"runtime_sha256"`
	PreviousControlID     string                             `json:"previous_control_id"`
	PreviousOwner         engine.Ownership                   `json:"previous_owner"`
	ConfigurationOwner    engine.Ownership                   `json:"configuration_owner"`
	Initializer           string                             `json:"initializer"`
	UID                   int                                `json:"uid"`
	GID                   int                                `json:"gid"`
	PreviousPayloadSHA256 string                             `json:"previous_payload_sha256"`
	PayloadSHA256         string                             `json:"payload_sha256"`
	CertificateName       string                             `json:"certificate_name"`
	PrivateKeyName        string                             `json:"private_key_name"`
	Request               configuration.TLSGenerationRequest `json:"request"`
	Identity              tlsidentity.Identity               `json:"identity"`
}
type RestoredTLSInspection struct {
	Status        string          `json:"status"`
	Plan          RestoredTLSPlan `json:"plan"`
	PlanSHA256    string          `json:"plan_sha256"`
	Qualification string          `json:"qualification"`
}

func InspectRestoredTLS(ctx context.Context, c configuration.Config, r configuration.TLSGenerationRequest) (RestoredTLSInspection, error) {
	var out RestoredTLSInspection
	if r.Validate() != nil || c.Engine == nil || c.Compose == nil {
		return out, failure("OFFLINE_SCOPE_RESTORED_TLS_CONFIGURATION_REQUIRED")
	}
	client, e := engine.New(*c.Engine)
	if e != nil {
		return out, e
	}
	defer client.Close()
	worker, e := compose.New(c)
	if e != nil {
		return out, e
	}
	l, e := open(c, false)
	if e != nil {
		return out, e
	}
	defer l.close()
	verified, e := l.verifyRuntimeContext(ctx, client, worker, false)
	if e != nil {
		return out, e
	}
	if e = l.verifyRotationProcess(ctx, client); e != nil {
		return out, e
	}
	generation, e := nonce()
	if e != nil {
		return out, e
	}
	if r.FormatVersion == 2 {
		verified.previousTLSFiles, e = l.readSelectedTLSPredecessor(ctx, client, generation, r.Target, verified)
		if e != nil {
			return out, e
		}
	}
	plan, _, e := l.makeRestoredTLSPlan(ctx, r, generation, verified, time.Now())
	if e != nil {
		return out, e
	}
	return RestoredTLSInspection{Status: "validated_proposed_restored_TLS_generation_no_effects", Plan: plan, PlanSHA256: wire.Digest(encode(plan)), Qualification: "readonly_TLS_plan_only_not_projection_replacement_activation_browser_or_target"}, nil
}
func (l *locked) makeRestoredTLSPlan(ctx context.Context, r configuration.TLSGenerationRequest, generation string, v verifiedRuntimeContext, now time.Time) (RestoredTLSPlan, []engine.ConfigurationFile, error) {
	var out RestoredTLSPlan
	if ctx.Err() != nil {
		return out, nil, failure("OFFLINE_SCOPE_OPERATION_INTERRUPTED")
	}
	if r.Validate() != nil || !identityPattern.MatchString(generation) {
		return out, nil, failure("OFFLINE_SCOPE_RESTORED_TLS_CONFIGURATION_REQUIRED")
	}
	state := tlsProjectionState(l.events)
	for _, e := range l.events {
		if e.TLSProjection != nil && e.TLSProjection.GenerationID == generation && (state.record == nil || state.record.GenerationID != generation || state.aborted) {
			return out, nil, failure("OFFLINE_SCOPE_RESTORED_TLS_UNUSED_GENERATION_REQUIRED")
		}
		if e.OperationID == generation || e.Volume != nil && e.Volume.ResourceOperationID == generation {
			return out, nil, failure("OFFLINE_SCOPE_RESTORED_TLS_UNUSED_GENERATION_REQUIRED")
		}
	}
	sql, sqlOK := restoreSQLProof(l.events)
	database, dbOK := restoreDatabaseProof(l.events)
	store, storeOK := restoreStoresProof(l.events)
	if !sqlOK || !dbOK || !storeOK {
		return out, nil, failure("OFFLINE_SCOPE_RESTORED_TLS_COMPLETED_RESTORE_STAGES_REQUIRED")
	}
	restore, e := l.readBoundRestorePlan()
	if e != nil {
		return out, nil, e
	}
	if generation == restore.AttemptID || generation == restoreStoresState(l.events).record.GenerationID {
		return out, nil, failure("OFFLINE_SCOPE_RESTORED_TLS_UNUSED_GENERATION_REQUIRED")
	}
	if _, e = l.readRestoreStoresPlan(); e != nil {
		return out, nil, e
	}
	runtime := runtimeState(l.events)
	effective := effectiveRuntimeState(l.events)
	if processState(l.events).pending != "" || !rotationChecksQuiescent(l.events) || runtime.pending != "" || runtime.record == nil || effective.record == nil || !runtime.completed || !reflect.DeepEqual(effective.record, v.creation.record) || restore.Target.RuntimeSHA256 != wire.Digest(encode(*runtime.record)) || restore.Target.DescriptorSHA256 != wire.Digest(encode(v.inputs.descriptor)) || !runtimeIDsMatch(*effective.record, v.observed) || v.observed.Status != "observed_complete_compose_resources" {
		return out, nil, failure("OFFLINE_SCOPE_RESTORED_TLS_RUNTIME_CONTEXT_CHANGED")
	}
	for _, s := range v.observed.Services {
		if s.State != "created" && s.State != "exited" || s.ExitCode != 0 {
			return out, nil, failure("OFFLINE_SCOPE_RESTORED_TLS_STOPPED_RUNTIME_REQUIRED")
		}
	}
	base, model, e := l.replacementModels(ctx)
	if e != nil {
		return out, nil, e
	}
	original, ok := volumeState(l.events).roles["control-config"]
	uid, gid, e := roleUser(model, "control-config")
	if !ok || original.Phase != phasePrepared || e != nil || original.UID != uid || original.GID != gid {
		return out, nil, failure("OFFLINE_SCOPE_RESTORED_TLS_CONFIGURATION_OWNER_CHANGED")
	}
	certificate, e := protectedInput(l.config, r.CertificateFile, l.config.Verification.MaximumMetadataBytes)
	if e != nil {
		return out, nil, e
	}
	key, e := protectedInput(l.config, r.PrivateKeyFile, l.config.Verification.MaximumMetadataBytes)
	if e != nil {
		return out, nil, e
	}
	trust, e := protectedInput(l.config, r.TrustBundleFile, l.config.Verification.MaximumMetadataBytes)
	if e != nil {
		return out, nil, e
	}
	var files []engine.ConfigurationFile
	var identity tlsidentity.Identity
	var certName, keyName string
	if r.FormatVersion == 2 {
		previous, pe := l.previousSelectedTLSPlan(generation)
		if pe != nil {
			return out, nil, pe
		}
		if len(v.previousTLSFiles) == 0 || restoreConfigurationDigest(v.previousTLSFiles) != previous.PayloadSHA256 {
			return out, nil, failure("OFFLINE_SCOPE_SELECTED_TLS_PRIVATE_READBACK_REQUIRED")
		}
		base.files = map[string][]engine.ConfigurationFile{"control-config": v.previousTLSFiles}
		files, identity, certName, keyName, e = projectSelectedRestoredTLS(base, model, r.Target, certificate, key, trust, now)
	} else {
		files, identity, certName, keyName, e = projectRestoredTLS(base, model, certificate, key, trust, now)
	}
	if e != nil {
		return out, nil, e
	}
	previousControl, previousOwner, previousPayload, e := l.previousTLSConfiguration(ctx, generation)
	if e != nil || previousPayload == restoreConfigurationDigest(files) {
		return out, nil, failure("OFFLINE_SCOPE_TLS_ROTATION_CHANGED_PAYLOAD_REQUIRED")
	}
	out = RestoredTLSPlan{FormatVersion: r.FormatVersion, GenerationID: generation, ScopeID: l.marker.ScopeID, RestoreAttemptID: restore.AttemptID, RestorePlanSHA256: wire.Digest(encode(restore)), SQLProofSHA256: sql, DatabaseProofSHA256: database, StoreProofSHA256: store, DescriptorSHA256: restore.Target.DescriptorSHA256, RuntimeSHA256: restore.Target.RuntimeSHA256, PreviousControlID: previousControl, PreviousOwner: previousOwner, ConfigurationOwner: engine.Ownership{ScopeID: l.marker.ScopeID, OperationID: generation, Role: engine.TLSConfigurationRole + "-" + generation}, Initializer: v.inputs.descriptor.ImageID, UID: uid, GID: gid, PreviousPayloadSHA256: previousPayload, PayloadSHA256: restoreConfigurationDigest(files), CertificateName: certName, PrivateKeyName: keyName, Request: r, Identity: identity}
	for _, f := range files {
		out.ConfigurationFiles = append(out.ConfigurationFiles, InputFile{Name: f.Name, Bytes: int64(len(f.Contents)), SHA256: wire.Digest(f.Contents)})
	}
	return out, files, nil
}
func projectRestoredTLS(p preparedInputs, model compose.Model, certificate, key, trust []byte, now time.Time) ([]engine.ConfigurationFile, tlsidentity.Identity, string, string, error) {
	bad := func() ([]engine.ConfigurationFile, tlsidentity.Identity, string, string, error) {
		return nil, tlsidentity.Identity{}, "", "", failure("OFFLINE_SCOPE_RESTORED_TLS_INPUT_INVALID")
	}
	var envelope struct {
		Server struct {
			PortalOrigin    string `json:"portal_origin"`
			AdminOrigin     string `json:"admin_origin"`
			CertificateFile string `json:"tls_certificate_file"`
			PrivateKeyFile  string `json:"tls_private_key_file"`
		} `json:"server"`
	}
	raw, e := restoreNamedFile(p.files["control-config"], "runtime.yaml")
	if e != nil || wire.UniqueJSON(raw, &envelope) != nil {
		return bad()
	}
	certRef, keyRef := envelope.Server.CertificateFile, envelope.Server.PrivateKeyFile
	if certRef == keyRef || path.Base(certRef) == "runtime.yaml" || path.Base(keyRef) == "runtime.yaml" {
		return bad()
	}
	var other restoreConfigurationEnvelope
	if wire.UniqueJSON(raw, &other) != nil {
		return bad()
	}
	for _, reference := range []string{other.Database.DSNFile, other.Security.AuthHMACKeyFile, other.Security.MailKeyringFile, other.SSO.KeyringFile} {
		if reference != "" && (reference == certRef || reference == keyRef) {
			return bad()
		}
	}
	for _, reference := range []string{certRef, keyRef} {
		if _, e = restorePayloadAt(p, model, "control-config", reference); e != nil {
			return bad()
		}
	}
	identity, e := tlsidentity.Validate(certificate, key, trust, []string{envelope.Server.PortalOrigin, envelope.Server.AdminOrigin}, now)
	if e != nil {
		return nil, tlsidentity.Identity{}, "", "", e
	}
	certName, keyName := path.Base(certRef), path.Base(keyRef)
	previous, e := restoreNamedFile(p.files["control-config"], certName)
	if e != nil || wire.Digest(previous) == identity.CertificateSHA256 {
		return bad()
	}
	seen := map[string]bool{}
	files := []engine.ConfigurationFile{}
	for _, f := range p.files["control-config"] {
		if !wire.SafePath(f.Name) || path.Base(f.Name) != f.Name || strings.ContainsAny(f.Name, "\\\x00") || seen[f.Name] || len(f.Contents) == 0 {
			return bad()
		}
		seen[f.Name] = true
		contents := f.Contents
		if f.Name == certName {
			contents = certificate
		}
		if f.Name == keyName {
			contents = key
		}
		files = append(files, engine.ConfigurationFile{Name: f.Name, Contents: append([]byte{}, contents...)})
	}
	slices.SortFunc(files, func(a, b engine.ConfigurationFile) int { return strings.Compare(a.Name, b.Name) })
	return files, identity, certName, keyName, nil
}
