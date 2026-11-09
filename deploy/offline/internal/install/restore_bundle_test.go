package install

import (
	"bytes"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	license "github.com/Passion-Flow/ownsolid/pkg/license/v1"
	"go.yaml.in/yaml/v3"
)

func assertSignedRestoreIsolatedDocker(t *testing.T, installer, config, archive, verification string, licenseKey license.PrivateKey, run func(string, ...string) (string, error)) {
	t.Helper()
	source, err := LoadOperational(config)
	if err != nil {
		t.Fatal(err)
	}
	active, err := readBundleBinding(source)
	if err != nil || active == nil {
		t.Fatalf("source release binding: %v", err)
	}
	command := func(t *testing.T, binary, want string, fails bool, args ...string) string {
		t.Helper()
		out, err := run(binary, args...)
		t.Logf("%s:\n%s", args[0], out)
		if (err != nil) != fails || !strings.Contains(out, want) {
			t.Fatalf("want %s (failure=%t), got %v", want, fails, err)
		}
		return out
	}
	write := func(path string, raw []byte) {
		t.Helper()
		if err := os.WriteFile(path, raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	read := func(path string) []byte {
		t.Helper()
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	privateDir := func() string {
		t.Helper()
		path := t.TempDir()
		if err := os.Chmod(path, 0o700); err != nil {
			t.Fatal(err)
		}
		return path
	}
	query := func(t *testing.T, site Config, sql string) string {
		t.Helper()
		out, err := newOperation(site, ExecRunner{}, io.Discard).query(t.Context(), databaseName, sql)
		if err != nil {
			t.Fatalf("PostgreSQL query: %v", err)
		}
		return out
	}
	f, err := os.Open(archive)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.New()
	_, err = io.Copy(digest, f)
	if err := errors.Join(err, f.Close()); err != nil {
		t.Fatal(err)
	}
	t.Logf("one signed archive sha256=%x manifest_sha256=%s", digest.Sum(nil), active.ManifestSHA256)

	const tenant = "SELECT name FROM tenants WHERE id=(SELECT tenant_id FROM installation_state WHERE id=1)"
	const marker = "Signed recovery point"
	query(t, source, "UPDATE tenants SET name='"+marker+"' WHERE id=(SELECT tenant_id FROM installation_state WHERE id=1)")
	admin := installedAdministrator(t, newOperation(source, ExecRunner{}, io.Discard))
	credential := read(source.CredentialPath())
	var modelID string
	if model := filepath.Join(os.Getenv("OWNSOLID_E2E_MODEL_ARCHIVE"), "model.tar"); exists(model) {
		workspace := query(t, source, "SELECT default_workspace_id FROM installation_state WHERE id=1")
		modelID = publishColdRestoreModel(t, coldRestoreLogin(t, source, source.Origins.Admin, credential), licenseKey, workspace, model)
	} else {
		t.Log("no approved model archive was staged for this recovery drill")
	}
	plainDir, archiveDir, keyDir := privateDir(), privateDir(), privateDir()
	old := filepath.Join(os.Getenv("OWNSOLID_E2E_MODEL_ARCHIVE"), "legacy-ownsolid-install")
	command(t, old, "BACKUP_COMPLETE", false, "backup", "--config", config, "--output", plainDir)
	backups, err := filepath.Glob(filepath.Join(plainDir, backupPrefix+"*"))
	if err != nil || len(backups) != 1 {
		t.Fatalf("expected one verified legacy backup, got %v: %v", backups, err)
	}
	plain := backups[0]
	legacy, err := readBackup(plain)
	if err != nil || legacy.ReleaseManifestSHA256 != "" || legacy.ControlImage != source.Images.Control || legacy.PostgresImage != source.Images.Postgres {
		t.Fatalf("legacy manifest must have matching image IDs and no release digest: %v", err)
	}
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	unrelated, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	key, wrong, recipient := filepath.Join(keyDir, "identity"), filepath.Join(keyDir, "wrong"), filepath.Join(keyDir, "recipient")
	write(key, []byte(identity.String()+"\n"))
	write(wrong, []byte(unrelated.String()+"\n"))
	write(recipient, []byte(identity.Recipient().String()+"\n"))
	encrypted := filepath.Join(archiveDir, "verified.tar.age")
	command(t, installer, "BACKUP_COMPLETE", false, "backup", "--config", config, "--output", encrypted, "--recipient-file", recipient)
	_, sealed, cleanup, err := loadEncryptedBackup(t.Context(), source, encrypted, key, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	cleanup()
	if sealed.ReleaseManifestSHA256 != active.ManifestSHA256 {
		t.Fatal("encrypted backup did not record the exact signed release")
	}

	down := func(t *testing.T, site Config) {
		t.Helper()
		if out, err := run("docker", composeArguments(site, "down")...); err != nil {
			t.Fatalf("stop isolated project: %v: %s", err, out)
		}
	}
	clearImages := func(t *testing.T) {
		t.Helper()
		if out, err := run("docker", "image", "rm", source.Images.Control, source.Images.Postgres, source.Images.Metrics, source.Images.Logs); err != nil {
			t.Fatalf("remove the four images from the isolated daemon: %v: %s", err, out)
		}
	}
	down(t, source)
	clearImages(t)

	target := func(t *testing.T, name string) (Config, string) {
		t.Helper()
		site := source
		site.InstallRoot = filepath.Join(filepath.Dir(source.InstallRoot), name)
		site.Project = source.Project + "-" + name
		site.ComposeFile, site.Images = "", ImagesConfig{}
		path := filepath.Join(t.TempDir(), "site.yaml")
		raw, err := yaml.Marshal(site)
		if err != nil {
			t.Fatal(err)
		}
		write(path, raw)
		return site, path
	}
	restore := func(t *testing.T, site, release, from, want string, fails bool, extra ...string) {
		t.Helper()
		args := []string{"restore-bundle", "--config", site, "--bundle", release, "--verification", verification, "--from", from}
		command(t, installer, want, fails, append(args, extra...)...)
	}
	assertEmpty := func(t *testing.T, site Config) {
		t.Helper()
		for _, path := range []string{site.Layout().ComposeFile, site.Layout().Postgres, site.Layout().Secrets,
			filepath.Join(site.InstallRoot, bundleBindingName), filepath.Join(site.InstallRoot, encryptedWorkDirectory, "data")} {
			if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("invalid input prepared deployment state at %s: %v", filepath.Base(path), err)
			}
		}
		for _, args := range [][]string{
			{"ps", "--all", "--quiet", "--filter", "label=com.docker.compose.project=" + site.Project},
			{"image", "ls", "--quiet"},
		} {
			if out, err := run("docker", args...); err != nil || strings.TrimSpace(out) != "" {
				t.Fatalf("invalid input created a container or image: %v: %s", err, out)
			}
		}
	}
	operational := func(t *testing.T, path string) Config {
		t.Helper()
		site, err := LoadOperational(path)
		if err != nil {
			t.Fatal(err)
		}
		return site
	}
	assertRecovered := func(t *testing.T, path string) {
		t.Helper()
		site := operational(t, path)
		binding, err := readBundleBinding(site)
		if err != nil || binding == nil || *binding != *active {
			t.Fatalf("restored release binding differs from the source: %v", err)
		}
		if query(t, site, tenant) != marker || installedAdministrator(t, newOperation(site, ExecRunner{}, io.Discard)) != admin ||
			!bytes.Equal(credential, read(site.CredentialPath())) {
			t.Fatal("restore did not preserve tenant, workspace and administrator state")
		}
		out := command(t, installer, site.Origins.Portal+": ready", false, "status", "--config", path)
		if !strings.Contains(out, site.Origins.Admin+": ready") {
			t.Fatal("Admin did not become ready")
		}
		admin := coldRestoreLogin(t, site, site.Origins.Admin, credential)
		coldRestoreLogin(t, site, site.Origins.Portal, credential)
		if modelID != "" {
			assertColdRestoreModel(t, admin, site, modelID)
		}
		t.Log("both entrances served the UI and authenticated the restored administrator; tenant and workspace match")
		down(t, site)
		clearImages(t)
	}

	t.Run("rejects invalid inputs on an empty target", func(t *testing.T) {
		site, path := target(t, "bad")
		truncated := filepath.Join(filepath.Dir(archive), "truncated.tar.gz")
		restore(t, path, truncated, plain, "INSTALL_BUNDLE_VERIFICATION_FAILED", true)
		assertEmpty(t, site)

		dumpPath := filepath.Join(plain, backupDatabaseName)
		dump := read(dumpPath)
		write(dumpPath, []byte("damaged fixture"))
		restore(t, path, archive, plain, "INSTALL_BACKUP_CORRUPT", true)
		write(dumpPath, dump)
		assertEmpty(t, site)

		manifestPath := filepath.Join(plain, backupManifestName)
		original := read(manifestPath)
		mismatch := legacy
		mismatch.ReleaseManifestSHA256 = strings.Repeat("0", 64)
		raw, err := json.Marshal(mismatch)
		if err != nil {
			t.Fatal(err)
		}
		write(manifestPath, raw)
		restore(t, path, archive, plain, "INSTALL_BACKUP_RELEASE_MISMATCH", true)
		write(manifestPath, original)
		assertEmpty(t, site)

		restore(t, path, archive, encrypted, "INSTALL_ENCRYPTED_BACKUP_WRONG_IDENTITY", true, "--identity-file", wrong)
		assertEmpty(t, site)
		tampered := filepath.Join(archiveDir, "tampered.tar.age")
		if _, err := copyPrivateFile(encrypted, tampered); err != nil {
			t.Fatal(err)
		}
		f, err := os.OpenFile(tampered, os.O_RDWR, 0)
		if err != nil {
			t.Fatal(err)
		}
		const position = 4096
		var value [1]byte
		if _, err := f.ReadAt(value[:], position); err != nil {
			f.Close()
			t.Fatal(err)
		}
		value[0] ^= 1
		_, err = f.WriteAt(value[:], position)
		if err := errors.Join(err, f.Close()); err != nil {
			t.Fatal(err)
		}
		restore(t, path, archive, tampered, "INSTALL_ENCRYPTED_BACKUP_INVALID", true, "--identity-file", key)
		assertEmpty(t, site)
	})
	if t.Failed() {
		return
	}
	t.Run("rolls back a cold writer failure then restores the legacy backup", func(t *testing.T) {
		site, path := target(t, "old")
		t.Run("writer start fails after database exchange", func(t *testing.T) {
			interruptInstallerDocker(t, "fail-control")
			restore(t, path, archive, plain, "RESTORE_ROLLED_BACK", true)
		})
		installed := operational(t, path)
		if query(t, installed, "SELECT count(*) FROM installation_state WHERE bootstrap_complete") != "0" {
			t.Fatal("rollback kept the restored database active")
		}
		out, err := run("docker", composeArguments(site, "ps", "--status", "running", "--quiet", "control")...)
		if err != nil || strings.TrimSpace(out) != "" {
			t.Fatalf("rollback started the fresh target's writer: %v: %s", err, out)
		}
		if err := restorePendingError(site); err != nil {
			t.Fatal(err)
		}
		restore(t, path, archive, plain, "RESTORE_COMPLETE", false)
		assertRecovered(t, path)
	})
	if t.Failed() {
		return
	}
	t.Run("restores the encrypted backup on a separate empty target", func(t *testing.T) {
		site, path := target(t, "age")
		assertEmpty(t, site)
		restore(t, path, archive, encrypted, "RESTORE_COMPLETE", false, "--identity-file", key)
		assertRecovered(t, path)
	})
}

type coldRestoreBrowser struct {
	origin string
	client *http.Client
}

func (b coldRestoreBrowser) request(t *testing.T, method, path string, body io.Reader, headers map[string]string, want int) []byte {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), method, b.origin+path, body)
	if err != nil {
		t.Fatal(err)
	}
	if method == http.MethodPost {
		req.Header.Set("Origin", b.origin)
		req.Header.Set("Content-Type", "application/json")
	}
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	resp, err := b.client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != want {
		var problem struct {
			Code string `json:"code"`
		}
		_ = json.Unmarshal(raw, &problem)
		t.Fatalf("%s %s returned HTTP %d at %s (%s), want %d", method, path, resp.StatusCode, b.origin, problem.Code, want)
	}
	return raw
}

func (b coldRestoreBrowser) csrf(t *testing.T) string {
	t.Helper()
	var csrf struct {
		Token string `json:"token"`
	}
	b.get(t, "/api/v1/auth/csrf", &csrf)
	if csrf.Token == "" {
		t.Fatalf("%s did not issue a CSRF token", b.origin)
	}
	return csrf.Token
}

func (b coldRestoreBrowser) get(t *testing.T, path string, target any) {
	t.Helper()
	decodeColdRestoreResponse(t, b.request(t, http.MethodGet, path, nil, nil, http.StatusOK), target)
}

func decodeColdRestoreResponse(t *testing.T, raw []byte, target any) {
	t.Helper()
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if json.Unmarshal(raw, &envelope) != nil || json.Unmarshal(envelope.Data, target) != nil {
		t.Fatal("response did not contain the expected data")
	}
}

func coldRestoreJSON(t *testing.T, value any) io.Reader {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return bytes.NewReader(raw)
}

func coldRestoreLogin(t *testing.T, site Config, origin string, credential []byte) coldRestoreBrowser {
	t.Helper()
	var intent struct {
		Password string `json:"password"`
	}
	if json.Unmarshal(credential, &intent) != nil || intent.Password == "" {
		t.Fatal("restored administrator credential is invalid")
	}
	authority, err := os.ReadFile(filepath.Join(site.Layout().Config, "ca.pem"))
	roots := x509.NewCertPool()
	if err != nil || !roots.AppendCertsFromPEM(authority) {
		t.Fatal("restored CA is unavailable")
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}
	t.Cleanup(transport.CloseIdleConnections)
	b := coldRestoreBrowser{origin: origin, client: &http.Client{Jar: jar, Transport: transport, Timeout: 45 * time.Second}}
	if page := b.request(t, http.MethodGet, "/", nil, nil, http.StatusOK); !bytes.Contains(page, []byte("<html")) {
		t.Fatalf("%s did not serve the UI", origin)
	}
	payload := coldRestoreJSON(t, map[string]string{"email": site.Administrator.Email, "password": intent.Password})
	b.request(t, http.MethodPost, "/api/v1/auth/login", payload, map[string]string{"X-CSRF-Token": b.csrf(t)}, http.StatusOK)
	b.request(t, http.MethodGet, "/api/v1/me", nil, nil, http.StatusOK)
	return b
}
