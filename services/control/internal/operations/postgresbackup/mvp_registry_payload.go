package postgresbackup

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"reflect"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.yaml.in/yaml/v3"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/imageregistry"
	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/postgresmaintenance"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	release "github.com/Passion-Flow/ownsolid/api/release/v1"
)

const mvpArtifactsDirectory = "artifacts"
const mvpRegistryDirectory = "imageregistry"

type MVPRegistryPayload struct {
	Receipt                 imageregistry.OperatorSnapshotReceipt `json:"receipt"`
	Tree                    TreeReadback                          `json:"tree"`
	ConfigurationFileSHA256 string                                `json:"configuration_file_sha256"`
}

func registryBinding(b *MVPSourceBinding, r release.PostgreSQLBackupRequest) imageregistry.OperatorSnapshotBinding {
	return imageregistry.OperatorSnapshotBinding{ApplicationProfile: r.EffectiveApplicationProfile(), TenantID: r.TenantID, Database: r.Database, Operator: r.Operator, ScopeID: r.SourceScopeID, SystemIdentifier: b.SystemIdentifier, DatabaseOID: b.DatabaseOID, BackendPID: b.BackendPID}
}

// Nested registry inputs keep their own typed configuration authority. Their
// trust/credential/CA files must belong to the complete private projection;
// prepared signed releases and the existing registry CAS remain artifact owners.
func registryConfigurationClosure(ctx context.Context, c config.Config, r release.PostgreSQLBackupRequest) error {
	if c.ImageRegistry == nil {
		if c.ImageRegistryConfigFile != "" {
			return errors.New("POSTGRES_BACKUP_MVP_REGISTRY_PAYLOAD_OWNER_REQUIRED")
		}
		return nil
	}
	if !contained(r.ConfigurationRoot, c.ImageRegistryConfigFile) {
		return errors.New("POSTGRES_BACKUP_MVP_REGISTRY_CONFIGURATION_CLOSURE_REQUIRED")
	}
	for _, path := range []string{c.ImageRegistry.CredentialFile, c.ImageRegistry.CAFile, c.ImageRegistry.Verification.TrustFile} {
		if !contained(r.ConfigurationRoot, path) {
			return errors.New("POSTGRES_BACKUP_MVP_REGISTRY_CONFIGURATION_CLOSURE_REQUIRED")
		}
		if _, e := fileDigest(ctx, path, r.Policy.MaximumConfigurationBytes); e != nil {
			return e
		}
	}
	for _, path := range append(storeRoots(c), r.ConfigurationRoot, r.BackupRoot, r.Policy.ScratchRoot) {
		if relatedPaths(c.ImageRegistry.StoreRoot, path) {
			return errors.New("POSTGRES_BACKUP_RELATED_PATHS_REFUSED")
		}
	}
	return nil
}

func mvpRegistrySourceTree(ctx context.Context, c config.Config, r release.PostgreSQLBackupRequest, usedBytes int64, usedFiles int) (*TreeReadback, error) {
	if r.EffectiveApplicationProfile() != postgresmaintenance.MVPApplicationProfile || c.ImageRegistry == nil {
		return nil, nil
	}
	if e := registryConfigurationClosure(ctx, c, r); e != nil {
		return nil, e
	}
	tree, e := TreeDigest(ctx, c.ImageRegistry.StoreRoot, r.Policy.MaximumStoreBytes-usedBytes, r.Policy.MaximumStoreFiles-usedFiles, r.Policy.MaximumManifestBytes)
	if e != nil {
		return nil, e
	}
	return &tree, nil
}

