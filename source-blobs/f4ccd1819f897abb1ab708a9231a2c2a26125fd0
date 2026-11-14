package identityfixture

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	strict "github.com/Passion-Flow/ownsolid/api/datasets/v1"
)

const nativePostgresInputFormat = "projectone.identity.native-postgres-fixture.v1"

var nativeReceiptName = regexp.MustCompile(`^native-account-recovery-[0-9]{8}-[0-9]{6}-[a-f0-9]{8}$`)
var nativeFixtureReceiptName = regexp.MustCompile(`^(native-account-recovery|m13-cumulative-(backend|causal)|m14-cumulative-(backend|operations|browser|catalog|catalog-restore|tool-only|causal)|m16-cumulative-(target-client|capture|catalog|backend|upgrade|runtime|browser|operations|wire|causal)|m15-cumulative-(catalog|catalog-restore|backend|browser|operations|wire|tool-only|packaged-encoder|causal))-[0-9]{8}-[0-9]{6}-[a-f0-9]{8}$`)
var nativeSourceSchema = regexp.MustCompile(`^m20_repository_[a-f0-9]{32}$`)
var nativeFixtureID = regexp.MustCompile(`^[a-f0-9]{32}$`)
var nativeDigest = regexp.MustCompile(`^[a-f0-9]{64}$`)
var nativeSystemID = regexp.MustCompile(`^[1-9][0-9]{0,19}$`)
var nativePrefixName = regexp.MustCompile(`^darwin-arm64-[a-f0-9]{32}$`)
var nativeSQLName = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)

type nativeTool struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

type nativePostgresInput struct {
	Format           string                `json:"format"`
	SourceSchema     string                `json:"source_schema"`
	RestoreDatabase  string                `json:"restore_database"`
	Host             string                `json:"host"`
	Port             uint16                `json:"port"`
	Database         string                `json:"database"`
	User             string                `json:"user"`
	DataDirectory    string                `json:"data_directory"`
	SystemIdentifier string                `json:"system_identifier"`
	Prefix           string                `json:"prefix"`
	Tools            map[string]nativeTool `json:"tools"`
	PasswordFile     string                `json:"password_file"`
	PasswordSHA256   string                `json:"password_sha256"`
	CAFile           string                `json:"ca_file"`
	CASHA256         string                `json:"ca_sha256"`
	RestartRequest   string                `json:"restart_request"`
	RestartAck       string                `json:"restart_ack"`
	Nonce            string                `json:"nonce"`
	scopeFile        string
	scopeSHA256      string
	scopeIdentity    os.FileInfo
}

