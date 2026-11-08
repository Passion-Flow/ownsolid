//go:build repository_integration && postgres_backup_integration

package postgresbackup

import (
	"github.com/Passion-Flow/ownsolid/api/release/releasetest"
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/imageregistry"
	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/postgresmaintenance"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/tools/release/offline/pkg/bundle"
	release "github.com/Passion-Flow/ownsolid/api/release/v1"
)

const nativeMVPOCIManifest = "application/vnd.oci.image.manifest.v1+json"

// Mirror the original serialized CAS contract only in this synthetic producer.
// Export/import and independent readers execute the unchanged registry owner.
type nativeMVPRegistryImage struct {
	Asset                 release.Asset              `json:"asset"`
	ReleaseID             string                     `json:"release_id"`
	ReleaseManifestSHA256 string                     `json:"release_manifest_sha256"`
	ReleaseArchiveSHA256  string                     `json:"release_archive_sha256"`
	Inventory             bundle.ImageInventory      `json:"inventory"`
	Archive               bundle.ImportReceipt       `json:"archive"`
	Descriptors           []imageregistry.Descriptor `json:"descriptors"`
	Proof                 bundle.ImageProof          `json:"release_proof"`
}

func nativeMVPWritePrivate(root *os.Root, name string, raw []byte) error {
	f, e := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return e
	}
	_, e = f.Write(raw)
	if e == nil {
		e = f.Sync()
	}
	closeError := f.Close()
	if e != nil {
		return e
	}
	return closeError
}

