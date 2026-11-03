package scope

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"time"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/compose"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

type TLSProjectionResult struct {
	ScopeID        string `json:"scope_id"`
	GenerationID   string `json:"generation_id,omitempty"`
	Status         string `json:"status"`
	CompletedSteps int    `json:"completed_steps"`
	TotalSteps     int    `json:"total_steps"`
	Pending        bool   `json:"pending"`
	ProofSHA256    string `json:"proof_sha256,omitempty"`
	Qualification  string `json:"qualification"`
}

func (l *locked) tlsProjectionResult() TLSProjectionResult {
	s := tlsProjectionState(l.events)
	r := TLSProjectionResult{ScopeID: l.marker.ScopeID, Status: "no_TLS_projection_recorded", TotalSteps: len(tlsProjectionSteps()), Qualification: "TLS_configuration_preparation_only_not_control_replacement_activation_health_browser_or_target"}
	if s.record == nil {
		return r
	}
	r.GenerationID = s.record.GenerationID
	r.CompletedSteps = len(s.completed)
	r.Pending = true
	r.Status = "TLS_projection_incomplete"
	if s.pending != "" {
		r.Status = "TLS_projection_requires_reconciliation"
	}
	if s.aborting {
		r.Status = "TLS_projection_cleanup_pending"
	}
	if s.aborted {
		r.Status = "TLS_projection_aborted_original_configuration_preserved"
		r.Pending = false
	}
	if proof, ok := tlsProjectionProof(l.events); ok {
		r.ProofSHA256 = proof
		r.Pending = false
		r.Status = "TLS_configuration_prepared_replacement_activation_pending"
	}
	return r
}
func TLSProjectionCommand(mode string) bool {
	return mode == "prepare-restored-tls" || mode == "rotate-restored-tls" || mode == "reconcile-restored-tls" || mode == "abort-restored-tls"
}
func RunTLSProjection(ctx context.Context, c configuration.Config, r configuration.TLSGenerationRequest, mode string) (TLSProjectionResult, error) {
	if r.Validate() != nil || !TLSProjectionCommand(mode) {
		return TLSProjectionResult{}, failure("OFFLINE_SCOPE_TLS_PROJECTION_CONFIGURATION_REQUIRED")
	}
	if mode == "abort-restored-tls" {
		out, handled, e := abortTLSProjectionMetadata(ctx, c, r)
		if handled || e != nil {
			return out, e
		}
	}
	if c.Engine == nil || c.Engine.Utility == nil || c.Compose == nil {
		return TLSProjectionResult{}, failure("OFFLINE_SCOPE_TLS_PROJECTION_CONFIGURATION_REQUIRED")
	}
	client, e := engine.New(*c.Engine)
	if e != nil {
		return TLSProjectionResult{}, e
	}
	defer client.Close()
	worker, e := compose.New(c)
	if e != nil {
		return TLSProjectionResult{}, e
	}
	return runTLSProjection(ctx, c, r, mode, client, worker)
}
func tlsProjectionActorsIntended(events []Event, generation string) bool {
	return tlsProjectionIntentSeen(events, generation, "volume-create")
}
func abortTLSProjectionMetadata(ctx context.Context, c configuration.Config, request configuration.TLSGenerationRequest) (TLSProjectionResult, bool, error) {
	if ctx.Err() != nil {
		return TLSProjectionResult{}, true, failure("OFFLINE_SCOPE_OPERATION_INTERRUPTED")
	}
	l, e := open(c, true)
	if e != nil {
		return TLSProjectionResult{}, true, e
	}
	defer l.close()
	s := tlsProjectionState(l.events)
	if s.record == nil {
		return l.tlsProjectionResult(), true, failure("OFFLINE_SCOPE_TLS_PROJECTION_NOT_RECORDED")
	}
	if s.aborted {
		return l.tlsProjectionResult(), true, nil
	}
	if tlsProjectionActorsIntended(l.events, s.record.GenerationID) {
		return l.tlsProjectionResult(), false, nil
	}
	if p, e := l.readTLSProjectionPlan(*s.record); e == nil && !reflect.DeepEqual(p.Request, request) {
		return l.tlsProjectionResult(), true, failure("OFFLINE_SCOPE_TLS_PROJECTION_REQUEST_CHANGED")
	}
	out, e := l.finishTLSProjectionAbort(ctx, *s.record, nil)
	return out, true, e
}
func (l *locked) finishTLSProjectionAbort(ctx context.Context, r TLSProjectionRecord, cleanup func() error) (TLSProjectionResult, error) {
	if ctx.Err() != nil {
		return l.tlsProjectionResult(), failure("OFFLINE_SCOPE_OPERATION_INTERRUPTED")
	}
	if cleanup == nil && tlsProjectionActorsIntended(l.events, r.GenerationID) {
		return l.tlsProjectionResult(), failure("OFFLINE_SCOPE_TLS_PROJECTION_GENERATED_CLEANUP_REQUIRED")
	}
	s := tlsProjectionState(l.events)
	if s.aborted {
		return l.tlsProjectionResult(), nil
	}
	if e := admitProcessJournal(l, 2); e != nil {
		return l.tlsProjectionResult(), e
	}
	r.ObjectID = ""
	id, reference := s.pending, s.abortReference
	if !s.aborting {
		var e error
		id, e = nonce()
		if e != nil {
			return l.tlsProjectionResult(), e
		}
		reference = s.pending
		if e = l.appendEvent(Event{OperationID: id, ReferenceOperationID: reference, Action: "tls_projection_abort", Outcome: "started", TLSProjection: &r}); e != nil {
			return l.tlsProjectionResult(), e
		}
	}
	var cleanupError error
	if cleanup != nil {
		cleanupError = cleanup()
	}
	if cleanupError == nil && r.PayloadCustody {
		p, pe := l.readTLSProjectionPlan(r)
		if pe != nil {
			if _, exists := l.root.Lstat(generationPayloadDirectory(r.GenerationID)); !os.IsNotExist(exists) {
				cleanupError = failure("OFFLINE_SCOPE_GENERATION_PAYLOAD_CLEANUP_PLAN_REQUIRED")
			}
		} else {
			cleanupError = l.removeGenerationPayload(ctx, p)
		}
	}
	outcome, code := "completed", ""
	if cleanupError != nil {
		outcome = "uncertain"
		code = "OFFLINE_SCOPE_TLS_PROJECTION_CLEANUP_UNRESOLVED"
	}
	if e := l.appendEvent(Event{OperationID: id, ReferenceOperationID: reference, Action: "tls_projection_abort", Outcome: outcome, Code: code, TLSProjection: &r}); e != nil {
		return l.tlsProjectionResult(), e
	}
	return l.tlsProjectionResult(), cleanupError
}
func (l *locked) abortTLSProjection(ctx context.Context, c tlsProjectionEngine, p RestoredTLSPlan) (TLSProjectionResult, error) {
	if _, e := l.checkEngine(ctx, c); e != nil {
		return l.tlsProjectionResult(), e
	}
	return l.finishTLSProjectionAbort(ctx, *tlsProjectionState(l.events).record, func() error { return l.removeTLSProjectionActors(ctx, c, p) })
}
func (l *locked) currentTLSProjectionInput(ctx context.Context, c tlsProjectionEngine, w runtimeOperator, request configuration.TLSGenerationRequest, generation string) (RestoredTLSPlan, []engine.ConfigurationFile, error) {
	var p RestoredTLSPlan
	var published *RestoredTLSPlan
	s := tlsProjectionState(l.events)
	var view runtimeEngine = c
	if s.record != nil && s.record.GenerationID == generation && !s.aborted {
		existing, e := l.readTLSProjectionPlan(*s.record)
		if e == nil {
			published = &existing
			view, e = l.tlsProjectionRuntimeView(ctx, c, existing)
			if e != nil {
				return p, nil, e
			}
		} else if _, published := s.completed[0]; published || tlsProjectionActorsIntended(l.events, generation) {
			return p, nil, e
		}
	}
	verified, e := l.verifyRuntimeContext(ctx, view, w, false)
	if e != nil {
		return p, nil, e
	}
	if e = l.verifyRotationProcess(ctx, view); e != nil {
		return p, nil, e
	}
	if published != nil && published.FormatVersion == 2 {
		if !reflect.DeepEqual(published.Request, request) {
			return p, nil, failure("OFFLINE_SCOPE_TLS_PROJECTION_REQUEST_CHANGED")
		}
		files, _, e := l.readGenerationPayload(ctx, *published)
		return *published, files, e
	}
	if request.FormatVersion == 2 {
		verified.previousTLSFiles, e = l.readSelectedTLSPredecessor(ctx, c, generation, request.Target, verified)
		if e != nil {
			return p, nil, e
		}
	}
	return l.makeRestoredTLSPlan(ctx, request, generation, verified, time.Now())
}

