//go:build repository_integration && registry_integration

package imageregistry

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	release "github.com/Passion-Flow/ownsolid/api/release/v1"
	"github.com/Passion-Flow/ownsolid/tools/release/offline/pkg/bundle"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

type fixtureArchiveEntry struct {
	name string
	data []byte
}

func fixtureJSON(t *testing.T, v any) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func fixtureRead(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func fixtureTar(t *testing.T, entries []fixtureArchiveEntry) []byte {
	t.Helper()
	var out bytes.Buffer
	w := tar.NewWriter(&out)
	for _, entry := range entries {
		if err := w.WriteHeader(&tar.Header{Name: entry.name, Mode: 0644, Size: int64(len(entry.data)), Typeflag: tar.TypeReg, Format: tar.FormatUSTAR}); err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(entry.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func prepareRegistryTestRelease(t *testing.T, mount, profile string, img nativeTransportImage, manifest Descriptor, raw []byte) Config {
	t.Helper()
	c := Defaults()
	c.RequestSeconds, c.OperationSeconds, c.GrantSeconds = 5, 30, 30
	c.MaximumBlobBytes, c.MaximumExpandedBytes, c.MaximumArchiveBytes, c.MaximumRetainedBytes = 1<<20, 1<<20, 1<<20, 2<<20
	c.Verification.MountRoot = mount
	c.Verification.MinimumFreeBytes = 1 << 20
	c.Verification.AllowTestFixture = profile == release.FixtureProfile
	root := t.TempDir()
	c.Verification.TrustFile = filepath.Join(root, "trust.json")
	c.PreparedReleaseRoot = filepath.Join(root, "prepared")

	index := fixtureJSON(t, map[string]any{"schemaVersion": 2, "manifests": []nativeOCIReference{{manifest.MediaType, manifest.Digest, manifest.Size}}})
	archive := fixtureTar(t, []fixtureArchiveEntry{
		{"oci-layout", []byte(`{"imageLayoutVersion":"1.0.0"}`)},
		{"index.json", index},
		{"blobs/sha256/" + manifest.Digest[7:], raw},
		{"blobs/sha256/" + img.config.Digest[7:], fixtureRead(t, img.configFile)},
		{"blobs/sha256/" + img.layer.Digest[7:], fixtureRead(t, img.layerFile)},
	})
	m := release.Manifest{FormatVersion: 1, ReleaseID: "site-admission-fixture", Profile: profile,
		SourceSHA256: release.Digest(archive), SchemaVersion: 1, MinimumPredecessor: 1, MaximumPredecessor: 1,
		Capabilities: []string{}, Prerequisites: []string{"Isolated Linux fixture"}}
	var entries []fixtureArchiveEntry
	add := func(id, kind string, payload []byte) {
		asset := release.Asset{ID: id, Path: "assets/" + id, Kind: kind, Size: int64(len(payload)), SHA256: release.Digest(payload), Licenses: []string{}}
		if kind != "license" {
			asset.Licenses = []string{"fixture-notice"}
		}
		if kind == "image_archive" {
			asset.Architecture, asset.ImageID, asset.ImageConfigDigest = "amd64", manifest.Digest, img.config.Digest
		}
		m.Assets = append(m.Assets, asset)
		entries = append(entries, fixtureArchiveEntry{asset.Path, payload})
	}
	add("fixture-notice", "license", []byte("Synthetic image bytes used only by the integration test.\n"))
	add("control-image", "image_archive", archive)
	if profile == bundle.CurrentProfile {
		for _, id := range []string{"postgres-image", "metrics-image", "logs-image"} {
			add(id, "image_archive", archive)
		}
		add("compose", "compose", []byte("Synthetic Compose asset.\n"))
		add("installer", "operator", []byte("Synthetic operator asset.\n"))
		add("operator-docs", "runbook", []byte("Synthetic operator documentation.\n"))
		packages := []map[string]any{}
		for _, a := range m.Assets {
			packages = append(packages, map[string]any{"SPDXID": "SPDXRef-" + a.ID, "name": a.ID,
				"checksums": []map[string]string{{"algorithm": "SHA256", "checksumValue": a.SHA256}}})
		}
		add("release-sbom", "sbom", fixtureJSON(t, map[string]any{"spdxVersion": "SPDX-2.3", "dataLicense": "CC0-1.0",
			"SPDXID": "SPDXRef-DOCUMENT", "name": "Site admission fixture", "documentNamespace": "https://ownsolid.test/site-admission", "packages": packages}))
	}

	pub, private, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	key := release.Digest(pub)
	trust := fixtureJSON(t, map[string]any{"keys": map[string]string{key: base64.StdEncoding.EncodeToString(pub)}})
	signed := fixtureJSON(t, m)
	c.ReleaseManifestSHA256 = release.Digest(signed)
	sig := release.Signature{Algorithm: release.SignatureAlgorithm, KeyID: key, ManifestSHA256: c.ReleaseManifestSHA256,
		Value: base64.StdEncoding.EncodeToString(ed25519.Sign(private, append([]byte("projectone-offline-release-v1\n"), signed...)))}
	entries = append([]fixtureArchiveEntry{{"manifest.json", signed}, {"signature.json", fixtureJSON(t, sig)}}, entries...)
	var compressed bytes.Buffer
	zip := gzip.NewWriter(&compressed)
	if _, err := zip.Write(fixtureTar(t, entries)); err != nil {
		t.Fatal(err)
	}
	if err := zip.Close(); err != nil {
		t.Fatal(err)
	}
	c.ReleaseArchiveSHA256 = release.Digest(compressed.Bytes())
	file := filepath.Join(root, "release.tar.gz")
	for path, data := range map[string][]byte{file: compressed.Bytes(), c.Verification.TrustFile: trust} {
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}

	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	if profile == bundle.CurrentProfile {
		_, err = bundle.UnpackCurrent(ctx, c.Verification, file, c.PreparedReleaseRoot)
	} else {
		_, err = bundle.Unpack(ctx, c.Verification, file, c.PreparedReleaseRoot, c.ReleaseArchiveSHA256, c.ReleaseManifestSHA256)
	}
	if err != nil {
		t.Fatal("signed fixture preparation failed", profile, err)
	}
	return c
}

func seedSiteAdmissionFixture(t *testing.T, g *registryStory, img nativeTransportImage, manifest Descriptor, raw []byte) {
	t.Helper()
	remote := g.s.remote
	owned := repository(g.f.Tenant, g.f.Workspace, uuid.NewString())
	site := g.native.Endpoint + "/v2/ownsolid/release/control-image/"
	for _, blob := range []struct {
		descriptor Descriptor
		file       string
	}{{img.config, img.configFile}, {img.layer, img.layerFile}} {
		if err := remote.PutBlob(g.ctx, owned, blob.descriptor, blob.file); err != nil {
			t.Fatal(err)
		}
		query := url.Values{"mount": {blob.descriptor.Digest}, "from": {owned}}
		res, err := remote.request(g.ctx, http.MethodPost, site+"blobs/uploads/?"+query.Encode(), "", nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusCreated {
			t.Fatal("site fixture blob mount", res.StatusCode)
		}
	}
	res, err := remote.request(g.ctx, http.MethodPut, site+"manifests/"+manifest.Digest, manifest.MediaType, bytes.NewReader(raw), manifest.Size)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatal("site fixture manifest publish", res.StatusCode)
	}
}

func readonlySiteClient(t *testing.T, g *registryStory) (*distributionClient, siteSourceConfig) {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	listener.Close()
	root := g.native.Root
	dir := t.TempDir()
	path := filepath.Join(dir, "registry.yml")
	htpasswd, credentialFile := filepath.Join(dir, "htpasswd"), filepath.Join(dir, "credential.json")
	var passwordBytes [24]byte
	rand.Read(passwordBytes[:])
	credential := registryCredential{Username: "site-reader", Password: base64.RawURLEncoding.EncodeToString(passwordBytes[:])}
	hash, err := bcrypt.GenerateFromPassword([]byte(credential.Password), bcrypt.DefaultCost)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(htpasswd, fmt.Appendf(nil, "%s:%s\n", credential.Username, hash), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(credentialFile, fixtureJSON(t, credential), 0600); err != nil {
		t.Fatal(err)
	}
	raw := fmt.Sprintf(`version: 0.1
log:
  level: warn
storage:
  filesystem:
    rootdirectory: %q
  maintenance:
    readonly:
      enabled: true
    uploadpurging:
      enabled: false
http:
  addr: %q
  tls:
    certificate: %q
    key: %q
auth:
  htpasswd:
    realm: site-fixture
    path: %q
`, filepath.Join(root, "data"), addr, filepath.Join(root, "server.pem"), filepath.Join(root, "server-key.pem"), htpasswd)
	if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	log, err := os.Create(filepath.Join(filepath.Dir(path), "registry.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	run, cancel := context.WithCancel(g.ctx)
	cmd := exec.CommandContext(run, g.native.Binary, "serve", path)
	cmd.Env = []string{"GOMAXPROCS=1", "GOMEMLIMIT=128MiB", "OTEL_TRACES_EXPORTER=none"}
	cmd.Stdout, cmd.Stderr = log, log
	if err := cmd.Start(); err != nil {
		cancel()
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	t.Cleanup(func() { cancel(); <-done })

	c := g.s.config
	c.Endpoint = "https://" + addr
	c.CredentialFile = credentialFile
	client, err := newDistribution(c)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	ctx, stop := context.WithTimeout(g.ctx, 5*time.Second)
	defer stop()
	for {
		resp, err := client.request(ctx, http.MethodGet, c.Endpoint+"/v2/", "", nil, 0)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return client, siteSourceConfig{Endpoint: c.Endpoint, AllowedCIDRs: c.AllowedCIDRs, CAFile: c.CAFile, CredentialFile: c.CredentialFile}
			}
		}
		select {
		case <-done:
			t.Fatalf("read-only Distribution stopped: %s", fixtureRead(t, log.Name()))
		case <-ctx.Done():
			t.Fatalf("read-only Distribution readiness timed out: %s", fixtureRead(t, log.Name()))
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func assertSiteDigestReads(t *testing.T, g *registryStory, site *distributionClient, descriptors ...Descriptor) {
	t.Helper()
	base := site.origin.String() + "/v2/ownsolid/release/control-image/"
	for _, d := range descriptors {
		kind := "blobs/"
		if d.Kind == "manifest" {
			kind = "manifests/"
		}
		res, err := site.request(g.ctx, http.MethodGet, base+kind+d.Digest, "", nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := io.ReadAll(io.LimitReader(res.Body, d.Size+1))
		res.Body.Close()
		if err != nil || res.StatusCode != http.StatusOK || int64(len(raw)) != d.Size || digestBytes(raw) != d.Digest {
			t.Fatal("site fixture digest readback failed", err, res.StatusCode)
		}
	}
	res, err := site.request(g.ctx, http.MethodPost, base+"blobs/uploads/", "", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusMethodNotAllowed {
		t.Fatal("site fixture did not refuse a write", res.StatusCode)
	}
}

func TestActualSiteRegistryPinAdmissionBoundary(t *testing.T) {
	mount := os.Getenv("OWNSOLID_TEST_REGISTRY_MOUNT")
	if !filepath.IsAbs(mount) || !withinPath(mount, t.TempDir()) {
		t.Fatal("an isolated real mount and TMPDIR inside it are required")
	}
	img := nativeTransportBytes(t, t.TempDir())
	approved, approvedRaw := img.manifest(t, "approved synthetic asset")
	current := prepareRegistryTestRelease(t, mount, bundle.CurrentProfile, img, approved, approvedRaw)
	g := registrySetupWithConfig(t, current)

	if _, _, err := bundle.PreparedImageProof(g.ctx, current.Verification, current.PreparedReleaseRoot, current.ReleaseArchiveSHA256, current.ReleaseManifestSHA256, "control-image"); err == nil || bundle.ErrorCode(err) != "RELEASE_PREPARED_INVENTORY_INVALID" {
		t.Fatalf("unexpected prepared proof boundary: %v", err)
	}
	unsigned, raw := img.manifest(t, "digest present in the registry but absent from the release")
	seedSiteAdmissionFixture(t, g, img, unsigned, raw)
	site, siteConfig := readonlySiteClient(t, g)
	assertSiteDigestReads(t, g, site, img.config, img.layer, unsigned)
	previous := g.s
	previous.deployments.RegistryActions = nil
	configFile := siteSourceFixtureFile(t, siteConfig)
	service, err := NewWithSiteReleaseSource(previous.identity, previous.workspaces, previous.nodes, previous.deployments, previous.license, previous.config, configFile)
	if err != nil {
		t.Fatal(err)
	}
	g.s = service
	t.Cleanup(service.Close)
	wrong := *site
	wrong.credential = previous.remote.credential
	response, err := wrong.request(g.ctx, http.MethodGet, site.origin.String()+"/v2/", "", nil, 0)
	if response != nil {
		response.Body.Close()
	}
	if err == nil || err.Error() != "REGISTRY_AUTH_REJECTED" {
		t.Fatalf("writable credentials admitted by the site source: %v", err)
	}
	t.Logf("isolated Distribution endpoints: writer %s, read-only %s", g.native.Endpoint, site.origin)
	mutation := g.mutation()
	refuses := func(asset, code string) {
		t.Helper()
		pin, err := g.s.Import(g.ctx, g.a, mutation, asset, true)
		files, readErr := os.ReadDir(g.s.config.StoreRoot)
		if err == nil || code != "" && err.Error() != code || !reflect.DeepEqual(pin, Pin{}) ||
			g.count("image_registry_pins") != 0 || g.count("image_registry_mutations") != 0 || readErr != nil || len(files) != 0 {
			t.Fatalf("invalid source reserved a pin: %+v %v", pin, err)
		}
	}
	refuses("control-image", "REGISTRY_RELEASE_INVALID")

	// Enable the existing synthetic release verifier only inside this fixture.
	legacy := prepareRegistryTestRelease(t, mount, release.FixtureProfile, img, approved, approvedRaw)
	g.s.config.Verification = legacy.Verification
	g.s.config.PreparedReleaseRoot = legacy.PreparedReleaseRoot
	g.s.config.ReleaseArchiveSHA256, g.s.config.ReleaseManifestSHA256 = legacy.ReleaseArchiveSHA256, legacy.ReleaseManifestSHA256
	refuses(unsigned.Digest, "REGISTRY_RELEASE_INVALID")
	refuses("control-image@"+unsigned.Digest, "REGISTRY_RELEASE_INVALID")

	fault := &registryFaultStore{Store: g.f.Store, table: "image_registry_pins"}
	fault.armed.Store(true)
	g.s.identity.Storage = fault
	refuses("control-image", "")
	if fault.armed.Load() {
		t.Fatal("reservation did not reach the rollback boundary")
	}
	g.s.identity.Storage = g.f.Store
	// The signed archive has the requested manifest, but the site has not
	// published it yet. The incomplete source read cannot fall back or push.
	if pin, err := g.s.Import(g.ctx, g.a, mutation, "control-image", true); err == nil || err.Error() != "REGISTRY_CONTENT_MISSING" || !reflect.DeepEqual(pin, Pin{}) {
		t.Fatalf("missing source content was admitted: %+v %v", pin, err)
	}
	incomplete, err := g.s.Import(g.ctx, g.a, mutation, "control-image", true)
	if err != nil || incomplete.State != "importing" || incomplete.ManifestDigest != nil {
		t.Fatalf("incomplete source changed on replay: %+v %v", incomplete, err)
	}
	if err := g.s.remote.Verify(g.ctx, repository(g.f.Tenant, g.f.Workspace, incomplete.ID), approved); err == nil || err.Error() != "REGISTRY_CONTENT_MISSING" {
		t.Fatalf("failed preparation wrote the tenant Registry: %v", err)
	}
	discard, err := g.s.AbandonPrepared(g.ctx, g.a, g.mutation(), incomplete.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := g.s.DiscardFailed(g.ctx, g.a, g.mutation(), incomplete.ID, discard.SHA256); err != nil {
		t.Fatal(err)
	}

	seedSiteAdmissionFixture(t, g, img, approved, approvedRaw)
	assertSiteDigestReads(t, g, site, approved, img.config, img.layer)
	mutation = g.mutation()
	// An actual SQL failure rolls back binding the freshly verified CAS.
	fault = &registryFaultStore{Store: g.f.Store, table: "image_registry_blobs"}
	fault.armed.Store(true)
	g.s.identity.Storage = fault
	if pin, err := g.s.Import(g.ctx, g.a, mutation, "control-image", true); err == nil || !reflect.DeepEqual(pin, Pin{}) || fault.armed.Load() {
		t.Fatalf("expected a rolled-back CAS bind: %+v %v", pin, err)
	}
	g.s.identity.Storage = g.f.Store
	pending, err := g.s.Import(g.ctx, g.a, mutation, "control-image", true)
	if err != nil || pending.State != "importing" || pending.ManifestDigest != nil {
		t.Fatalf("bind rollback left ready metadata: %+v %v", pending, err)
	}
	g.s.siteSource.client.Close()
	g.s.siteSource.client.http.Transport = unavailableSiteFixture{t}
	pin, err := g.s.Reconcile(g.ctx, g.a, g.mutation(), pending.ID, true)
	if err != nil || pin.ID != pending.ID || pin.State != "ready" || pin.WorkspaceID != g.f.Workspace ||
		pin.ManifestDigest == nil || *pin.ManifestDigest != approved.Digest {
		t.Fatalf("explicit retry did not complete the verified site pin: %+v %v", pin, err)
	}
	cache, err := readCached(g.ctx, g.s.config, pin.ID)
	if err != nil || cachedMatches(pin, cache) != nil || cache.ReleaseArchiveSHA256 != legacy.ReleaseArchiveSHA256 ||
		cache.ReleaseManifestSHA256 != legacy.ReleaseManifestSHA256 || !reflect.DeepEqual(cache.Asset.Licenses, []string{"fixture-notice"}) ||
		cache.SiteSource == nil || cache.SiteSource.RegistryIdentity != site.identity ||
		cache.SiteSource.Repository != "ownsolid/release/control-image" || cache.SiteSource.ManifestDigest != approved.Digest {
		t.Fatal("signed source and notices were not retained", err)
	}
	for _, d := range cache.Descriptors {
		if err := g.s.remote.Verify(g.ctx, repository(g.f.Tenant, g.f.Workspace, pin.ID), d); err != nil {
			t.Fatal("tenant registry readback failed", err)
		}
	}
	other := uuid.NewString()
	g.exec(`INSERT INTO workspaces(id,tenant_id,name,status) VALUES($1,$2,'Other Workspace','active')`, "", other, g.f.Tenant)
	if foreign, err := g.s.Get(g.ctx, g.a, other, pin.ID); err == nil || err.Error() != "RESOURCE_NOT_FOUND" || !reflect.DeepEqual(foreign, Pin{}) {
		t.Fatalf("pin crossed the Workspace boundary: %+v %v", foreign, err)
	}
	pins, mutations := g.count("image_registry_pins"), g.count("image_registry_mutations")
	g.s.config.PreparedReleaseRoot = filepath.Join(mount, "unavailable")
	replay, err := g.s.Import(g.ctx, g.a, mutation, "control-image", true)
	if err != nil || replay.ID != pin.ID || g.count("image_registry_pins") != pins || g.count("image_registry_mutations") != mutations {
		t.Fatalf("replay depended on the source or duplicated the pin: %+v %v", replay, err)
	}
	if _, err := g.s.Import(g.ctx, g.a, mutation, "different-image", true); err == nil || err.Error() != "IDEMPOTENCY_CONFLICT" {
		t.Fatalf("changed key fingerprint accepted: %v", err)
	}
}

type unavailableSiteFixture struct{ t *testing.T }

func (f unavailableSiteFixture) RoundTrip(*http.Request) (*http.Response, error) {
	f.t.Error("prepared reconciliation or replay contacted the site source")
	return nil, failure("REGISTRY_UNAVAILABLE")
}
