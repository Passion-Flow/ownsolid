package imageregistry

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	release "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// OperatorSnapshotBinding is the current caller's original stopped installation
// session. A historical receipt cannot authorize a different live session.
type OperatorSnapshotBinding struct {
	ApplicationProfile string `json:"application_profile"`
	TenantID           string `json:"tenant_id"`
	Database           string `json:"database"`
	Operator           string `json:"operator"`
	ScopeID            string `json:"scope_id"`
	SystemIdentifier   string `json:"system_identifier"`
	DatabaseOID        uint32 `json:"database_oid"`
	BackendPID         int    `json:"backend_pid"`
}

type OperatorSnapshotReceipt struct {
	Format                string                  `json:"format"`
	ManifestSHA256        string                  `json:"manifest_sha256"`
	Source                OperatorSnapshotBinding `json:"source"`
	SourceRoot            OperatorSnapshotRoot    `json:"source_root"`
	ConfigurationSHA256   string                  `json:"configuration_sha256"`
	ReleaseManifestSHA256 string                  `json:"release_manifest_sha256"`
	ReleaseArchiveSHA256  string                  `json:"release_archive_sha256"`
	TrustFileSHA256       string                  `json:"trust_file_sha256"`
	Pins                  int                     `json:"pins"`
	Files                 int                     `json:"files"`
	Bytes                 int64                   `json:"bytes"`
}

type OperatorSnapshotImportReceipt struct {
	Format                    string                  `json:"format"`
	Snapshot                  OperatorSnapshotReceipt `json:"snapshot"`
	Target                    OperatorSnapshotBinding `json:"target"`
	TargetRoot                OperatorSnapshotRoot    `json:"target_root"`
	TargetConfigurationSHA256 string                  `json:"target_configuration_sha256"`
	ReadbackSHA256            string                  `json:"readback_sha256"`
	Complete                  bool                    `json:"complete"`
}

type operatorPin struct {
	ID              string `json:"id"`
	WorkspaceID     string `json:"workspace_id"`
	InventorySHA256 string `json:"inventory_sha256"`
}
type operatorMetadata struct {
	Table  string `json:"table"`
	Rows   int    `json:"rows"`
	SHA256 string `json:"sha256"`
}
type operatorManifest struct {
	Format                string                  `json:"format"`
	Source                OperatorSnapshotBinding `json:"source"`
	SourceRoot            OperatorSnapshotRoot    `json:"source_root"`
	ConfigurationSHA256   string                  `json:"configuration_sha256"`
	ReleaseManifestSHA256 string                  `json:"release_manifest_sha256"`
	ReleaseArchiveSHA256  string                  `json:"release_archive_sha256"`
	TrustFileSHA256       string                  `json:"trust_file_sha256"`
	Pins                  []operatorPin           `json:"pins"`
	Metadata              []operatorMetadata      `json:"metadata"`
	Files                 []operatorFile          `json:"files"`
}

const operatorApplicationProfile = database.LineageName
const operatorManifestName = "registry-snapshot.json"
const operatorManifestFormat = "projectone.imageregistry.installation-snapshot.v1"

var operatorMetadataTables = [...]string{"image_registry_pins", "image_registry_blobs", "image_registry_imports", "image_registry_deployment_refs", "image_registry_grants", "image_registry_offline_refs", "image_registry_mutations"}

// OperatorSnapshot opens no transport, credential, pool, worker or transaction.
// The coordinator excludes physical writers throughout this owner's lifetime.
type OperatorSnapshot struct {
	config  Config
	binding OperatorSnapshotBinding
	root    OperatorSnapshotRoot
}

func validOperatorBinding(b OperatorSnapshotBinding) bool {
	return b.ApplicationProfile == operatorApplicationProfile && validID(b.TenantID) && len(b.ScopeID) == 32 && release.ValidSHA256(b.ScopeID+b.ScopeID) && b.Database != "" && b.Operator != "" && b.SystemIdentifier != "" && b.DatabaseOID > 0 && b.BackendPID > 0
}

