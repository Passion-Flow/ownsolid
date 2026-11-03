package postgresbackup

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"time"

	release "github.com/Passion-Flow/ownsolid/api/release/v1"
)

const targetInitializationIntentName = "intent.json"
const targetInitializationCompleteName = "complete.json"

type targetInitializationIntent struct {
	Format                  string                                                `json:"format"`
	Request                 release.PostgreSQLRestoreTargetInitializationRequest  `json:"request"`
	RequestSHA256           string                                                `json:"request_sha256"`
	InputsSHA256            string                                                `json:"inputs_sha256"`
	RecordRoot              RootIdentity                                          `json:"record_root"`
	Preimage                release.PostgreSQLRestoreTargetInitializationIdentity `json:"preimage"`
	PristinePredicateSHA256 string                                                `json:"pristine_predicate_sha256"`
	CreatedAt               string                                                `json:"created_at"`
}

type targetInitializationCompletion struct {
	Format                  string                                                `json:"format"`
	RequestSHA256           string                                                `json:"request_sha256"`
	IntentSHA256            string                                                `json:"intent_sha256"`
	InputsSHA256            string                                                `json:"inputs_sha256"`
	Target                  release.PostgreSQLRestoreTargetInitializationIdentity `json:"target"`
	TargetBindingSHA256     string                                                `json:"target_binding_sha256"`
	PristinePredicateSHA256 string                                                `json:"pristine_predicate_sha256"`
	CompletedAt             string                                                `json:"completed_at"`
}

type targetInitializationJournal struct {
	root     *os.Root
	lock     *os.File
	identity RootIdentity
}

func openTargetInitializationJournal(path string, create bool) (*targetInitializationJournal, error) {
	var root *os.Root
	var e error
	if create {
		root, e = newDirectory(path)
	} else {
		root, e = openPrivateRoot(path)
	}
	if e != nil {
		return nil, errors.New("POSTGRES_RESTORE_TARGET_INITIALIZATION_JOURNAL_REFUSED")
	}
	lock, e := root.Open(".")
	if e != nil {
		root.Close()
		return nil, errors.New("POSTGRES_RESTORE_TARGET_INITIALIZATION_LOCK_UNAVAILABLE")
	}
	j := &targetInitializationJournal{root: root, lock: lock}
	fail := func(e error) (*targetInitializationJournal, error) { return nil, errors.Join(e, j.close()) }
	if syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil {
		return fail(errors.New("POSTGRES_RESTORE_TARGET_INITIALIZATION_LOCK_UNAVAILABLE"))
	}
	info, e := lock.Stat()
	if e != nil {
		return fail(errors.New("POSTGRES_RESTORE_TARGET_INITIALIZATION_ROOT_CHANGED"))
	}
	j.identity, e = rootIdentity(path, info)
	if e != nil {
		return fail(e)
	}
	if e = j.check(); e != nil {
		return fail(e)
	}
	return j, nil
}

func (j *targetInitializationJournal) check() error {
	bad := errors.New("POSTGRES_RESTORE_TARGET_INITIALIZATION_ROOT_CHANGED")
	if j == nil || j.root == nil || j.lock == nil {
		return bad
	}
	a, ae := j.lock.Stat()
	b, be := j.root.Stat(".")
	c, ce := os.Lstat(j.identity.Path)
	resolved, re := filepath.EvalSymlinks(j.identity.Path)
	if ae != nil || be != nil || ce != nil || re != nil || resolved != j.identity.Path || !os.SameFile(a, b) || !os.SameFile(a, c) {
		return bad
	}
	id, e := rootIdentity(j.identity.Path, a)
	if e != nil || id != j.identity || !owned(b, true) || !owned(c, true) {
		return bad
	}
	return nil
}

func (j *targetInitializationJournal) inventory() (intent, complete bool, result error) {
	if e := j.check(); e != nil {
		return false, false, e
	}
	f, e := j.root.Open(".")
	if e != nil {
		return false, false, e
	}
	entries, re := f.ReadDir(3)
	ce := f.Close()
	if re != nil && re != io.EOF || ce != nil || len(entries) > 2 {
		return false, false, errors.New("POSTGRES_RESTORE_TARGET_INITIALIZATION_RECORDS_INVALID")
	}
	for _, entry := range entries {
		switch entry.Name() {
		case targetInitializationIntentName:
			intent = true
		case targetInitializationCompleteName:
			complete = true
		default:
			return false, false, errors.New("POSTGRES_RESTORE_TARGET_INITIALIZATION_RECORDS_INVALID")
		}
	}
	return intent, complete, nil
}

func (j *targetInitializationJournal) close() (result error) {
	if j == nil {
		return nil
	}
	if j.lock != nil {
		if syscall.Flock(int(j.lock.Fd()), syscall.LOCK_UN) != nil {
			result = errors.New("POSTGRES_RESTORE_TARGET_INITIALIZATION_UNLOCK_UNCONFIRMED")
		}
		result = errors.Join(result, j.lock.Close())
	}
	if j.root != nil {
		result = errors.Join(result, j.root.Close())
	}
	return result
}

func validTargetInitializationTime(value string) bool {
	t, e := time.Parse(time.RFC3339Nano, value)
	return e == nil && !t.IsZero() && t.UTC().Format(time.RFC3339Nano) == value
}

func readTargetInitializationRecords(j *targetInitializationJournal, r release.PostgreSQLRestoreTargetInitializationRequest, inputs string) (i targetInitializationIntent, c targetInitializationCompletion, intentSHA, completionSHA string, result error) {
	bad := errors.New("POSTGRES_RESTORE_TARGET_INITIALIZATION_RECORDS_INVALID")
	hasIntent, hasComplete, e := j.inventory()
	if e != nil || !hasIntent || !r.Valid() {
		return i, c, "", "", bad
	}
	raw, e := readPrivateDocument(filepath.Join(r.InitializationRoot, targetInitializationIntentName), r.Policy.MaximumRecordBytes, &i)
	if e != nil || i.Format != "projectone.postgres.restore-target-intent.v1" || i.Request != r || i.RequestSHA256 != r.SHA256() || i.InputsSHA256 != inputs || i.RecordRoot != j.identity || i.PristinePredicateSHA256 != initializationPredicateSHA256() || !validTargetInitializationTime(i.CreatedAt) || i.Preimage.SystemIdentifier != r.ExpectedSystemIdentifier || i.Preimage.DatabaseOID != r.ExpectedDatabaseOID || i.Preimage.Database != r.TargetDatabase || i.Preimage.Operator != r.TargetOperator || i.Preimage.ServerVersion != 180006 || i.Preimage.BackendPID < 1 || i.Preimage.Marker != "" {
		return i, c, "", "", bad
	}
	intentSHA = digest(raw)
	if hasComplete {
		raw, e = readPrivateDocument(filepath.Join(r.InitializationRoot, targetInitializationCompleteName), r.Policy.MaximumRecordBytes, &c)
		post := i.Preimage
		post.Marker = r.Marker()
		if e != nil || c.Format != "projectone.postgres.restore-target-complete.v1" || c.RequestSHA256 != r.SHA256() || c.IntentSHA256 != intentSHA || c.InputsSHA256 != inputs || c.PristinePredicateSHA256 != initializationPredicateSHA256() || c.Target != post || c.TargetBindingSHA256 != post.SHA256() || !validTargetInitializationTime(c.CompletedAt) {
			return i, c, "", "", bad
		}
		completionSHA = digest(raw)
	}
	return i, c, intentSHA, completionSHA, nil
}
