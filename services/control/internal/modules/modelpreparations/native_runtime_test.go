package modelpreparations

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/httpserver"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/pagination"
)

type nativePreparationValues struct {
	values []any
	err    error
}

func (r nativePreparationValues) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if len(dest) != len(r.values) {
		return errors.New("unexpected fixture projection")
	}
	for i, value := range r.values {
		pointer := reflect.ValueOf(dest[i]).Elem()
		if value == nil {
			pointer.SetZero()
			continue
		}
		if boolean, ok := dest[i].(*bool); ok {
			if n, ok := value.(int64); ok && (n == 0 || n == 1) {
				*boolean = n == 1
				continue
			}
		}
		v := reflect.ValueOf(value)
		if !v.Type().AssignableTo(pointer.Type()) {
			return errors.New("unexpected fixture destination")
		}
		pointer.Set(v)
	}
	return nil
}

func TestNativePreparationRowRejectsMalformedValuesWithoutPartialOutput(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Microsecond)
	id := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	for _, test := range []struct {
		name    string
		id      string
		stamp   time.Time
		boolean int64
		scan    error
		valid   bool
	}{
		{"false", id, now, 0, nil, true}, {"true", id, now, 1, nil, true},
		{"negative", id, now, -1, nil, false}, {"two", id, now, 2, nil, false},
		{"uppercase_uuid", strings.ToUpper(id), now, 1, nil, false},
		{"nil_uuid", uuid.Nil.String(), now, 1, nil, false},
		{"offset", id, now.In(time.FixedZone("offset", 3600)), 1, nil, false},
		{"sub_microsecond", id, now.Add(time.Nanosecond), 1, nil, false},
		{"null_time", id, time.Time{}, 1, nil, false},
		{"scan_failure", id, now, 1, errors.New("synthetic scan failure"), false},
	} {
		t.Run(test.name, func(t *testing.T) {
			var gotID string
			var gotTime time.Time
			var gotBool bool
			row := preparationNativeRow{Row: nativePreparationValues{[]any{test.id, test.stamp, test.boolean}, test.scan}, ids: []int{0}}
			e := row.Scan(&gotID, &gotTime, &gotBool)
			if test.valid {
				if e != nil || gotID != id || gotTime != now || gotBool != (test.boolean == 1) {
					t.Fatal("valid native row", e)
				}
			} else if e == nil || gotID != "" || !gotTime.IsZero() || gotBool {
				t.Fatal("malformed row exposed values", e)
			}
		})
	}
}