func validOperatorReceipt(c Config, r OperatorSnapshotReceipt) bool {
	root := r.SourceRoot
	if r.Format != operatorManifestFormat || !validOperatorBinding(r.Source) ||
		!filepath.IsAbs(root.Path) || filepath.Clean(root.Path) != root.Path || root.Device == 0 || root.Inode == 0 ||
		r.Pins < 0 || r.Pins > c.MaximumPins || r.Files < r.Pins || r.Files > c.Verification.MaximumArchiveEntries || r.Bytes < 0 || r.Bytes > c.MaximumRetainedBytes ||
		(r.Pins == 0 && (r.Files != 0 || r.Bytes != 0)) || (r.Pins > 0 && r.Bytes == 0) {
		return false
	}
	for _, sha := range []string{r.ManifestSHA256, r.ConfigurationSHA256, r.ReleaseManifestSHA256, r.ReleaseArchiveSHA256, r.TrustFileSHA256} {
		if !release.ValidSHA256(sha) {
			return false
		}
	}
	return true
}

func NewOperatorSnapshot(c Config, b OperatorSnapshotBinding) (*OperatorSnapshot, error) {
	if c.Validate() != nil || !validOperatorBinding(b) {
		return nil, failure("REGISTRY_OPERATOR_CONFIGURATION_INVALID")
	}
	root, id, e := operatorRoot(c.StoreRoot)
	if e != nil {
		return nil, e
	}
	root.Close()
	mountPath, e := filepath.EvalSymlinks(c.Verification.MountRoot)
	if e != nil || mountPath != c.Verification.MountRoot {
		return nil, failure("REGISTRY_OPERATOR_STORAGE_IDENTITY_INVALID")
	}
	mountInfo, e := os.Lstat(mountPath)
	if e != nil || !mountInfo.IsDir() {
		return nil, failure("REGISTRY_OPERATOR_STORAGE_IDENTITY_INVALID")
	}
	mountStat, ok := mountInfo.Sys().(*syscall.Stat_t)
	if !ok || uint64(mountStat.Dev) != id.Device {
		return nil, failure("REGISTRY_OPERATOR_STORAGE_IDENTITY_INVALID")
	}
	return &OperatorSnapshot{c, b, id}, nil
}

func operatorConfigurationSHA(c Config) string { raw, _ := json.Marshal(c); return release.Digest(raw) }
func (o *OperatorSnapshot) checkWithin(ctx context.Context, q transaction.Executor) error {
	if o == nil || ctx == nil || q == nil || q.Dialect() != transaction.PostgreSQL {
		return failure("REGISTRY_OPERATOR_TRANSACTION_REQUIRED")
	}
	if _, pool := q.(transaction.Store); pool {
		return failure("REGISTRY_OPERATOR_TRANSACTION_REQUIRED")
	}
	if _, ok := ctx.Deadline(); !ok {
		return failure("REGISTRY_OPERATOR_DEADLINE_REQUIRED")
	}
	if e := ctx.Err(); e != nil {
		return e
	}
	b := o.binding
	var system, tenant, db, principal string
	var oid uint32
	var pid int
	var super, held, readonly bool
	if q.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, `SELECT (SELECT system_identifier::text FROM pg_catalog.pg_control_system()),d.oid,pg_catalog.pg_backend_pid(),d.datname,CURRENT_USER,(SELECT rolsuper FROM pg_catalog.pg_roles WHERE rolname=CURRENT_USER),i.tenant_id::text,EXISTS(SELECT 1 FROM pg_catalog.pg_locks WHERE locktype='advisory' AND pid=pg_catalog.pg_backend_pid() AND classid=0::oid AND objid=$1::bigint::oid AND objsubid=1 AND mode='ExclusiveLock' AND granted AND database=d.oid),pg_catalog.current_setting('transaction_read_only')='on' AND pg_catalog.current_schemas(true)=ARRAY['pg_catalog','public']::name[] FROM pg_catalog.pg_database d CROSS JOIN public.installation_state i JOIN public.tenants t ON t.id=i.tenant_id AND t.status='active' WHERE d.datname=pg_catalog.current_database() AND i.id=1 AND i.bootstrap_complete`, database.InstallationLockID)).Scan(&system, &oid, &pid, &db, &principal, &super, &tenant, &held, &readonly) != nil || system != b.SystemIdentifier || oid != b.DatabaseOID || pid != b.BackendPID || db != b.Database || principal != b.Operator || tenant != b.TenantID || !super || !held || !readonly {
		return failure("REGISTRY_OPERATOR_ROLE_IDENTITY_REQUIRED")
	}
	if e := database.CheckWithin(ctx, q); e != nil {
		return failure("REGISTRY_OPERATOR_PROFILE_REQUIRED")
	}
	r, id, e := operatorRoot(o.config.StoreRoot)
	if e != nil {
		return e
	}
	r.Close()
	if id != o.root {
		return failure("REGISTRY_OPERATOR_STORAGE_IDENTITY_INVALID")
	}
	return nil
}

