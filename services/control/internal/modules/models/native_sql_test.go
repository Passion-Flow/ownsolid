package models

import (
	"context"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"reflect"
	"testing"
)

type nativeModelProbe struct {
	transaction.Executor
	count int64
	err   error
	calls int
}

func (*nativeModelProbe) Dialect() transaction.Dialect { return transaction.Dameng }
func (p *nativeModelProbe) Exec(context.Context, transaction.Statement) (int64, error) {
	p.calls++
	return p.count, p.err
}

type nativeModelPool struct{ *nativeModelProbe }

func (*nativeModelPool) Within(context.Context, bool, func(context.Context, transaction.Executor) error) error {
	panic("unowned transaction")
}

func TestNativeImportRequiresPinnedSession(t *testing.T) {
	probe := &nativeModelProbe{}
	owner := &identity.Service{Storage: &nativeModelPool{probe}}
	service := &Service{Identity: owner, Workspaces: &workspaces.Service{Identity: owner}}
	called := false
	e := service.withImportSession(t.Context(), "00000000-0000-4000-8000-000000000001", func(context.Context, transaction.Store) error { called = true; return nil })
	if e == nil || e.Error() != "MODEL_STORAGE_UNAVAILABLE" || called || probe.calls != 0 {
		t.Fatal("missing physical-session owner must refuse before SQL or filesystem work")
	}
}
func TestNativeModelMutationPreservesFailureAndCardinality(t *testing.T) {
	for _, count := range []int64{0, 2, -1} {
		probe := &nativeModelProbe{count: count}
		if _, e := modelExecOne(t.Context(), probe, transaction.SQL(transaction.Dameng, "UPDATE MODEL_IMPORTS SET STATE='ready' WHERE ID=?", "synthetic")); e == nil || probe.calls != 1 {
			t.Fatal("non-single mutation was accepted")
		}
	}
	for _, kind := range []transaction.ErrorKind{transaction.UniqueConflict, transaction.CommitUnknown, transaction.Deadlock} {
		original := &transaction.Error{Kind: kind}
		probe := &nativeModelProbe{err: original}
		_, e := modelExecOne(t.Context(), probe, transaction.SQL(transaction.Dameng, "native"))
		if e != original || probe.calls != 1 {
			t.Fatal("must preserve the first failure without retrying a poisoned connection")
		}
	}
}
func TestNativeModelPageBoundAndDialectFence(t *testing.T) {
	q := &nativeModelProbe{}
	statement := modelPageSQL(q, "SELECT ID FROM MODEL_VERSIONS WHERE TENANT_ID=?", "pg", "mysql", "tenant", 101)
	if !reflect.DeepEqual(statement, transaction.SQL(transaction.Dameng, "SELECT ID FROM MODEL_VERSIONS WHERE TENANT_ID=? LIMIT 101", "tenant")) {
		t.Fatal("overflow sentinel or parameter binding lost")
	}
	for _, limit := range []any{0, 102, -1, "1; DROP TABLE MODEL_VERSIONS"} {
		if !reflect.DeepEqual(modelPageSQL(q, "native", "pg", "my", "tenant", limit), transaction.SQL(transaction.Dameng, "")) {
			t.Fatal("unvalidated native limit accepted")
		}
	}
	if !reflect.DeepEqual(modelSQL(q, "SELECT pg_only()", "SELECT mysql_only()"), transaction.SQL(transaction.Dameng, "")) {
		t.Fatal("unsupported native path fell back to another dialect")
	}
}

// This session double enforces the native poison boundary: a statement failure
// invalidates the session, and a later Within would return a different error.
// The complete process callback is exercised; no model bytes or SQL engine are
// claimed verified by these fault-injection tests.
type nativeWorkerSessionProbe struct {
	transaction.Executor
	nextError                                             error
	poisoned                                              bool
	withinCalls, withinAfterPoison, queryCalls, execCalls int
}