// The native caller lends its actual exported-snapshot transaction. This owner
// creates no SQL authority and does not start a browser session or transport.
func exportMVPRegistryWithin(ctx context.Context, tx pgx.Tx, c config.Config, r release.PostgreSQLBackupRequest, b *MVPSourceBinding) (*MVPRegistryPayload, error) {
	if r.EffectiveApplicationProfile() != postgresmaintenance.MVPApplicationProfile || c.ImageRegistry == nil {
		return nil, nil
	}
	if b == nil || !b.RegistryConfigured {
		return nil, errors.New("POSTGRES_BACKUP_MVP_REGISTRY_SOURCE_REQUIRED")
	}
	q := transaction.PostgreSQLTransaction(tx)
	if _, e := postgresmaintenance.CheckWithin(ctx, q, r.EffectiveApplicationProfile()); e != nil {
		return nil, e
	}
	owner, e := imageregistry.NewOperatorSnapshot(*c.ImageRegistry, registryBinding(b, r))
	if e != nil {
		return nil, e
	}
	destination := filepath.Join(r.BackupRoot, mvpArtifactsDirectory, mvpRegistryDirectory)
	receipt, e := owner.ExportFilesWithin(ctx, q, destination)
	if e != nil {
		return nil, e
	}
	if !reflect.DeepEqual(receipt.Source, registryBinding(b, r)) {
		return nil, errors.New("POSTGRES_BACKUP_MVP_REGISTRY_SOURCE_CHANGED")
	}
	tree, e := TreeDigest(ctx, destination, r.Policy.MaximumStoreBytes, r.Policy.MaximumStoreFiles, r.Policy.MaximumManifestBytes)
	if e != nil {
		return nil, e
	}
	configuration, e := fileDigest(ctx, c.ImageRegistryConfigFile, r.Policy.MaximumConfigurationBytes)
	if e != nil {
		return nil, e
	}
	if _, e = postgresmaintenance.CheckWithin(ctx, q, r.EffectiveApplicationProfile()); e != nil {
		return nil, e
	}
	return &MVPRegistryPayload{Receipt: receipt, Tree: tree, ConfigurationFileSHA256: configuration.SHA256}, nil
}

func verifyMVPRegistrySource(ctx context.Context, lease *pgxpool.Conn, c config.Config, r release.PostgreSQLBackupRequest, b *MVPSourceBinding, expected *TreeReadback) (result error) {
	if r.EffectiveApplicationProfile() != postgresmaintenance.MVPApplicationProfile || c.ImageRegistry == nil {
		return nil
	}
	current, e := mvpRegistrySourceTree(ctx, c, r, 0, 0)
	if e != nil || expected == nil || !reflect.DeepEqual(current, expected) {
		return errors.New("POSTGRES_BACKUP_MVP_REGISTRY_SOURCE_CHANGED")
	}
	tx, e := lease.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly, IsoLevel: pgx.RepeatableRead})
	if e != nil {
		return errors.New("POSTGRES_BACKUP_READ_ONLY_TRANSACTION_UNAVAILABLE")
	}
	defer func() {
		result = errors.Join(result, closeRestoreTransaction(ctx, tx, r.Policy.TerminationGraceSeconds))
	}()
	q := transaction.PostgreSQLTransaction(tx)
	if _, e = postgresmaintenance.CheckWithin(ctx, q, r.EffectiveApplicationProfile()); e != nil {
		return e
	}
	owner, e := imageregistry.NewOperatorSnapshot(*c.ImageRegistry, registryBinding(b, r))
	if e != nil {
		return e
	}
	if e = owner.VerifyStoredWithin(ctx, q); e != nil {
		return e
	}
	_, e = postgresmaintenance.CheckWithin(ctx, q, r.EffectiveApplicationProfile())
	return e
}

func sourceRegistryConfiguration(ctx context.Context, m Manifest) (imageregistry.Config, string, error) {
	var empty imageregistry.Config
	raw, e := readPrivateBytes(filepath.Join(m.Request.BackupRoot, effectiveName), m.Request.Policy.MaximumConfigurationBytes)
	if e != nil {
		return empty, "", e
	}
	var refs struct {
		ImageRegistryConfigFile string `yaml:"image_registry_config_file"`
	}
	if e = yaml.Unmarshal(raw, &refs); e != nil {
		return empty, "", e
	}
	path, e := sourceReferencePath(m, refs.ImageRegistryConfigFile)
	if e != nil {
		return empty, "", e
	}
	original, e := fileDigest(ctx, path, m.Request.Policy.MaximumConfigurationBytes)
	if e != nil || m.MVPSource == nil || m.MVPSource.Registry == nil || original.SHA256 != m.MVPSource.Registry.ConfigurationFileSHA256 {
		return empty, "", errors.New("POSTGRES_BACKUP_MVP_REGISTRY_CONFIGURATION_CHANGED")
	}
	c, e := imageregistry.Load(path)
	if e != nil {
		return empty, "", e
	}
	trust, e := sourceReferencePath(m, c.Verification.TrustFile)
	if e != nil {
		return empty, "", e
	}
	return c, trust, nil
}