func readNativePrivate(path string, maximum int64) ([]byte, error) {
	parent := filepath.Dir(path)
	canonical, e := filepath.EvalSymlinks(parent)
	if e != nil || !filepath.IsAbs(path) || filepath.Clean(path) != path || canonical != parent {
		return nil, errors.New("NATIVE_FIXTURE_PRIVATE_PATH_REQUIRED")
	}
	root, e := os.OpenRoot(parent)
	if e != nil {
		return nil, errors.New("NATIVE_FIXTURE_PRIVATE_PARENT_REQUIRED")
	}
	defer root.Close()
	owner := func(i os.FileInfo, directory bool) bool {
		value, ok := i.Sys().(*syscall.Stat_t)
		return ok && uint64(value.Uid) == uint64(os.Geteuid()) && (directory || value.Nlink == 1)
	}
	directory, e := root.Stat(".")
	if e != nil || directory.Mode() != os.ModeDir|0700 || !owner(directory, true) {
		return nil, errors.New("NATIVE_FIXTURE_PRIVATE_PARENT_REQUIRED")
	}
	file, e := root.OpenFile(filepath.Base(path), os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if e != nil {
		return nil, errors.New("NATIVE_FIXTURE_PRIVATE_FILE_REQUIRED")
	}
	defer file.Close()
	before, e := file.Stat()
	if e != nil || !before.Mode().IsRegular() || before.Mode() != 0600 || !owner(before, false) || before.Size() < 1 || before.Size() > maximum {
		return nil, errors.New("NATIVE_FIXTURE_PRIVATE_FILE_REQUIRED")
	}
	raw, e := io.ReadAll(io.LimitReader(file, maximum+1))
	after, ae := file.Stat()
	current, ce := root.Lstat(filepath.Base(path))
	if e != nil || ae != nil || ce != nil || !os.SameFile(before, after) || !os.SameFile(before, current) || !owner(current, false) || current.Mode() != 0600 || after.Size() != int64(len(raw)) || after.Size() != before.Size() || !after.ModTime().Equal(before.ModTime()) {
		return nil, errors.New("NATIVE_FIXTURE_PRIVATE_FILE_CHANGED")
	}
	return raw, nil
}

func nativeBytesSHA(raw []byte) string {
	value := sha256.Sum256(raw)
	return hex.EncodeToString(value[:])
}

func nativeToolMatches(tool nativeTool) bool {
	canonical, e := filepath.EvalSymlinks(tool.Path)
	if e != nil || !filepath.IsAbs(tool.Path) || canonical != tool.Path || !nativeDigest.MatchString(tool.SHA256) {
		return false
	}
	file, e := os.Open(tool.Path)
	if e != nil {
		return false
	}
	defer file.Close()
	i, e := file.Stat()
	if e != nil || !i.Mode().IsRegular() || i.Mode().Perm()&0022 != 0 || i.Mode().Perm()&0111 == 0 || i.Size() < 1 || i.Size() > 64<<20 {
		return false
	}
	value, ok := i.Sys().(*syscall.Stat_t)
	if !ok || uint64(value.Uid) != uint64(os.Geteuid()) || value.Nlink != 1 {
		return false
	}
	hash := sha256.New()
	if _, e = io.Copy(hash, io.LimitReader(file, (64<<20)+1)); e != nil {
		return false
	}
	return hex.EncodeToString(hash.Sum(nil)) == tool.SHA256
}

func loadNativePostgresInput(file string) (nativePostgresInput, error) {
	var input nativePostgresInput
	root := filepath.Dir(file)
	if filepath.Base(file) != "native-postgres.json" || !nativeFixtureReceiptName.MatchString(filepath.Base(root)) || filepath.Base(filepath.Dir(root)) != "m20" || filepath.Base(filepath.Dir(filepath.Dir(root))) != "evidence" {
		return input, errors.New("NATIVE_FIXTURE_RECEIPT_SCOPE_REQUIRED")
	}
	raw, e := readNativePrivate(file, 64<<10)
	if e != nil {
		return input, e
	}
	if strict.DecodeStrict(raw, &input) != nil {
		return input, errors.New("NATIVE_FIXTURE_DOCUMENT_INVALID")
	}
	state := filepath.Dir(filepath.Dir(filepath.Dir(root)))
	expectedDataParent := filepath.Join(state, "postgresql", "fixtures")
	dataRoot := filepath.Dir(input.DataDirectory)
	if input.Format != nativePostgresInputFormat || !nativeSourceSchema.MatchString(input.SourceSchema) || !isolatedRestoreDatabase.MatchString(input.RestoreDatabase) || input.Host != "127.0.0.1" || input.Port < 1024 || !nativeSQLName.MatchString(input.Database) || !strings.HasSuffix(input.Database, "_test") || !nativeSQLName.MatchString(input.User) || !nativeSystemID.MatchString(input.SystemIdentifier) || filepath.Base(input.DataDirectory) != "data" || filepath.Dir(dataRoot) != expectedDataParent || !nativeFixtureID.MatchString(filepath.Base(dataRoot)) || !nativeFixtureID.MatchString(input.Nonce) {
		return input, errors.New("NATIVE_FIXTURE_IDENTITY_INVALID")
	}
	prefixParent := filepath.Join(state, "toolchains", "postgresql")
	if filepath.Dir(filepath.Dir(input.Prefix)) != prefixParent || filepath.Base(filepath.Dir(input.Prefix)) != "18.6-vector0.8.7" || !nativePrefixName.MatchString(filepath.Base(input.Prefix)) || input.PasswordFile != filepath.Join(root, "libpq-password") || input.CAFile != filepath.Join(dataRoot, "credentials", "ca.pem") || input.RestartRequest != filepath.Join(root, "restart.request.json") || input.RestartAck != filepath.Join(root, "restart.ack.json") {
		return input, errors.New("NATIVE_FIXTURE_REFERENCE_SCOPE_INVALID")
	}
	if len(input.Tools) != 2 {
		return input, errors.New("NATIVE_FIXTURE_MEASURED_TOOLS_REQUIRED")
	}
	for _, name := range []string{"pg_dump", "pg_restore"} {
		tool, ok := input.Tools[name]
		if !ok || tool.Path != filepath.Join(input.Prefix, "bin", name) || !nativeToolMatches(tool) {
			return input, errors.New("NATIVE_FIXTURE_MEASURED_TOOLS_REQUIRED")
		}
	}
	for path, digest := range map[string]string{input.PasswordFile: input.PasswordSHA256, input.CAFile: input.CASHA256} {
		raw, e := readNativePrivate(path, 64<<10)
		if e != nil || !nativeDigest.MatchString(digest) || nativeBytesSHA(raw) != digest {
			return input, errors.New("NATIVE_FIXTURE_PROTECTED_REFERENCE_CHANGED")
		}
	}
	input.scopeFile, input.scopeSHA256 = file, nativeBytesSHA(raw)
	input.scopeIdentity, e = os.Stat(root)
	if e != nil {
		return input, errors.New("NATIVE_FIXTURE_RECEIPT_SCOPE_CHANGED")
	}
	return input, nil
}

// ReadNativeRecoveryDocument is the narrow private entrance for the fixed
// account recovery Go profile, not a general configuration or secret reader.
func ReadNativeRecoveryDocument(file string) ([]byte, error) {
	root := filepath.Dir(file)
	if filepath.Base(file) != "account-recovery-input.json" || !nativeReceiptName.MatchString(filepath.Base(root)) || filepath.Base(filepath.Dir(root)) != "m20" || filepath.Base(filepath.Dir(filepath.Dir(root))) != "evidence" {
		return nil, errors.New("NATIVE_ACCOUNT_RECOVERY_SCOPE_REQUIRED")
	}
	return readNativePrivate(file, 64<<10)
}

// RegisterNativeSourceCleanup disposes only the already attested synthetic
// source before repositoryfixture's final empty-schema cleanup. One autocommit
// per table bounds relation locks on the admitted small native fixture; a single
// DROP SCHEMA can exceed its global lock table with the cumulative catalog.
func (f PostgresFixture) RegisterNativeSourceCleanup(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	if f.native == nil || !f.ownedSchema() || pool == nil {
		t.Fatal("OWNED_NATIVE_SOURCE_CLEANUP_REQUIRED")
	}
	c := pool.Config().ConnConfig
	if c.Database != f.Database || c.User != f.User || c.RuntimeParams["search_path"] != f.Schema {
		t.Fatal("OWNED_NATIVE_SOURCE_CLEANUP_REQUIRED")
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), fixtureMaintenanceCleanupSeconds*time.Second)
		defer cancel()
		count, e := disposeOwnedPostgresTables(ctx, pool, f.Schema)
		if e != nil {
			t.Error("OWNED_NATIVE_SOURCE_CLEANUP_FAILED", e)
			return
		}
		t.Logf("NATIVE_ACCOUNT_SOURCE_DISPOSAL autocommit_tables=%d exact_owned_schema=true", count)
	})
}

