//go:build sso_recovery_integration

package recovery_test

import (
	"bytes"
	"context"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/sso"
	recovery "github.com/Passion-Flow/ownsolid/services/control/internal/operations/recovery"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
)

func TestM11ActualServiceRestartLogicalExportRestoreAndSourceAuthorityRecovery(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	container := os.Getenv("PROJECT_ONE_FIXTURE_CONTAINER")
	scope := os.Getenv("PROJECT_ONE_SSO_QUALIFICATION_SCOPE")
	dockerContext := "desktop-linux"
	if scope != "" {
		id, err := uuid.Parse(scope)
		if err != nil || id == uuid.Nil || id.String() != scope || runtime.GOOS != "linux" || runtime.GOARCH != "amd64" || container != "projectone-sso-pg-"+scope[:8] {
			t.Fatal("native isolated SSO qualification scope required")
		}
		dockerContext = os.Getenv("DOCKER_CONTEXT")
		if !regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`).MatchString(dockerContext) {
			t.Fatal("explicit local Docker context required")
		}
	} else {
		owned := fixture.PreflightOwnedPostgres(t)
		container = owned.Container
		dockerContext = owned.Context
	}
	docker := func(arguments ...string) *exec.Cmd {
		command := exec.CommandContext(ctx, "docker", append([]string{"--context", dockerContext}, arguments...)...)
		for _, item := range os.Environ() {
			name, _, _ := strings.Cut(item, "=")
			if name != "DOCKER_HOST" && name != "DOCKER_CONTEXT" && name != "DOCKER_TLS_VERIFY" && name != "DOCKER_CERT_PATH" {
				command.Env = append(command.Env, item)
			}
		}
		return command
	}
	label, e := docker("container", "inspect", container, "--format", `{{index .Config.Labels "projectone.owner"}}`).Output()
	if e != nil || strings.TrimSpace(string(label)) != "mvp-development" {
		t.Fatal("isolated fixture ownership mismatch")
	}
	originalConfig, e := pgxpool.ParseConfig(os.Getenv("PROJECT_ONE_TEST_DSN"))
	if e != nil || originalConfig.ConnConfig.Host != "127.0.0.1" || !strings.HasSuffix(originalConfig.ConnConfig.Database, "_test") {
		t.Fatal("isolated source database required")
	}
	if scope != "" {
		label, e = docker("container", "inspect", container, "--format", `{{index .Config.Labels "projectone.scope"}}`).Output()
		suffix := strings.ReplaceAll(scope, "-", "")
		if e != nil || strings.TrimSpace(string(label)) != scope || originalConfig.ConnConfig.Database != "projectone_sso_"+suffix+"_test" || originalConfig.ConnConfig.User != "projectone_sso_"+suffix {
			t.Fatal("dedicated SSO cluster identity required")
		}
		probe, err := pgxpool.NewWithConfig(ctx, originalConfig.Copy())
		if err != nil {
			t.Fatal("dedicated SSO cluster unavailable")
		}
		var cluster string
		err = probe.QueryRow(ctx, `SELECT current_setting('cluster_name')`).Scan(&cluster)
		probe.Close()
		if err != nil || cluster != "sso-qualification-"+scope {
			t.Fatal("dedicated SSO cluster marker required before fixture mutations")
		}
	}
	accounts := fixture.New(t)
	password := fixture.Bootstrap(t, accounts)
	_, localToken, _, e := accounts.Login(ctx, "admin@example.invalid", password, "admin", "", uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	a, e := accounts.Authenticate(ctx, localToken, "admin")
	if e != nil {
		t.Fatal(e)
	}
	dir := t.TempDir()
	if e = os.Chmod(dir, 0700); e != nil {
		t.Fatal(e)
	}
	keyring := filepath.Join(dir, "source-keyring.json")
	if e = sso.InitializeKeyring(keyring, ""); e != nil {
		t.Fatal(e)
	}
	accounts.Config.SSO = config.SSOConfiguration{Enabled: true, KeyringFile: keyring, Providers: []config.SSOProvider{{Origin: "https://provider.recovery.example.invalid", Issuer: "https://provider.recovery.example.invalid", DialAddresses: []string{"127.0.0.1"}}}}
	service, e := sso.New(accounts)
	if e != nil {
		t.Fatal(e)
	}
	defer service.Close()
	accounts.Sources = service
	connector, e := service.CreateConnector(ctx, a, sso.CreateConnector{ID: uuid.NewString(), Name: "Synthetic backup source", Provider: "gitea", Origin: "https://provider.recovery.example.invalid", ClientID: uuid.NewString(), ClientSecret: security.Token(), Enabled: true, Reason: "Actual isolated logical backup fixture"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	start := func(mode string) sso.StartResult {
		t.Helper()
		input := sso.StartInput{Mode: mode}
		previous := ""
		var actor *identity.Auth
		if mode == "link" {
			previous = localToken
			actor = &a
			reason := "Explicit source fixture link"
			input.Password = &password
			input.Reason = &reason
		}
		result, e := service.Start(ctx, connector.ID, "admin", previous, actor, input, uuid.NewString())
		if e != nil {
			t.Fatal(e)
		}
		return result
	}
	flowID := func(result sso.StartResult) string {
		t.Helper()
		parsed, e := url.Parse(result.AuthorizationURL)
		if e != nil {
			t.Fatal("source fixture URL invalid")
		}
		var id string
		if e = accounts.DB.QueryRow(ctx, `SELECT id::text FROM sso_flows WHERE state_hash=$1`, security.Digest(parsed.Query().Get("state"))).Scan(&id); e != nil {
			t.Fatal(e)
		}
		return id
	}
	completed := flowID(start("link"))
	binding := uuid.NewString()
	if _, e = accounts.DB.Exec(ctx, `INSERT INTO sso_bindings(id,tenant_id,connector_id,subject,user_id) VALUES($1,$2,$3,'2',$4)`, binding, a.Me.Workspace.TenantID, connector.ID, a.Me.User.ID); e != nil {
		t.Fatal(e)
	}
	// Explicit successful-provider-output fixture. Real Gitea exchange is covered
	// separately; no fake adapter or runtime success branch is introduced.
	if _, e = accounts.DB.Exec(ctx, `UPDATE sso_flows SET state='claimed',claim_id=$2,verifier_key_id=NULL,verifier_nonce=NULL,verifier_ciphertext=NULL WHERE id=$1`, completed, uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	if _, e = accounts.DB.Exec(ctx, `UPDATE sso_flows SET state='succeeded',authenticated_user_id=$2,finished_at=clock_timestamp() WHERE id=$1`, completed, a.Me.User.ID); e != nil {
		t.Fatal(e)
	}
	tx, e := accounts.DB.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback(ctx)
	issued, e := accounts.IssueSession(ctx, tx, a.Me.User.ID, a.Epoch, "admin", "", uuid.NewString(), &identity.SessionSource{TenantID: a.Me.Workspace.TenantID, ConnectorID: connector.ID, BindingID: binding, FlowID: completed, AuthVersion: 1})
	if e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(ctx); e != nil {
		t.Fatal(e)
	}
	pendingResult := start("login")
	pending := flowID(pendingResult)
	claimed := flowID(start("login"))
	if _, e = accounts.DB.Exec(ctx, `UPDATE sso_flows SET state='claimed',claim_id=$2,verifier_key_id=NULL,verifier_nonce=NULL,verifier_ciphertext=NULL WHERE id=$1`, claimed, uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	service.Close()
	restarted, e := sso.New(accounts)
	if e != nil {
		t.Fatal(e)
	}
	defer restarted.Close()
	accounts.Sources = restarted
	if _, e = accounts.Authenticate(ctx, issued.Token, "admin"); e != nil {
		t.Fatal("service restart lost source authority", e)
	}
	verifySSONativeProcessRestarts(t, ctx, accounts, issued.Token, keyring, dir)
	schema := accounts.DB.Config().ConnConfig.RuntimeParams["search_path"]
	if !strings.HasPrefix(schema, "m01_") || strings.ContainsAny(schema, "; \"'") {
		t.Fatal("generated isolated source schema required")
	}
	dump := docker("exec", container, "sh", "-c", `PGPASSWORD="$POSTGRES_PASSWORD" pg_dump -U "$POSTGRES_USER" -d "$1" -Fc --no-owner --schema "$2"`, "sh", originalConfig.ConnConfig.Database, schema)
	archive, e := dump.Output()
	if e != nil || len(archive) < 100 {
		t.Fatal("actual consistent SSO logical export failed")
	}
	if e = os.WriteFile(filepath.Join(dir, "backup.dump"), archive, 0600); e != nil {
		t.Fatal(e)
	}
	keys, e := os.ReadFile(keyring)
	if e != nil {
		t.Fatal(e)
	}
	backupKeys := filepath.Join(dir, "restored-keyring.json")
	if e = os.WriteFile(backupKeys, keys, 0600); e != nil {
		t.Fatal(e)
	}
	targetDB := "m11_restore_" + strings.ReplaceAll(uuid.NewString(), "-", "") + "_test"
	operator, e := pgxpool.New(ctx, os.Getenv("PROJECT_ONE_TEST_DSN"))
	if e != nil {
		t.Fatal("isolated restore operator unavailable")
	}
	defer operator.Close()
	if _, e = operator.Exec(ctx, "CREATE DATABASE "+targetDB); e != nil {
		t.Fatal("new isolated restore database could not be created")
	}
	defer operator.Exec(context.Background(), "DROP DATABASE "+targetDB+" WITH (FORCE)")
	// A current source schema includes vector columns. Install the exact approved
	// extension only in this freshly generated isolated restore database.
	prerequisite := docker("exec", container, "psql", "-X", "-v", "ON_ERROR_STOP=1", "-U", originalConfig.ConnConfig.User, "-d", targetDB, "-c", "CREATE EXTENSION vector WITH SCHEMA public VERSION '0.8.7'")
	if e = prerequisite.Run(); e != nil {
		t.Fatal("isolated restore vector prerequisite failed")
	}
	command := docker("exec", "-i", container, "sh", "-c", `PGPASSWORD="$POSTGRES_PASSWORD" pg_restore -U "$POSTGRES_USER" -d "$1" --no-owner --no-privileges --exit-on-error`, "sh", targetDB)
	command.Stdin = bytes.NewReader(archive)
	if e = command.Run(); e != nil {
		t.Fatal("actual isolated SSO pg_restore failed")
	}
	pc := accounts.DB.Config().Copy()
	pc.ConnConfig.Database = targetDB
	restoredDB, e := pgxpool.NewWithConfig(ctx, pc)
	if e != nil {
		t.Fatal("isolated restored pool failed")
	}
	defer restoredDB.Close()
	if e = database.Check(ctx, restoredDB, accounts.Config.Server.Profile()); e != nil {
		t.Fatal(e)
	}
	restored := *accounts
	restored.DB = restoredDB
	restored.Sources = nil
	restored.Config.SSO.KeyringFile = backupKeys
	outbox := *accounts.Mail
	outbox.DB = restoredDB
	restored.Mail = &outbox
	restoredService, e := sso.New(&restored)
	if e != nil {
		t.Fatal("matching protected backup keyring could not open restored ciphertext", e)
	}
	defer restoredService.Close()
	restored.Sources = restoredService
	if _, e = restored.Authenticate(ctx, issued.Token, "admin"); e != nil {
		t.Fatal("isolated restored lineage damaged before offline invalidation", e)
	}
	if _, e = restoredDB.Exec(ctx, `CREATE FUNCTION reject_sso_restore() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic SSO restore failure'; END; $$; CREATE TRIGGER reject_sso_restore BEFORE UPDATE ON sso_flows FOR EACH ROW EXECUTE FUNCTION reject_sso_restore()`); e != nil {
		t.Fatal(e)
	}
	if e = recovery.InvalidateRestored(ctx, &restored, "Synthetic injected atomic SSO restore failure"); e == nil {
		t.Fatal("restore failure reported success")
	}
	if _, e = restored.Authenticate(ctx, issued.Token, "admin"); e != nil {
		t.Fatal("failed common restore escaped rollback", e)
	}
	if _, e = restoredDB.Exec(ctx, `DROP TRIGGER reject_sso_restore ON sso_flows;DROP FUNCTION reject_sso_restore()`); e != nil {
		t.Fatal(e)
	}
	if e = recovery.InvalidateRestored(ctx, &restored, "Actual isolated SSO restore authority invalidation"); e != nil {
		t.Fatal(e)
	}
	for _, token := range []string{localToken, issued.Token} {
		if _, e = restored.Authenticate(ctx, token, "admin"); e == nil || e.Error() != "AUTH_SESSION_EXPIRED" {
			t.Fatal("restored session authority survived", e)
		}
	}
	var count int
	if e = restoredDB.QueryRow(ctx, `SELECT count(*) FROM sso_flows WHERE id=ANY($1::uuid[]) AND state='failed' AND verifier_key_id IS NULL AND verifier_ciphertext IS NULL AND authenticated_user_id IS NULL`, []string{pending, claimed}).Scan(&count); e != nil || count != 2 {
		t.Fatal("restored flow retry authority survived", e)
	}
	if e = restoredDB.QueryRow(ctx, `SELECT count(*) FROM sso_bindings WHERE id=$1 AND user_id=$2 AND subject='2' AND active`, binding, a.Me.User.ID).Scan(&count); e != nil || count != 1 {
		t.Fatal("restored immutable binding was lost", e)
	}
	if _, _, _, e = restored.Login(ctx, "admin@example.invalid", password, "admin", "", uuid.NewString()); e != nil {
		t.Fatal("local recovery after real restore failed", e)
	}
	wrong := filepath.Join(dir, "wrong-keyring.json")
	if e = sso.InitializeKeyring(wrong, ""); e != nil {
		t.Fatal(e)
	}
	restored.Config.SSO.KeyringFile = wrong
	if _, e = sso.New(&restored); e == nil || e.Error() != "SSO_ENCRYPTED_STATE_INVALID" {
		t.Fatal("enabled startup accepted a missing retained decryption key", e)
	}
	restored.Config.SSO.Enabled = false
	restored.Config.SSO.KeyringFile = filepath.Join(dir, "missing-recovery-keyring.json")
	disabled, e := sso.New(&restored)
	if e != nil {
		t.Fatal("explicit disabled recovery tried to open the missing ring", e)
	}
	defer disabled.Close()
	restored.Sources = disabled
	if _, _, _, e = restored.Login(ctx, "admin@example.invalid", password, "admin", "", uuid.NewString()); e != nil {
		t.Fatal("disabled-mode local recovery failed", e)
	}
	t.Log("Actual PostgreSQL custom pg_dump/pg_restore into a new isolated database, protected matching keyring copy, enabled service restart, preserved binding/source lineage, injected common-restore rollback, successful ordinary/source/flow revocation and missing-key disabled recovery passed. Successful provider result is a source-data fixture; actual Gitea and native process restart have separate profiles.")
}