// readPins uses the same original CAS metadata/state/descriptor contracts, with
// no browser actor and no FOR UPDATE in the caller's read-only transaction.
func (o *OperatorSnapshot) readPins(ctx context.Context, q transaction.Executor, files bool) ([]operatorPin, map[string]bool, error) {
	entries := []operatorPin{}
	names := map[string]bool{}
	cursor := ""
	count := 0
	for {
		rows, e := q.Query(ctx, transaction.SQL(transaction.PostgreSQL, "SELECT "+pinPG+" FROM public.image_registry_pins WHERE tenant_id=$1 AND ($2='' OR id>NULLIF($2,'')::uuid) ORDER BY id LIMIT $3", o.binding.TenantID, cursor, o.config.PageSize))
		if e != nil {
			return nil, nil, e
		}
		batch := []Pin{}
		for rows.Next() {
			p, e := scanPin(rows)
			if e != nil {
				rows.Close()
				return nil, nil, e
			}
			batch = append(batch, p)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return nil, nil, e
		}
		if len(batch) == 0 {
			break
		}
		for _, p := range batch {
			cursor = p.ID
			count++
			if count > o.config.MaximumReferences {
				return nil, nil, failure("REGISTRY_CAPACITY_REQUIRED")
			}
			var owner, phase string
			if e = q.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, `SELECT owner_token::text,phase FROM public.image_registry_imports WHERE tenant_id=$1 AND workspace_id=$2 AND pin_id=$3`, p.tenant, p.WorkspaceID, p.ID)).Scan(&owner, &phase); e != nil {
				return nil, nil, e
			}
			availability, e := storedPinState(p, owner, phase)
			if e != nil {
				return nil, nil, e
			}
			if e = storedBoundMetadata(p, o.config.Verification.MaximumMetadataBytes); e != nil {
				return nil, nil, e
			}
			if availability == storedPinDeleted {
				continue
			}
			if availability != storedPinReady {
				return nil, nil, failure("REGISTRY_RECOVERY_REQUIRED")
			}
			if !validID(p.ID) || !validID(p.WorkspaceID) || p.tenant != o.binding.TenantID || len(entries) >= o.config.MaximumPins {
				return nil, nil, failure("REGISTRY_STORED_STATE_INVALID")
			}
			var image cachedImage
			if release.StrictJSON(p.inventory, &image) != nil {
				return nil, nil, failure("REGISTRY_STORED_STATE_INVALID")
			}
			if files {
				image, e = readCached(ctx, o.config, p.ID)
				if e != nil {
					return nil, nil, e
				}
				if e = cachedMatches(p, image); e != nil {
					return nil, nil, e
				}
				if e = verifyOperatorImageManifest(ctx, o.config, p.ID, image); e != nil {
					return nil, nil, e
				}
			}
			raw, _ := json.Marshal(image)
			if p.inventorySHA == nil || *p.inventorySHA != release.Digest(raw) {
				return nil, nil, failure("REGISTRY_CONTENT_INVALID")
			}
			descriptors, e := q.Query(ctx, transaction.SQL(transaction.PostgreSQL, `SELECT digest,size,media_type,kind FROM public.image_registry_blobs WHERE tenant_id=$1 AND workspace_id=$2 AND pin_id=$3 ORDER BY ordinal`, p.tenant, p.WorkspaceID, p.ID))
			if e != nil {
				return nil, nil, e
			}
			i := 0
			for descriptors.Next() {
				var d Descriptor
				if e = descriptors.Scan(&d.Digest, &d.Size, &d.MediaType, &d.Kind); e != nil {
					break
				}
				if i >= len(image.Descriptors) || d != image.Descriptors[i] {
					e = failure("REGISTRY_STORED_STATE_INVALID")
					break
				}
				i++
			}
			if e == nil {
				e = descriptors.Err()
			}
			descriptors.Close()
			if e != nil {
				return nil, nil, e
			}
			if i != len(image.Descriptors) {
				return nil, nil, failure("REGISTRY_STORED_STATE_INVALID")
			}
			entries = append(entries, operatorPin{p.ID, p.WorkspaceID, *p.inventorySHA})
			for _, name := range imageFiles(p.ID, image) {
				names[name] = true
			}
		}
	}
	var invalid bool
	if q.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, `SELECT EXISTS(SELECT 1 FROM public.image_registry_pins WHERE tenant_id<>$1) OR EXISTS(SELECT 1 FROM public.image_registry_deployment_refs r JOIN public.image_registry_pins p ON p.id=r.pin_id JOIN public.deployments d ON d.id=r.deployment_id WHERE r.released_at IS NULL AND (p.state<>'ready' OR r.node_id<>d.node_id)) OR EXISTS(SELECT 1 FROM public.image_registry_offline_refs r JOIN public.image_registry_pins p ON p.id=r.pin_id WHERE r.released_at IS NULL AND p.state<>'ready')`, o.binding.TenantID)).Scan(&invalid) != nil || invalid {
		return nil, nil, failure("REGISTRY_STORED_STATE_INVALID")
	}
	return entries, names, nil
}