func verifyMVPRegistryPayload(ctx context.Context, m Manifest) error {
	if m.Request.EffectiveApplicationProfile() != postgresmaintenance.MVPApplicationProfile {
		return nil
	}
	root, e := openPrivateRoot(filepath.Join(m.Request.BackupRoot, mvpArtifactsDirectory))
	if e != nil {
		return e
	}
	defer root.Close()
	f, e := root.Open(".")
	if e != nil {
		return e
	}
	entries, readError := f.ReadDir(2)
	closeError := f.Close()
	if readError != nil && readError != io.EOF || closeError != nil {
		return errors.New("POSTGRES_BACKUP_MVP_ARTIFACT_INVENTORY_INVALID")
	}
	payload := m.MVPSource.Registry
	if payload == nil {
		if m.MVPSource.RegistryConfigured || m.MVPSource.ExternalArtifacts[1].Records != 0 || len(entries) != 0 {
			return errors.New("POSTGRES_BACKUP_MVP_REGISTRY_PAYLOAD_OWNER_REQUIRED")
		}
		return nil
	}
	if len(entries) != 1 || entries[0].Name() != mvpRegistryDirectory || !entries[0].IsDir() {
		return errors.New("POSTGRES_BACKUP_MVP_ARTIFACT_INVENTORY_INVALID")
	}
	if !reflect.DeepEqual(payload.Receipt.Source, registryBinding(m.MVPSource, m.Request)) || !release.ValidSHA256(payload.Receipt.ManifestSHA256) || !release.ValidSHA256(payload.ConfigurationFileSHA256) {
		return errors.New("POSTGRES_BACKUP_MVP_REGISTRY_SOURCE_CHANGED")
	}
	path := filepath.Join(m.Request.BackupRoot, mvpArtifactsDirectory, mvpRegistryDirectory)
	tree, e := TreeDigest(ctx, path, m.Request.Policy.MaximumStoreBytes, m.Request.Policy.MaximumStoreFiles, m.Request.Policy.MaximumManifestBytes)
	if e != nil || tree != payload.Tree {
		return errors.New("POSTGRES_BACKUP_MVP_REGISTRY_PAYLOAD_CHANGED")
	}
	if e = mvpRegistryPayloadLimits(m); e != nil {
		return e
	}
	c, trust, e := sourceRegistryConfiguration(ctx, m)
	if e != nil {
		return e
	}
	actual, e := imageregistry.VerifyOperatorSnapshotWithTrustFile(ctx, c, path, payload.Receipt, trust)
	if e != nil || !reflect.DeepEqual(actual, payload.Receipt) {
		return errors.New("POSTGRES_BACKUP_MVP_REGISTRY_READBACK_FAILED")
	}
	return nil
}

func mvpRegistryPayloadLimits(m Manifest) error {
	if m.MVPSource == nil || m.MVPSource.Registry == nil {
		return nil
	}
	bytes, files := m.Request.Policy.MaximumStoreBytes, m.Request.Policy.MaximumStoreFiles
	for _, store := range m.Stores {
		if store.Tree.Bytes < 0 || store.Tree.Files < 0 || store.Tree.Bytes > bytes || store.Tree.Files > files {
			return errors.New("POSTGRES_BACKUP_STORE_LIMIT_REACHED")
		}
		bytes -= store.Tree.Bytes
		files -= store.Tree.Files
	}
	tree := m.MVPSource.Registry.Tree
	if tree.Bytes < 0 || tree.Files < 0 || tree.Bytes > bytes || tree.Files > files {
		return errors.New("POSTGRES_BACKUP_STORE_LIMIT_REACHED")
	}
	return nil
}

func mvpRegistryTargetRoot(c config.Config, m Manifest, r release.PostgreSQLRestoreRequest, empty bool) (*RootIdentity, error) {
	if r.EffectiveApplicationProfile() != postgresmaintenance.MVPApplicationProfile {
		return nil, nil
	}
	payload := m.MVPSource.Registry
	if payload == nil {
		if c.ImageRegistry != nil || c.ImageRegistryConfigFile != "" {
			return nil, errors.New("POSTGRES_RESTORE_MVP_REGISTRY_CONFIGURATION_MISMATCH")
		}
		return nil, nil
	}
	if c.ImageRegistry == nil {
		return nil, errors.New("POSTGRES_RESTORE_MVP_REGISTRY_CONFIGURATION_REQUIRED")
	}
	for _, path := range append(storeRoots(c), r.BackupRoot, r.RestoreRoot, r.ConfigurationRoot, r.Policy.ScratchRoot) {
		if relatedPaths(c.ImageRegistry.StoreRoot, path) {
			return nil, errors.New("POSTGRES_RESTORE_RELATED_PATHS_REFUSED")
		}
	}
	root, e := openPrivateRoot(c.ImageRegistry.StoreRoot)
	if e != nil {
		return nil, e
	}
	defer root.Close()
	info, e := root.Stat(".")
	if e != nil {
		return nil, e
	}
	identity, e := rootIdentity(c.ImageRegistry.StoreRoot, info)
	if e != nil {
		return nil, e
	}
	if identity.Device == payload.Receipt.SourceRoot.Device && identity.Inode == payload.Receipt.SourceRoot.Inode {
		return nil, errors.New("POSTGRES_RESTORE_MVP_SOURCE_REGISTRY_ALIAS")
	}
	if empty {
		f, e := root.Open(".")
		if e != nil {
			return nil, e
		}
		entries, re := f.ReadDir(1)
		ce := f.Close()
		if len(entries) != 0 || re != nil && re != io.EOF || ce != nil {
			return nil, errors.New("POSTGRES_RESTORE_MVP_FRESH_REGISTRY_ROOT_REQUIRED")
		}
	}
	return &identity, nil
}

