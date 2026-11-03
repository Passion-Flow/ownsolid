//go:build cgo && odbc_manager

package damengbackup

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"database/sql/driver"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/mysqlstore"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/registryconfig"
	"strings"
	"testing"
	"time"
)

func TestProjectionCopiesExactPrivateBytesAndRejectsMutation(t *testing.T) {
	parent := projectionFixtureRoot(t)
	source := filepath.Join(parent, "source")
	target := filepath.Join(parent, "target")
	if e := os.Mkdir(source, 0700); e != nil {
		t.Fatal(e)
	}
	writeTest(t, filepath.Join(source, "keyring.json"), []byte("synthetic retained keyring bytes\n"))
	p := rowTestPolicy()
	before, e := copyConfigurationProjection(context.Background(), source, target, p, 1)
	if e != nil || before.Files != 1 {
		t.Fatal(e)
	}
	raw, e := os.ReadFile(filepath.Join(target, "keyring.json"))
	if e != nil || string(raw) != "synthetic retained keyring bytes\n" {
		t.Fatal("copy lost bytes")
	}
	if _, e = copyConfigurationProjection(context.Background(), source, target, p, 1); e == nil {
		t.Fatal("existing projection overwritten")
	}
	link := filepath.Join(source, "outside")
	if e = os.Symlink("/etc/passwd", link); e != nil {
		t.Fatal(e)
	}
	if _, e = copyConfigurationProjection(context.Background(), source, filepath.Join(parent, "unsafe"), p, 1); e == nil {
		t.Fatal("projection symlink admitted")
	}
}
func TestImagePinsCannotExemptSecrets(t *testing.T) {
	c := config.Config{}
	c.Security.AuthHMACKeyFile = "/opt/projectone/private/auth.key"
	r := testOperatorRequest()
	r.ImageRoots = []ImageRootBinding{{"/opt/projectone", sha([]byte("caller pin"))}}
	if projectionReferences(c, r) == nil {
		t.Fatal("image declaration exempted an authentication secret")
	}
	root := projectionFixtureRoot(t)
	r.ConfigurationRoot = root
	c.Security.AuthHMACKeyFile = filepath.Join(root, "auth.key")
	writeTest(t, c.Security.AuthHMACKeyFile, []byte("synthetic original hmac"))
	if e := projectionReferences(c, r); e != nil {
		t.Fatal(e)
	}
}

func TestProjectionImageSymlinksBindOnlyDeclaredTargets(t *testing.T) {
	root := projectionFixtureRoot(t)
	image := filepath.Join(root, "image")
	if e := os.Mkdir(image, 0700); e != nil {
		t.Fatal(e)
	}
	writeTest(t, filepath.Join(image, "python"), []byte("synthetic interpreter bytes"))
	if e := os.Symlink("python", filepath.Join(image, "venv-python")); e != nil {
		t.Fatal(e)
	}
	allowed := func(path string) bool { return withinRoot(image, path) }
	p := rowTestPolicy()
	first, e := digestImageAssets(context.Background(), image, p, allowed)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.Remove(filepath.Join(image, "venv-python")); e != nil {
		t.Fatal(e)
	}
	if e = os.Symlink("./python", filepath.Join(image, "venv-python")); e != nil {
		t.Fatal(e)
	}
	second, e := digestImageAssets(context.Background(), image, p, allowed)
	if e != nil || first == second {
		t.Fatal("changed link bytes not bound")
	}
	if e = os.Remove(filepath.Join(image, "venv-python")); e != nil {
		t.Fatal(e)
	}
	if e = os.Symlink("/etc/passwd", filepath.Join(image, "venv-python")); e != nil {
		t.Fatal(e)
	}
	if _, e = digestImageAssets(context.Background(), image, p, allowed); e == nil {
		t.Fatal("unpinned host fallback admitted")
	}
}
func TestProjectionResolversRefuseOutsideFilesBeforeDecoder(t *testing.T) {
	r := testOperatorRequest()
	for _, raw := range []string{"image_registry_config_file: /foreign/registry.json\n", "mongodb_config_file: /foreign/mongodb.json\n", "licensing:\n  environment_file: /foreign/license.env\n"} {
		if admitApplicationResolution([]byte(raw), r) == nil {
			t.Fatal("outside resolver admitted")
		}
	}
	c := config.Config{}
	c.MongoDB.Sources = []config.MongoDBSource{{URIFile: "/foreign/mongodb.secret"}}
	if projectionReferences(c, r) == nil {
		t.Fatal("json-owned URI reference escaped")
	}
}
func TestProjectionRestoreVerificationRejectsChangedBytesBeforeSQL(t *testing.T) {
	parent := projectionFixtureRoot(t)
	r := testOperatorRequest()
	r.Kind = "restore"
	r.ConfirmFreshTarget = true
	r.SourceRoot = filepath.Join(parent, "original")
	r.OutputRoot = filepath.Join(parent, "restore")
	r.ConfigurationRoot = filepath.Join(parent, "configuration")
	r.ApplicationConfigurationFile = filepath.Join(r.ConfigurationRoot, "platform.yaml")
	root, e := newRoot(r.OutputRoot)
	if e != nil {
		t.Fatal(e)
	}
	defer root.Close()
	application := config.Config{}
	projection := ConfigurationProjection{Root: r.ConfigurationRoot, EffectiveSHA256: nativePlatformConfigurationSHA256(application), Images: r.ImageRoots}
	intent := operatorIntent{1, r, fingerprint(r), projection.EffectiveSHA256, projection, ""}
	if e = writeNew(root, "operator.intent.json", jsonBytes(intent)); e != nil {
		t.Fatal(e)
	}
	projection.Tree.Bytes = 1
	inputs := operatorInputs{}
	if _, e = verifyRestoredOperator(context.Background(), &inputs, application, r, projection); e == nil || inputs.ownerOpened {
		t.Fatal("changed projection reached SQL or was sealed")
	}
	if _, e = root.Lstat("operator.complete.json"); !os.IsNotExist(e) {
		t.Fatal("changed projection completion was written")
	}
}