// OwnedNativePostgres accepts only the fixed private native-test document and
// independently compares it with the actual privileged fixture pool. It never
// adopts a server PID or opens a customer connection from the document.
func OwnedNativePostgres(t *testing.T, pool *pgxpool.Pool, file string) PostgresFixture {
	return nativePostgresScope(t, pool, file, true)
}

// Before creation, the measured endpoint must prove the declared synthetic
// namespace is absent. An existing schema is never adopted.
func nativePostgresScope(t *testing.T, pool *pgxpool.Pool, file string, created bool) PostgresFixture {
	t.Helper()
	input, e := loadNativePostgresInput(file)
	if e != nil || pool == nil {
		t.Fatal("EXPLICIT_OWNED_NATIVE_POSTGRES_REQUIRED")
	}
	c := pool.Config().ConnConfig
	if c.Host != input.Host || c.Port != input.Port || c.Database != input.Database || c.User != input.User || (created && c.RuntimeParams["search_path"] != input.SourceSchema) || (!created && c.RuntimeParams["search_path"] != "" && c.RuntimeParams["search_path"] != input.SourceSchema) || len(c.Fallbacks) != 0 || c.TLSConfig == nil || c.TLSConfig.InsecureSkipVerify || c.TLSConfig.RootCAs == nil {
		t.Fatal("OWNED_NATIVE_POSTGRES_CONNECTION_MISMATCH")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	var database, user, schema, data, system, tlsVersion string
	var version, port int
	var encrypted bool
	e = pool.QueryRow(ctx, `SELECT current_database(),session_user,COALESCE(current_schema(),''),current_setting('data_directory'),
	 (SELECT system_identifier::text FROM pg_control_system()),current_setting('server_version_num')::integer,inet_server_port(),
	 (SELECT ssl FROM pg_stat_ssl WHERE pid=pg_backend_pid()),(SELECT version FROM pg_stat_ssl WHERE pid=pg_backend_pid())`).Scan(&database, &user, &schema, &data, &system, &version, &port, &encrypted, &tlsVersion)
	if e != nil || database != input.Database || user != input.User || created && schema != input.SourceSchema || data != input.DataDirectory || system != input.SystemIdentifier || version != 180006 || port != int(input.Port) || !encrypted || tlsVersion != "TLSv1.2" && tlsVersion != "TLSv1.3" {
		t.Fatal("OWNED_NATIVE_POSTGRES_ACTUAL_IDENTITY_REQUIRED")
	}
	if !created {
		var count int
		if e = pool.QueryRow(ctx, "SELECT count(*) FROM pg_namespace WHERE nspname=$1", input.SourceSchema).Scan(&count); e != nil || count != 0 {
			t.Fatal("OWNED_NATIVE_FRESH_SOURCE_SCHEMA_REQUIRED")
		}
	}
	return PostgresFixture{Database: input.Database, User: input.User, Schema: input.SourceSchema, native: &input}
}

func (f PostgresFixture) ownedSchema() bool {
	if f.native != nil {
		return f.Schema == f.native.SourceSchema && nativeSourceSchema.MatchString(f.Schema) && f.Database == f.native.Database && f.User == f.native.User
	}
	return isolatedSchema.MatchString(f.Schema)
}

func (f PostgresFixture) nativeCommand(t *testing.T, ctx context.Context, name, database string, arguments ...string) *exec.Cmd {
	t.Helper()
	if f.native == nil || !f.ownedSchema() || name != "pg_dump" && name != "pg_restore" || database != f.Database && database != f.native.RestoreDatabase {
		t.Fatal("OWNED_NATIVE_CLIENT_SCOPE_REQUIRED")
	}
	tool := f.native.Tools[name]
	if !nativeToolMatches(tool) {
		t.Fatal("OWNED_NATIVE_CLIENT_CHANGED")
	}
	for path, digest := range map[string]string{f.native.PasswordFile: f.native.PasswordSHA256, f.native.CAFile: f.native.CASHA256} {
		raw, e := readNativePrivate(path, 64<<10)
		if e != nil || nativeBytesSHA(raw) != digest {
			t.Fatal("OWNED_NATIVE_CLIENT_REFERENCE_CHANGED")
		}
	}
	argv := []string{"--host", f.native.Host, "--port", strconv.Itoa(int(f.native.Port)), "--username", f.User, "--dbname", database, "--no-password"}
	command := exec.CommandContext(ctx, tool.Path, append(argv, arguments...)...)
	command.WaitDelay = 2 * time.Second
	for _, value := range os.Environ() {
		key, _, _ := strings.Cut(value, "=")
		if !strings.HasPrefix(key, "PG") && !strings.HasPrefix(key, "MYSQL") && !strings.HasPrefix(key, "DOCKER_") && !strings.HasPrefix(key, "PROJECT_ONE_") {
			command.Env = append(command.Env, value)
		}
	}
	command.Env = append(command.Env, "PGPASSFILE="+f.native.PasswordFile, "PGSSLMODE=verify-full", "PGSSLROOTCERT="+f.native.CAFile, "PGCONNECT_TIMEOUT=5")
	return command
}

// RestartNative requests one bounded effect from the existing Python Popen
// owner. Go does not signal an arbitrary PID or automatically resend a request.
func (f PostgresFixture) RestartNative(t *testing.T, ctx context.Context) {
	t.Helper()
	if f.native == nil || !f.ownedSchema() {
		t.Fatal("OWNED_NATIVE_RESTART_CONTEXT_REQUIRED")
	}
	if ctx.Err() != nil {
		t.Fatal("OWNED_NATIVE_RESTART_NOT_ADMITTED")
	}
	if _, e := os.Lstat(f.native.RestartAck); !os.IsNotExist(e) {
		t.Fatal("OWNED_NATIVE_RESTART_ACK_ALREADY_EXISTS")
	}
	original, e := readNativePrivate(f.native.scopeFile, 64<<10)
	if e != nil || nativeBytesSHA(original) != f.native.scopeSHA256 {
		t.Fatal("OWNED_NATIVE_RESTART_INPUT_CHANGED")
	}
	scope, e := os.OpenRoot(filepath.Dir(f.native.RestartRequest))
	if e != nil {
		t.Fatal("OWNED_NATIVE_RESTART_DIRECTORY_UNAVAILABLE")
	}
	defer scope.Close()
	identity, e := scope.Stat(".")
	if e != nil || !os.SameFile(identity, f.native.scopeIdentity) || identity.Mode() != os.ModeDir|0700 {
		t.Fatal("OWNED_NATIVE_RESTART_DIRECTORY_CHANGED")
	}
	request := struct {
		Format           string `json:"format"`
		Nonce            string `json:"nonce"`
		SourceSchema     string `json:"source_schema"`
		SystemIdentifier string `json:"system_identifier"`
	}{"projectone.identity.native-restart-request.v1", f.native.Nonce, f.Schema, f.native.SystemIdentifier}
	raw, e := json.Marshal(request)
	if e != nil {
		t.Fatal("OWNED_NATIVE_RESTART_REQUEST_INVALID")
	}
	file, e := scope.OpenFile(filepath.Base(f.native.RestartRequest), os.O_CREATE|os.O_EXCL|os.O_WRONLY|syscall.O_NOFOLLOW, 0600)
	if e != nil {
		t.Fatal("OWNED_NATIVE_RESTART_REQUEST_EXISTS_OR_INVALID")
	}
	_, we := file.Write(raw)
	se, ce := file.Sync(), file.Close()
	if we != nil || se != nil || ce != nil {
		t.Fatal("OWNED_NATIVE_RESTART_REQUEST_UNCERTAIN")
	}
	dir, e := scope.Open(".")
	if e != nil {
		t.Fatal("OWNED_NATIVE_RESTART_DIRECTORY_UNCERTAIN")
	}
	se, ce = dir.Sync(), dir.Close()
	if se != nil || ce != nil {
		t.Fatal("OWNED_NATIVE_RESTART_DIRECTORY_UNCERTAIN")
	}
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		if _, e := os.Lstat(f.native.RestartAck); e == nil {
			raw, e := readNativePrivate(f.native.RestartAck, 64<<10)
			var ack struct {
				Format           string `json:"format"`
				Nonce            string `json:"nonce"`
				SourceSchema     string `json:"source_schema"`
				SystemIdentifier string `json:"system_identifier"`
				PreviousPID      int    `json:"previous_pid"`
				NewPID           int    `json:"new_pid"`
				Status           string `json:"status"`
			}
			if e != nil || strict.DecodeStrict(raw, &ack) != nil || ack.Format != "projectone.identity.native-restart-ack.v1" || ack.Nonce != f.native.Nonce || ack.SourceSchema != f.Schema || ack.SystemIdentifier != f.native.SystemIdentifier || ack.PreviousPID <= 1 || ack.NewPID <= 1 || ack.PreviousPID == ack.NewPID || ack.Status != "restarted_owned_native_postgres" {
				t.Fatal("OWNED_NATIVE_RESTART_ACK_INVALID")
			}
			return
		} else if !os.IsNotExist(e) {
			t.Fatal("OWNED_NATIVE_RESTART_ACK_UNAVAILABLE")
		}
		select {
		case <-ctx.Done():
			t.Fatal("OWNED_NATIVE_RESTART_UNCERTAIN_RECONCILE_REQUIRED")
		case <-ticker.C:
		}
	}
}