// All seven original tables, including original grant revocation facts, are
// retained exactly. Import precedes the native owner's single invalidation.
func (o *OperatorSnapshot) metadata(ctx context.Context, q transaction.Executor) ([]operatorMetadata, error) {
	out := []operatorMetadata{}
	remaining := o.config.Verification.MaximumManifestBytes
	for _, table := range operatorMetadataTables {
		value := "pg_catalog.to_jsonb(t)"
		rows, e := q.Query(ctx, transaction.SQL(transaction.PostgreSQL, `SELECT sha FROM (SELECT pg_catalog.encode(pg_catalog.sha256(pg_catalog.convert_to((`+value+`)::text,'UTF8')),'hex') AS sha FROM public.`+table+` t) measured ORDER BY sha COLLATE "C"`))
		if e != nil {
			return nil, e
		}
		h := sha256.New()
		entry := operatorMetadata{Table: table}
		for rows.Next() {
			var sha string
			if e = rows.Scan(&sha); e != nil {
				break
			}
			remaining -= 65
			if !release.ValidSHA256(sha) || remaining < 0 {
				e = failure("REGISTRY_CAPACITY_REQUIRED")
				break
			}
			h.Write([]byte(sha + "\n"))
			entry.Rows++
		}
		if e == nil {
			e = rows.Err()
		}
		rows.Close()
		if e != nil {
			return nil, e
		}
		entry.SHA256 = hex.EncodeToString(h.Sum(nil))
		out = append(out, entry)
	}
	return out, nil
}

func exactOperatorFiles(t *operatorTree, names map[string]bool, manifest bool) error {
	wanted := len(names)
	if manifest {
		wanted++
	}
	if len(t.files) != wanted {
		return failure("REGISTRY_SNAPSHOT_INVALID")
	}
	allowedDirs := map[string]bool{".": true}
	for name := range names {
		for dir := filepath.Dir(name); dir != "."; dir = filepath.Dir(dir) {
			allowedDirs[dir] = true
		}
	}
	for name, stamp := range t.stamps {
		if stamp.mode.IsDir() && !allowedDirs[name] {
			return failure("REGISTRY_SNAPSHOT_INVALID")
		}
	}
	for _, f := range t.files {
		if !names[f.Path] && !(manifest && f.Path == operatorManifestName) {
			return failure("REGISTRY_SNAPSHOT_INVALID")
		}
	}
	return nil
}

