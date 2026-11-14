package cloudinventory

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// Test-only unsupported dialect double: nil embedded operations must never run.
type unsupportedBusinessStore struct {
	transaction.Store
	dialect transaction.Dialect
}

func (s unsupportedBusinessStore) Dialect() transaction.Dialect { return s.dialect }

func TestAWSUnsupportedBusinessDialectDoesNotExecute(t *testing.T) {
	s := &Service{Identity: &identity.Service{Storage: unsupportedBusinessStore{dialect: transaction.Dialect("future")}}}
	called := false
	e := s.scope(context.Background(), identity.Auth{}, "unused", true, func(context.Context, transaction.Executor, workspaces.Workspace) error {
		called = true
		return nil
	})
	if e == nil || e.Error() != "AWS_BUSINESS_DATABASE_UNSUPPORTED" || called {
		t.Fatal("unsupported dialect performed work", e, called)
	}
}

func TestAWSUnsupportedImportPrecedesBusinessAdmission(t *testing.T) {
	s := &Service{Identity: &identity.Service{Storage: unsupportedBusinessStore{dialect: transaction.Dialect("future")}}}
	// A missing BusinessAdmission would reject unlicensed PostgreSQL work. An
	// unsupported dialect must instead reject before that potentially SQL owner.
	out, e := s.Import(context.Background(), identity.Auth{}, "unused", ImportInput{Key: uuid.NewString()}, uuid.NewString())
	if e == nil || e.Error() != "AWS_BUSINESS_DATABASE_UNSUPPORTED" || out.Key != "" {
		t.Fatal("unsupported import reached admission", e)
	}
}

type trackedAWSAdmission struct {
	calls int
	err   error
}

func (a *trackedAWSAdmission) Check(context.Context) error        { a.calls++; return a.err }
func (a *trackedAWSAdmission) VerifyStored(context.Context) error { a.calls++; return a.err }

func TestAWSUnsupportedPublicOperationsRejectBeforeAdmissionAndSQL(t *testing.T) {
	for _, dialect := range []transaction.Dialect{"", "future"} {
		t.Run(string(dialect), func(t *testing.T) {
			admission := &trackedAWSAdmission{}
			s := &Service{Identity: &identity.Service{Storage: unsupportedBusinessStore{dialect: dialect}, BusinessAdmission: admission}}
			workspace, key := uuid.NewString(), uuid.NewString()
			_, e := s.List(context.Background(), identity.Auth{}, workspace, "", 1)
			wantCode(t, e, "AWS_BUSINESS_DATABASE_UNSUPPORTED")
			_, e = s.Receipt(context.Background(), identity.Auth{}, workspace, key)
			wantCode(t, e, "AWS_BUSINESS_DATABASE_UNSUPPORTED")
			out, e := s.Import(context.Background(), identity.Auth{}, workspace, ImportInput{Connection: "fixture", Region: "us-east-1", Key: key}, uuid.NewString())
			wantCode(t, e, "AWS_BUSINESS_DATABASE_UNSUPPORTED")
			if admission.calls != 0 || out.Key != "" {
				t.Fatal("unsupported public operation admitted License/SQL or projected success")
			}
		})
	}
}

// Selected admission tests stop at the real transaction entrance, before any
// SQL. This establishes dispatch/ordering only, not a vendor execution result.
type selectedAWSStore struct {
	transaction.Store
	dialect  transaction.Dialect
	within   int
	stop     error
	executor transaction.Executor
}