func projectionFixtureRoot(t *testing.T) string {
	t.Helper()
	root, e := os.MkdirTemp("/tmp", "p1-20261103-M29-cli-")
	if e != nil {
		t.Fatal(e)
	}
	info, e := os.Lstat(root)
	if e != nil {
		t.Fatal(e)
	}
	original := identity(info)
	t.Cleanup(func() {
		current, e := os.Lstat(root)
		if e != nil || identity(current) != original || noSymlinks(root) != nil {
			t.Errorf("owned projection fixture changed; retained")
			return
		}
		if e = os.RemoveAll(root); e != nil {
			t.Errorf("owned projection cleanup failed")
		}
	})
	return root
}

func TestProjectionEphemeralOutputsRemainDistinctWithoutReadingThem(t *testing.T) {
	r := testOperatorRequest()
	c := config.Config{}
	c.Knowledge.ScratchRoot = "/private/separate-ephemeral/knowledge"
	c.ObjectStorage = &config.ObjectStorageConfiguration{StagingRoot: "/private/separate-ephemeral/object-stage"}
	if e := projectionReferences(c, r); e != nil {
		t.Fatal(e)
	}
	c.Knowledge.ScratchRoot = r.ConfigurationRoot
	if projectionReferences(c, r) == nil {
		t.Fatal("ephemeral output overlaps read-only configuration")
	}
	c.Knowledge.ScratchRoot = "relative"
	if projectionReferences(c, r) == nil {
		t.Fatal("relative scratch admitted")
	}
	c.Knowledge.ScratchRoot = "/private/separate-ephemeral/knowledge"
	c.ObjectStorage.StagingRoot = r.OutputRoot
	if projectionReferences(c, r) == nil {
		t.Fatal("staging overwrites operation proof")
	}
}

