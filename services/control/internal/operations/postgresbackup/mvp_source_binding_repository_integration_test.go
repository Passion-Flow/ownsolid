//go:build repository_integration && postgres_mvp_binding_integration

package postgresbackup

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.yaml.in/yaml/v3"
	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/databaseaccess"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
	release "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// This focused native SQL case deliberately establishes binding/grant/content
// behavior only. It does not call pg_dump or claim populated four-store serving.
func TestActualMVPSourceBindingOriginalPrincipalAndFullContents(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	raw, e := config.Secret(os.Getenv("PROJECT_ONE_TEST_MVP_BINDING_DSN_FILE"))
	if e != nil {
		t.Fatal("protected native fixture required")
	}
	pc, e := pgxpool.ParseConfig(strings.TrimSpace(string(raw)))
	if e != nil || pc.ConnConfig.Host != "127.0.0.1" || pc.ConnConfig.Port < 44900 || pc.ConnConfig.Port > 44999 || pc.ConnConfig.User != "projectone_operator" || !strings.HasPrefix(pc.ConnConfig.Database, "p1_mvp_binding_") || !strings.HasSuffix(pc.ConnConfig.Database, "_test") {
		t.Fatal("exact owned loopback fixture required")
	}
	scope := strings.TrimSuffix(strings.TrimPrefix(pc.ConnConfig.Database, "p1_mvp_binding_"), "_test")
	if len(scope) != 32 {
		t.Fatal("owned UUID fixture required")
	}
	pc.MaxConns = 3
	pc.MinConns = 0
	pc.ConnConfig.RuntimeParams["search_path"] = "public"
	pool, e := pgxpool.NewWithConfig(ctx, pc)
	if e != nil {
		t.Fatal(e)
	}
	defer pool.Close()
	var marker string
	var tables int
	if pool.QueryRow(ctx, `SELECT pg_catalog.shobj_description((SELECT oid FROM pg_catalog.pg_database WHERE datname=pg_catalog.current_database()),'pg_database')`).Scan(&marker) != nil || marker != "projectone:mvp-native-binding:"+scope || pool.QueryRow(ctx, `SELECT count(*) FROM pg_catalog.pg_tables WHERE schemaname='public'`).Scan(&tables) != nil || tables != 0 {
		t.Fatal("fresh marked fixture required, never reset/replay")
	}
	if e = database.Migrate(ctx, pool); e != nil {
		t.Fatal(e)
	}
	lease, e := pool.Acquire(ctx)
	if e != nil {
		t.Fatal(e)
	}
	var held bool
	if lease.QueryRow(ctx, `SELECT pg_catalog.pg_try_advisory_lock($1)`, database.InstallationLockID).Scan(&held) != nil || !held {
		lease.Release()
		t.Fatal("original installation lease")
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		var unlocked bool
		if lease.QueryRow(cleanup, `SELECT pg_catalog.pg_advisory_unlock($1)`, database.InstallationLockID).Scan(&unlocked) != nil || !unlocked {
			t.Error("lease release unconfirmed")
		}
		lease.Release()
	}()
	f := newRestoreInputFixture(t)
	profile := database.LineageName
	endpoint, e := url.Parse(strings.TrimSpace(string(raw)))
	if e != nil {
		t.Fatal(e)
	}
	credential, e := databaseaccess.NewCredential()
	if e != nil {
		t.Fatal(e)
	}
	role := "mvp_binding_" + scope
	endpoint.User = url.UserPassword(role, credential)
	query := endpoint.Query()
	query.Set("search_path", "public")
	endpoint.RawQuery = query.Encode()
	if e = os.WriteFile(f.runtime.Database.DSNFile, []byte(endpoint.String()), 0600); e != nil {
		t.Fatal(e)
	}
	for path, c := range map[string]config.Config{f.operatorPath: f.operator, f.request.RuntimeConfigurationFile: f.runtime} {
		b, e := yaml.Marshal(c)
		if e != nil || os.WriteFile(path, b, 0600) != nil {
			t.Fatal("protected typed configuration")
		}
	}
	input, e := databaseaccess.LoadRuntimeWithEnvironment(f.request.RuntimeConfigurationFile, "")
	if e != nil {
		t.Fatal(e)
	}
	if status, e := databaseaccess.Prepare(ctx, pool, lease, input); e != nil || status != databaseaccess.Verified {
		t.Fatal("original exact principal producer", status, e)
	}
	tenant, workspace, user := uuid.NewString(), uuid.NewString(), uuid.NewString()
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO public.tenants(id,name,status) VALUES($1,'Synthetic native binding tenant','active')`, []any{tenant}},
		{`INSERT INTO public.workspaces(id,tenant_id,name,status) VALUES($1,$2,'Synthetic native binding Workspace','active')`, []any{workspace, tenant}},
		{`INSERT INTO public.users(id,email_canonical,email_display,display_name,locale,status,is_platform_admin) VALUES($1,'binding@example.invalid','binding@example.invalid','Synthetic binding actor','en','active',true)`, []any{user}},
		{`UPDATE public.installation_state SET bootstrap_complete=true,tenant_id=$1,default_workspace_id=$2,primary_admin_user_id=$3,bootstrapped_at=clock_timestamp() WHERE id=1`, []any{tenant, workspace, user}},
	} {
		if _, e = pool.Exec(ctx, q.sql, q.args...); e != nil {
			t.Fatal(e)
		}
	}
	r := f.manifest.Request
	r.RuntimeConfigurationFile = f.request.RuntimeConfigurationFile
	r.RuntimeEnvironmentFile = ""
	r.ConfigurationRoot = f.request.ConfigurationRoot
	r.Policy = f.request.Policy
	r.ApplicationProfile = release.MVPPostgreSQLCPUApplicationProfile
	r.Database = pc.ConnConfig.Database
	r.Operator = pc.ConnConfig.User
	r.TenantID = tenant
	if !r.Valid() {
		t.Fatal("complete original request required")
	}
	if e = verifyMVPPrincipal(ctx, pool, lease, r); e != nil {
		t.Fatal("same selected principal verification", e)
	}
	var binding *MVPSourceBinding
	data, e := withDatabaseSnapshot(ctx, lease, r, func(tx pgx.Tx, data DatabaseReadback) error {
		var e error
		binding, e = readMVPSourceBinding(ctx, tx, r)
		if e != nil {
			return e
		}
		var actual MVPSourceMetadata
		var pid int
		if tx.QueryRow(ctx, `SELECT pg_catalog.current_setting('server_version_num')::integer,(SELECT extversion FROM pg_catalog.pg_extension WHERE extname='vector'),pg_catalog.pg_backend_pid()`).Scan(&actual.ServerVersionNumber, &actual.VectorVersion, &pid) != nil || binding.SourceMetadata == nil || *binding.SourceMetadata != actual || pid != binding.BackendPID || pid != data.BackendPID {
			return errors.New("MVP_SOURCE_ACTUAL_METADATA_SAME_SNAPSHOT_REQUIRED")
		}
		return nil
	})
	if e != nil || !matchesDatabaseProfile(profile, data) || binding == nil || binding.RuntimePrincipal != role || binding.BackendPID != data.BackendPID {
		t.Fatal("actual native source binding/content", e)
	}
	if e = validMVPSourceBinding(Manifest{Request: r, Database: data, MVPSource: binding}); e != nil {
		t.Fatal(e)
	}
	if e = recheckMVPSourceBinding(ctx, lease, r, binding); e != nil {
		t.Fatal("current original binding changed", e)
	}
	foreign := *binding
	changed := *binding.SourceMetadata
	changed.VectorVersion = "0.8.6"
	foreign.SourceMetadata = &changed
	if e = recheckMVPSourceBinding(ctx, lease, r, &foreign); e == nil {
		t.Fatal("foreign source facts accepted")
	}
	if _, e = pool.Exec(ctx, `REVOKE INSERT ON public.private_cluster_intents FROM `+pgx.Identifier{role}.Sanitize()); e != nil {
		t.Fatal(e)
	}
	if e = verifyMVPPrincipal(ctx, pool, lease, r); e == nil {
		t.Fatal("partial new grant set admitted")
	}
	if _, e = pool.Exec(ctx, `GRANT INSERT ON public.private_cluster_intents TO `+pgx.Identifier{role}.Sanitize()); e != nil {
		t.Fatal(e)
	}
	if e = verifyMVPPrincipal(ctx, pool, lease, r); e != nil {
		t.Fatal(e)
	}
	var after int
	if e = lease.QueryRow(ctx, `SELECT pg_catalog.pg_backend_pid()`).Scan(&after); e != nil || after != binding.BackendPID {
		t.Fatal("original session replaced")
	}
	if out := os.Getenv("PROJECT_ONE_TEST_MVP_BINDING_RECEIPT"); out != "" {
		b, e := json.MarshalIndent(map[string]any{"profile": profile, "data": data, "binding": binding, "grant_tamper_refused": true, "one_original_backend": true, "qualification": "Actual native principal/content binding only, no pg_dump/four-store or served acceptance"}, "", "  ")
		if e != nil || filepath.Clean(out) != out || os.WriteFile(out, b, 0600) != nil {
			t.Fatal("private focused receipt")
		}
	}
	t.Logf("actual native profile=%s version=%d tables=%d rows=%d original_backend=%d principalOID=%d", profile, data.SchemaVersion, data.Tables, data.Rows, binding.BackendPID, binding.PrincipalOID)
}
