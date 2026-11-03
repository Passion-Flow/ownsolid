//go:build cgo && odbc_manager && repository_integration && dameng_deployments_integration

package deployments

import (
	"context"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/licensing"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/models"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/dameng"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/repositoryfixture"
	licensewire "github.com/Passion-Flow/ownsolid/pkg/license/v1"
	trainwire "github.com/Passion-Flow/ownsolid/api/training/v1"
)

var nativeDeploymentConfiguration = flag.String("dameng-deployments-config", "", "protected native connector file reserved by the coordinator")
var nativeDeploymentFixtureFile = flag.String("dameng-deployments-fixture", "", "protected synthetic current schema/identity/License/node/model fixture manifest")
var nativeDeploymentDisposable = flag.Bool("dameng-deployments-disposable", false, "explicit authorization to mutate the root-reserved synthetic fixture")

// The coordinator provisions this fixture through actual current migrations and
// authority owners. The test never seeds a substitute auth/License/node schema,
// opens another vendor pool, starts a server, or accepts customer credentials.
type nativeDeploymentFixture struct {
	FixtureID          string `json:"fixture_id"`
	Tenant             string `json:"tenant_id"`
	Workspace          string `json:"workspace_id"`
	User               string `json:"user_id"`
	Node               string `json:"node_id"`
	Model              string `json:"model_id"`
	GPU                string `json:"gpu_uuid"`
	HMACKeyFile        string `json:"hmac_key_file"`
	AdminTokenFile     string `json:"admin_token_file"`
	NodeCredentialFile string `json:"node_credential_file"`
	LicenseTrustFile   string `json:"license_trust_file"`
}

func nativeDeploymentPrivate(t *testing.T, path string, maximum int64) []byte {
	t.Helper()
	st, e := os.Lstat(path)
	if e != nil || !filepath.IsAbs(path) || filepath.Clean(path) != path || !st.Mode().IsRegular() || st.Mode().Perm()&0077 != 0 || st.Size() < 1 || st.Size() > maximum {
		t.Fatal("protected bounded synthetic fixture input required")
	}
	raw, e := os.ReadFile(path)
	if e != nil {
		t.Fatal("private fixture input unavailable")
	}
	return raw
}

