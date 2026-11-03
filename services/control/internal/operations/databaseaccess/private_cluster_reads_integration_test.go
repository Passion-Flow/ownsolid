//go:build repository_integration && privatecluster_schema_integration

package databaseaccess

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/licensing"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/privateclusters"
	clusterhttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/privateclusters/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/repositoryfixture"
	licensewire "github.com/Passion-Flow/ownsolid/pkg/license/v1"
)

// This instrumentation only counts calls delegated to the actual selected SQL
// transaction. It returns no synthetic row, outcome, permission or provider fact.
type privateClusterReadStore struct {
	transaction.Store
	queries int
}
type privateClusterReadExecutor struct {
	transaction.Executor
	owner *privateClusterReadStore
}

func (q *privateClusterReadExecutor) Query(ctx context.Context, s transaction.Statement) (transaction.Rows, error) {
	q.owner.queries++
	return q.Executor.Query(ctx, s)
}
func (s *privateClusterReadStore) Within(ctx context.Context, readOnly bool, fn func(context.Context, transaction.Executor) error) error {
	return s.Store.Within(ctx, readOnly, func(ctx context.Context, q transaction.Executor) error {
		return fn(ctx, &privateClusterReadExecutor{q, s})
	})
}

type privateClusterReadStory struct {
	t             *testing.T
	ctx           context.Context
	f             repositoryfixture.Fixture
	store         *privateClusterReadStore
	accounts      *identity.Service
	workspaces    *workspaces.Service
	license       *licensing.Service
	auth          identity.Auth
	configuration privateclusters.Config
	service       *privateclusters.Service
	ids           []string
}