func (*nativeWorkerSessionProbe) Dialect() transaction.Dialect { return transaction.Dameng }
func (p *nativeWorkerSessionProbe) Within(ctx context.Context, _ bool, fn func(context.Context, transaction.Executor) error) error {
	p.withinCalls++
	if p.poisoned {
		p.withinAfterPoison++
		return &transaction.Error{Kind: transaction.Unavailable}
	}
	return fn(ctx, struct{ transaction.Executor }{p})
}
func (p *nativeWorkerSessionProbe) Exec(context.Context, transaction.Statement) (int64, error) {
	p.execCalls++
	if p.nextError != nil {
		p.poisoned = true
		return 0, p.nextError
	}
	return 1, nil
}
func (p *nativeWorkerSessionProbe) Query(context.Context, transaction.Statement) (transaction.Rows, error) {
	p.queryCalls++
	if p.nextError == nil {
		panic("fault probe only supports a failing file-inventory query")
	}
	p.poisoned = true
	return nil, p.nextError
}
func nativeWorkerProbeMutation(ctx context.Context, session transaction.Store) error {
	return session.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
		_, e := q.Exec(ctx, transaction.SQL(transaction.Dameng, "UPDATE MODEL_IMPORTS SET STATE=? WHERE ID=?", "verifying", "synthetic"))
		return e
	})
}
func TestNativeModelProcessStopsAfterSQLFailure(t *testing.T) {
	for _, phase := range []string{"claim", "verify", "publish"} {
		for _, kind := range []transaction.ErrorKind{transaction.UniqueConflict, transaction.CheckConflict, transaction.ForeignKeyConflict, transaction.Deadlock, transaction.LockTimeout, transaction.Unavailable, transaction.CommitUnknown, transaction.Cancelled, transaction.Timeout, transaction.CommitRolledBack} {
			t.Run(phase+"/"+string(kind), func(t *testing.T) {
				original := &transaction.Error{Kind: kind}
				session := &nativeWorkerSessionProbe{}
				failureCalls, publishCalls := 0, 0
				current := workerImport{Import: Import{ID: "00000000-0000-4000-8000-000000000001"}}
				operations := workerSessionOperations{
					claim: func(ctx context.Context, store transaction.Store, _ string) (workerImport, bool, bool, error) {
						if phase == "claim" {
							session.nextError = original
						}
						return current, true, false, nativeWorkerProbeMutation(ctx, store)
					},
					verify: func(ctx context.Context, store transaction.Store, _ workerImport) error {
						if phase != "verify" {
							return nil
						}
						session.nextError = original
						_, e := store.Query(ctx, transaction.SQL(transaction.Dameng, "SELECT BYTE_OFFSET FROM MODEL_IMPORT_CHUNKS WHERE IMPORT_ID=?", current.ID))
						return e
					},
					publish: func(ctx context.Context, store transaction.Store, _ workerImport) error {
						publishCalls++
						session.nextError = original
						return nativeWorkerProbeMutation(ctx, store)
					},
					recordFailure: func(ctx context.Context, store transaction.Store, _ workerImport, _ error) error {
						failureCalls++
						return nativeWorkerProbeMutation(ctx, store)
					},
					verifyReady: func(context.Context, string) error { panic("no ready publication in failure probe") },
				}
				e := processWorkerSession(t.Context(), session, current.ID, operations)
				if e != original || !session.poisoned || session.withinAfterPoison != 0 || failureCalls != 0 {
					t.Fatal("process callback reused poisoned session or replaced original database error")
				}
				expectedWithin := 1
				if phase == "publish" {
					expectedWithin = 2
				}
				if session.withinCalls != expectedWithin {
					t.Fatalf("Within calls=%d, expected %d", session.withinCalls, expectedWithin)
				}
				if (phase == "publish" && publishCalls != 1) || (phase != "publish" && publishCalls != 0) {
					t.Fatal("a later processing stage ran after SQL failure")
				}
				ctx, cancel := context.WithCancel(t.Context())
				cancel()
				if normalizedWorkerError(ctx, transaction.Dameng, e) != original {
					t.Fatal("ambient cancellation replaced classified database outcome")
				}
			})
		}
	}
}
func TestNativeModelProcessRecordsOnlyKnownHealthyFailure(t *testing.T) {
	for _, code := range []string{"MODEL_CHUNK_INVALID", "STORAGE_UNAVAILABLE", "REPOSITORY_STATEMENT_INVALID", "MODEL_IMPORT_FAILED"} {
		t.Run(code, func(t *testing.T) {
			session := &nativeWorkerSessionProbe{}
			original := fail(code)
			current := workerImport{Import: Import{ID: "00000000-0000-4000-8000-000000000001"}}
			failureCalls := 0
			operations := workerSessionOperations{
				claim: func(ctx context.Context, store transaction.Store, _ string) (workerImport, bool, bool, error) {
					return current, true, false, nativeWorkerProbeMutation(ctx, store)
				},
				verify: func(context.Context, transaction.Store, workerImport) error { return original },
				publish: func(context.Context, transaction.Store, workerImport) error {
					panic("must not publish failed verification")
				},
				recordFailure: func(ctx context.Context, store transaction.Store, _ workerImport, cause error) error {
					failureCalls++
					if cause != original {
						t.Fatal("business cause changed")
					}
					return nativeWorkerProbeMutation(ctx, store)
				},
				verifyReady: func(context.Context, string) error { panic("unexpected ready branch") },
			}
			if e := processWorkerSession(t.Context(), session, current.ID, operations); e != original {
				t.Fatal("original file/business failure changed")
			}
			expected := 0
			if code == "MODEL_CHUNK_INVALID" {
				expected = 1
			}
			if failureCalls != expected || session.withinCalls != 1+expected || session.withinAfterPoison != 0 {
				t.Fatal("only a known healthy file/business failure may enter failure bookkeeping")
			}
		})
	}
}
