package postgresbackup

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/databaseaccess"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	release "github.com/Passion-Flow/ownsolid/api/release/v1"
)

func restoreRequestFixture() release.PostgreSQLRestoreRequest {
	return release.PostgreSQLRestoreRequest{FormatVersion: 1, ApplicationProfile: release.MVPPostgreSQLCPUApplicationProfile, RestoreID: strings.Repeat("c", 32), TargetScopeID: strings.Repeat("d", 32), SourceManifestSHA256: strings.Repeat("e", 64), BackupRoot: "/private/backups/source", RestoreRoot: "/private/restores/target", TargetDatabase: "synthetic_target", TargetOperator: "synthetic_operator", RuntimeConfigurationFile: "/private/config/runtime.yaml", ConfigurationRoot: "/private/config", Reason: "Synthetic new target restoration", Policy: policyFixture()}
}

func TestPostgresRestoreAuditUsesCanonicalSQLUUIDs(t *testing.T) {
	i := RestoreIntent{Request: restoreRequestFixture()}
	canonicalID := "cccccccc-cccc-cccc-cccc-cccccccccccc"
	if !restoreAuditSQLIdentities(i, canonicalID, canonicalID) {
		t.Fatal("actual SQL UUID representation refused")
	}
	for _, pair := range [][2]string{{i.Request.RestoreID, canonicalID}, {canonicalID, i.Request.RestoreID}, {"dddddddd-dddd-dddd-dddd-dddddddddddd", canonicalID}, {canonicalID, "dddddddd-dddd-dddd-dddd-dddddddddddd"}} {
		if restoreAuditSQLIdentities(i, pair[0], pair[1]) {
			t.Fatal("unbound SQL identity accepted")
		}
	}
}

func TestPostgresRestoreSourceNameAndScopeRefuseBeforeNativeOrSQL(t *testing.T) {
	root := privateTestRoot(t)
	source := requestFixture()
	source.BackupRoot = root
	m := Manifest{Request: source}
	raw, e := canonical(m)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(root, candidateName), raw, 0600); e != nil {
		t.Fatal(e)
	}
	r := restoreRequestFixture()
	r.BackupRoot = root
	r.SourceManifestSHA256 = digest(raw)
	for _, change := range []func(*release.PostgreSQLRestoreRequest){func(v *release.PostgreSQLRestoreRequest) { v.TargetDatabase = source.Database }, func(v *release.PostgreSQLRestoreRequest) { v.TargetScopeID = source.SourceScopeID }, func(v *release.PostgreSQLRestoreRequest) { v.RestoreID = source.SourceScopeID }, func(v *release.PostgreSQLRestoreRequest) { v.RestoreID = source.BackupID }} {
		v := r
		change(&v)
		if !v.Valid() {
			t.Fatal("test must reach source binding")
		}
		if _, e := loadRestoreSource(context.Background(), v); e == nil || e.Error() != "POSTGRES_RESTORE_SOURCE_BINDING_INVALID" {
			t.Fatal("source reuse reached absent native tool/SQL", e)
		}
	}
	if _, e := os.Stat(r.RestoreRoot); !os.IsNotExist(e) {
		t.Fatal("refusal wrote a restore record")
	}
}

func TestPostgresRestoreRootReplacementRejectsIdenticalBytes(t *testing.T) {
	root := privateTestRoot(t)
	paths := []string{}
	for _, name := range []string{"models", "datasets", "training", "preparations"} {
		p := filepath.Join(root, name)
		if e := os.Mkdir(p, 0700); e != nil {
			t.Fatal(e)
		}
		paths = append(paths, p)
	}
	held, e := acquireFreshRoots(paths)
	if e != nil {
		t.Fatal(e)
	}
	defer held.Close()
	if _, e := acquireFreshRoots(paths); e == nil {
		t.Fatal("second owner acquired held roots")
	}
	for _, p := range paths {
		if e = os.WriteFile(filepath.Join(p, "payload"), []byte("same synthetic immutable bytes"), 0600); e != nil {
			t.Fatal(e)
		}
	}
	if e = held.Check(false); e != nil {
		t.Fatal(e)
	}
	before, e := TreeDigest(context.Background(), paths[0], 1<<20, 100, 4096)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.Rename(paths[0], paths[0]+"-retained"); e != nil {
		t.Fatal(e)
	}
	if e = os.Mkdir(paths[0], 0700); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(paths[0], "payload"), []byte("same synthetic immutable bytes"), 0600); e != nil {
		t.Fatal(e)
	}
	after, e := TreeDigest(context.Background(), paths[0], 1<<20, 100, 4096)
	if e != nil || before != after {
		t.Fatal("byte-identical replacement fixture", e)
	}
	if e = held.Check(false); e == nil || e.Error() != "POSTGRES_RESTORE_ROOT_CHANGED" {
		t.Fatal("identity replacement accepted", e)
	}
	if e = observeRestoreRoots(held.Identities()); e == nil {
		t.Fatal("independent reader accepted replacement")
	}
	r := restoreRequestFixture()
	r.RestoreRoot = filepath.Join(root, "never-created")
	if e = finalRestoreAdmission(context.Background(), nil, held, config.Config{}, config.Config{}, "", "", Manifest{}, RestoreIntent{Request: r}); e == nil || e.Error() != "POSTGRES_RESTORE_ROOT_CHANGED" {
		t.Fatal("final admission did not reject replacement first", e)
	}
	if _, e = os.Stat(filepath.Join(r.RestoreRoot, restoreCompleteName)); !os.IsNotExist(e) {
		t.Fatal("replacement published completion")
	}
}

