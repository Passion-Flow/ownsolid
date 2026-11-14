package postgresbackup

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	release "github.com/Passion-Flow/ownsolid/api/release/v1"
)

type initializationRow struct{ values []any }

func (r initializationRow) Scan(dst ...any) error {
	if len(dst) != len(r.values) {
		return errors.New("synthetic row shape")
	}
	for i, v := range r.values {
		reflect.ValueOf(dst[i]).Elem().Set(reflect.ValueOf(v))
	}
	return nil
}

type initializationProtocolSession struct {
	target                                                     release.PostgreSQLRestoreTargetInitializationIdentity
	pristine, ordinary, super, owner, primary, held, exclusive bool
	execCalls, commitCalls                                     int
	commitUnknown, commitApplied                               bool
	onBegin                                                    func()
}

func (s *initializationProtocolSession) row(query string, readonly bool, marker string) pgx.Row {
	if query == "SELECT ("+emptyPostgresTarget+") AND ("+initializationPristineExtras+")" {
		return initializationRow{[]any{s.pristine}}
	}
	return initializationRow{[]any{s.target.SystemIdentifier, s.target.DatabaseOID, s.target.Database, s.target.Operator, s.target.BackendPID, s.target.ServerVersion, marker, s.ordinary, s.super, s.owner, s.primary, readonly, s.held, s.exclusive}}
}
func (s *initializationProtocolSession) QueryRow(_ context.Context, q string, _ ...any) pgx.Row {
	return s.row(q, false, s.target.Marker)
}
func (s *initializationProtocolSession) begin(_ context.Context, readonly bool) (targetInitializationTransaction, error) {
	if s.onBegin != nil {
		s.onBegin()
	}
	return &initializationProtocolTransaction{session: s, readonly: readonly, pending: s.target.Marker}, nil
}

type initializationProtocolTransaction struct {
	session          *initializationProtocolSession
	readonly, closed bool
	pending          string
}

func (t *initializationProtocolTransaction) QueryRow(_ context.Context, q string, _ ...any) pgx.Row {
	return t.session.row(q, t.readonly, t.pending)
}
func (t *initializationProtocolTransaction) Exec(_ context.Context, q string, _ ...any) (pgconn.CommandTag, error) {
	if t.readonly || t.closed || !strings.HasPrefix(q, `COMMENT ON DATABASE "`) {
		return pgconn.CommandTag{}, errors.New("unexpected synthetic effect")
	}
	parts := strings.Split(q, "'")
	if len(parts) != 3 {
		return pgconn.CommandTag{}, errors.New("fixed comment syntax")
	}
	t.pending = parts[1]
	t.session.execCalls++
	return pgconn.NewCommandTag("COMMENT"), nil
}
func (t *initializationProtocolTransaction) Commit(context.Context) error {
	t.closed = true
	t.session.commitCalls++
	if !t.session.commitUnknown || t.session.commitApplied {
		t.session.target.Marker = t.pending
	}
	if t.session.commitUnknown {
		return errors.New("synthetic lost commit response")
	}
	return nil
}
func (t *initializationProtocolTransaction) Rollback(context.Context) error {
	if t.closed {
		return pgx.ErrTxClosed
	}
	t.closed = true
	return nil
}

func targetInitializationProtocolFixture(t *testing.T) (release.PostgreSQLRestoreTargetInitializationRequest, *initializationProtocolSession, string) {
	t.Helper()
	r := release.PostgreSQLRestoreTargetInitializationRequest{FormatVersion: 1, ApplicationProfile: release.MVPPostgreSQLCPUApplicationProfile, InitializationID: strings.Repeat("a", 32), TargetScopeID: strings.Repeat("b", 32), Binding: release.PostgreSQLRestoreTargetInitializationBinding{RestoreAttemptID: strings.Repeat("c", 32), ReservationPlanSHA256: strings.Repeat("d", 64), TargetDescriptorSHA256: strings.Repeat("e", 64), TargetRuntimeSHA256: strings.Repeat("f", 64), SourceCandidateSHA256: strings.Repeat("1", 64), NetworkID: strings.Repeat("2", 64), PostgresContainerID: strings.Repeat("3", 64)}, SourceDatabase: "source_fixture", TargetDatabase: "target_fixture", TargetOperator: "projectone_operator", Endpoint: release.PostgreSQLRestoreTargetInitializationEndpoint{Host: "postgres", Port: 5432, SSLMode: "disable"}, ExpectedSystemIdentifier: "7703660038325649453", ExpectedDatabaseOID: 40000, InitializationRoot: filepath.Join(privateTestRoot(t), "initialization"), Policy: release.PostgreSQLRestoreTargetInitializationPolicy{OperationSeconds: 60, CleanupSeconds: 2, MaximumRecordBytes: 65536, MinimumFreeBytes: 1 << 20}}
	s := &initializationProtocolSession{target: release.PostgreSQLRestoreTargetInitializationIdentity{SystemIdentifier: r.ExpectedSystemIdentifier, DatabaseOID: r.ExpectedDatabaseOID, Database: r.TargetDatabase, Operator: r.TargetOperator, BackendPID: 100, ServerVersion: 180006}, pristine: true, ordinary: true, super: true, owner: true, primary: true, held: true, exclusive: true}
	return r, s, strings.Repeat("4", 64)
}