func registryTargetBinding(i RestoreIntent) imageregistry.OperatorSnapshotBinding {
	return imageregistry.OperatorSnapshotBinding{ApplicationProfile: i.Request.EffectiveApplicationProfile(), TenantID: i.TenantID, Database: i.Target.Database, Operator: i.Target.Operator, ScopeID: i.Request.TargetScopeID, SystemIdentifier: i.Target.SystemIdentifier, DatabaseOID: i.Target.DatabaseOID, BackendPID: i.Target.BackendPID}
}

func importMVPRegistry(ctx context.Context, lease *pgxpool.Conn, c config.Config, m Manifest, i RestoreIntent) (out *imageregistry.OperatorSnapshotImportReceipt, result error) {
	if i.Request.EffectiveApplicationProfile() != postgresmaintenance.MVPApplicationProfile || m.MVPSource.Registry == nil {
		return nil, nil
	}
	identity, e := mvpRegistryTargetRoot(c, m, i.Request, true)
	if e != nil || i.RegistryRoot == nil || !reflect.DeepEqual(identity, i.RegistryRoot) {
		return nil, errors.New("POSTGRES_RESTORE_MVP_REGISTRY_ROOT_CHANGED")
	}
	tx, e := lease.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly, IsoLevel: pgx.RepeatableRead})
	if e != nil {
		return nil, errors.New("POSTGRES_RESTORE_TRANSACTION_UNAVAILABLE")
	}
	defer func() {
		if e := closeRestoreTransaction(ctx, tx, i.Request.Policy.TerminationGraceSeconds); e != nil {
			out = nil
			result = errors.Join(result, e)
		}
	}()
	q := transaction.PostgreSQLTransaction(tx)
	if _, e = postgresmaintenance.CheckWithin(ctx, q, i.Request.EffectiveApplicationProfile()); e != nil {
		return nil, e
	}
	owner, e := imageregistry.NewOperatorSnapshot(*c.ImageRegistry, registryTargetBinding(i))
	if e != nil {
		return nil, e
	}
	receipt, e := owner.ImportFilesWithin(ctx, q, filepath.Join(i.Request.BackupRoot, mvpArtifactsDirectory, mvpRegistryDirectory), m.MVPSource.Registry.Receipt)
	if e != nil {
		return nil, e
	}
	if !receipt.Complete || !reflect.DeepEqual(receipt.Target, registryTargetBinding(i)) || receipt.TargetRoot.Path != i.RegistryRoot.Path || receipt.TargetRoot.Device != i.RegistryRoot.Device || receipt.TargetRoot.Inode != i.RegistryRoot.Inode || receipt.TargetRoot.UID != i.RegistryRoot.UID || receipt.TargetRoot.GID != i.RegistryRoot.GID {
		return nil, errors.New("POSTGRES_RESTORE_MVP_REGISTRY_IMPORT_UNCONFIRMED")
	}
	if e = owner.VerifyStoredWithin(ctx, q); e != nil {
		return nil, e
	}
	if _, e = postgresmaintenance.CheckWithin(ctx, q, i.Request.EffectiveApplicationProfile()); e != nil {
		return nil, e
	}
	return &receipt, nil
}