func TestPostgresRestoreRecordRootAndFinalFilesRejectReplacementOrDrift(t *testing.T) {
	f := newRestoreInputFixture(t)
	ctx := context.Background()
	roots, e := acquireFreshRoots(storeRoots(f.runtime))
	if e != nil {
		t.Fatal(e)
	}
	defer roots.Close()
	expected, e := targetStoreTrees(ctx, f.runtime, f.request)
	if e != nil {
		t.Fatal(e)
	}
	if e = finalRestoreFiles(ctx, roots, f.runtime, f.request, expected); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(storeRoots(f.runtime)[0], "drift"), []byte("synthetic drift under original inode"), 0600); e != nil {
		t.Fatal(e)
	}
	if e = finalRestoreFiles(ctx, roots, f.runtime, f.request, expected); e == nil {
		t.Fatal("final byte drift accepted")
	}
	if e = os.Remove(filepath.Join(storeRoots(f.runtime)[0], "drift")); e != nil {
		t.Fatal(e)
	}
	record, e := newDirectory(f.request.RestoreRoot)
	if e != nil {
		t.Fatal(e)
	}
	defer record.Close()
	info, e := record.Stat(".")
	if e != nil {
		t.Fatal(e)
	}
	id, e := rootIdentity(f.request.RestoreRoot, info)
	if e != nil {
		t.Fatal(e)
	}
	// Exercise the real non-null four-store wire shape. Null collections are
	// correctly refused by the existing strict decoder before binding checks.
	f.manifest.Stores = []StoreReadback{}
	for n, kind := range []string{"models", "datasets", "training", "preparations"} {
		f.manifest.Stores = append(f.manifest.Stores, StoreReadback{kind, expected[n]})
	}
	i := newRestoreIntent(f.request, f.manifest, RestoreTarget{SystemIdentifier: "synthetic-system", DatabaseOID: 42, Database: f.request.TargetDatabase, Operator: f.request.TargetOperator, BackendPID: 100, ServerVersion: 180006, Marker: "projectone:kubernetes-restore:" + f.request.TargetScopeID}, roots.Identities(), restoreConfigurationDigest(f.runtime), id)
	files := RestoreFiles{"projectone.postgres.restore-files.v1", f.request.RestoreID, valueDigest(i), roots.Identities(), expected, f.manifest.Stores, nil}
	if e = writeDocument(record, restoreIntentName, i, f.request.Policy.MaximumManifestBytes); e != nil {
		t.Fatal(e)
	}
	if e = writeDocument(record, restoreFilesName, files, f.request.Policy.MaximumManifestBytes); e != nil {
		t.Fatal(e)
	}
	if _, _, _, _, e = readRestoreRecords(f.request, f.manifest, f.runtime); e != nil {
		t.Fatal("original record", e)
	}
	if e = os.Rename(f.request.RestoreRoot, f.request.RestoreRoot+"-retained"); e != nil {
		t.Fatal(e)
	}
	if _, e = copyTree(ctx, f.request.RestoreRoot+"-retained", f.request.RestoreRoot, f.request.Policy.MaximumConfigurationBytes, f.request.Policy.MaximumStoreFiles, f.request.Policy.MaximumManifestBytes, f.request.Policy.MinimumFreeBytes); e != nil {
		t.Fatal(e)
	}
	if _, _, _, _, e = readRestoreRecords(f.request, f.manifest, f.runtime); e == nil || e.Error() != "POSTGRES_RESTORE_RECORD_ROOT_CHANGED" {
		t.Fatal("equal-byte record replacement accepted", e)
	}
	if _, e = os.Stat(filepath.Join(f.request.RestoreRoot, restoreCompleteName)); !os.IsNotExist(e) {
		t.Fatal("drift/replacement published completion")
	}
}