func (m operatorManifest) receipt(raw []byte) OperatorSnapshotReceipt {
	var size int64
	for _, f := range m.Files {
		size += f.Bytes
	}
	return OperatorSnapshotReceipt{operatorManifestFormat, release.Digest(raw), m.Source, m.SourceRoot, m.ConfigurationSHA256, m.ReleaseManifestSHA256, m.ReleaseArchiveSHA256, m.TrustFileSHA256, len(m.Pins), len(m.Files), size}
}

func (o *OperatorSnapshot) VerifyStoredWithin(ctx context.Context, q transaction.Executor) error {
	if e := o.checkWithin(ctx, q); e != nil {
		return e
	}
	t, e := scanOperatorTree(ctx, o.config.StoreRoot, o.config)
	if e != nil {
		return e
	}
	defer t.close()
	_, names, e := o.readPins(ctx, q, true)
	if e != nil {
		return e
	}
	if e = exactOperatorFiles(t, names, false); e != nil {
		return e
	}
	if e = t.check(ctx); e != nil {
		return e
	}
	return o.checkWithin(ctx, q)
}

func (o *OperatorSnapshot) ExportFilesWithin(ctx context.Context, q transaction.Executor, destination string) (OperatorSnapshotReceipt, error) {
	if e := o.checkWithin(ctx, q); e != nil {
		return OperatorSnapshotReceipt{}, e
	}
	if !filepath.IsAbs(destination) || filepath.Clean(destination) != destination || withinPath(o.config.StoreRoot, destination) || withinPath(destination, o.config.StoreRoot) {
		return OperatorSnapshotReceipt{}, failure("REGISTRY_SNAPSHOT_INVALID")
	}
	source, e := scanOperatorTree(ctx, o.config.StoreRoot, o.config)
	if e != nil {
		return OperatorSnapshotReceipt{}, e
	}
	defer source.close()
	pins, names, e := o.readPins(ctx, q, true)
	if e != nil {
		return OperatorSnapshotReceipt{}, e
	}
	if e = exactOperatorFiles(source, names, false); e != nil {
		return OperatorSnapshotReceipt{}, e
	}
	metadata, e := o.metadata(ctx, q)
	if e != nil {
		return OperatorSnapshotReceipt{}, e
	}
	trustSHA, e := operatorTrustSHA(ctx, o.config, o.config.Verification.TrustFile)
	if e != nil {
		return OperatorSnapshotReceipt{}, e
	}
	m := operatorManifest{operatorManifestFormat, o.binding, o.root, operatorConfigurationSHA(o.config), o.config.ReleaseManifestSHA256, o.config.ReleaseArchiveSHA256, trustSHA, pins, metadata, source.files}
	raw, e := json.Marshal(m)
	if e != nil || int64(len(raw)) > o.config.Verification.MaximumManifestBytes {
		return OperatorSnapshotReceipt{}, failure("REGISTRY_CAPACITY_REQUIRED")
	}
	parent, _, e := operatorRoot(filepath.Dir(destination))
	if e != nil {
		return OperatorSnapshotReceipt{}, e
	}
	defer parent.Close()
	if e = admitBytes(filepath.Dir(destination), source.bytes+int64(len(raw)), o.config.MinimumHeadroomPercent); e != nil {
		return OperatorSnapshotReceipt{}, e
	}
	if e = parent.Mkdir(filepath.Base(destination), 0700); e != nil {
		return OperatorSnapshotReceipt{}, failure("REGISTRY_NEW_SNAPSHOT_REQUIRED")
	}
	target, _, e := operatorRoot(destination)
	if e != nil {
		return OperatorSnapshotReceipt{}, e
	}
	defer target.Close()
	if e = copyOperatorPins(ctx, source, target, pins, source.files); e != nil {
		return OperatorSnapshotReceipt{}, e
	}
	if e = source.check(ctx); e != nil {
		return OperatorSnapshotReceipt{}, e
	}
	if e = writePrivate(target, operatorManifestName, raw); e != nil {
		return OperatorSnapshotReceipt{}, e
	}
	if e = syncDirectory(target, "."); e != nil {
		return OperatorSnapshotReceipt{}, e
	}
	if e = syncDirectory(parent, "."); e != nil {
		return OperatorSnapshotReceipt{}, e
	}
	receipt := m.receipt(raw)
	if _, e = VerifyOperatorSnapshot(ctx, o.config, destination, receipt); e != nil {
		return OperatorSnapshotReceipt{}, e
	}
	if e = source.check(ctx); e != nil {
		return OperatorSnapshotReceipt{}, e
	}
	after, e := o.metadata(ctx, q)
	if e != nil {
		return OperatorSnapshotReceipt{}, e
	}
	if !reflect.DeepEqual(after, metadata) {
		return OperatorSnapshotReceipt{}, failure("REGISTRY_SNAPSHOT_METADATA_MISMATCH")
	}
	if e = o.checkWithin(ctx, q); e != nil {
		return OperatorSnapshotReceipt{}, e
	}
	return receipt, nil
}