func (l *locked) publishTLSPlanSource(ctx context.Context, p RestoredTLSPlan, reconcile bool, files []engine.ConfigurationFile) error {
	var trust []byte
	if p.FormatVersion == 2 && !reconcile {
		var e error
		trust, e = protectedInput(l.config, p.Request.TrustBundleFile, l.config.Verification.MaximumMetadataBytes)
		if e != nil {
			return e
		}
		if wire.Digest(trust) != p.Identity.TrustBundleSHA256 {
			return failure("OFFLINE_SCOPE_SELECTED_TLS_TRUST_CHANGED")
		}
	}
	return l.publishTLSProjectionPlanWithCustody(ctx, p, reconcile, func(p RestoredTLSPlan) error {
		return writeNew(l.root, tlsProjectionPlanFile(p.GenerationID), encode(p))
	}, files, trust)
}
func runTLSProjection(ctx context.Context, c configuration.Config, request configuration.TLSGenerationRequest, mode string, client tlsProjectionEngine, worker runtimeOperator) (out TLSProjectionResult, err error) {
	if request.Validate() != nil || !TLSProjectionCommand(mode) {
		return out, failure("OFFLINE_SCOPE_TLS_PROJECTION_CONFIGURATION_REQUIRED")
	}
	if ctx.Err() != nil {
		return out, failure("OFFLINE_SCOPE_OPERATION_INTERRUPTED")
	}
	l, e := open(c, true)
	if e != nil {
		return out, e
	}
	defer l.close()
	s := tlsProjectionState(l.events)
	if mode == "rotate-restored-tls" {
		if s.record == nil {
			return l.tlsProjectionResult(), failure("OFFLINE_SCOPE_TLS_ROTATION_COMPLETED_REPLACEMENT_REQUIRED")
		}
		if s.complete {
			previous, e := l.readTLSProjectionPlan(*s.record)
			if e != nil {
				return l.tlsProjectionResult(), e
			}
			if reflect.DeepEqual(previous.Request, request) {
				return l.tlsProjectionResult(), nil
			}
			if !rotationReady(l.events) {
				return l.tlsProjectionResult(), failure("OFFLINE_SCOPE_TLS_ROTATION_COMPLETED_REPLACEMENT_REQUIRED")
			}
			s = tlsProjectionRecords{completed: map[int]TLSProjectionRecord{}}
		}
		mode = "prepare-restored-tls"
	}
	if mode == "abort-restored-tls" {
		for _, event := range l.events {
			if s.record != nil && event.ControlReplacement != nil && event.ControlReplacement.GenerationID == s.record.GenerationID {
				return l.tlsProjectionResult(), failure("OFFLINE_SCOPE_TLS_ROTATION_CONSUMED_GENERATION_REQUIRED")
			}
		}
		if s.record == nil {
			return l.tlsProjectionResult(), failure("OFFLINE_SCOPE_TLS_PROJECTION_NOT_RECORDED")
		}
		if s.aborted {
			return l.tlsProjectionResult(), nil
		}
		if !tlsProjectionActorsIntended(l.events, s.record.GenerationID) {
			return l.finishTLSProjectionAbort(ctx, *s.record, nil)
		}
		plan, e := l.readTLSProjectionPlan(*s.record)
		if e != nil {
			return l.tlsProjectionResult(), e
		}
		if !reflect.DeepEqual(plan.Request, request) {
			return l.tlsProjectionResult(), failure("OFFLINE_SCOPE_TLS_PROJECTION_REQUEST_CHANGED")
		}
		return l.abortTLSProjection(ctx, client, plan)
	}
	if s.aborting {
		return l.tlsProjectionResult(), failure("OFFLINE_SCOPE_TLS_PROJECTION_CLEANUP_REQUIRED")
	}
	if s.pending != "" && mode != "reconcile-restored-tls" {
		return l.tlsProjectionResult(), failure("OFFLINE_SCOPE_TLS_PROJECTION_RECONCILIATION_REQUIRED")
	}
	if (s.record == nil || s.aborted) && mode != "prepare-restored-tls" {
		return l.tlsProjectionResult(), failure("OFFLINE_SCOPE_TLS_PROJECTION_NOT_RECORDED")
	}
	if s.record != nil && !s.aborted && s.complete {
		plan, e := l.readTLSProjectionPlan(*s.record)
		if e != nil {
			return l.tlsProjectionResult(), e
		}
		if !reflect.DeepEqual(plan.Request, request) {
			return l.tlsProjectionResult(), failure("OFFLINE_SCOPE_TLS_PROJECTION_REQUEST_CHANGED")
		}
		return l.tlsProjectionResult(), nil
	}
	var plan RestoredTLSPlan
	var publicationFiles []engine.ConfigurationFile
	if s.record == nil || s.aborted {
		generation, e := nonce()
		if e != nil {
			return l.tlsProjectionResult(), e
		}
		plan, publicationFiles, e = l.currentTLSProjectionInput(ctx, client, worker, request, generation)
		if e != nil {
			return l.tlsProjectionResult(), e
		}
		if e = l.publishTLSPlanSource(ctx, plan, false, publicationFiles); e != nil {
			return l.tlsProjectionResult(), e
		}
	} else if _, published := s.completed[0]; !published {
		plan, e = l.readTLSProjectionPlan(*s.record)
		if e != nil {
			if _, exists := l.root.Lstat(tlsProjectionPlanFile(s.record.GenerationID)); !os.IsNotExist(exists) {
				return l.tlsProjectionResult(), e
			}
			plan, publicationFiles, e = l.currentTLSProjectionInput(ctx, client, worker, request, s.record.GenerationID)
			if e != nil {
				return l.tlsProjectionResult(), e
			}
		}
		if !reflect.DeepEqual(plan.Request, request) {
			return l.tlsProjectionResult(), failure("OFFLINE_SCOPE_TLS_PROJECTION_REQUEST_CHANGED")
		}
		if e = l.publishTLSPlanSource(ctx, plan, true, publicationFiles); e != nil {
			return l.tlsProjectionResult(), e
		}
	} else {
		plan, e = l.readTLSProjectionPlan(*s.record)
		if e != nil {
			return l.tlsProjectionResult(), e
		}
	}
	if !reflect.DeepEqual(plan.Request, request) {
		return l.tlsProjectionResult(), failure("OFFLINE_SCOPE_TLS_PROJECTION_REQUEST_CHANGED")
	}
	if e = admitProcessJournal(l, (len(tlsProjectionSteps())-len(tlsProjectionState(l.events).completed))*3+3); e != nil {
		return l.tlsProjectionResult(), e
	}
	defer func() {
		if ctx.Err() == nil {
			return
		}
		s := tlsProjectionState(l.events)
		if s.record == nil || s.complete || s.aborted {
			return
		}
		cleanup, done := context.WithTimeout(context.Background(), time.Duration(c.Compose.CommandSeconds)*time.Second)
		defer done()
		if _, e := l.abortTLSProjection(cleanup, client, plan); e != nil {
			err = failure("OFFLINE_SCOPE_TLS_PROJECTION_CANCELLATION_CLEANUP_UNRESOLVED")
		}
		out = l.tlsProjectionResult()
	}()
	base := tlsProjectionPlanRecord(plan)
	for index, phase := range tlsProjectionSteps() {
		s = tlsProjectionState(l.events)
		if _, done := s.completed[index]; done {
			continue
		}
		if index == 0 {
			return l.tlsProjectionResult(), failure("OFFLINE_SCOPE_TLS_PROJECTION_PUBLICATION_REQUIRED")
		}
		current, files, e := l.currentTLSProjectionInput(ctx, client, worker, request, plan.GenerationID)
		if e != nil {
			return l.tlsProjectionResult(), e
		}
		if !reflect.DeepEqual(current, plan) {
			return l.tlsProjectionResult(), failure("OFFLINE_SCOPE_TLS_PROJECTION_CONTEXT_CHANGED")
		}
		if e = client.ValidateConfigurationFiles(files, plan.UID, plan.GID); e != nil {
			return l.tlsProjectionResult(), e
		}
		var bytes int64
		for _, f := range files {
			bytes += int64(len(f.Contents))
		}
		if e = backupDiskAdmission(filepath.Join(c.ScopeRoot, tlsProjectionPlanFile(plan.GenerationID)), bytes*2+l.maximumJournalBytes()); e != nil {
			return l.tlsProjectionResult(), failure("OFFLINE_SCOPE_TLS_PROJECTION_DISK_HEADROOM_REQUIRED")
		}
		r := base
		r.Step = index
		r.Phase = phase
		r.ObjectID = ""
		id, e := nonce()
		if e != nil {
			return l.tlsProjectionResult(), e
		}
		action, reference := "tls_projection_effect", ""
		reconcile := s.pending != ""
		if reconcile {
			if mode != "reconcile-restored-tls" || s.record.Step != index {
				return l.tlsProjectionResult(), failure("OFFLINE_SCOPE_TLS_PROJECTION_RECONCILIATION_REQUIRED")
			}
			action = "tls_projection_reconcile"
			reference = s.pending
		} else {
			if phase == "volume-create" || phase == "utility-create" || phase == "payload-write" {
				if e = l.admitSelectedTLSCurrent(ctx, plan, time.Now()); e != nil {
					return l.tlsProjectionResult(), e
				}
			}
			if e = l.appendEvent(Event{OperationID: id, Action: action, Outcome: "started", TLSProjection: &r}); e != nil {
				return l.tlsProjectionResult(), e
			}
		}
		observed, e := executeTLSProjectionStep(ctx, client, plan, r, files, reconcile)
		if _, binding := l.checkEngine(ctx, client); binding != nil {
			e = binding
		}
		outcome, code := "completed", ""
		if e != nil {
			observed = r
			outcome = "uncertain"
			if reconcile {
				outcome = "unresolved"
			}
			code = "OFFLINE_SCOPE_TLS_PROJECTION_EFFECT_UNRESOLVED"
		}
		if journalError := l.appendEvent(Event{OperationID: id, ReferenceOperationID: reference, Action: action, Outcome: outcome, Code: code, TLSProjection: &observed}); journalError != nil {
			return l.tlsProjectionResult(), journalError
		}
		if e != nil {
			return l.tlsProjectionResult(), e
		}
	}
	return l.tlsProjectionResult(), nil
}
