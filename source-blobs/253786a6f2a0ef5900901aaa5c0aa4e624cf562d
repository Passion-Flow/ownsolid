package deployments

import (
	"context"
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/models"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

type nativeRuntimeRow func(...any) error

func (r nativeRuntimeRow) Scan(dest ...any) error { return r(dest...) }

type nativeRuntimeProbe struct {
	transaction.Executor
	statements []transaction.Statement
	counts     []int64
	err        error
	rows       []nativeRuntimeRow
	dialect    transaction.Dialect
}

func (p *nativeRuntimeProbe) Dialect() transaction.Dialect {
	if p.dialect != "" {
		return p.dialect
	}
	return transaction.Dameng
}
func (p *nativeRuntimeProbe) Exec(_ context.Context, statement transaction.Statement) (int64, error) {
	p.statements = append(p.statements, statement)
	if p.err != nil {
		return 17, p.err
	}
	if len(p.counts) == 0 {
		return 1, nil
	}
	n := p.counts[0]
	p.counts = p.counts[1:]
	return n, nil
}
func (p *nativeRuntimeProbe) QueryRow(_ context.Context, statement transaction.Statement) transaction.Row {
	p.statements = append(p.statements, statement)
	if p.err != nil {
		return nativeRuntimeRow(func(...any) error { return p.err })
	}
	if len(p.rows) == 0 {
		return nativeRuntimeRow(func(...any) error { return &transaction.Error{Kind: transaction.NoRows} })
	}
	r := p.rows[0]
	p.rows = p.rows[1:]
	return r
}

type nativeRuntimePool struct {
	*nativeRuntimeProbe
	outcome error
}

func (p *nativeRuntimePool) OwnsDamengExecutor(q transaction.Executor) bool {
	return q == p.nativeRuntimeProbe
}
func (p *nativeRuntimePool) Within(context.Context, bool, func(context.Context, transaction.Executor) error) error {
	return p.outcome
}
func nativeRuntimeService(q *nativeRuntimeProbe) *Service {
	a := &identity.Service{Storage: &nativeRuntimePool{nativeRuntimeProbe: q}}
	w := &workspaces.Service{Identity: a}
	return &Service{Nodes: &nodes.Service{Identity: a, Workspaces: w}, Models: &models.Service{Identity: a, Workspaces: w}}
}

func TestNativeRuntimeSelectedOwnerAndNoFallback(t *testing.T) {
	q := &nativeRuntimeProbe{}
	s := nativeRuntimeService(q)
	if e := s.runtimeExecutor(t.Context(), q); e != nil {
		t.Fatal(e)
	}
	if e := s.runtimeExecutor(t.Context(), s.Nodes.Identity.Storage); e == nil {
		t.Fatal("native pool granted transaction authority")
	}
	if e := s.runtimeExecutor(t.Context(), &nativeRuntimeProbe{dialect: transaction.MySQL}); e == nil {
		t.Fatal("foreign selected executor")
	}
	s.Models.Identity = &identity.Service{}
	if e := s.runtimeExecutor(t.Context(), q); e == nil {
		t.Fatal("foreign model owner")
	}
	if e := nativeRuntimeService(q).runtimeExecutor(t.Context(), &nativeRuntimeProbe{}); e == nil {
		t.Fatal("same-dialect foreign kernel accepted")
	}
	if len(q.statements) != 0 {
		t.Fatal("owner rejection performed SQL")
	}
	if !reflect.DeepEqual(deploymentSQL(q, "pg-only", "mysql-only"), transaction.SQL(transaction.Dameng, "")) {
		t.Fatal("native fell through to PostgreSQL")
	}
	unknown := &nativeRuntimeProbe{dialect: "unknown"}
	if !reflect.DeepEqual(deploymentNativeSQL(unknown, "dm", "pg", "my"), transaction.SQL("unknown", "")) {
		t.Fatal("unknown dialect executed SQL")
	}
	for _, d := range []transaction.Dialect{transaction.PostgreSQL, transaction.MySQL} {
		q := &nativeRuntimeProbe{dialect: d}
		want := "pg"
		if d == transaction.MySQL {
			want = "my"
		}
		if !reflect.DeepEqual(deploymentNativeSQL(q, "dm", "pg", "my", "scope"), transaction.SQL(d, want, "scope")) {
			t.Fatal("existing engine changed")
		}
	}
}

func TestNativeRuntimeScalarAndPageBindings(t *testing.T) {
	q := &nativeRuntimeProbe{}
	value := "digest"
	var absent *string
	if !reflect.DeepEqual(deploymentNativeSQL(q, "native", "pg", "my", &value, absent), transaction.SQL(transaction.Dameng, "native", value, nil)) {
		t.Fatal("native text pointers reached driver")
	}
	raw := []byte(`{"revision":9007199254740990}`)
	if deploymentJSON(q, raw) != string(raw) {
		t.Fatal("JSON bound as binary")
	}
	if !reflect.DeepEqual(deploymentJSON(&nativeRuntimeProbe{dialect: transaction.PostgreSQL}, raw), raw) {
		t.Fatal("PG JSON changed")
	}
	for _, model := range []string{"", "model"} {
		for _, retained := range []bool{false, true} {
			text := "SELECT " + nativeRuntimeFields + nativeRuntimeJoins + ` WHERE D."TENANT_ID"=? AND D."WORKSPACE_ID"=?`
			args := []any{27, 27, "tenant", "workspace"}
			if model != "" {
				text += ` AND D."MODEL_ID"=?`
				args = append(args, model)
			}
			if retained {
				text += ` AND D."REMOVED_AT" IS NULL`
			}
			text += ` ORDER BY D."CREATED_AT" DESC,D."ID" DESC LIMIT 7`
			if !reflect.DeepEqual(nativeRuntimeList("tenant", "workspace", model, retained, 27, 7), transaction.SQL(transaction.Dameng, text, args...)) {
				t.Fatal("scope/filter/page binding order")
			}
		}
	}
	for _, limits := range [][2]int{{19, 7}, {301, 7}, {27, 0}, {27, 101}} {
		if !reflect.DeepEqual(nativeRuntimeList("t", "w", "", false, limits[0], limits[1]), transaction.SQL(transaction.Dameng, "")) {
			t.Fatal("unbounded page accepted")
		}
	}
}

func TestNativeRuntimeHardwareMutationDoesNotRetryOrCrossScope(t *testing.T) {
	n := nodes.Node{TenantID: "tenant", WorkspaceID: "workspace", ID: "node"}
	raw := []byte(`{"gpus":[]}`)
	for _, counts := range [][]int64{{1}, {0, 1}, {2}, {0, 0}, {0, 2}} {
		q := &nativeRuntimeProbe{counts: counts}
		e := nativeRecordHardware(t.Context(), q, n, raw)
		wantError := counts[0] == 2 || len(counts) == 2 && counts[1] != 1
		if (e != nil) != wantError {
			t.Fatal("hardware cardinality", counts, e)
		}
		want := []transaction.Statement{transaction.SQL(transaction.Dameng, `UPDATE "INFERENCE_NODES" SET "OBSERVATION"=?,"RECEIVED_AT"=SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)) WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=? AND "NODE_ID"=?`, string(raw), "tenant", "workspace", "node")}
		if counts[0] == 0 {
			want = append(want, transaction.SQL(transaction.Dameng, `INSERT INTO "INFERENCE_NODES"("TENANT_ID","WORKSPACE_ID","NODE_ID","OBSERVATION","RECEIVED_AT") VALUES(?,?,?,?,SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)))`, "tenant", "workspace", "node", string(raw)))
		}
		if !reflect.DeepEqual(q.statements, want) {
			t.Fatal("native hardware scope/binding/first insertion")
		}
	}
	for _, kind := range []transaction.ErrorKind{transaction.UniqueConflict, transaction.Deadlock, transaction.CommitUnknown} {
		original := &transaction.Error{Kind: kind}
		q := &nativeRuntimeProbe{err: original}
		if e := nativeRecordHardware(t.Context(), q, n, raw); e != original || len(q.statements) != 1 {
			t.Fatal("poisoned native update retried")
		}
		q = &nativeRuntimeProbe{err: original}
		if n, e := deploymentExecOne(t.Context(), q, transaction.SQL(transaction.Dameng, "INSERT")); n != 0 || e != original || len(q.statements) != 1 {
			t.Fatal("partial failed count/retry")
		}
	}
	for _, count := range []int64{-1, 0, 2} {
		q := &nativeRuntimeProbe{counts: []int64{count}}
		if n, e := deploymentExecOne(t.Context(), q, transaction.SQL(transaction.Dameng, "INSERT")); n != 0 || e == nil {
			t.Fatal("non-single native insert")
		}
	}
}

func nativeRuntimeDeploymentRow(d Deployment, raw []byte) nativeRuntimeRow {
	return func(dest ...any) error {
		values := []any{d.ID, d.WorkspaceID, d.NodeID, d.ModelID, d.GPUUUID, d.DesiredState, d.ObservedState, d.Revision, raw, d.ObservedAt, d.LastTaskID, d.TaskState, d.TaskErrorCode, d.TaskDeadlineAt, d.ResourceState, d.Stale, d.CreatedAt, d.RemovedAt, d.ModelKind, d.CatalogDigest, d.ServingRecipeSHA256}
		for i, value := range values {
			reflect.ValueOf(dest[i]).Elem().Set(reflect.ValueOf(value))
		}
		return nil
	}
}
func TestNativeRuntimeLockOrderAndRevisionReduction(t *testing.T) {
	stamp := time.Now().UTC().Truncate(time.Microsecond)
	d := Deployment{ID: "00000000-0000-4000-8000-000000000001", WorkspaceID: "workspace", NodeID: "node", ModelID: "00000000-0000-4000-8000-000000000002", GPUUUID: "GPU-00000000-0000-4000-8000-000000000003", DesiredState: "running", ObservedState: "unknown", Revision: 9007199254740990, ResourceState: "held", CreatedAt: stamp, ModelKind: "approved_base", CatalogDigest: wire.ModelCatalogDigest()}
	for _, revision := range []int64{d.Revision - 1, d.Revision, d.Revision + 1} {
		d.ObservedAt = &stamp
		o := wire.DeploymentObservation{DeploymentID: d.ID, ModelID: d.ModelID, GPUUUID: d.GPUUUID, CatalogDigest: d.CatalogDigest, Revision: revision, State: "unknown"}
		raw, _ := json.Marshal(o)
		q := &nativeRuntimeProbe{}
		out, e := runtimeScan(q, nativeRuntimeDeploymentRow(d, raw))
		if revision > d.Revision {
			if e == nil || out.ID != "" {
				t.Fatal("future observation accepted")
			}
			continue
		}
		if e != nil || out.Stale != (revision < d.Revision) {
			t.Fatal("exact native JSON revision reduction", e)
		}
	}
	q := &nativeRuntimeProbe{rows: []nativeRuntimeRow{
		func(dest ...any) error { *dest[0].(*string) = "node"; return nil },
		func(dest ...any) error { *dest[0].(*string) = d.ID; return nil }, nativeRuntimeDeploymentRow(d, nil),
	}}
	s := nativeRuntimeService(q)
	if _, e := s.nativeDeploymentWithin(t.Context(), q, "tenant", "workspace", d.ID, true); e != nil {
		t.Fatal(e)
	}
	want := []transaction.Statement{
		transaction.SQL(transaction.Dameng, `SELECT "NODE_ID" FROM "DEPLOYMENTS" WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=? AND "ID"=? FOR UPDATE`, "tenant", "workspace", d.ID),
		transaction.SQL(transaction.Dameng, `SELECT "DEPLOYMENT_ID" FROM "GPU_LEASES" WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=? AND "NODE_ID"=? AND "DEPLOYMENT_ID"=? FOR UPDATE`, "tenant", "workspace", "node", d.ID),
		transaction.SQL(transaction.Dameng, "SELECT "+nativeRuntimeFields+nativeRuntimeJoins+` WHERE D."TENANT_ID"=? AND D."WORKSPACE_ID"=? AND D."ID"=?`, 30, 30, "tenant", "workspace", d.ID),
	}
	if !reflect.DeepEqual(q.statements, want) {
		t.Fatal("native d -> g -> projection lock order/bindings")
	}
	q = &nativeRuntimeProbe{}
	if out, e := s.nativeDeploymentWithin(t.Context(), q, "tenant", "workspace", d.ID, true); e == nil || e.Error() != "RESOURCE_NOT_FOUND" || out.ID != "" || len(q.statements) != 1 {
		t.Fatal("missing deployment lock must stop projection")
	}
	d.CreatedAt = stamp.Add(time.Nanosecond)
	if out, e := runtimeScan(q, nativeRuntimeDeploymentRow(d, nil)); e == nil || out.ID != "" {
		t.Fatal("native precision was rounded")
	}
}

func TestNativeRuntimeUnknownAndCancellationHaveZeroProjection(t *testing.T) {
	for _, outcome := range []error{&transaction.Error{Kind: transaction.CommitUnknown}, errors.Join(context.Canceled, &transaction.Error{Kind: transaction.CommitUnknown}), context.Canceled} {
		q := &nativeRuntimeProbe{}
		s := nativeRuntimeService(q)
		s.Nodes.Identity.Storage.(*nativeRuntimePool).outcome = outcome
		a := identity.Auth{}
		a.Me.Audience = "admin"
		a.Me.User.IsPlatformAdmin = true
		if out, e := s.ListFiltered(t.Context(), a, "00000000-0000-4000-8000-000000000001", "", false); e == nil || out != nil {
			t.Fatal("failed list leaked a projection")
		}
		if out, e := s.Get(t.Context(), a, "", "00000000-0000-4000-8000-000000000001"); e == nil || out.ID != "" {
			t.Fatal("failed get leaked a projection")
		}
		if out, e := s.Node(t.Context(), a, "", "00000000-0000-4000-8000-000000000001"); e == nil || out.NodeID != "" {
			t.Fatal("failed node leaked a projection")
		}
		if len(q.statements) != 0 {
			t.Fatal("unknown outcome replayed business SQL")
		}
	}
}

// Inspect concrete native call sites so an added operation cannot quietly select
// PostgreSQL, omit a positional argument, or reintroduce native JSON coercion.
func TestNativeRuntimeConcreteStatementInventory(t *testing.T) {
	count := 0
	for _, path := range []string{"runtime_repository.go", "intent_repository.go", "observation_repository.go"} {
		file, e := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if e != nil {
			t.Fatal(e)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			name, ok := call.Fun.(*ast.Ident)
			if !ok || name.Name != "deploymentNativeSQL" {
				return true
			}
			literal, ok := call.Args[1].(*ast.BasicLit)
			if !ok {
				t.Fatal("native statement must be explicit")
			}
			text, e := strconv.Unquote(literal.Value)
			if e != nil {
				t.Fatal(e)
			}
			if text == "" || strings.Count(text, "?") != len(call.Args)-4 {
				t.Fatalf("native positional binding mismatch: %s", path)
			}
			for _, foreign := range []string{"$1", "::", "clock_timestamp", "UTC_TIMESTAMP", "JSON_EXTRACT", "JSON_UNQUOTE", "ON CONFLICT", "ON DUPLICATE"} {
				if strings.Contains(text, foreign) {
					t.Fatal("foreign SQL in native operation", path)
				}
			}
			if strings.HasPrefix(text, `INSERT INTO "DEPLOYMENTS"`) && (!strings.Contains(text, `"CREATED_AT","UPDATED_AT"`) || strings.Count(text, "SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6))") != 2) {
				t.Fatal("native creation inherited timezone-dependent defaults")
			}
			if strings.HasPrefix(text, `INSERT INTO "GPU_LEASES"`) && !strings.Contains(text, `"UPDATED_AT"`) {
				t.Fatal("native lease inherited timezone-dependent default")
			}
			count++
			return true
		})
	}
	if count != 16 {
		t.Fatalf("review every concrete operation; got %d", count)
	}
}