func TestProjectionTypedNestedMySQLConfigurationAndOverride(t *testing.T) {
	root := projectionFixtureRoot(t)
	r := testOperatorRequest()
	r.ConfigurationRoot = root
	public, private, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	certificate := &x509.Certificate{SerialNumber: big.NewInt(1), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	der, e := x509.CreateCertificate(rand.Reader, certificate, certificate, public, private)
	if e != nil {
		t.Fatal(e)
	}
	ca := filepath.Join(root, "ca.pem")
	writeTest(t, ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	first, second := filepath.Join(root, "first.secret"), filepath.Join(root, "second.secret")
	writeTest(t, first, []byte("synthetic first"))
	writeTest(t, second, []byte("synthetic second"))
	c := mysqlstore.Configuration{Host: "localhost", Port: 3306, Database: "projectone", User: "runtime", PasswordFile: first, CAFile: ca, ServerName: "localhost", ExpectedVersion: "8.4.8", Policy: mysqlstore.DefaultPolicy()}
	path, env := filepath.Join(root, "mysql.json"), filepath.Join(root, "mysql.env")
	writeTest(t, path, jsonBytes(c))
	selected, e := projectionNestedMySQL(path, "", r)
	if e != nil || selected.PasswordFile != first {
		t.Fatal("real typed base rejected", e)
	}
	writeTest(t, env, []byte("PROJECT_ONE_MYSQL_PASSWORD_FILE="+second+"\n"))
	selected, e = projectionNestedMySQL(path, env, r)
	if e != nil || selected.PasswordFile != second {
		t.Fatal("real typed override not selected", e)
	}
	if e = os.Remove(second); e != nil {
		t.Fatal(e)
	}
	if _, e = projectionNestedMySQL(path, env, r); e == nil {
		t.Fatal("missing selected credential admitted")
	}
	writeTest(t, second, []byte("synthetic second"))
	writeTest(t, env, []byte("PROJECT_ONE_MYSQL_PASSWORD_FILE=/foreign/credential\n"))
	if _, e = projectionNestedMySQL(path, env, r); e == nil || strings.Contains(e.Error(), "/foreign") {
		t.Fatal("escaping selected credential admitted or leaked")
	}
	writeTest(t, env, []byte("PROJECT_ONE_MYSQL_CA_FILE=/foreign/ca.pem\n"))
	if _, e = projectionNestedMySQL(path, env, r); e == nil {
		t.Fatal("escaping selected trust admitted")
	}
}
func TestProjectionUnknownRolesAndOtherStateOwnersRefuseExplicitly(t *testing.T) {
	r := testOperatorRequest()
	unknown := struct {
		Reference string `yaml:"unknown_file"`
	}{"/private/opaque"}
	if e := projectionOwnerReferences(unknown, "future_owner", r); e == nil || e.Error() != "DAMENG_RECOVERY_REFERENCE_ROLE_UNIMPLEMENTED" {
		t.Fatal("unknown reference role gained file authority", e)
	}
	c := config.Config{}
	c.ImageRegistry = &registryconfig.Config{StoreRoot: "/private/registry-state"}
	if e := projectionReferences(c, r); e == nil || e.Error() != "DAMENG_RECOVERY_STATE_OWNER_BACKUP_UNIMPLEMENTED" {
		t.Fatal("mutable registry state misclassified as configuration", e)
	}
	c.ImageRegistry = nil
	c.Models.ArtifactRoot = "/private/model-state"
	if e := projectionReferences(c, r); e != nil {
		t.Fatal("existing four-store role treated as secret", e)
	}
}

func TestProjectionInitialNativeProfileCannotHidePersistedState(t *testing.T) {
	for _, name := range []string{"empty", "persisted", "missing_catalog", "unavailable"} {
		t.Run(name, func(t *testing.T) {
			f := &rowFixture{}
			if name == "persisted" {
				f.values = [][]driver.Value{{int64(1)}}
			}
			db := rowTestDB(t, f)
			catalog := initialNativeProfileTables()
			if name == "missing_catalog" {
				catalog = catalog[1:]
			}
			if name == "unavailable" {
				if e := db.Close(); e != nil {
					t.Fatal(e)
				}
			}
			e := readInitialNativeProfile(context.Background(), db, catalog)
			if name == "empty" {
				if e != nil {
					t.Fatal(e)
				}
			} else if e == nil {
				t.Fatal("unproven absence admitted")
			}
		})
	}
}

func TestProjectionInitialProfileProofBindsOriginalDatabaseAndRows(t *testing.T) {
	path := projectionFixtureRoot(t)
	root, e := openPrivateRoot(path)
	if e != nil {
		t.Fatal(e)
	}
	defer root.Close()
	database := Complete{Database: State{ServerID: "synthetic-source", EndpointSHA256: sha([]byte("endpoint")), RowsSHA256: sha([]byte("original rows"))}}
	completion := operatorComplete{DatabaseSHA256: fingerprint(database)}
	proof := initialProfileProof{FormatVersion: 1, Profile: initialNativeProfile, Root: path, DatabaseCompleteSHA256: completion.DatabaseSHA256, RowsSHA256: database.Database.RowsSHA256, ServerID: database.Database.ServerID, EndpointSHA256: database.Database.EndpointSHA256, Tables: initialNativeProfileTables(), BeforeExport: true, AfterCapture: true}
	raw := jsonBytes(proof)
	if e = writeNew(root, "profile.absent.json", raw); e != nil {
		t.Fatal(e)
	}
	completion.InitialProfileSHA256 = sha(raw)
	if e = readInitialProfileProof(root, path, completion, database); e != nil {
		t.Fatal("original bound proof rejected", e)
	}
	changed := database
	changed.Database.RowsSHA256 = sha([]byte("other rows"))
	if readInitialProfileProof(root, path, completion, changed) == nil {
		t.Fatal("different rows reused absence proof")
	}
	changedCompletion := completion
	changedCompletion.DatabaseSHA256 = sha([]byte("other completion"))
	if readInitialProfileProof(root, path, changedCompletion, database) == nil {
		t.Fatal("different database completion reused absence proof")
	}
	proof.BeforeExport = false
	raw = jsonBytes(proof)
	writeTest(t, filepath.Join(path, "profile.absent.json"), raw)
	completion.InitialProfileSHA256 = sha(raw)
	if readInitialProfileProof(root, path, completion, database) == nil {
		t.Fatal("unproven pre-export absence accepted")
	}
}
