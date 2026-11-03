package postgresbackup

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	release "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// InitializeRestoreTarget lends no new SQL authority. The selected original pool
// and exact installation-lock lease remain owned by its caller. Only one fixed
// database comment can be changed, never a database/role/schema or source store.
func InitializeRestoreTarget(ctx context.Context, pool *pgxpool.Pool, lease *pgxpool.Conn, operator config.Config, operatorPath, operatorEnvironment, requestPath, policyEnvironment string, r release.PostgreSQLRestoreTargetInitializationRequest) (release.PostgreSQLRestoreTargetInitializationObservation, error) {
	return operateRestoreTargetInitialization(ctx, pool, lease, operator, operatorPath, operatorEnvironment, requestPath, policyEnvironment, r, false)
}

// InspectRestoreTargetInitialization never creates/repairs a marker or a missing
// journal/completion. Paired zero SQL pins permit only uninitialized discovery.
func InspectRestoreTargetInitialization(ctx context.Context, pool *pgxpool.Pool, lease *pgxpool.Conn, operator config.Config, operatorPath, operatorEnvironment, requestPath, policyEnvironment string, r release.PostgreSQLRestoreTargetInitializationRequest) (release.PostgreSQLRestoreTargetInitializationObservation, error) {
	return operateRestoreTargetInitialization(ctx, pool, lease, operator, operatorPath, operatorEnvironment, requestPath, policyEnvironment, r, true)
}

func operateRestoreTargetInitialization(ctx context.Context, pool *pgxpool.Pool, lease *pgxpool.Conn, operator config.Config, operatorPath, operatorEnvironment, requestPath, policyEnvironment string, r release.PostgreSQLRestoreTargetInitializationRequest, inspection bool) (out release.PostgreSQLRestoreTargetInitializationObservation, result error) {
	if ctx == nil || pool == nil || lease == nil || !r.ValidForInspection() || !inspection && !r.Valid() {
		return out, errors.New("POSTGRES_RESTORE_TARGET_INITIALIZATION_CONTEXT_REQUIRED")
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(r.Policy.OperationSeconds)*time.Second)
	defer cancel()
	if e := AdmitRestoreTargetInitialization(ctx, operator, operatorPath, operatorEnvironment, requestPath, policyEnvironment, r, inspection); e != nil {
		return out, e
	}
	pc, lc := pool.Config().ConnConfig, lease.Conn().Config()
	if pc.Host != lc.Host || pc.Port != lc.Port || pc.Database != lc.Database || pc.User != lc.User || pc.Password != lc.Password {
		return out, errors.New("POSTGRES_RESTORE_TARGET_INITIALIZATION_SESSION_MISMATCH")
	}
	inputs, e := captureTargetInitializationInputs(ctx, operator, operatorPath, operatorEnvironment, requestPath, policyEnvironment, r, lc)
	if e != nil {
		return out, e
	}
	check := func() error {
		current, e := captureTargetInitializationInputs(ctx, operator, operatorPath, operatorEnvironment, requestPath, policyEnvironment, r, lc)
		if e != nil || current.sha256 != inputs.sha256 {
			return errors.New("POSTGRES_RESTORE_TARGET_INITIALIZATION_INPUT_CHANGED")
		}
		return nil
	}
	result = withNativeReferenceRecords(ctx, inputs.records, func() error {
		var e error
		session := selectedTargetInitializationSession{lease}
		if inspection {
			out, e = inspectRestoreTargetSession(ctx, session, r, inputs.sha256, check)
		} else {
			out, e = initializeRestoreTargetSession(ctx, session, r, inputs.sha256, check)
		}
		return e
	})
	if result != nil {
		out = release.PostgreSQLRestoreTargetInitializationObservation{}
	}
	return out, result
}

func initializationObservation(r release.PostgreSQLRestoreTargetInitializationRequest, target release.PostgreSQLRestoreTargetInitializationIdentity, readonly bool, intentSHA, completeSHA string) (release.PostgreSQLRestoreTargetInitializationObservation, error) {
	o := release.PostgreSQLRestoreTargetInitializationObservation{Format: release.PostgreSQLRestoreTargetObservationFormat, RequestSHA256: r.SHA256(), InitializationID: r.InitializationID, TargetScopeID: r.TargetScopeID, Binding: r.Binding, Target: target, TargetBindingSHA256: target.SHA256(), PristinePredicateSHA256: initializationPredicateSHA256(), IntentSHA256: intentSHA, CompletionSHA256: completeSHA, ReadOnly: readonly, IdentityPinned: r.IdentityPinned(), OriginalIntentPresent: intentSHA != "", OriginalCompletionPresent: completeSHA != "", EffectObserved: target.Marker == r.Marker(), CurrentLockHeld: false, Qualification: "Original PostgreSQL target marker/pristine observation only; no database creation, restore, runtime role, physical custody or signed-release authority. Missing original completion remains missing."}
	switch {
	case target.Marker == "" && intentSHA == "":
		o.State = "uninitialized"
	case target.Marker == "" && intentSHA != "" && completeSHA == "":
		o.State = "effect_not_observed"
	case target.Marker == r.Marker() && intentSHA != "":
		o.State = "initialized"
	default:
		return release.PostgreSQLRestoreTargetInitializationObservation{}, errors.New("POSTGRES_RESTORE_TARGET_INITIALIZATION_MARKER_CONFLICT")
	}
	if !o.ValidFor(r, readonly) {
		return release.PostgreSQLRestoreTargetInitializationObservation{}, errors.New("POSTGRES_RESTORE_TARGET_INITIALIZATION_OBSERVATION_INVALID")
	}
	return o, nil
}