func TestPostgresRestoreChangedSourceManifestRefusesBeforeNativeOrSQL(t *testing.T) {
	root := privateTestRoot(t)
	m := Manifest{Request: requestFixture()}
	m.Request.BackupRoot = root
	raw, e := canonical(m)
	if e != nil {
		t.Fatal(e)
	}
	r := restoreRequestFixture()
	r.BackupRoot = root
	r.SourceManifestSHA256 = digest(raw)
	m.Request.Reason = "Changed source binding after approval"
	changed, e := canonical(m)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(root, candidateName), changed, 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = loadRestoreSource(context.Background(), r); e == nil || e.Error() != "POSTGRES_RESTORE_SOURCE_BINDING_INVALID" {
		t.Fatal("changed source reached native/SQL", e)
	}
}

type restoreInputFixture struct {
	operator, runtime config.Config
	operatorPath      string
	manifest          Manifest
	request           release.PostgreSQLRestoreRequest
	intent            RestoreIntent
}

func newRestoreInputFixture(t *testing.T) restoreInputFixture {
	t.Helper()
	root := privateTestRoot(t)
	for _, name := range []string{"configuration", "source", "scratch", "backups", "restores"} {
		if e := os.Mkdir(filepath.Join(root, name), 0700); e != nil {
			t.Fatal(e)
		}
	}
	template, e := os.ReadFile("../../../config/examples/control/releases-cumulative.example.yaml")
	if e != nil {
		t.Fatal(e)
	}
	c, e := emptyBackupFixtureConfiguration(template, root)
	if e != nil {
		t.Fatal(e)
	}
	for _, p := range storeRoots(c) {
		if e = os.Mkdir(p, 0700); e != nil {
			t.Fatal(e)
		}
	}
	write := func(path string, raw []byte) {
		t.Helper()
		if e := os.WriteFile(path, raw, 0600); e != nil {
			t.Fatal(e)
		}
	}
	credential, e := databaseaccess.NewCredential()
	if e != nil {
		t.Fatal(e)
	}
	u := url.URL{Scheme: "postgresql", Host: "127.0.0.1:65431", Path: "/synthetic_target", User: url.UserPassword("synthetic_operator", credential), RawQuery: "sslmode=disable&search_path=public"}
	write(c.Database.DSNFile, []byte(u.String()))
	key := make([]byte, 32)
	if _, e = rand.Read(key); e != nil {
		t.Fatal(e)
	}
	write(c.Security.AuthHMACKeyFile, key)
	keys, _ := json.Marshal(map[string]any{"active": "synthetic", "keys": map[string]string{"synthetic": base64.StdEncoding.EncodeToString(key)}})
	write(c.Security.MailKeyringFile, keys)
	write(c.Security.PasswordBlocklistFile, []byte("synthetic disallowed credential\n"))
	for _, path := range []string{c.Server.TLSCertificateFile, c.Server.TLSPrivateKeyFile, c.SMTP.CAFile, c.ReleaseViewConfigFile} {
		write(path, []byte("Synthetic non-serving referenced input\n"))
	}
	op := filepath.Join(root, "operator.yaml")
	raw, _ := yaml.Marshal(c)
	write(op, raw)
	runtime := c
	runtime.Database.DSNFile = filepath.Join(root, "runtime-dsn")
	credential, e = databaseaccess.NewCredential()
	if e != nil {
		t.Fatal(e)
	}
	u.User = url.UserPassword("synthetic_runtime", credential)
	write(runtime.Database.DSNFile, []byte(u.String()))
	r := restoreRequestFixture()
	r.RuntimeConfigurationFile = filepath.Join(root, "configuration", "runtime.yaml")
	r.ConfigurationRoot = filepath.Join(root, "configuration")
	r.BackupRoot = filepath.Join(root, "backups", "source")
	r.RestoreRoot = filepath.Join(root, "restores", "new")
	r.Policy.ScratchRoot = filepath.Join(root, "scratch")
	raw, _ = yaml.Marshal(runtime)
	write(r.RuntimeConfigurationFile, raw)
	c, e = config.LoadProtected(op)
	if e != nil {
		t.Fatal(e)
	}
	runtime, e = config.LoadProtected(r.RuntimeConfigurationFile)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.Mkdir(r.BackupRoot, 0700); e != nil {
		t.Fatal(e)
	}
	source := requestFixture()
	source.ConfigurationRoot = r.ConfigurationRoot
	source.BackupRoot = r.BackupRoot
	m := Manifest{Request: source, MVPSource: mvpBindingManifestFixture().MVPSource}
	if _, e = copyTree(context.Background(), r.ConfigurationRoot, filepath.Join(r.BackupRoot, "configuration"), r.Policy.MaximumConfigurationBytes, r.Policy.MaximumStoreFiles, r.Policy.MaximumManifestBytes, r.Policy.MinimumFreeBytes); e != nil {
		t.Fatal(e)
	}
	raw, _ = yaml.Marshal(runtime)
	write(filepath.Join(r.BackupRoot, effectiveName), raw)
	i := RestoreIntent{Request: r, ConfigurationSHA256: restoreConfigurationDigest(runtime)}
	if e = restoreInputClosure(context.Background(), c, runtime, op, m, r); e != nil {
		t.Fatal("complete source/target input bindings", e)
	}
	return restoreInputFixture{c, runtime, op, m, r, i}
}