func TestNativeRuntimeHeartbeatCannotReleaseOrPromoteRevision(t *testing.T) {
	stamp := time.Now().UTC().Truncate(time.Microsecond)
	n := nodes.Node{TenantID: "00000000-0000-4000-8000-000000000010", WorkspaceID: "00000000-0000-4000-8000-000000000011", ID: "00000000-0000-4000-8000-000000000012", ExecutionProfile: wire.DockerInferenceProfile}
	for _, sample := range []struct {
		name      string
		revision  int64
		foreign   bool
		wantError bool
		updates   int
	}{
		{"current", 9, false, false, 2}, {"old", 8, false, false, 1}, {"future", 10, false, true, 0}, {"foreign_model", 9, true, true, 0},
	} {
		t.Run(sample.name, func(t *testing.T) {
			d := wire.DeploymentObservation{DeploymentID: "00000000-0000-4000-8000-000000000001", ModelID: "00000000-0000-4000-8000-000000000002", GPUUUID: "GPU-00000000-0000-4000-8000-000000000003", CatalogDigest: wire.ModelCatalogDigest(), Revision: sample.revision, State: "unknown"}
			q := &nativeRuntimeProbe{rows: []nativeRuntimeRow{func(dest ...any) error {
				model := d.ModelID
				if sample.foreign {
					model = "00000000-0000-4000-8000-000000000004"
				}
				*dest[0].(*string) = model
				*dest[1].(*string) = d.GPUUUID
				*dest[2].(*int64) = 9
				*dest[3].(*string) = "approved_base"
				*dest[4].(*string) = d.CatalogDigest
				*dest[5].(**string) = nil
				return nil
			}}}
			s := nativeRuntimeService(q)
			o := wire.InferenceObservation{GPUs: []wire.NVIDIAGPU{}, Deployment: &d, ObservedAt: stamp}
			e := s.ObserveInferenceWithin(t.Context(), q, n, o)
			if (e != nil) != sample.wantError || len(q.statements) != sample.updates+1 {
				t.Fatal("heartbeat revision reduction or mutation boundary", e)
			}
			if sample.revision == 9 && !sample.foreign {
				raw, _ := json.Marshal(d)
				want := deploymentNativeSQL(q, `UPDATE "DEPLOYMENTS" SET "OBSERVED_STATE"=?,"OBSERVATION"=?,"OBSERVED_AT"=SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)),"UPDATED_AT"=SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)) WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=? AND "NODE_ID"=? AND "ID"=? AND "REVISION"=? AND "REMOVED_AT" IS NULL`, "", "", d.State, string(raw), n.TenantID, n.WorkspaceID, n.ID, d.DeploymentID, int64(9))
				if !reflect.DeepEqual(q.statements[1], want) {
					t.Fatal("observation JSON/scope/revision binding")
				}
			}
		})
	}
}