func TestNativePreparationBindingKeepsCLOBAndBinaryTypesAndCursorOrder(t *testing.T) {
	raw := []byte("{\"message\":\"\u4e16\u754c\"}")
	binary := []byte{0, 1, 255}
	query := preparationDM(`INSERT INTO "SYNTHETIC"("JSON","BINARY") VALUES(CAST(? AS CLOB),?)`, 0)
	_, args, ok := query.bind([]any{raw, binary})
	if !ok || args[0] != string(raw) || !reflect.DeepEqual(args[1], binary) {
		t.Fatal("CLOB/binary binding changed")
	}
	if _, _, ok = query.bind([]any{[]byte(`{"broken"`), binary}); ok {
		t.Fatal("invalid JSON admitted")
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	id := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	for _, state := range []string{"", "queued"} {
		for _, cursor := range []bool{false, true} {
			text, bound, ok := preparationNativePage(state, cursor).bind([]any{"tenant", "workspace", state, state, now, now, id, 101})
			want := []any{"tenant", "workspace"}
			if state != "" {
				want = append(want, state)
			}
			if cursor {
				want = append(want, now, now, id)
			}
			if !ok || !strings.HasSuffix(text, " LIMIT 101") || strings.Count(text, "?") != len(bound) || !reflect.DeepEqual(bound, want) {
				t.Fatal("scoped cursor binding", state, cursor)
			}
		}
	}
	for _, limit := range []any{0, -1, 102, "100"} {
		if _, _, ok := preparationNativePage("", false).bind([]any{"t", "w", "", "", nil, nil, nil, limit}); ok {
			t.Fatal("unbounded page admitted")
		}
	}
	if _, _, ok := preparationNativePage("unknown", false).bind([]any{}); ok {
		t.Fatal("unknown state admitted")
	}
	if _, _, ok := preparationDM("SELECT ? FROM DUAL").bind([]any{now.Add(time.Nanosecond)}); ok {
		t.Fatal("noncanonical cursor admitted")
	}
	for _, cursor := range []pagination.Cursor{
		{ID: strings.ToUpper(id), Time: now},
		{ID: uuid.Nil.String(), Time: now},
		{ID: id, Time: now.In(time.FixedZone("offset", 3600))},
		{ID: id, Time: now.Add(time.Nanosecond)},
	} {
		q := nativePreparationExecutor{}
		if e := preparationSelectedCursor(q, cursor); e == nil || preparationRepositoryError(e).Error() != "INVALID_CURSOR" {
			t.Fatal("noncanonical native page cursor admitted", e)
		}
	}
}

type nativePreparationStore struct {
	nativePreparationExecutor
	actor                       identity.Auth
	tenant, workspace           string
	role, userStatus            string
	commitError                 error
	callbacks, commits, queries int
	allowOwner                  bool
}
type nativePreparationExecutor struct{ fixture *nativePreparationStore }

func (nativePreparationExecutor) Dialect() transaction.Dialect { return transaction.Dameng }
func (q nativePreparationExecutor) Exec(context.Context, transaction.Statement) (int64, error) {
	return 0, errors.New("unexpected mutation")
}
func (q nativePreparationExecutor) Query(context.Context, transaction.Statement) (transaction.Rows, error) {
	return nil, errors.New("unexpected inventory")
}
func (q nativePreparationExecutor) QueryRow(_ context.Context, statement transaction.Statement) transaction.Row {
	f := q.fixture
	f.queries++
	text := reflect.ValueOf(statement).FieldByName("text").String()
	switch {
	case strings.Contains(text, "SSO_TENANT_ID"):
		return nativePreparationValues{values: []any{nil, nil, nil, nil, nil}}
	case strings.Contains(text, "AUTH_EPOCH,STATUS,IS_PLATFORM_ADMIN"):
		return nativePreparationValues{values: []any{int64(1), f.userStatus, int64(1)}}
	case strings.Contains(text, "FROM SESSIONS"):
		return nativePreparationValues{values: []any{f.actor.SessionID}}
	case strings.Contains(text, "FROM WORKSPACES W"):
		now := time.Now().UTC().Truncate(time.Microsecond)
		return nativePreparationValues{values: []any{f.workspace, f.tenant, "Synthetic scope", "active", int64(1), int64(1), now, now}}
	case strings.Contains(text, "SELECT ROLE FROM WORKSPACE_MEMBERSHIPS"):
		if f.role == "" {
			return nativePreparationValues{err: sql.ErrNoRows}
		}
		return nativePreparationValues{values: []any{f.role}}
	}
	return nativePreparationValues{err: errors.New("unexpected authority query")}
}
func (f *nativePreparationStore) OwnsDamengExecutor(q transaction.Executor) bool {
	value, ok := q.(nativePreparationExecutor)
	return f.allowOwner && ok && value.fixture == f
}
func (f *nativePreparationStore) Within(ctx context.Context, _ bool, fn func(context.Context, transaction.Executor) error) error {
	f.callbacks++
	if e := fn(ctx, f.nativePreparationExecutor); e != nil {
		return e
	}
	f.commits++
	return f.commitError
}
func nativePreparationService(t *testing.T) (*Service, *nativePreparationStore) {
	t.Helper()
	f := &nativePreparationStore{tenant: uuid.NewString(), workspace: uuid.NewString(), role: "owner", userStatus: "active", allowOwner: true}
	f.nativePreparationExecutor.fixture = f
	f.actor = identity.Auth{Me: identity.Me{User: identity.User{ID: uuid.NewString()}, Audience: "portal"}, SessionID: uuid.NewString(), TokenHash: make([]byte, 32), Epoch: 1}
	i := &identity.Service{Storage: f}
	w := &workspaces.Service{Identity: i}
	s, e := New(nil, i, w, nil)
	if e != nil {
		t.Fatal("native selected constructor", e)
	}
	return s, f
}

func TestNativePreparationAuthorityAndUnknownCommitKeepZeroDTOAndNoReplay(t *testing.T) {
	for _, outcome := range []error{nil, &transaction.Error{Kind: transaction.CommitUnknown}, errors.Join(context.DeadlineExceeded, &transaction.Error{Kind: transaction.CommitUnknown}), &transaction.Error{Kind: transaction.CommitRolledBack}} {
		s, f := nativePreparationService(t)
		f.commitError = outcome
		out, e := s.PreparationCapabilities(context.Background(), f.actor, f.workspace)
		if outcome == nil {
			if e != nil || !out.CanManage || !out.CanCancelOwn || out.MaximumAttempts < 1 {
				t.Fatal("authorized native capability", e)
			}
		} else {
			kind, _ := transaction.CommitOutcome(outcome)
			if e == nil || e.Error() != string(kind) || !reflect.DeepEqual(out, PreparationCapabilities{}) {
				t.Fatal("uncertain callback DTO escaped", e)
			}
		}
		if f.callbacks != 1 || f.commits != 1 {
			t.Fatal("transaction was replayed")
		}
	}
	for _, boundary := range []string{"revoked_user", "foreign_executor", "viewer_write", "wrong_audience"} {
		t.Run(boundary, func(t *testing.T) {
			s, f := nativePreparationService(t)
			called := false
			a := f.actor
			switch boundary {
			case "revoked_user":
				f.userStatus = "disabled"
			case "foreign_executor":
				f.allowOwner = false
			case "viewer_write":
				f.role = "viewer"
			case "wrong_audience":
				a.Me.Audience = "machine"
			}
			e := s.preparationActorTransaction(context.Background(), a, f.workspace, true, func(context.Context, transaction.Executor, workspaces.Workspace) error { called = true; return nil })
			if e == nil || called || f.commits != 0 {
				t.Fatal("authority fence bypassed", e)
			}
			if boundary == "foreign_executor" && f.queries != 0 {
				t.Fatal("foreign kernel executed SQL")
			}
		})
	}
}

func TestNativePreparationBorrowRejectsSameDialectDifferentKernel(t *testing.T) {
	_, first := nativePreparationService(t)
	_, second := nativePreparationService(t)
	if preparationBorrowedExecutor(first, second.nativePreparationExecutor) == nil {
		t.Fatal("same dialect accepted a different native kernel")
	}
	if preparationBorrowedExecutor(first, first.nativePreparationExecutor) != nil {
		t.Fatal("own transaction rejected")
	}
}

type nativePreparationGPUProbe struct {
	row          transaction.Row
	read, writes int
	writeError   error
}

func (*nativePreparationGPUProbe) Dialect() transaction.Dialect { return transaction.Dameng }
func (q *nativePreparationGPUProbe) QueryRow(_ context.Context, statement transaction.Statement) transaction.Row {
	q.read++
	if reflect.ValueOf(statement).FieldByName("dialect").String() != string(transaction.Dameng) {
		return nativePreparationValues{err: errors.New("foreign vendor statement")}
	}
	return q.row
}
func (q *nativePreparationGPUProbe) Exec(_ context.Context, statement transaction.Statement) (int64, error) {
	q.writes++
	value := reflect.ValueOf(statement)
	text := value.FieldByName("text").String()
	if value.FieldByName("dialect").String() != string(transaction.Dameng) || text == "" || strings.Count(text, "?") != value.FieldByName("args").Len() {
		return 0, errors.New("invalid selected native mutation")
	}
	return 1, q.writeError
}
func (*nativePreparationGPUProbe) Query(context.Context, transaction.Statement) (transaction.Rows, error) {
	return nil, errors.New("unexpected inventory")
}

func TestNativePreparationGPUReservationRetainsPhysicalUncertainty(t *testing.T) {
	w := workspaces.Workspace{ID: uuid.NewString(), TenantID: uuid.NewString()}
	n := nodes.Node{ID: uuid.NewString()}
	p := Preparation{ID: uuid.NewString(), GPUUUID: "GPU-" + uuid.NewString()}
	for _, test := range []struct {
		name, state, gpu      string
		readError, writeError error
		reserved              bool
		writes                int
	}{
		{"held", "held", p.GPUUUID, nil, nil, false, 0},
		{"uncertain", "recovery_required", p.GPUUUID, nil, nil, false, 0},
		{"different_gpu", "released", "GPU-" + uuid.NewString(), nil, nil, false, 0},
		{"released", "released", p.GPUUUID, nil, nil, true, 1},
		{"fresh", "", "", sql.ErrNoRows, nil, true, 1},
		{"collision", "", "", sql.ErrNoRows, &transaction.Error{Kind: transaction.UniqueConflict}, false, 1},
		{"storage_error", "", "", &transaction.Error{Kind: transaction.Unavailable}, nil, false, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			q := &nativePreparationGPUProbe{row: nativePreparationValues{[]any{test.state, test.gpu}, test.readError}, writeError: test.writeError}
			reserved, e := reservePreparationGPU(context.Background(), q, w, n, p, 2)
			if reserved && e != nil {
				reserved = false
			}
			if reserved != test.reserved || q.writes != test.writes || q.read != 1 {
				t.Fatal("physical owner changed or mutation replayed", reserved, e)
			}
			if test.writeError != nil && !errors.Is(e, test.writeError) {
				t.Fatal("native conflict lost", e)
			}
			if test.readError != nil && !errors.Is(test.readError, sql.ErrNoRows) && !errors.Is(e, test.readError) {
				t.Fatal("uncertainty lost", e)
			}
		})
	}
}

func TestNativePreparationUncertainTransferClearsMetadataAndClosesFile(t *testing.T) {
	for _, failure := range []error{nil, &transaction.Error{Kind: transaction.CommitUnknown}, context.Canceled} {
		file, e := os.CreateTemp(t.TempDir(), "owned-transfer-")
		if e != nil {
			t.Fatal(e)
		}
		defer file.Close()
		original := httpserver.Download{File: file, Size: 123, Offset: 9, SHA256: strings.Repeat("a", 64), FileName: "synthetic-model.bin", ContentType: "application/octet-stream"}
		out, e := preparationDownloadOutcome(original, failure)
		_, statError := file.Stat()
		if failure == nil {
			if e != nil || !reflect.DeepEqual(out, original) || statError != nil {
				t.Fatal("successful transfer changed", e)
			}
		} else if !errors.Is(e, failure) || !reflect.DeepEqual(out, httpserver.Download{}) || !errors.Is(statError, os.ErrClosed) {
			t.Fatal("failed transfer retained metadata or file", e, statError)
		}
	}
}