func TestPostgresRestoreFinalConfigurationAndReusableBindings(t *testing.T) {
	for _, name := range []string{"same-inputs-existing-record", "runtime-uri-newline", "operator-change", "runtime-change", "key-bytes-change", "source-locale-change", "runtime-wrong-database"} {
		t.Run(name, func(t *testing.T) {
			f := newRestoreInputFixture(t)
			ctx := context.Background()
			if e := os.Mkdir(f.request.RestoreRoot, 0700); e != nil {
				t.Fatal(e)
			}
			if e := restoreInputClosure(ctx, f.operator, f.runtime, f.operatorPath, f.manifest, f.request); e == nil || e.Error() != "POSTGRES_RESTORE_SCOPE_EXISTS" {
				t.Fatal("fresh-only predicate missing", e)
			}
			if e := finalRestoreConfiguration(ctx, f.operator, f.runtime, f.operatorPath, "", f.manifest, f.intent); e != nil {
				t.Fatal("reusable binding incorrectly requires record absence", e)
			}
			if name == "same-inputs-existing-record" {
				return
			}
			if name == "runtime-uri-newline" {
				raw, e := os.ReadFile(f.runtime.Database.DSNFile)
				if e != nil {
					t.Fatal(e)
				}
				if e = os.WriteFile(f.runtime.Database.DSNFile, append(raw, '\n'), 0600); e != nil {
					t.Fatal(e)
				}
				if _, e = databaseaccess.LoadRuntime(f.request.RuntimeConfigurationFile); e != nil {
					t.Fatal("existing URI producer normalization", e)
				}
				if e = finalRestoreConfiguration(ctx, f.operator, f.runtime, f.operatorPath, "", f.manifest, f.intent); e != nil {
					t.Fatal("same semantic runtime URI refused", e)
				}
				return
			}
			switch name {
			case "operator-change":
				c := f.operator
				c.Database.MaxOpenConnections++
				raw, _ := yaml.Marshal(c)
				if e := os.WriteFile(f.operatorPath, raw, 0600); e != nil {
					t.Fatal(e)
				}
			case "runtime-change":
				c := f.runtime
				c.Database.MaxOpenConnections++
				raw, _ := yaml.Marshal(c)
				if e := os.WriteFile(f.request.RuntimeConfigurationFile, raw, 0600); e != nil {
					t.Fatal(e)
				}
			case "key-bytes-change":
				if e := os.WriteFile(f.runtime.Security.AuthHMACKeyFile, []byte(strings.Repeat("x", 32)), 0600); e != nil {
					t.Fatal(e)
				}
			case "source-locale-change":
				raw, _ := yaml.Marshal(map[string]any{"security": map[string]any{"auth_hmac_key_file": f.runtime.Security.AuthHMACKeyFile, "mail_keyring_file": f.runtime.Security.MailKeyringFile}, "localization": map[string]any{"required_locales": []string{"en"}}})
				if e := os.WriteFile(filepath.Join(f.request.BackupRoot, effectiveName), raw, 0600); e != nil {
					t.Fatal(e)
				}
			case "runtime-wrong-database":
				raw, e := os.ReadFile(f.runtime.Database.DSNFile)
				if e != nil {
					t.Fatal(e)
				}
				u, e := url.Parse(string(raw))
				if e != nil {
					t.Fatal(e)
				}
				u.Path = "/another_target"
				if e = os.WriteFile(f.runtime.Database.DSNFile, []byte(u.String()), 0600); e != nil {
					t.Fatal(e)
				}
			}
			if e := finalRestoreConfiguration(ctx, f.operator, f.runtime, f.operatorPath, "", f.manifest, f.intent); e == nil {
				t.Fatal("changed input accepted")
			}
			if _, e := os.Stat(filepath.Join(f.request.RestoreRoot, restoreCompleteName)); !os.IsNotExist(e) {
				t.Fatal("changed input published completion")
			}
		})
	}
}