func copyOperatorPins(ctx context.Context, source *operatorTree, target *os.Root, pins []operatorPin, files []operatorFile) error {
	for _, p := range pins {
		if e := target.Mkdir(p.ID, 0700); e != nil {
			return failure("REGISTRY_RECOVERY_REQUIRED")
		}
		if e := target.Mkdir(p.ID+"/blobs", 0700); e != nil {
			return e
		}
	}
	for _, file := range files {
		if e := operatorCopy(ctx, source, target, file); e != nil {
			return e
		}
	}
	for _, p := range pins {
		if e := syncDirectory(target, p.ID+"/blobs"); e != nil {
			return e
		}
		if e := syncDirectory(target, p.ID); e != nil {
			return e
		}
	}
	return syncDirectory(target, ".")
}

func readOperatorSnapshot(ctx context.Context, c Config, path string, expected OperatorSnapshotReceipt, trustFile string) (operatorManifest, *operatorTree, error) {
	bad := failure("REGISTRY_SNAPSHOT_INVALID")
	var m operatorManifest
	if ctx == nil {
		return m, nil, bad
	}
	if e := ctx.Err(); e != nil {
		return m, nil, e
	}
	if c.Validate() != nil || !validOperatorReceipt(c, expected) {
		return m, nil, bad
	}
	trustSHA, e := operatorTrustSHA(ctx, c, trustFile)
	if e != nil || trustSHA != expected.TrustFileSHA256 {
		return m, nil, bad
	}
	t, e := scanOperatorTree(ctx, path, c)
	if e != nil {
		return m, nil, e
	}
	fail := func(e error) (operatorManifest, *operatorTree, error) { t.close(); return m, nil, e }
	var raw bytes.Buffer
	if _, e = operatorRead(ctx, t, operatorManifestName, c.Verification.MaximumManifestBytes, &raw); e != nil {
		return fail(e)
	}
	if release.StrictJSON(raw.Bytes(), &m) != nil || m.Format != operatorManifestFormat || m.receipt(raw.Bytes()) != expected || m.ReleaseManifestSHA256 != c.ReleaseManifestSHA256 || m.ReleaseArchiveSHA256 != c.ReleaseArchiveSHA256 || !release.ValidSHA256(m.ConfigurationSHA256) || len(m.Pins) > c.MaximumPins || len(m.Metadata) != 7 {
		return fail(bad)
	}
	for i, entry := range m.Metadata {
		if entry.Table != operatorMetadataTables[i] || entry.Rows < 0 || !release.ValidSHA256(entry.SHA256) {
			return fail(bad)
		}
	}
	if !filepath.IsAbs(m.SourceRoot.Path) || filepath.Clean(m.SourceRoot.Path) != m.SourceRoot.Path || m.SourceRoot.Device == 0 || m.SourceRoot.Inode == 0 {
		return fail(bad)
	}
	names := map[string]bool{}
	previous := ""
	pinSet := map[string]bool{}
	for _, p := range m.Pins {
		if !validID(p.ID) || !validID(p.WorkspaceID) || !release.ValidSHA256(p.InventorySHA256) || p.ID <= previous {
			return fail(bad)
		}
		previous = p.ID
		pinSet[p.ID] = true
	}
	previous = ""
	var total int64
	for _, f := range m.Files {
		parts := strings.Split(f.Path, "/")
		if f.Path <= previous || len(parts) < 2 || !pinSet[parts[0]] || !release.ValidSHA256(f.SHA256) || f.Bytes < 1 || f.Bytes > c.MaximumRetainedBytes-total {
			return fail(bad)
		}
		previous = f.Path
		total += f.Bytes
		names[f.Path] = true
	}
	if e = exactOperatorFiles(t, names, true); e != nil {
		return fail(e)
	}
	actual := []operatorFile{}
	for _, f := range t.files {
		if f.Path != operatorManifestName {
			actual = append(actual, f)
		}
	}
	if !reflect.DeepEqual(actual, m.Files) {
		return fail(bad)
	}
	copyConfig := c
	copyConfig.StoreRoot = path
	copyConfig.Verification.TrustFile = trustFile
	derivedNames := map[string]bool{}
	for _, p := range m.Pins {
		image, e := readCached(ctx, copyConfig, p.ID)
		if e != nil {
			return fail(e)
		}
		if e = verifyOperatorImageManifest(ctx, copyConfig, p.ID, image); e != nil {
			return fail(e)
		}
		raw, _ := json.Marshal(image)
		if release.Digest(raw) != p.InventorySHA256 {
			return fail(bad)
		}
		files := imageFiles(p.ID, image)
		for _, name := range files {
			if !names[name] {
				return fail(bad)
			}
			derivedNames[name] = true
		}
		if len(files) < 3 {
			return fail(bad)
		}
	}
	if e = exactOperatorFiles(t, derivedNames, true); e != nil {
		return fail(e)
	}
	if e = t.check(ctx); e != nil {
		return fail(e)
	}
	return m, t, nil
}