func TestPostgresRestoreTargetInitializationProtocolOnceAndReadonly(t *testing.T) {
	r, s, inputs := targetInitializationProtocolFixture(t)
	check := func() error { return nil }
	out, e := initializeRestoreTargetSession(context.Background(), s, r, inputs, check)
	if e != nil || !out.ValidFor(r, false) || s.execCalls != 1 || s.commitCalls != 1 {
		t.Fatal("one original fixed effect", e, s.execCalls, s.commitCalls)
	}
	if _, e = initializeRestoreTargetSession(context.Background(), s, r, inputs, check); e == nil || s.execCalls != 1 {
		t.Fatal("duplicate initialization resent comment", e)
	}
	s.target.BackendPID++
	out, e = inspectRestoreTargetSession(context.Background(), s, r, inputs, check)
	if e != nil || !out.ValidFor(r, true) || !out.OriginalCompletionPresent || s.execCalls != 1 {
		t.Fatal("new PID original readonly completion", e)
	}
	complete := filepath.Join(r.InitializationRoot, targetInitializationCompleteName)
	retained := filepath.Join(filepath.Dir(r.InitializationRoot), "retained-completion.json")
	if e = os.Rename(complete, retained); e != nil {
		t.Fatal(e)
	}
	out, e = inspectRestoreTargetSession(context.Background(), s, r, inputs, check)
	if e != nil || !out.ValidFor(r, true) || out.OriginalCompletionPresent || !out.EffectObserved || s.execCalls != 1 {
		t.Fatal("missing completion was not preserved", e)
	}
	if _, e = os.Lstat(complete); !os.IsNotExist(e) {
		t.Fatal("observer fabricated completion")
	}
	s.target.Marker = ""
	out, e = inspectRestoreTargetSession(context.Background(), s, r, inputs, check)
	if e != nil || out.State != "effect_not_observed" || out.EffectObserved {
		t.Fatal("current absent effect observation", e)
	}
}

func TestPostgresRestoreTargetInitializationUnknownCommitNeverReplays(t *testing.T) {
	for _, applied := range []bool{false, true} {
		t.Run(map[bool]string{false: "not-observed", true: "observed"}[applied], func(t *testing.T) {
			r, s, inputs := targetInitializationProtocolFixture(t)
			s.commitUnknown = true
			s.commitApplied = applied
			check := func() error { return nil }
			if _, e := initializeRestoreTargetSession(context.Background(), s, r, inputs, check); e == nil || !strings.Contains(e.Error(), "COMMIT_UNKNOWN") {
				t.Fatal("commit uncertainty erased", e)
			}
			if _, e := os.Lstat(filepath.Join(r.InitializationRoot, targetInitializationCompleteName)); !os.IsNotExist(e) {
				t.Fatal("unknown commit published completion")
			}
			out, e := inspectRestoreTargetSession(context.Background(), s, r, inputs, check)
			if e != nil || !out.ValidFor(r, true) || out.EffectObserved != applied || out.OriginalCompletionPresent || s.execCalls != 1 || s.commitCalls != 1 {
				t.Fatal("readonly unknown reconciliation", e, out)
			}
			if _, e = initializeRestoreTargetSession(context.Background(), s, r, inputs, check); e == nil || s.execCalls != 1 {
				t.Fatal("unknown mutation resent", e)
			}
		})
	}
}