func (s *selectedAWSStore) Dialect() transaction.Dialect { return s.dialect }
func (s *selectedAWSStore) Within(ctx context.Context, readonly bool, fn func(context.Context, transaction.Executor) error) error {
	s.within++
	if _, ok := ctx.Deadline(); !ok || readonly {
		return errors.New("AWS_TEST_TRANSACTION_BOUNDARY")
	}
	if s.stop != nil {
		return s.stop
	}
	return fn(ctx, s.executor)
}
func TestAWSSelectedDialectsReachOriginalTransactionAdmission(t *testing.T) {
	for _, dialect := range []transaction.Dialect{transaction.PostgreSQL, transaction.MySQL, transaction.Dameng} {
		t.Run(string(dialect), func(t *testing.T) {
			stop := errors.New("synthetic transaction entrance")
			store := &selectedAWSStore{dialect: dialect, stop: stop}
			admission := &trackedAWSAdmission{}
			account := &identity.Service{Storage: store, BusinessAdmission: admission}
			service := &Service{Identity: account, Workspaces: &workspaces.Service{Identity: account}}
			workspace, key := uuid.NewString(), uuid.NewString()
			_, e := service.List(context.Background(), identity.Auth{}, workspace, "", 1)
			if !errors.Is(e, stop) {
				t.Fatal("list selected transaction admission", e)
			}
			_, e = service.Receipt(context.Background(), identity.Auth{}, workspace, key)
			if !errors.Is(e, stop) {
				t.Fatal("receipt selected transaction admission", e)
			}
			out, e := service.Import(context.Background(), identity.Auth{}, workspace, ImportInput{Key: key}, uuid.NewString())
			if !errors.Is(e, stop) || out.Key != "" || admission.calls != 1 || store.within != 3 {
				t.Fatal("import bypassed License/transaction ordering", e, admission.calls, store.within)
			}
		})
	}
}
func TestAWSRejectsDifferentIdentityBeforeAdmission(t *testing.T) {
	store := &selectedAWSStore{dialect: transaction.MySQL}
	admission := &trackedAWSAdmission{}
	actor := &identity.Service{Storage: store, BusinessAdmission: admission}
	other := &identity.Service{Storage: store}
	service := &Service{Identity: actor, Workspaces: &workspaces.Service{Identity: other}}
	_, e := service.Import(context.Background(), identity.Auth{}, uuid.NewString(), ImportInput{Key: uuid.NewString()}, uuid.NewString())
	wantCode(t, e, "STORAGE_UNAVAILABLE")
	if admission.calls != 0 || store.within != 0 {
		t.Fatal("mismatched Identity reached admission")
	}
}
func TestAWSRejectsWrongBorrowedDialectAndPoolBeforeSQL(t *testing.T) {
	for _, q := range []transaction.Executor{nil, &awsRecordingExecutor{dialect: transaction.PostgreSQL}, &selectedAWSStore{dialect: transaction.MySQL}} {
		store := &selectedAWSStore{dialect: transaction.MySQL, executor: q}
		actor := &identity.Service{Storage: store}
		service := &Service{Identity: actor, Workspaces: &workspaces.Service{Identity: actor}}
		called := false
		e := service.scope(context.Background(), identity.Auth{}, uuid.NewString(), false, func(context.Context, transaction.Executor, workspaces.Workspace) error { called = true; return nil })
		wantCode(t, e, "STORAGE_UNAVAILABLE")
		if called {
			t.Fatal("invalid transaction reached authority/business callback")
		}
	}
}

func TestAWSSelectedLicenseFailurePrecedesTransactionAndProvider(t *testing.T) {
	for _, d := range []transaction.Dialect{transaction.MySQL, transaction.Dameng} {
		store := &selectedAWSStore{dialect: d}
		stop := errors.New("synthetic License refusal")
		admission := &trackedAWSAdmission{err: stop}
		actor := &identity.Service{Storage: store, BusinessAdmission: admission}
		service := &Service{Identity: actor, Workspaces: &workspaces.Service{Identity: actor}}
		out, e := service.Import(context.Background(), identity.Auth{}, uuid.NewString(), ImportInput{Key: uuid.NewString()}, uuid.NewString())
		if !errors.Is(e, stop) || out.Key != "" || admission.calls != 1 || store.within != 0 {
			t.Fatal("selected dialect bypassed original License refusal", d, e)
		}
	}
}

func TestAWSCommitOutcomeOutranksJoinedTimeoutAndCleanup(t *testing.T) {
	for _, kind := range []transaction.ErrorKind{transaction.CommitUnknown, transaction.CommitRolledBack} {
		direct := &transaction.Error{Kind: kind, SQLState: "synthetic", NativeCode: -123}
		if safe(direct) != direct {
			t.Fatal("direct safe commit classification lost")
		}
		for _, joined := range []error{errors.Join(&transaction.Error{Kind: transaction.Timeout}, direct), errors.Join(context.Canceled, direct, errors.New("private vendor detail"))} {
			result := safe(joined)
			typed, ok := result.(*transaction.Error)
			if !ok || typed.Kind != kind || result.Error() != string(kind) || typed.SQLState != "" || typed.NativeCode != 0 {
				t.Fatal("joined commit result hidden or raw diagnostics exposed", result)
			}
		}
	}
	unknown := errors.Join(&transaction.Error{Kind: transaction.CommitRolledBack}, &transaction.Error{Kind: transaction.CommitUnknown})
	if result := safe(unknown); result == nil || result.Error() != string(transaction.CommitUnknown) {
		t.Fatal("unknown commit lost precedence", result)
	}
	wantCode(t, safe(&transaction.Error{Kind: transaction.Timeout}), "STORAGE_UNAVAILABLE")
}
