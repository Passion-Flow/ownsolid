//go:build training_artifact_integration && model_lifecycle_admission_integration && training_operator_integration

package training_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	agent "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/models"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/databaseaccess"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
)

type ssoUpgradeFile struct {
	Device, Inode, Links uint64
	Size, Modified       int64
	Mode                 uint32
	SmallSHA256          string
}

// The caller has just verified the complete actual accepted artifact bytes. This
// metadata proof avoids rehashing large unchanged files during native startup.
func ssoUpgradeFiles(t *testing.T, g preparationScenario) map[string]ssoUpgradeFile {
	t.Helper()
	out := map[string]ssoUpgradeFile{}
	for _, root := range []string{g.f.s.Models.RootDirectory, g.f.s.Identity.Config.Datasets.ArtifactRoot, g.f.s.Identity.Config.Training.ArtifactRoot, g.f.s.Identity.Config.ModelPreparation.ArtifactRoot} {
		if e := filepath.WalkDir(root, func(path string, entry fs.DirEntry, e error) error {
			if e != nil {
				return e
			}
			info, e := entry.Info()
			if e != nil {
				return e
			}
			if info.Mode()&os.ModeSymlink != 0 {
				return os.ErrInvalid
			}
			if !info.Mode().IsRegular() {
				return nil
			}
			stat, ok := info.Sys().(*syscall.Stat_t)
			if !ok {
				return os.ErrInvalid
			}
			value := ssoUpgradeFile{Device: uint64(stat.Dev), Inode: stat.Ino, Links: uint64(stat.Nlink), Size: info.Size(), Modified: info.ModTime().UnixNano(), Mode: uint32(info.Mode())}
			if info.Size() <= 1<<20 {
				raw, e := os.ReadFile(path)
				if e != nil {
					return e
				}
				sum := sha256.Sum256(raw)
				value.SmallSHA256 = hex.EncodeToString(sum[:])
			}
			out[path] = value
			return nil
		}); e != nil {
			t.Fatal("owned four-store file identity", e)
		}
	}
	return out
}

func upgradePopulatedM10ToSSO(t *testing.T, g preparationScenario) {
	t.Helper()
	if os.Getenv("PROJECT_ONE_M11_NATIVE_UPGRADE") == "1" {
		upgradePopulatedCumulative(t, g, "lifecycle", "sso", os.Getenv("PROJECT_ONE_M10_CONTROL_BINARY"), 63, 16)
	}
}

func upgradePopulatedM11ToKnowledge(t *testing.T, g preparationScenario) {
	t.Helper()
	if os.Getenv("PROJECT_ONE_M12_NATIVE_UPGRADE") == "1" {
		upgradePopulatedCumulative(t, g, "sso", "knowledge", os.Getenv("PROJECT_ONE_M11_CONTROL_BINARY"), 67, 17)
	}
}

func upgradePopulatedM12ToWorkflows(t *testing.T, g preparationScenario) {
	t.Helper()
	if os.Getenv("PROJECT_ONE_M13_NATIVE_UPGRADE") == "1" {
		upgradePopulatedCumulative(t, g, "knowledge", "workflows", os.Getenv("PROJECT_ONE_M12_CONTROL_BINARY"), 74, 18)
	}
}

func upgradePopulatedM13ToPlugins(t *testing.T, g preparationScenario) {
	t.Helper()
	if os.Getenv("PROJECT_ONE_M14_NATIVE_UPGRADE") == "1" {
		upgradePopulatedCumulative(t, g, "workflows", "plugins", os.Getenv("PROJECT_ONE_M13_CONTROL_BINARY"), 80, 19)
	}
}

func upgradePopulatedM14ToPortal(t *testing.T, g preparationScenario) {
	t.Helper()
	if os.Getenv("PROJECT_ONE_M15_NATIVE_UPGRADE") == "1" {
		upgradePopulatedCumulative(t, g, "plugins", "portal", os.Getenv("PROJECT_ONE_M14_CONTROL_BINARY"), 84, 20)
	}
}