// VerifyOperatorSnapshot independently reopens all bytes without live SQL,
// credentials or network. Its result proves no live installation authority.
func VerifyOperatorSnapshot(ctx context.Context, c Config, path string, expected OperatorSnapshotReceipt) (OperatorSnapshotReceipt, error) {
	return VerifyOperatorSnapshotWithTrustFile(ctx, c, path, expected, c.Verification.TrustFile)
}

// VerifyOperatorSnapshotWithTrustFile permits only an explicit hash-matched
// projection of the original public trust file. Original configuration identity
// stays unchanged; no current credential or source-root existence is required.
func VerifyOperatorSnapshotWithTrustFile(ctx context.Context, c Config, path string, expected OperatorSnapshotReceipt, trustFile string) (OperatorSnapshotReceipt, error) {
	if operatorConfigurationSHA(c) != expected.ConfigurationSHA256 || expected.SourceRoot.Path != c.StoreRoot {
		return OperatorSnapshotReceipt{}, failure("REGISTRY_SNAPSHOT_CONFIGURATION_MISMATCH")
	}
	_, t, e := readOperatorSnapshot(ctx, c, path, expected, trustFile)
	if e != nil {
		return OperatorSnapshotReceipt{}, e
	}
	defer t.close()
	return expected, t.check(ctx)
}

func (o *OperatorSnapshot) ImportFilesWithin(ctx context.Context, q transaction.Executor, snapshot string, expected OperatorSnapshotReceipt) (OperatorSnapshotImportReceipt, error) {
	var out OperatorSnapshotImportReceipt
	if e := o.checkWithin(ctx, q); e != nil {
		return out, e
	}
	if expected.Source.TenantID != o.binding.TenantID || expected.Source.ApplicationProfile != o.binding.ApplicationProfile || withinPath(o.config.StoreRoot, snapshot) || withinPath(snapshot, o.config.StoreRoot) {
		return out, failure("REGISTRY_SNAPSHOT_INVALID")
	}
	m, source, e := readOperatorSnapshot(ctx, o.config, snapshot, expected, o.config.Verification.TrustFile)
	if e != nil {
		return out, e
	}
	defer source.close()
	pins, _, e := o.readPins(ctx, q, false)
	if e != nil {
		return out, e
	}
	metadata, e := o.metadata(ctx, q)
	if e != nil {
		return out, e
	}
	if !reflect.DeepEqual(pins, m.Pins) || !reflect.DeepEqual(metadata, m.Metadata) {
		return out, failure("REGISTRY_SNAPSHOT_METADATA_MISMATCH")
	}
	target, e := scanOperatorTree(ctx, o.config.StoreRoot, o.config)
	if e != nil {
		return out, e
	}
	defer target.close()
	if len(target.files) != 0 || len(target.stamps) != 1 {
		return out, failure("REGISTRY_NEW_STORE_REQUIRED")
	}
	if e = admitBytes(o.config.StoreRoot, expected.Bytes, o.config.MinimumHeadroomPercent); e != nil {
		return out, e
	}
	if e = copyOperatorPins(ctx, source, target.root, m.Pins, m.Files); e != nil {
		return out, e
	}
	if e = source.check(ctx); e != nil {
		return out, e
	}
	if e = o.VerifyStoredWithin(ctx, q); e != nil {
		return out, e
	}
	out = OperatorSnapshotImportReceipt{"projectone.imageregistry.installation-import.v1", expected, o.binding, o.root, operatorConfigurationSHA(o.config), operatorFilesSHA(m.Files), true}
	if e = ReadbackOperatorImport(ctx, o.config, out); e != nil {
		return OperatorSnapshotImportReceipt{}, e
	}
	if e = o.checkWithin(ctx, q); e != nil {
		return OperatorSnapshotImportReceipt{}, e
	}
	return out, nil
}