func newPrivateClusterReadStory(t *testing.T, ctx context.Context, f repositoryfixture.Fixture) *privateClusterReadStory {
	t.Helper()
	store := &privateClusterReadStore{Store: f.Store}
	accounts := &identity.Service{Storage: store, HMACKey: make([]byte, 32)}
	if _, e := rand.Read(accounts.HMACKey); e != nil {
		t.Fatal(e)
	}
	policy := config.DefaultRuntimePolicy()
	accounts.Config.Runtime = &policy
	accounts.Config.Server.AdminOrigin = "https://admin.example.invalid"
	accounts.Config.Server.PortalOrigin = "https://portal.example.invalid"
	var issued identity.IssuedSession
	if e := store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
		var e error
		issued, e = accounts.IssueSessionWithin(ctx, q, f.User, 1, "admin", "", uuid.NewString(), nil)
		return e
	}); e != nil {
		t.Fatal(e)
	}
	auth, e := accounts.Authenticate(ctx, issued.Token, "admin")
	if e != nil {
		t.Fatal(e)
	}
	_, trust, e := licensewire.GenerateKey("explicit-m28-read-budget-only")
	if e != nil {
		t.Fatal(e)
	}
	license, e := licensing.New(accounts, trust, licensewire.DefaultPolicy(), func() time.Time { return time.Now().UTC() })
	if e != nil {
		t.Fatal(e)
	}
	accounts.BusinessAdmission = license
	state, e := license.StatusAdmin(ctx, auth)
	if e != nil {
		t.Fatal(e)
	}
	// No license artifact is imported: these existing reads retain current
	// authority while new business is unavailable. No provider method is invoked.
	c := privateclusters.Defaults()
	c.Policy.MaximumBodyBytes = privateclusters.MinimumBodyBytes
	c.Policy.PageSize = 2
	c.Policy.RequestSeconds, c.Policy.OperationSeconds = 2, 8
	c.Verification.MountRoot = "/synthetic/m28-read-budget"
	c.Verification.TrustFile = c.Verification.MountRoot + "/nonexistent-trust.json"
	g := &privateClusterReadStory{t: t, ctx: ctx, f: f, store: store, accounts: accounts, license: license, auth: auth, configuration: c}
	g.workspaces = &workspaces.Service{Identity: accounts}
	cluster := uuid.NewString()
	hash := strings.Repeat("a", 64)
	for index := range 3 {
		suffix := fmt.Sprint(index)
		namespace := uuid.NewString()
		p := privateclusters.Profile{ID: "synthetic-budget-" + suffix, Name: "Synthetic SQL budget profile " + suffix,
			ServerOrigin: "https://127.0.0.1:1", TLSServerName: "127.0.0.1", AllowedCIDRs: []string{"127.0.0.0/8"},
			CAFile: c.Verification.MountRoot + "/nonexistent-ca-" + suffix, TokenFile: c.Verification.MountRoot + "/nonexistent-token-" + suffix,
			ImageProofFile: c.Verification.MountRoot + "/nonexistent-proof-" + suffix,
			ClusterUID:     cluster, Namespace: "projectone-test-read-budget-" + suffix, NamespaceUID: namespace,
			ControlServiceAccountUID: uuid.NewString(), InstallationID: state.DeploymentID, WorkspaceID: f.Workspace,
			Profile: privateclusters.LocalARM64Profile, ImageRepository: "synthetic.example.invalid/probe"}
		g.configuration.Profiles = append(g.configuration.Profiles, p)
		observation := privateclusters.Observation{ServerVersion: privateclusters.ServerVersion, ClusterUID: cluster,
			NamespaceUID: namespace, Namespace: p.Namespace, QuotaUID: uuid.NewString(), PolicyUID: uuid.NewString(),
			ServiceAccountUID: p.ControlServiceAccountUID, ObservedAt: time.Now().UTC(),
			Qualification: "Synthetic SQL fixture only; no API, OCI, CNI or execution observation. " + strings.Repeat("q", 900)}
		for node := range 4 {
			observation.Nodes = append(observation.Nodes, privateclusters.NodeCapacity{UID: uuid.NewString(),
				Name: fmt.Sprintf("node-%d-%s.synthetic.invalid", node, strings.Repeat("n", 40)), CPU: "1", Memory: "1Gi", Architecture: "arm64"})
		}
		sort.Slice(observation.Nodes, func(i, j int) bool { return observation.Nodes[i].UID < observation.Nodes[j].UID })
		raw, e := json.Marshal(observation)
		if e != nil {
			t.Fatal(e)
		}
		id := uuid.NewString()
		g.ids = append(g.ids, id)
		_, e = f.PostgreSQL.Exec(ctx, `INSERT INTO private_cluster_connections(id,tenant_id,workspace_id,profile_id,cluster_uid,namespace_uid,namespace_name,installation_id,profile,config_sha256,credential_sha256,manifest_sha256,image_digest,config_digest,state,revision,observation,created_by) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$10,$10,$11,$11,'active',1,$12::jsonb,$13)`, id, f.Tenant, f.Workspace, p.ID, cluster, namespace, p.Namespace, state.DeploymentID, p.Profile, hash, "sha256:"+hash, raw, f.User)
		if e != nil {
			t.Fatal("actual synthetic connection insert", e)
		}
	}
	g.reconstruct()
	return g
}
func (g *privateClusterReadStory) reconstruct() {
	g.t.Helper()
	var e error
	g.service, e = privateclusters.New(g.accounts, g.workspaces, g.license, g.configuration)
	if e != nil {
		g.t.Fatal("actual selected owner reconstruction, no provider credential read", e)
	}
}
func (g *privateClusterReadStory) assertRows() {
	g.t.Helper()
	var count, revision int
	if e := g.f.PostgreSQL.QueryRow(g.ctx, `SELECT count(*),sum(revision) FROM private_cluster_connections`).Scan(&count, &revision); e != nil || count != 3 || revision != 3 {
		g.t.Fatal("read/recovery changed stored rows", count, revision, e)
	}
}
func privateClusterReadBudget(t *testing.T, ctx context.Context, f repositoryfixture.Fixture) {
	g := newPrivateClusterReadStory(t, ctx, f)
	out, e := g.service.List(ctx, g.auth, f.Workspace, "")
	if e == nil || e.Error() != "PRIVATE_CLUSTER_RESPONSE_LIMIT" || len(out.Items) != 0 || out.NextCursor != nil {
		t.Fatal("oversize page returned partial success", e)
	}
	handler := &clusterhttp.Handler{Service: g.service}
	r := httptest.NewRequest(http.MethodGet, "/api/v1/admin/private-clusters/connections?workspace_id="+f.Workspace, nil)
	data, status, e := handler.Handle(r, g.auth, uuid.NewString())
	if e == nil || e.Error() != "PRIVATE_CLUSTER_RESPONSE_LIMIT" || data != nil || status != 0 {
		t.Fatal("handler retained an oversized successful projection", status, e)
	}
	if _, e = g.service.List(ctx, g.auth, uuid.NewString(), ""); e == nil || e.Error() != "RESOURCE_NOT_FOUND" {
		t.Fatal("foreign Workspace read leaked", e)
	}
	g.assertRows()
	// The same selected owners/artifact recover through supported component
	// reconstruction with a smaller page; no remote state or database is reset.
	g.configuration.Policy.PageSize = 1
	g.reconstruct()
	seen := map[string]bool{}
	cursor := ""
	for range 4 {
		page, e := g.service.List(ctx, g.auth, f.Workspace, cursor)
		if e != nil || len(page.Items) != 1 {
			t.Fatal("nondefault page read", e)
		}
		if seen[page.Items[0].ID] {
			t.Fatal("cursor repeated an item")
		}
		seen[page.Items[0].ID] = true
		if page.NextCursor == nil {
			break
		}
		cursor = *page.NextCursor
	}
	if len(seen) != 3 {
		t.Fatal("page recovery lost retained history", len(seen))
	}
	g.configuration.Policy.PageSize = 2
	g.configuration.Policy.MaximumBodyBytes = 8192
	g.reconstruct()
	page, e := g.service.List(ctx, g.auth, f.Workspace, "")
	if e != nil || len(page.Items) != 2 || page.NextCursor == nil || g.service.MaximumResponseBytes() != 8192 {
		t.Fatal("nondefault byte-budget recovery", e)
	}
	handler.Service = g.service
	data, status, e = handler.Handle(r, g.auth, uuid.NewString())
	if e != nil || data == nil || status != 200 {
		t.Fatal("known fitting handler result refused", status, e)
	}
	g.assertRows()
}
func privateClusterReadiness(t *testing.T, ctx context.Context, f repositoryfixture.Fixture) {
	g := newPrivateClusterReadStory(t, ctx, f)
	g.configuration.Policy.PageSize = 100
	g.reconstruct()
	g.store.queries = 0
	if e := g.service.VerifyStored(ctx); e != nil {
		t.Fatal("actual complete graph refused", e)
	}
	// Three actual single-connection pages plus an empty page and three empty
	// streamed run queries. This observes delegation, never fabricates a result.
	if g.store.queries != 7 {
		t.Fatal("readiness retained an adjustable connection page", g.store.queries)
	}
	sort.Strings(g.ids)
	last := g.ids[len(g.ids)-1]
	if _, e := f.PostgreSQL.Exec(ctx, `UPDATE private_cluster_connections SET observation='{"unexpected":true}'::jsonb,revision=revision+1 WHERE id=$1`, last); e != nil {
		t.Fatal(e)
	}
	if e := g.service.VerifyStored(ctx); e == nil || e.Error() != "PRIVATE_CLUSTER_STORED_INVALID" {
		t.Fatal("later corrupt graph entry was skipped or repaired", e)
	}
	var retained bool
	if e := f.PostgreSQL.QueryRow(ctx, `SELECT observation='{"unexpected":true}'::jsonb FROM private_cluster_connections WHERE id=$1`, last).Scan(&retained); e != nil || !retained {
		t.Fatal("readiness silently rewrote corrupt input", e)
	}
}