func upgradePopulatedM15ToLocalization(t *testing.T, g preparationScenario) {
	t.Helper()
	if os.Getenv("PROJECT_ONE_M16_NATIVE_UPGRADE") != "1" {
		return
	}
	a := g.f.s.Identity
	actor := fixture.Login(t, a, "admin@example.invalid", g.f.password, "portal")
	if _, e := a.UpdateMe(t.Context(), actor, actor.Me.User.DisplayName, "zh-CN", actor.Me.User.Version, uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	upgradePopulatedCumulative(t, g, "portal", "localization", os.Getenv("PROJECT_ONE_M15_CONTROL_BINARY"), 86, 21)
	// The operator has quiesced the predecessor. Reopen this fixture pool's
	// connections as a restarted application does: account22 changes the
	// locale typmod, so predecessor prepared result plans are incompatible.
	// This is an explicit upgrade boundary, never a mutation retry.
	g.f.s.DB.Reset()
}

func upgradePopulatedCumulative(t *testing.T, g preparationScenario, from, target, prior string, priorTables, priorEntries int) {
	t.Helper()
	if g.f.s.Identity.Config.Server.Profile() != from {
		t.Fatal("native upgrade must begin on exact predecessor", from)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	// Preserve a real8MiB resumable import separately from accepted model files.
	workspace, e := g.f.s.Workspaces.Create(ctx, g.f.admin, "Retained M10 partial import", g.f.admin.Me.User.ID, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	archive, e := os.Open(filepath.Join(filepath.Dir(os.Getenv("PROJECT_ONE_APPROVED_MODEL_DIRECTORY")), "qwen3-0.6b.tar"))
	if e != nil {
		t.Fatal(e)
	}
	info, e := archive.Stat()
	if e != nil {
		archive.Close()
		t.Fatal(e)
	}
	partial, e := g.f.s.Models.StartImport(ctx, g.f.admin, models.ImportRequest{WorkspaceID: workspace.ID, Name: "Retained actual M10 partial", CatalogDigest: agent.ModelCatalogDigest(), ExpectedBytes: info.Size(), LicenseAcknowledged: true, Reason: "Explicit cumulative upgrade preservation"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		archive.Close()
		t.Fatal(e)
	}
	chunk := make([]byte, agent.ModelTransferChunkBytes)
	_, e = io.ReadFull(archive, chunk)
	archive.Close()
	if e != nil {
		t.Fatal(e)
	}
	sum := sha256.Sum256(chunk)
	if _, e = g.f.s.Models.UploadChunk(ctx, g.f.admin, partial.ID, 0, int64(len(chunk)), hex.EncodeToString(sum[:]), bytes.NewReader(chunk)); e != nil {
		t.Fatal(e)
	}
	h := newNativeTrainingOperator(t, g.f)
	root := filepath.Dir(h.Configuration)
	raw, e := os.ReadFile(h.Configuration)
	if e != nil {
		t.Fatal(e)
	}
	var values map[string]any
	if json.Unmarshal(raw, &values) != nil {
		t.Fatal("protected predecessor config")
	}
	dsnBytes, e := config.Secret(values["database"].(map[string]any)["dsn_file"].(string))
	if e != nil {
		t.Fatal(e)
	}
	dsn, e := url.Parse(strings.TrimSpace(string(dsnBytes)))
	if e != nil {
		t.Fatal("protected predecessor DSN")
	}
	role := "m11_upgrade_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	credential, e := databaseaccess.NewCredential()
	if e != nil {
		t.Fatal(e)
	}
	runtime := *dsn
	runtime.User = url.UserPassword(role, credential)
	write := func(name string, raw []byte) string {
		path := filepath.Join(root, name)
		if e := os.WriteFile(path, raw, 0600); e != nil {
			t.Fatal(e)
		}
		return path
	}
	runtimeDSN := write("m11-runtime-dsn", []byte(runtime.String()))
	save := func(name string, runtime bool) string {
		raw, _ := json.Marshal(values)
		var copy map[string]any
		json.Unmarshal(raw, &copy)
		if runtime {
			copy["database"].(map[string]any)["dsn_file"] = runtimeDSN
		}
		raw, _ = json.Marshal(copy)
		return write(name, raw)
	}
	current := os.Getenv("PROJECT_ONE_CONTROL_BINARY")
	if !filepath.IsAbs(prior) || !filepath.IsAbs(current) {
		t.Fatal("accepted M10 and current native binary required")
	}
	run := func(binary, command, operator, runtime string) {
		args := []string{command, "--config", operator}
		if runtime != "" {
			args = append(args, "--runtime-config", runtime)
		}
		raw, e := exec.CommandContext(ctx, binary, args...).CombinedOutput()
		if strings.Contains(string(raw), runtimeDSN) || strings.Contains(string(raw), credential) || strings.Contains(string(raw), string(dsnBytes)) {
			t.Fatal("secret in native operator output")
		}
		if e != nil {
			t.Fatal("native operator failed", command, e)
		}
	}
	// Explicit disposable installed-role fixture; preparation correctly refuses a
	// populated database. The real accepted predecessor verifies its exact grants.
	if _, e = g.f.s.DB.Exec(ctx, "CREATE ROLE "+pgx.Identifier{role}.Sanitize()+" LOGIN NOINHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS CONNECTION LIMIT 8 PASSWORD '"+credential+"'"); e != nil {
		t.Fatal("owned installed role", e)
	}
	t.Cleanup(func() {
		if _, e := g.f.s.DB.Exec(context.Background(), "DROP OWNED BY "+pgx.Identifier{role}.Sanitize()+"; DROP ROLE "+pgx.Identifier{role}.Sanitize()); e != nil {
			t.Error("exact disposable upgrade role cleanup", e)
		}
	})
	schema := g.f.s.DB.Config().ConnConfig.RuntimeParams["search_path"]
	if _, e = g.f.s.DB.Exec(ctx, "GRANT CONNECT ON DATABASE "+pgx.Identifier{g.f.s.DB.Config().ConnConfig.Database}.Sanitize()+" TO "+pgx.Identifier{role}.Sanitize()+"; GRANT USAGE ON SCHEMA "+pgx.Identifier{schema}.Sanitize()+" TO "+pgx.Identifier{role}.Sanitize()+"; GRANT SELECT ON schema_migrations TO "+pgx.Identifier{role}.Sanitize()); e != nil {
		t.Fatal(e)
	}
	rows, e := g.f.s.DB.Query(ctx, "SELECT table_name,column_name FROM information_schema.columns WHERE table_schema=current_schema() ORDER BY table_name,ordinal_position")
	if e != nil {
		t.Fatal(e)
	}
	columns := map[string][]string{}
	for rows.Next() {
		var table, column string
		if e = rows.Scan(&table, &column); e != nil {
			t.Fatal(e)
		}
		columns[table] = append(columns[table], pgx.Identifier{column}.Sanitize())
	}
	rows.Close()
	if rows.Err() != nil || len(columns) != priorTables {
		t.Fatal("exact populated M10 prefix", len(columns), rows.Err())
	}
	for table := range columns {
		if table != "schema_migrations" {
			if _, e = g.f.s.DB.Exec(ctx, "GRANT SELECT,INSERT,UPDATE,DELETE ON "+pgx.Identifier{schema, table}.Sanitize()+" TO "+pgx.Identifier{role}.Sanitize()); e != nil {
				t.Fatal(e)
			}
		}
	}
	oldOperator, oldRuntime := save("m10-operator.json", false), save("m10-runtime.json", true)
	run(prior, "check-schema", oldOperator, "")
	run(prior, "check-runtime-access", oldOperator, oldRuntime)
	snapshot := func() map[string]string {
		out := map[string]string{}
		for table, names := range columns {
			where := ""
			if table == "schema_migrations" {
				where = fmt.Sprintf(" WHERE version<=%d", priorEntries)
			}
			var raw string
			if e := g.f.s.DB.QueryRow(ctx, "SELECT COALESCE(jsonb_agg(v ORDER BY v::text),'[]'::jsonb)::text FROM (SELECT to_jsonb(t) v FROM (SELECT "+strings.Join(names, ",")+" FROM "+pgx.Identifier{table}.Sanitize()+where+") t) rows").Scan(&raw); e != nil {
				t.Fatal(e)
			}
			out[table] = raw
		}
		return out
	}
	before, files := snapshot(), ssoUpgradeFiles(t, g)
	var oid uint32
	var verifier string
	if e = g.f.s.DB.QueryRow(ctx, "SELECT oid,rolpassword FROM pg_authid WHERE rolname=$1", role).Scan(&oid, &verifier); e != nil {
		t.Fatal(e)
	}
	values["server"].(map[string]any)["application_profile"] = target
	if target == "workflows" {
		values["workflows"] = map[string]any{"enabled": true}
	}
	operator, currentRuntime := save("m11-operator.json", false), save("m11-runtime.json", true)
	for i := 0; i < 2; i++ {
		run(current, "upgrade-"+target, operator, currentRuntime)
	}
	run(current, "check-runtime-access", operator, currentRuntime)
	if e = database.Check(ctx, g.f.s.DB, target); e != nil {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(before, snapshot()) || !reflect.DeepEqual(files, ssoUpgradeFiles(t, g)) {
		t.Fatal("populated M10 projections or accepted files changed")
	}
	var after uint32
	var hash string
	if e = g.f.s.DB.QueryRow(ctx, "SELECT oid,rolpassword FROM pg_authid WHERE rolname=$1", role).Scan(&after, &hash); e != nil || after != oid || hash != verifier {
		t.Fatal("original principal changed", e)
	}
	profile := target
	g.f.s.Identity.Config.Server.ApplicationProfile = &profile
	if target == "workflows" {
		g.f.s.Identity.Config.Workflows.Enabled = true
	}
	t.Logf("Actual accepted %s native binary verified populated%d-table/%d-entry predecessor and restricted principal; current native upgrade repeated to %s retained every original column projection, role OID/SCRAM and four-store device/inode/link/mode/size/mtime plus small-file hashes; accepted large bytes verified before SQL-only upgrade", from, priorTables, priorEntries, target)
	// The upgrade preservation proof above includes the live partial8MiB import.
	// A separate logical/file backup must first quiesce it through the real API.
	if e = g.f.s.Models.ExportFiles(ctx, filepath.Join(t.TempDir(), "pending-refused")); e == nil || e.Error() != "MODEL_IMPORT_BUSY" {
		t.Fatal("active upgraded import did not retain backup refusal", e)
	}
	cancelled, e := g.f.s.Models.CancelImport(ctx, g.f.admin, partial.ID, "Explicit disposable upgraded import quiescence before paired backup", uuid.NewString())
	if e != nil || cancelled.State != "cancelled" {
		t.Fatal("upgraded import explicit quiescence", e)
	}
	t.Log("Upgrade retained the live partial8MiB import and refused file backup while it was active; explicit audited cancellation quiesced only that disposable import before the separate paired recovery profile")

}