func closeInitializationTransaction(ctx context.Context, tx targetInitializationTransaction, seconds int) error {
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Duration(seconds)*time.Second)
	defer cancel()
	if e := tx.Rollback(cleanup); e != nil && !errors.Is(e, pgx.ErrTxClosed) {
		return errors.New("POSTGRES_RESTORE_TARGET_TRANSACTION_CLOSE_UNCONFIRMED")
	}
	return nil
}

func initializeRestoreTargetSession(ctx context.Context, session targetInitializationSession, r release.PostgreSQLRestoreTargetInitializationRequest, inputs string, check func() error) (out release.PostgreSQLRestoreTargetInitializationObservation, result error) {
	if ctx == nil || session == nil || !r.Valid() || !release.ValidSHA256(inputs) || check == nil {
		return out, errors.New("POSTGRES_RESTORE_TARGET_INITIALIZATION_CONTEXT_REQUIRED")
	}
	pre, e := readTargetInitializationIdentity(ctx, session, r, false)
	if e != nil {
		return out, e
	}
	if pre.Marker != "" {
		return out, errors.New("POSTGRES_RESTORE_TARGET_INITIALIZATION_BLANK_MARKER_REQUIRED")
	}
	if e = check(); e != nil {
		return out, e
	}
	j, e := openTargetInitializationJournal(r.InitializationRoot, true)
	if e != nil {
		return out, e
	}
	defer func() {
		result = errors.Join(result, j.close())
		if result != nil {
			out = release.PostgreSQLRestoreTargetInitializationObservation{}
		}
	}()
	i := targetInitializationIntent{Format: "projectone.postgres.restore-target-intent.v1", Request: r, RequestSHA256: r.SHA256(), InputsSHA256: inputs, RecordRoot: j.identity, Preimage: pre, PristinePredicateSHA256: initializationPredicateSHA256(), CreatedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	if e = writeDocument(j.root, targetInitializationIntentName, i, r.Policy.MaximumRecordBytes); e != nil {
		return out, e
	}
	_, _, intentSHA, _, e := readTargetInitializationRecords(j, r, inputs)
	if e != nil {
		return out, e
	}
	result = withNativeReferenceRecords(ctx, []nativeReferenceRecord{{filepath.Join(r.InitializationRoot, targetInitializationIntentName), r.Policy.MaximumRecordBytes, false}}, func() (result error) {
		tx, e := session.begin(ctx, false)
		if e != nil {
			return errors.New("POSTGRES_RESTORE_TARGET_INITIALIZATION_TRANSACTION_UNAVAILABLE")
		}
		defer func() { result = errors.Join(result, closeInitializationTransaction(ctx, tx, r.Policy.CleanupSeconds)) }()
		current, e := readTargetInitializationIdentity(ctx, tx, r, false)
		if e != nil || current != pre {
			return errors.New("POSTGRES_RESTORE_TARGET_INITIALIZATION_PREIMAGE_CHANGED")
		}
		if e = check(); e != nil {
			return e
		}
		if e = j.check(); e != nil {
			return e
		}
		hasIntent, hasComplete, e := j.inventory()
		if e != nil || !hasIntent || hasComplete {
			return errors.New("POSTGRES_RESTORE_TARGET_INITIALIZATION_RECORDS_INVALID")
		}
		statement, e := initializationComment(r)
		if e != nil {
			return e
		}
		if _, e = tx.Exec(ctx, statement); e != nil {
			return errors.New("POSTGRES_RESTORE_TARGET_INITIALIZATION_EFFECT_FAILED")
		}
		post, e := readTargetInitializationIdentity(ctx, tx, r, false)
		expected := pre
		expected.Marker = r.Marker()
		if e != nil || post != expected {
			return errors.New("POSTGRES_RESTORE_TARGET_INITIALIZATION_POSTIMAGE_CHANGED")
		}
		if e = check(); e != nil {
			return e
		}
		if e = j.check(); e != nil {
			return e
		}
		if _, _, sha, complete, e := readTargetInitializationRecords(j, r, inputs); e != nil || sha != intentSHA || complete != "" {
			return errors.New("POSTGRES_RESTORE_TARGET_INITIALIZATION_RECORD_CHANGED")
		}
		// Never retry or reconcile this commit by another mutation. The original
		// durable intent stays available to a later readonly observer.
		if e = tx.Commit(ctx); e != nil {
			return errors.New("POSTGRES_RESTORE_TARGET_INITIALIZATION_COMMIT_UNKNOWN")
		}
		post, e = readTargetInitializationIdentity(ctx, session, r, false)
		if e != nil || post != expected {
			return errors.New("POSTGRES_RESTORE_TARGET_INITIALIZATION_READBACK_UNCONFIRMED")
		}
		if e = check(); e != nil {
			return e
		}
		if e = j.check(); e != nil {
			return e
		}
		complete := targetInitializationCompletion{Format: "projectone.postgres.restore-target-complete.v1", RequestSHA256: r.SHA256(), IntentSHA256: intentSHA, InputsSHA256: inputs, Target: post, TargetBindingSHA256: post.SHA256(), PristinePredicateSHA256: initializationPredicateSHA256(), CompletedAt: time.Now().UTC().Format(time.RFC3339Nano)}
		if e = writeDocument(j.root, targetInitializationCompleteName, complete, r.Policy.MaximumRecordBytes); e != nil {
			return e
		}
		_, _, actualIntent, completeSHA, e := readTargetInitializationRecords(j, r, inputs)
		if e != nil || actualIntent != intentSHA {
			return errors.New("POSTGRES_RESTORE_TARGET_INITIALIZATION_COMPLETION_UNCONFIRMED")
		}
		out, e = initializationObservation(r, post, false, intentSHA, completeSHA)
		return e
	})
	return out, result
}

func inspectRestoreTargetSession(ctx context.Context, session targetInitializationSession, r release.PostgreSQLRestoreTargetInitializationRequest, inputs string, check func() error) (out release.PostgreSQLRestoreTargetInitializationObservation, result error) {
	if ctx == nil || session == nil || !r.ValidForInspection() || !release.ValidSHA256(inputs) || check == nil {
		return out, errors.New("POSTGRES_RESTORE_TARGET_INITIALIZATION_CONTEXT_REQUIRED")
	}
	var journal *targetInitializationJournal
	_, e := os.Lstat(r.InitializationRoot)
	if e == nil {
		journal, e = openTargetInitializationJournal(r.InitializationRoot, false)
		if e != nil {
			return out, e
		}
		defer func() {
			result = errors.Join(result, journal.close())
			if result != nil {
				out = release.PostgreSQLRestoreTargetInitializationObservation{}
			}
		}()
	} else if !os.IsNotExist(e) {
		return out, errors.New("POSTGRES_RESTORE_TARGET_INITIALIZATION_JOURNAL_REFUSED")
	}
	var records []nativeReferenceRecord
	if journal != nil {
		records = []nativeReferenceRecord{{filepath.Join(r.InitializationRoot, targetInitializationIntentName), r.Policy.MaximumRecordBytes, false}, {filepath.Join(r.InitializationRoot, targetInitializationCompleteName), r.Policy.MaximumRecordBytes, true}}
	}
	result = withNativeReferenceRecords(ctx, records, func() (result error) {
		var i targetInitializationIntent
		var c targetInitializationCompletion
		var intentSHA, completeSHA string
		if journal != nil {
			var e error
			i, c, intentSHA, completeSHA, e = readTargetInitializationRecords(journal, r, inputs)
			if e != nil {
				return e
			}
		}
		tx, e := session.begin(ctx, true)
		if e != nil {
			return errors.New("POSTGRES_RESTORE_TARGET_INITIALIZATION_READONLY_UNAVAILABLE")
		}
		defer func() { result = errors.Join(result, closeInitializationTransaction(ctx, tx, r.Policy.CleanupSeconds)) }()
		target, e := readTargetInitializationIdentity(ctx, tx, r, true)
		if e != nil {
			return e
		}
		if journal != nil {
			expected := i.Preimage
			if target.Marker == r.Marker() {
				expected.Marker = r.Marker()
			}
			if expected.SHA256() != target.SHA256() || completeSHA != "" && c.Target.SHA256() != target.SHA256() {
				return errors.New("POSTGRES_RESTORE_TARGET_INITIALIZATION_TARGET_CHANGED")
			}
			if _, _, a, b, e := readTargetInitializationRecords(journal, r, inputs); e != nil || a != intentSHA || b != completeSHA {
				return errors.New("POSTGRES_RESTORE_TARGET_INITIALIZATION_RECORD_CHANGED")
			}
		} else {
			if _, e = os.Lstat(r.InitializationRoot); !os.IsNotExist(e) {
				return errors.New("POSTGRES_RESTORE_TARGET_INITIALIZATION_RECORD_CHANGED")
			}
		}
		if e = check(); e != nil {
			return e
		}
		out, e = initializationObservation(r, target, true, intentSHA, completeSHA)
		return e
	})
	if result != nil {
		out = release.PostgreSQLRestoreTargetInitializationObservation{}
	}
	return out, result
}