func readbackMVPRegistryImport(ctx context.Context, c config.Config, m Manifest, i RestoreIntent, f RestoreFiles) error {
	if i.Request.EffectiveApplicationProfile() != postgresmaintenance.MVPApplicationProfile {
		if f.Registry != nil || i.RegistryRoot != nil {
			return errors.New("POSTGRES_RESTORE_MVP_REGISTRY_RECORD_MISMATCH")
		}
		return nil
	}
	if m.MVPSource.Registry == nil {
		if f.Registry != nil || i.RegistryRoot != nil {
			return errors.New("POSTGRES_RESTORE_MVP_REGISTRY_RECORD_MISMATCH")
		}
		return nil
	}
	if c.ImageRegistry == nil || f.Registry == nil || i.RegistryRoot == nil || !f.Registry.Complete || !reflect.DeepEqual(f.Registry.Snapshot, m.MVPSource.Registry.Receipt) || !reflect.DeepEqual(f.Registry.Target, registryTargetBinding(i)) {
		return errors.New("POSTGRES_RESTORE_MVP_REGISTRY_RECORD_MISMATCH")
	}
	actual, e := mvpRegistryTargetRoot(c, m, i.Request, false)
	if e != nil || !reflect.DeepEqual(actual, i.RegistryRoot) {
		return errors.New("POSTGRES_RESTORE_MVP_REGISTRY_ROOT_CHANGED")
	}
	return imageregistry.ReadbackOperatorImport(ctx, *c.ImageRegistry, *f.Registry)
}

// Source principal admission belongs only to backup. A genuinely fresh restore
// imports SQL before its original separate runtime-role preparation stage.
func readMVPBackupDatabase(ctx context.Context, lease *pgxpool.Conn, r release.PostgreSQLBackupRequest) (DatabaseReadback, error) {
	if r.EffectiveApplicationProfile() != postgresmaintenance.MVPApplicationProfile {
		return readDatabase(ctx, lease, r)
	}
	return withDatabaseSnapshot(ctx, lease, r, func(tx pgx.Tx, data DatabaseReadback) error {
		b, e := readMVPSourceBinding(ctx, tx, r)
		if e != nil {
			return e
		}
		return validMVPSourceBinding(Manifest{Request: r, Database: data, MVPSource: b})
	})
}

func admitMVPRegistryTargetConfiguration(ctx context.Context, c config.Config, m Manifest, r release.PostgreSQLRestoreRequest) error {
	if r.EffectiveApplicationProfile() != postgresmaintenance.MVPApplicationProfile {
		return nil
	}
	if m.MVPSource.Registry == nil {
		return nil
	}
	if c.ImageRegistry == nil {
		return errors.New("POSTGRES_RESTORE_MVP_REGISTRY_CONFIGURATION_REQUIRED")
	}
	original, _, e := sourceRegistryConfiguration(ctx, m)
	if e != nil {
		return e
	}
	expected := m.MVPSource.Registry.Receipt
	if c.ImageRegistry.Endpoint != original.Endpoint || c.ImageRegistry.ReleaseArchiveSHA256 != expected.ReleaseArchiveSHA256 || c.ImageRegistry.ReleaseManifestSHA256 != expected.ReleaseManifestSHA256 {
		return errors.New("POSTGRES_RESTORE_MVP_REGISTRY_ORIGIN_MISMATCH")
	}
	originalCA, e := sourceReferencePath(m, original.CAFile)
	if e != nil {
		return e
	}
	from, e := fileDigest(ctx, originalCA, r.Policy.MaximumConfigurationBytes)
	if e != nil {
		return e
	}
	to, e := fileDigest(ctx, c.ImageRegistry.CAFile, r.Policy.MaximumConfigurationBytes)
	if e != nil || from != to {
		return errors.New("POSTGRES_RESTORE_MVP_REGISTRY_ORIGIN_MISMATCH")
	}
	trust, e := fileDigest(ctx, c.ImageRegistry.Verification.TrustFile, r.Policy.MaximumConfigurationBytes)
	if e != nil || trust.SHA256 != expected.TrustFileSHA256 {
		return errors.New("POSTGRES_RESTORE_MVP_REGISTRY_TRUST_MISMATCH")
	}
	return nil
}
func mvpRegistryRestoreBytes(m Manifest) int64 {
	if m.Request.EffectiveApplicationProfile() == postgresmaintenance.MVPApplicationProfile && m.MVPSource != nil && m.MVPSource.Registry != nil {
		return m.MVPSource.Registry.Receipt.Bytes
	}
	return 0
}
func mvpRestoreAllocationRoots(c config.Config, m Manifest) []string {
	roots := storeRoots(c)
	if m.Request.EffectiveApplicationProfile() == postgresmaintenance.MVPApplicationProfile && m.MVPSource != nil && m.MVPSource.Registry != nil && c.ImageRegistry != nil {
		roots = append(roots, c.ImageRegistry.StoreRoot)
	}
	return roots
}