func TestNativeRuntimeTaskJSONRefusesMalformedOrChangedIntent(t *testing.T) {
	for _, raw := range []string{`{`, `{"deployment_id":"not-canonical"}`, `{"unknown":true}`, `{} {}`} {
		q := &nativeRuntimeProbe{rows: []nativeRuntimeRow{func(dest ...any) error { *dest[0].(*[]byte) = []byte(raw); return nil }}}
		s := nativeRuntimeService(q)
		if out, e := s.nativeDeploymentByTaskWithin(t.Context(), q, nodes.Task{}); e == nil || out.ID != "" || len(q.statements) != 1 {
			t.Fatal("invalid native task JSON admitted")
		}
	}
}

func TestNativeRuntimeStrictNativeObservationAndTaskObjects(t *testing.T) {
	stamp := time.Now().UTC().Truncate(time.Microsecond)
	d := Deployment{ID: "00000000-0000-4000-8000-000000000001", ModelID: "00000000-0000-4000-8000-000000000002", GPUUUID: "GPU-00000000-0000-4000-8000-000000000003", Revision: 1, CreatedAt: stamp, ObservedAt: &stamp, ModelKind: "approved_base", CatalogDigest: wire.ModelCatalogDigest()}
	o := wire.DeploymentObservation{DeploymentID: d.ID, ModelID: d.ModelID, GPUUUID: d.GPUUUID, CatalogDigest: d.CatalogDigest, Revision: 1, State: "unknown"}
	raw, _ := json.Marshal(o)
	for _, invalid := range []string{strings.TrimSuffix(string(raw), "}") + `,"revision":1}`, strings.TrimSuffix(string(raw), "}") + `,"extra":true}`, `null`} {
		if out, e := runtimeScan(&nativeRuntimeProbe{}, nativeRuntimeDeploymentRow(d, []byte(invalid))); e == nil || out.ID != "" {
			t.Fatal("non-exact native observation object admitted")
		}
	}
	c := wire.InferenceCommand{Action: "deploy", DeploymentID: d.ID, ModelID: d.ModelID, GPUUUID: d.GPUUUID, Revision: 1, CatalogDigest: d.CatalogDigest}
	raw, _ = json.Marshal(c)
	invalid := strings.TrimSuffix(string(raw), "}") + `,"deployment_id":"` + d.ID + `"}`
	q := &nativeRuntimeProbe{rows: []nativeRuntimeRow{func(dest ...any) error { *dest[0].(*[]byte) = []byte(invalid); return nil }}}
	if out, e := nativeRuntimeService(q).nativeDeploymentByTaskWithin(t.Context(), q, nodes.Task{Operation: c.Operation()}); e == nil || out.ID != "" || len(q.statements) != 1 {
		t.Fatal("duplicate native task identity admitted")
	}
	stamp = stamp.In(time.FixedZone("wrong-driver-zone", 3600))
	d.CreatedAt = stamp
	if out, e := runtimeScan(&nativeRuntimeProbe{}, nativeRuntimeDeploymentRow(d, nil)); e == nil || out.ID != "" {
		t.Fatal("non-UTC native timestamp reinterpreted")
	}
}

func TestNativeRuntimeBorrowedReferencesAndFencesRequireSelectedKernel(t *testing.T) {
	q := &nativeRuntimeProbe{}
	s := nativeRuntimeService(q)
	foreign := &nativeRuntimeProbe{}
	if out, e := s.HasModelReferences(t.Context(), foreign, "tenant", "workspace", "model"); e == nil || out || len(foreign.statements) != 0 {
		t.Fatal("foreign native reference read")
	}
	if e := s.FenceInferenceNode(t.Context(), foreign, nodes.Node{}); e == nil || len(foreign.statements) != 0 {
		t.Fatal("foreign native fence mutation")
	}
	if e := InvalidateAuthoritiesWithin(t.Context(), s.Nodes.Identity.Storage); e == nil || len(q.statements) != 0 {
		t.Fatal("native restore accepted pool without transaction")
	}
}