func operatorFilesSHA(files []operatorFile) string {
	raw, _ := json.Marshal(files)
	return release.Digest(raw)
}

// ReadbackOperatorImport reopens the completed destination independently. The
// native parent must persist this receipt in its original completion journal.
func ReadbackOperatorImport(ctx context.Context, c Config, expected OperatorSnapshotImportReceipt) error {
	if c.Validate() != nil || !validOperatorReceipt(c, expected.Snapshot) || expected.Format != "projectone.imageregistry.installation-import.v1" || !expected.Complete || !validOperatorBinding(expected.Target) || expected.Target.TenantID != expected.Snapshot.Source.TenantID || expected.Target.ApplicationProfile != expected.Snapshot.Source.ApplicationProfile || expected.TargetConfigurationSHA256 != operatorConfigurationSHA(c) || !release.ValidSHA256(expected.ReadbackSHA256) {
		return failure("REGISTRY_SNAPSHOT_INVALID")
	}
	trustSHA, e := operatorTrustSHA(ctx, c, c.Verification.TrustFile)
	if e != nil || trustSHA != expected.Snapshot.TrustFileSHA256 {
		return failure("REGISTRY_SNAPSHOT_TRUST_MISMATCH")
	}
	t, e := scanOperatorTree(ctx, c.StoreRoot, c)
	if e != nil {
		return e
	}
	defer t.close()
	if t.identity != expected.TargetRoot || len(t.files) != expected.Snapshot.Files || t.bytes != expected.Snapshot.Bytes || operatorFilesSHA(t.files) != expected.ReadbackSHA256 {
		return failure("REGISTRY_SNAPSHOT_CHANGED")
	}
	return t.check(ctx)
}

func operatorTrustSHA(ctx context.Context, c Config, path string) (string, error) {
	if ctx == nil || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return "", failure("REGISTRY_SNAPSHOT_INVALID")
	}
	root, id, e := operatorRoot(filepath.Dir(path))
	if e != nil {
		return "", e
	}
	defer root.Close()
	t := &operatorTree{root: root, identity: id, stamps: map[string]operatorStamp{}}
	file, e := operatorRead(ctx, t, filepath.Base(path), c.Verification.MaximumMetadataBytes, nil)
	if e != nil {
		return "", e
	}
	if e = t.check(ctx); e != nil {
		return "", e
	}
	return file.SHA256, nil
}

func verifyOperatorImageManifest(ctx context.Context, c Config, pin string, image cachedImage) error {
	if len(image.Descriptors) < 2 || image.Descriptors[0].Kind != "manifest" {
		return failure("REGISTRY_CONTENT_INVALID")
	}
	root, e := privateRoot(c.StoreRoot)
	if e != nil {
		return e
	}
	defer root.Close()
	sha, size, e := hashFile(ctx, root, pin+"/manifest.json", c.Verification.MaximumMetadataBytes)
	if e != nil || sha != image.Descriptors[0].Digest || size != image.Descriptors[0].Size {
		return failure("REGISTRY_DIGEST_MISMATCH")
	}
	return nil
}