func TestActualDamengDeploymentRuntimeCreateObserveReopenRelease(t *testing.T) {
	if !*nativeDeploymentDisposable {
		t.Fatal("explicit root-reserved disposable native fixture opt-in required")
	}
	var f nativeDeploymentFixture
	if trainwire.DecodeStrict(nativeDeploymentPrivate(t, *nativeDeploymentFixtureFile, 16<<10), &f) != nil || !strings.HasPrefix(f.FixtureID, "p1-20261103-M29-deployments-") || !id(strings.TrimPrefix(f.FixtureID, "p1-20261103-M29-deployments-")) {
		t.Fatal("exact synthetic fixture ownership required")
	}
	for _, v := range []string{f.Tenant, f.Workspace, f.User, f.Node, f.Model} {
		if !id(v) {
			t.Fatal("canonical fixture scope required")
		}
	}
	c, e := dameng.LoadConnectionConfiguration(*nativeDeploymentConfiguration, "")
	if e != nil || !strings.HasPrefix(c.Schema, "P1_M29_DEPLOYMENTS_") || c.Policy.MaximumColumns < 21 || c.Policy.MaximumSQLBytes < 8192 || c.Policy.MaximumCellBytes < 32768 {
		t.Fatal("root-reserved native schema and actual projection capacity required")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 240*time.Second)
	defer cancel()
	var kernel *dameng.Store
	closeCurrent := func() {
		if kernel == nil {
			return
		}
		cleanup, stop := context.WithTimeout(context.Background(), time.Duration(c.CleanupSeconds)*time.Second)
		defer stop()
		status, e := kernel.Close(cleanup)
		if e != nil || status.State != "closed" || status.PhysicalOwners != 0 || status.CleanupJobs != 0 || status.MemoryLeases != 0 || status.RetainedBytes != 0 {
			t.Fatal("native owners remain; retain exact fixture and original outcome")
		}
		kernel = nil
	}
	defer closeCurrent()
	openStory := func() deploymentRuntimeStory {
		var e error
		kernel, _, e = dameng.Open(ctx, c)
		if e != nil {
			t.Fatal("actual native connection refused", dameng.SafeError(e))
		}
		store, e := transaction.FromDameng(kernel)
		if e != nil {
			t.Fatal(e)
		}
		a := &identity.Service{Storage: store, HMACKey: nativeDeploymentPrivate(t, f.HMACKeyFile, 256)}
		if len(a.HMACKey) < 32 {
			t.Fatal("fixture HMAC key bound")
		}
		var trust licensewire.Trust
		if json.Unmarshal(nativeDeploymentPrivate(t, f.LicenseTrustFile, 4096), &trust) != nil {
			t.Fatal("synthetic License trust required")
		}
		lic, e := licensing.New(a, trust, licensewire.DefaultPolicy(), func() time.Time { return time.Now().UTC() })
		if e != nil {
			t.Fatal(e)
		}
		a.BusinessAdmission = lic
		w := &workspaces.Service{Identity: a}
		n := &nodes.Service{Identity: a, Workspaces: w}
		m := &models.Service{Identity: a, Workspaces: w}
		s := &Service{Nodes: n, Models: m}
		n.Inference = s
		admin, e := a.Authenticate(ctx, strings.TrimSpace(string(nativeDeploymentPrivate(t, f.AdminTokenFile, 4096))), "admin")
		if e != nil || !admin.Me.User.IsPlatformAdmin || admin.Me.User.ID != f.User {
			t.Fatal("actual current synthetic administrator required")
		}
		var name string
		if e := store.QueryRow(ctx, transaction.SQL(transaction.Dameng, `SELECT T."NAME" FROM "TENANTS" T JOIN "INSTALLATION_STATE" I ON I."TENANT_ID"=T."ID" WHERE I."ID"=1 AND I."BOOTSTRAP_COMPLETE"=1 AND T."ID"=?`, f.Tenant)).Scan(&name); e != nil || name != f.FixtureID {
			t.Fatal("isolated installed synthetic fixture mismatch")
		}
		current, e := n.Get(ctx, admin, f.Workspace, f.Node)
		if e != nil || current.TenantID != f.Tenant || current.ExecutionProfile != wire.DockerInferenceProfile {
			t.Fatal("actual synthetic inference node required")
		}
		g := deploymentRuntimeStory{f: repositoryfixture.Fixture{Store: store, Tenant: f.Tenant, Workspace: f.Workspace, User: f.User}, ctx: ctx, s: s, admin: admin, model: f.Model, gpu: f.GPU, node: wire.Enrolled{NodeID: f.Node, Credential: strings.TrimSpace(string(nativeDeploymentPrivate(t, f.NodeCredentialFile, 4096)))}}
		g.handshake = wire.Handshake{ProtocolVersion: current.ProtocolVersion, ExecutionProfile: current.ExecutionProfile, Capabilities: current.Capabilities, Facts: current.Facts}
		now := time.Now().UTC().Truncate(time.Microsecond)
		g.handshake.Facts.ObservedAt = now
		g.handshake.Inference = &wire.InferenceObservation{GPUs: []wire.NVIDIAGPU{{UUID: f.GPU, Name: "Synthetic native SQL GPU metadata", DriverVersion: "580.1", MemoryTotalMiB: 16384, MemoryFreeMiB: 16384, ComputeCapability: 8, MIGMode: "Disabled", ObservedAt: now}}, ObservedAt: now}
		g.heartbeat(t, g.node, g.handshake)
		return g
	}
	g := openStory()
	key := uuid.NewString()
	d, e := g.s.Create(ctx, g.admin, g.input(), key, uuid.NewString())
	if e != nil || d.ResourceState != "held" || d.LastTaskID == nil {
		t.Fatal("actual native atomic create", e)
	}
	replayed, e := g.s.Create(ctx, g.admin, g.input(), key, uuid.NewString())
	if e != nil || replayed.ID != d.ID || replayed.Revision != d.Revision || replayed.LastTaskID == nil || *replayed.LastTaskID != *d.LastTaskID {
		t.Fatal("actual native original-intent replay", e)
	}
	changed := g.input()
	changed.Reason = "Changed synthetic immutable native intent"
	if out, e := g.s.Create(ctx, g.admin, changed, key, uuid.NewString()); e == nil || e.Error() != "IDEMPOTENCY_CONFLICT" || out.ID != "" {
		t.Fatal("changed intent accepted or returned projection", e)
	}
	lease := g.claim(t)
	g.complete(t, lease, "running")
	ready, e := g.s.Get(ctx, g.admin, f.Workspace, d.ID)
	if e != nil || ready.Stale || ready.Observation == nil || !ready.Observation.Ready {
		t.Fatal("actual native observation reduction", e)
	}
	closeCurrent()
	g = openStory()
	reopened, e := g.s.Get(ctx, g.admin, f.Workspace, d.ID)
	if e != nil || reopened.Revision != d.Revision || reopened.ResourceState != "held" {
		t.Fatal("actual native store reopen", e)
	}
	unknown := g.wrapped(&deploymentRuntimeFaultStore{Store: g.f.Store, unknownRead: true})
	if out, e := unknown.s.Get(ctx, g.admin, f.Workspace, d.ID); e == nil || e.Error() != string(transaction.CommitUnknown) || out.ID != "" {
		t.Fatal("injected post-native-commit response uncertainty leaked a projection", e)
	}
	stopping := g.action(t, reopened, "stop")
	if stopping.ResourceState != "held" {
		t.Fatal("desired stop released physical ownership")
	}
	g.complete(t, g.claim(t), "stopped")
	stopped, e := g.s.Get(ctx, g.admin, f.Workspace, d.ID)
	if e != nil || stopped.ResourceState != "released" {
		t.Fatal("actual native confirmed stop release", e)
	}
	removing := g.action(t, stopped, "remove")
	g.complete(t, g.claim(t), "not_installed")
	removed, e := g.s.Get(ctx, g.admin, f.Workspace, d.ID)
	if e != nil || removed.Revision != removing.Revision || removed.ResourceState != "released" || removed.RemovedAt == nil || removed.ObservedState != "not_installed" {
		t.Fatal("actual native confirmed removal", e)
	}
	t.Log("Actual native owner transactions/immutable intent/observation/reopen/release only; all GPU and container facts are synthetic. Fixture retained for coordinator readback and exact schema cleanup:", f.FixtureID)
}

// This explicit fault wrapper preserves the original native provenance proof;
// only its test-only executor layer is removed before asking the real owner.
func (s *deploymentRuntimeFaultStore) OwnsDamengExecutor(q transaction.Executor) bool {
	if wrapped, ok := q.(*deploymentRuntimeFaultExecutor); ok {
		q = wrapped.Executor
	}
	owner, ok := s.Store.(interface {
		OwnsDamengExecutor(transaction.Executor) bool
	})
	return ok && owner.OwnsDamengExecutor(q)
}