// This early critical-path case uses initialized original stores and genuine
// tiny locally signed OCI bytes. It does not substitute for the populated
// production model/preparation, installed current-image, HTTP or M26 checks.
func TestActualMVPInitializedStoresSupplementNativeFreshRestore(t *testing.T) {
	ids := []string{uuid.NewString(), uuid.NewString()}
	var images []nativeMVPRegistryImage
	configure := func(c *config.Config) {
		registry := imageregistry.Defaults()
		registry.Endpoint = "https://127.0.0.1:44902"
		registry.AllowedCIDRs = []string{"127.0.0.0/8"}
		registry.CAFile = "/fixture/configuration/registry-ca.pem"
		registry.CredentialFile = "/fixture/configuration/registry-credentials.json"
		registry.StoreRoot = "/fixture/source-registry"
		registry.PreparedReleaseRoot = "/fixture/synthetic-registry-release"
		registry.Verification.MountRoot = "/fixture"
		registry.Verification.TrustFile = "/fixture/configuration/registry-trust.json"
		if e := os.Mkdir(registry.StoreRoot, 0700); e != nil {
			t.Fatal(e)
		}
		_, _, ca, e := restoredHTTPTLS()
		if e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(registry.CAFile, ca, 0600); e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(registry.CredentialFile, []byte(`{"username":"synthetic-native-fixture","password":"not-a-real-registry-credential"}`), 0600); e != nil {
			t.Fatal(e)
		}
		images = nativeMVPSyntheticRegistry(t, &registry, ids)
		if e = registry.Validate(); e != nil {
			t.Fatal(e)
		}
		c.ImageRegistryConfigFile = "/fixture/configuration/registry.json"
		c.ImageRegistry = &registry
		raw, e := json.Marshal(registry)
		if e != nil || os.WriteFile(c.ImageRegistryConfigFile, raw, 0600) != nil {
			t.Fatal("typed registry inputs before SQL", e)
		}
	}
	f := newActualPostgresBackupFixtureWithTools(t, 180*time.Second, 120, 1<<30, 300, 8<<20, actualPostgresFixtureTools{clientDistribution: release.PostgreSQLClientPGDGBookworm, packagedImage: true, applicationProfile: release.MVPPostgreSQLCPUApplicationProfile, configure: configure})
	var tenant, workspace, user string
	if e := f.pool.QueryRow(f.ctx, `SELECT tenant_id::text,default_workspace_id::text,primary_admin_user_id::text FROM public.installation_state WHERE id=1`).Scan(&tenant, &workspace, &user); e != nil {
		t.Fatal(e)
	}
	workspaces := []string{workspace, uuid.NewString()}
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, e := f.pool.Exec(f.ctx, sql, args...); e != nil {
			t.Fatal("owned registry synthetic seed", e)
		}
	}
	exec(`INSERT INTO public.workspaces(id,tenant_id,name,status) VALUES($1,$2,'Second synthetic native registry Workspace','active')`, workspaces[1], tenant)
	for i, image := range images {
		inventory, e := json.Marshal(image)
		if e != nil {
			t.Fatal(e)
		}
		exec(`INSERT INTO public.image_registry_pins(id,tenant_id,workspace_id,asset_id,source_archive_sha256,source_manifest_sha256,backend_identity,state,manifest_digest,config_digest,archive_sha256,archive_bytes,inventory,inventory_sha256,created_by,actor_epoch) VALUES($1,$2,$3,$4,$5,$6,$7,'ready',$8,$9,$10,$11,$12::jsonb,$13,$14,1)`, ids[i], tenant, workspaces[i], image.Asset.ID, image.ReleaseArchiveSHA256, image.ReleaseManifestSHA256, "sha256:"+strings.Repeat("a", 64), image.Inventory.ManifestDigest, image.Inventory.ConfigDigest, image.Archive.TransportSHA256, image.Archive.TransportBytes, string(inventory), release.Digest(inventory), user)
		exec(`INSERT INTO public.image_registry_imports(tenant_id,workspace_id,pin_id,owner_token,phase) VALUES($1,$2,$3,$4,'completed')`, tenant, workspaces[i], ids[i], uuid.NewString())
		for ordinal, d := range image.Descriptors {
			exec(`INSERT INTO public.image_registry_blobs(tenant_id,workspace_id,pin_id,ordinal,digest,size,media_type,kind) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, tenant, workspaces[i], ids[i], ordinal, d.Digest, d.Size, d.MediaType, d.Kind)
		}
		exec(`INSERT INTO public.image_registry_offline_refs(id,tenant_id,workspace_id,pin_id,created_by) VALUES($1,$2,$3,$4,$5)`, uuid.NewString(), tenant, workspaces[i], ids[i], user)
	}
	tx, e := f.lease.BeginTx(f.ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly, IsoLevel: pgx.RepeatableRead})
	if e != nil {
		t.Fatal(e)
	}
	maintenance, e := postgresmaintenance.CheckWithin(f.ctx, transaction.PostgreSQLTransaction(tx))
	closeError := tx.Rollback(f.ctx)
	if e != nil || closeError != nil {
		t.Fatal("original full maintenance gate before backup", e, closeError)
	}
	maintenanceBytes, e := canonical(maintenance)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile("/fixture/native-maintenance.json", maintenanceBytes, 0600); e != nil {
		t.Fatal(e)
	}
	f.request.MaintenanceSHA256 = digest(maintenanceBytes)
	raw, e := canonical(f.request)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile("/fixture/request-native.json", raw, 0600); e != nil {
		t.Fatal(e)
	}
	f.close()
	actualPostgresRestoreWithAuthorityCustody(t, 180*time.Second, false, true)
	var m Manifest
	if _, e = readPrivateDocument("/fixture/backups/authority-source/candidate.json", 1<<20, &m); e != nil {
		t.Fatal(e)
	}
	if m.Database.SchemaVersion != 51 || m.Database.Tables != 115 || len(m.Stores) != 4 || m.MVPSource == nil || m.MVPSource.Registry == nil || m.MVPSource.Registry.Receipt.Pins != 2 || m.MVPSource.Registry.Receipt.Files < 12 {
		t.Fatal("complete native MVP and supplementary proofs required")
	}
	var r release.PostgreSQLRestoreRequest
	if _, e = readPrivateDocument("/fixture/request-restore.json", 1<<20, &r); e != nil {
		t.Fatal(e)
	}
	if _, e = ReadbackRestoreFiles(t.Context(), r); e != nil {
		t.Fatal("independent target file readback after role preparation", e)
	}
	var i RestoreIntent
	if _, e = readPrivateDocument(filepath.Join(r.RestoreRoot, restoreIntentName), 1<<20, &i); e != nil || i.RegistryRoot == nil {
		t.Fatal("supplement root identity missing", e)
	}
	registryRoot := i.RegistryRoot.Path
	if e = os.Rename(registryRoot, registryRoot+"-retained-original"); e != nil {
		t.Fatal(e)
	}
	if _, e = copyTree(t.Context(), registryRoot+"-retained-original", registryRoot, r.Policy.MaximumStoreBytes, r.Policy.MaximumStoreFiles, r.Policy.MaximumManifestBytes, r.Policy.MinimumFreeBytes); e != nil {
		t.Fatal(e)
	}
	if _, e = ReadbackRestoreFiles(t.Context(), r); e == nil {
		t.Fatal("equal-byte supplementary root substitution accepted")
	}
	if e = os.Rename(registryRoot, registryRoot+"-rejected-copy"); e != nil {
		t.Fatal(e)
	}
	if e = os.Rename(registryRoot+"-retained-original", registryRoot); e != nil {
		t.Fatal(e)
	}
	if _, e = ReadbackRestoreFiles(t.Context(), r); e != nil {
		t.Fatal("original supplementary root not recovered", e)
	}
	t.Logf("actual native profile=%s schema=%d tables=%d stores=%d registry_pins=%d registry_files=%d; real SQL export/import, fresh CAS, original authority invalidation and rejected inode replacement; initialized/synthetic critical path only", r.EffectiveApplicationProfile(), m.Database.SchemaVersion, m.Database.Tables, len(m.Stores), m.MVPSource.Registry.Receipt.Pins, m.MVPSource.Registry.Receipt.Files)
}

func nativeMVPSyntheticRegistry(t *testing.T, c *imageregistry.Config, ids []string) []nativeMVPRegistryImage {
	t.Helper()
	encode := func(v any) []byte {
		raw, e := json.Marshal(v)
		if e != nil {
			t.Fatal(e)
		}
		return raw
	}
	var layer bytes.Buffer
	tw := tar.NewWriter(&layer)
	payload := []byte("Synthetic registry supplementary snapshot fixture.\n")
	if e := tw.WriteHeader(&tar.Header{Name: "fixture.txt", Mode: 0644, Size: int64(len(payload))}); e != nil {
		t.Fatal(e)
	}
	if _, e := tw.Write(payload); e != nil {
		t.Fatal(e)
	}
	if e := tw.Close(); e != nil {
		t.Fatal(e)
	}
	var compressed bytes.Buffer
	gz := gzip.NewWriter(&compressed)
	if _, e := gz.Write(layer.Bytes()); e != nil {
		t.Fatal(e)
	}
	if e := gz.Close(); e != nil {
		t.Fatal(e)
	}
	diff := "sha256:" + release.Digest(layer.Bytes())
	config := encode(map[string]any{"architecture": "amd64", "os": "linux", "rootfs": map[string]any{"type": "layers", "diff_ids": []string{diff}}})
	configDigest, layerDigest := "sha256:"+release.Digest(config), "sha256:"+release.Digest(compressed.Bytes())
	layerType := "application/vnd.oci.image.layer.v1.tar+gzip"
	manifest := encode(map[string]any{"schemaVersion": 2, "mediaType": nativeMVPOCIManifest, "config": map[string]any{"digest": configDigest, "size": len(config), "mediaType": "application/vnd.oci.image.config.v1+json"}, "layers": []any{map[string]any{"digest": layerDigest, "size": compressed.Len(), "mediaType": layerType}}})
	manifestDigest := "sha256:" + release.Digest(manifest)
	index := encode(map[string]any{"schemaVersion": 2, "manifests": []any{map[string]any{"mediaType": nativeMVPOCIManifest, "digest": manifestDigest, "size": len(manifest), "platform": map[string]string{"os": "linux", "architecture": "amd64"}}}})
	files := map[string][]byte{"oci-layout": []byte(`{"imageLayoutVersion":"1.0.0"}`), "index.json": index, "blobs/sha256/" + manifestDigest[7:]: manifest, "blobs/sha256/" + configDigest[7:]: config, "blobs/sha256/" + layerDigest[7:]: compressed.Bytes()}
	var archive bytes.Buffer
	at := tar.NewWriter(&archive)
	names := []string{}
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		raw := files[name]
		if e := at.WriteHeader(&tar.Header{Name: name, Mode: 0644, Size: int64(len(raw)), Typeflag: tar.TypeReg, Format: tar.FormatUSTAR}); e != nil {
			t.Fatal(e)
		}
		if _, e := at.Write(raw); e != nil {
			t.Fatal(e)
		}
	}
	if e := at.Close(); e != nil {
		t.Fatal(e)
	}
	asset := release.Asset{ID: "synthetic-snapshot-image", Path: "images/synthetic.tar", Kind: "image_archive", Size: int64(archive.Len()), SHA256: release.Digest(archive.Bytes()), Licenses: []string{"platform-license"}, ImageID: manifestDigest, ImageConfigDigest: configDigest, Architecture: "amd64"}
	m := release.Manifest{FormatVersion: 1, ReleaseID: "explicit-synthetic-operator-snapshot", Profile: release.MVPPostgreSQLCPUProfile, SourceSHA256: strings.Repeat("a", 64), SchemaVersion: 16, MinimumPredecessor: 16, MaximumPredecessor: 16, Prerequisites: []string{"Synthetic declaration-only release; no runtime qualification"}, Assets: []release.Asset{asset}}
	releasetest.Select(&m)
	required := release.RequiredAssets(m.Profile)
	requiredIDs := []string{}
	for id := range required {
		requiredIDs = append(requiredIDs, id)
	}
	sort.Strings(requiredIDs)
	for _, id := range requiredIDs {
		a := release.Asset{ID: id, Path: id, Kind: required[id], Size: 1, SHA256: strings.Repeat("b", 64), Licenses: []string{"platform-license"}}
		if a.Kind == "license" {
			a.Licenses = []string{}
		}
		if a.Kind == "image_archive" {
			a.ImageID = manifestDigest
			a.ImageConfigDigest = configDigest
			a.Architecture = "amd64"
		}
		m.Assets = append(m.Assets, a)
	}
	releasetest.Pin(&m)
	if e := m.Validate(c.Verification.MaximumAssets, c.Verification.MaximumAssetBytes, c.Verification.MaximumTotalBytes, false); e != nil {
		t.Fatal(e)
	}
	public, key, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(c.Verification.TrustFile, encode(map[string]any{"keys": map[string]string{release.Digest(public): base64.StdEncoding.EncodeToString(public)}}), 0600); e != nil {
		t.Fatal(e)
	}
	signed := encode(m)
	signature := release.Signature{Algorithm: release.SignatureAlgorithm, KeyID: release.Digest(public), ManifestSHA256: release.Digest(signed), Value: base64.StdEncoding.EncodeToString(ed25519.Sign(key, append([]byte("projectone-offline-release-v1\n"), signed...)))}
	c.ReleaseManifestSHA256 = release.Digest(signed)
	c.ReleaseArchiveSHA256 = release.Digest(archive.Bytes())
	proof := bundle.ImageProof{FormatVersion: 1, Manifest: signed, Signature: encode(signature), AssetID: asset.ID}
	if _, e = bundle.VerifyImageProof(t.Context(), c.Verification, proof); e != nil {
		t.Fatal("actual synthetic signature", e)
	}
	var inventory bundle.ImageInventory
	if e = json.Unmarshal(encode(map[string]any{"manifest_digest": manifestDigest, "config_digest": configDigest, "os": "linux", "architecture": "amd64", "diff_ids": []string{diff}, "layers": []any{map[string]any{"digest": layerDigest, "media_type": layerType, "size_bytes": compressed.Len(), "diff_id": diff}}, "expanded_bytes": layer.Len()}), &inventory); e != nil {
		t.Fatal(e)
	}
	root, e := openPrivateRoot(c.StoreRoot)
	if e != nil {
		t.Fatal(e)
	}
	defer root.Close()
	out := []nativeMVPRegistryImage{}
	for _, id := range ids {
		if e = root.Mkdir(id, 0700); e != nil {
			t.Fatal(e)
		}
		if e = nativeMVPWritePrivate(root, id+"/archive.tar", archive.Bytes()); e != nil {
			t.Fatal(e)
		}
		image := nativeMVPRegistryImage{Asset: asset, ReleaseID: m.ReleaseID, ReleaseManifestSHA256: c.ReleaseManifestSHA256, ReleaseArchiveSHA256: c.ReleaseArchiveSHA256, Inventory: inventory, Archive: bundle.ImportReceipt{SourceSHA256: asset.SHA256, TransportSHA256: asset.SHA256, TransportBytes: asset.Size, ExpandedBytes: int64(layer.Len())}, Proof: proof}
		image.Descriptors = []imageregistry.Descriptor{{Digest: manifestDigest, Size: int64(len(manifest)), MediaType: nativeMVPOCIManifest, Kind: "manifest"}, {Digest: configDigest, Size: int64(len(config)), MediaType: "application/vnd.oci.image.config.v1+json", Kind: "config"}, {Digest: layerDigest, Size: int64(compressed.Len()), MediaType: layerType, Kind: "layer"}}
		if e = root.Mkdir(id+"/blobs", 0700); e != nil {
			t.Fatal(e)
		}
		for name, data := range files {
			if strings.HasPrefix(name, "blobs/sha256/") {
				if e = nativeMVPWritePrivate(root, id+"/blobs/"+strings.TrimPrefix(name, "blobs/sha256/"), data); e != nil {
					t.Fatal(e)
				}
			}
		}
		if e = nativeMVPWritePrivate(root, id+"/manifest.json", manifest); e != nil {
			t.Fatal(e)
		}
		if e = nativeMVPWritePrivate(root, id+"/inventory.json", encode(image)); e != nil {
			t.Fatal(e)
		}
		original, e := bundle.OpenImageSource(t.Context(), c.Verification, filepath.Join(c.StoreRoot, id, "archive.tar"), asset, c.MaximumExpandedBytes)
		if e != nil {
			t.Fatal("real OCI archive before SQL", e)
		}
		// The parser also keeps unexported measurements; CAS stores its exact
		// public JSON contract. Compare every serialized field, not those private
		// parser fields that an independent producer cannot initialize.
		if !bytes.Equal(encode(original.Inventory()), encode(inventory)) {
			original.Close()
			t.Fatal("real OCI inventory mismatch")
		}
		if e = original.Close(); e != nil {
			t.Fatal(e)
		}

		out = append(out, image)
	}
	return out
}