func TestPostgresRestoreTargetInitializationRefusalsBeforeEffect(t *testing.T) {
	for _, name := range []string{"foreign-marker", "same-marker-without-intent", "nonempty", "wrong-system", "wrong-oid", "wrong-database", "wrong-operator", "wrong-version", "not-super", "not-owner", "not-primary", "not-ordinary", "no-lease", "other-client", "unpinned", "changed-preimage", "changed-input"} {
		t.Run(name, func(t *testing.T) {
			r, s, inputs := targetInitializationProtocolFixture(t)
			check := func() error { return nil }
			switch name {
			case "foreign-marker":
				s.target.Marker = "foreign"
			case "same-marker-without-intent":
				s.target.Marker = r.Marker()
			case "nonempty":
				s.pristine = false
			case "wrong-system":
				s.target.SystemIdentifier = "1"
			case "wrong-oid":
				s.target.DatabaseOID++
			case "wrong-database":
				s.target.Database = "foreign"
			case "wrong-operator":
				s.target.Operator = "foreign"
			case "wrong-version":
				s.target.ServerVersion = 180005
			case "not-super":
				s.super = false
			case "not-owner":
				s.owner = false
			case "not-primary":
				s.primary = false
			case "not-ordinary":
				s.ordinary = false
			case "no-lease":
				s.held = false
			case "other-client":
				s.exclusive = false
			case "unpinned":
				r.ExpectedSystemIdentifier = ""
				r.ExpectedDatabaseOID = 0
			case "changed-preimage":
				s.onBegin = func() { s.target.Marker = "foreign" }
			case "changed-input":
				check = func() error { return errors.New("synthetic input changed") }
			}
			if _, e := initializeRestoreTargetSession(context.Background(), s, r, inputs, check); e == nil || s.execCalls != 0 {
				t.Fatal("unsafe target changed", e, s.execCalls)
			}
		})
	}
}

func TestPostgresRestoreTargetInitializationDiscoveryAndRecordCustody(t *testing.T) {
	r, s, inputs := targetInitializationProtocolFixture(t)
	check := func() error { return nil }
	r.ExpectedSystemIdentifier = ""
	r.ExpectedDatabaseOID = 0
	out, e := inspectRestoreTargetSession(context.Background(), s, r, inputs, check)
	if e != nil || !out.ValidFor(r, true) || out.IdentityPinned || out.OriginalIntentPresent || out.State != "uninitialized" || s.execCalls != 0 {
		t.Fatal("readonly unpinned discovery", e)
	}
	if _, e = os.Lstat(r.InitializationRoot); !os.IsNotExist(e) {
		t.Fatal("discovery created journal")
	}
	s.target.Marker = r.Marker()
	if _, e = inspectRestoreTargetSession(context.Background(), s, r, inputs, check); e == nil {
		t.Fatal("marker adopted without original intent")
	}
	s.target.Marker = ""
	r.ExpectedSystemIdentifier = s.target.SystemIdentifier
	r.ExpectedDatabaseOID = s.target.DatabaseOID
	if _, e = initializeRestoreTargetSession(context.Background(), s, r, inputs, check); e != nil {
		t.Fatal(e)
	}
	j, e := openTargetInitializationJournal(r.InitializationRoot, false)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = inspectRestoreTargetSession(context.Background(), s, r, inputs, check); e == nil {
		t.Fatal("concurrent journal consumer ignored")
	}
	if e = j.close(); e != nil {
		t.Fatal(e)
	}
	retained := r.InitializationRoot + "-retained"
	if e = os.Rename(r.InitializationRoot, retained); e != nil {
		t.Fatal(e)
	}
	if e = os.Mkdir(r.InitializationRoot, 0700); e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{targetInitializationIntentName, targetInitializationCompleteName} {
		raw, e := os.ReadFile(filepath.Join(retained, name))
		if e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(filepath.Join(r.InitializationRoot, name), raw, 0600); e != nil {
			t.Fatal(e)
		}
	}
	if _, e = inspectRestoreTargetSession(context.Background(), s, r, inputs, check); e == nil {
		t.Fatal("equal-byte replacement journal root adopted")
	}
}

func TestPostgresRestoreTargetInitializationPolicyIsExplicit(t *testing.T) {
	r, _, _ := targetInitializationProtocolFixture(t)
	raw := []byte("# Explicit original initializer policy\n" + targetInitializationPolicyPrefix + "OPERATION_SECONDS=41\n" + targetInitializationPolicyPrefix + "MINIMUM_FREE_BYTES=2097152\n")
	if e := applyTargetInitializationPolicy(&r.Policy, raw); e != nil || r.Policy.OperationSeconds != 41 || r.Policy.MinimumFreeBytes != 2097152 {
		t.Fatal("typed policy overlay", e)
	}
	for _, suffix := range []string{"OPERATION_SECONDS=false\n", "OPERATION_SECONDS=\n", "OPERATION_SECONDS=041\n", "UNKNOWN=1\n", "MINIMUM_FREE_BYTES=-1\n", "OPERATION_SECONDS=41\n" + targetInitializationPolicyPrefix + "OPERATION_SECONDS=42\n"} {
		p := r.Policy
		if applyTargetInitializationPolicy(&p, []byte(targetInitializationPolicyPrefix+suffix)) == nil {
			t.Fatal("invalid policy accepted", suffix)
		}
	}
}
