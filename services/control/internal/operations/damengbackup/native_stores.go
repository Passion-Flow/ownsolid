//go:build cgo && odbc_manager

package damengbackup

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"syscall"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/datasets"
	accountidentity "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/modelpreparations"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/models"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/training"
	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/postgresbackup"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	lifecyclewire "github.com/Passion-Flow/ownsolid/api/model-lifecycle/v1"
	trainingwire "github.com/Passion-Flow/ownsolid/api/training/v1"
)

type StoreBinding struct {
	Kind     string       `json:"kind"`
	Path     string       `json:"path"`
	Identity FileIdentity `json:"identity"`
	SHA256   string       `json:"sha256"`
	Bytes    int64        `json:"bytes"`
	Files    int          `json:"files"`
}
type MatchedStores struct {
	FormatVersion          int            `json:"format_version"`
	DatabaseRoot           string         `json:"database_root"`
	DatabaseCompleteSHA256 string         `json:"database_complete_sha256"`
	Database               State          `json:"database"`
	ConfigurationSHA256    string         `json:"configuration_sha256"`
	Original               []StoreBinding `json:"original"`
	Snapshots              []StoreBinding `json:"snapshots"`
}
type RestoredStores struct {
	FormatVersion          int            `json:"format_version"`
	MatchedSHA256          string         `json:"matched_sha256"`
	DatabaseCompleteSHA256 string         `json:"database_complete_sha256"`
	ConfigurationSHA256    string         `json:"configuration_sha256"`
	Targets                []StoreBinding `json:"targets"`
}

var storeKinds = []string{"models", "datasets", "training", "preparations"}

func platformRoots(c config.Config) []string {
	return []string{c.Models.ArtifactRoot, c.Datasets.ArtifactRoot, c.Training.ArtifactRoot, c.ModelPreparation.ArtifactRoot}
}
func validatePlatformRoots(c config.Config, extra ...string) error {
	roots := append(platformRoots(c), extra...)
	for i, p := range roots {
		if !canonical(p) {
			return fail("FOUR_STORES_REQUIRED")
		}
		for _, q := range roots[:i] {
			if related(p, q) {
				return fail("STORE_ROOT_OVERLAP")
			}
		}
	}
	return nil
}
func (c *NativeCustody) platformIdentity(ctx context.Context, accounts *accountidentity.Service, configuration config.Config) error {
	if accounts == nil || c == nil || configuration.Validate() != nil || !digest.MatchString(nativePlatformConfigurationSHA256(configuration)) || configuration.Server.Profile() != "platform" || configuration.DatabaseEngine() != config.DamengDatabaseEngine || !reflect.DeepEqual(accounts.Config, configuration) {
		return fail("PLATFORM_IDENTITY_REQUIRED")
	}
	store, e := accounts.Repository()
	if e != nil || !transaction.BoundToDameng(store, c.kernel) {
		return fail("PLATFORM_KERNEL_MISMATCH")
	}
	if e = c.CheckFence(ctx); e != nil {
		return e
	}
	return c.lockInstallation(ctx)
}

type nativeStoreOwners struct {
	model       *models.Service
	data        *datasets.Service
	train       *training.Service
	preparation *modelpreparations.Service
}

func (s *nativeStoreOwners) Close() (result error) {
	if s == nil {
		return nil
	}
	if s.preparation != nil && s.preparation.Store != nil {
		result = errors.Join(result, s.preparation.Store.Close())
	}
	if s.train != nil && s.train.Store != nil {
		result = errors.Join(result, s.train.Store.Close())
	}
	if s.data != nil && s.data.Store != nil {
		result = errors.Join(result, s.data.Store.Close())
	}
	return result
}
func (s *nativeStoreOwners) Verify(ctx context.Context) error {
	for _, f := range []func(context.Context) error{s.model.VerifyStoredVersions, s.data.VerifyStored, s.train.VerifyStored, s.preparation.VerifyStored} {
		if e := f(ctx); e != nil {
			return fail("DOMAIN_STORE_INTEGRITY_FAILED")
		}
	}
	return nil
}
func openNativeOwners(ctx context.Context, c *NativeCustody, accounts *accountidentity.Service, configuration config.Config) (*nativeStoreOwners, error) {
	if e := c.platformIdentity(ctx, accounts, configuration); e != nil {
		return nil, e
	}
	if validatePlatformRoots(configuration) != nil {
		return nil, fail("FOUR_STORES_REQUIRED")
	}
	s := &nativeStoreOwners{model: &models.Service{Identity: accounts, RootDirectory: configuration.Models.ArtifactRoot}, data: &datasets.Service{Identity: accounts}}
	s.train = &training.Service{Identity: accounts, Datasets: s.data}
	s.preparation = &modelpreparations.Service{Identity: accounts, Datasets: s.data, Training: s.train}
	var e error
	s.data.Store, e = datasets.OpenStoreForVerification(configuration.Datasets.ArtifactRoot, c.installation, configuration.Policy().Datasets.MinimumFreeBytes)
	if e == nil {
		s.train.Store, e = training.OpenStoreForVerification(configuration.Training.ArtifactRoot, c.installation, configuration.Policy().Training.MinimumFreeBytes, trainingwire.MaximumCheckpointBytes, trainingwire.MaximumCheckpointFiles)
	}
	if e == nil {
		s.preparation.Store, e = modelpreparations.OpenStoreForVerification(configuration.ModelPreparation.ArtifactRoot, c.installation, configuration.Policy().ModelLifecycle.MinimumFreeBytes, lifecyclewire.MaximumModelBytes, lifecyclewire.MaximumModelFiles)
	}
	if e != nil {
		return nil, errors.Join(fail("DOMAIN_STORES_UNAVAILABLE"), s.Close())
	}
	if e = s.Verify(ctx); e != nil {
		return nil, errors.Join(e, s.Close())
	}
	return s, nil
}
func (c *NativeCustody) storeReadback(ctx context.Context, paths []string) ([]StoreBinding, error) {
	if len(paths) != 4 {
		return nil, fail("FOUR_STORES_REQUIRED")
	}
	out := []StoreBinding{}
	var total int64
	var files int
	for i, path := range paths {
		r, e := openPrivateRoot(path)
		if e != nil {
			return nil, e
		}
		info, e := r.Stat(".")
		r.Close()
		if e != nil {
			return nil, fail("STORE_IDENTITY_INVALID")
		}
		// Reuse the established read-only private tree protocol. This helper neither
		// selects PostgreSQL nor executes SQL/opens a pool or changes a domain owner.
		tree, e := postgresbackup.TreeDigest(ctx, path, c.policy.MaximumTreeBytes, c.policy.MaximumTreeFiles, c.policy.MaximumTreeMetadataBytes)
		if e != nil {
			return nil, fail("STORE_TREE_INVALID")
		}
		if tree.Bytes > c.policy.MaximumTreeBytes-total || tree.Files > c.policy.MaximumTreeFiles-files {
			return nil, fail("STORE_TREE_LIMIT")
		}
		total += tree.Bytes
		files += tree.Files
		out = append(out, StoreBinding{storeKinds[i], path, identity(info), tree.SHA256, tree.Bytes, tree.Files})
	}
	return out, c.CheckFence(ctx)
}
func completeAt(ctx context.Context, n *Native, path string) (Complete, string, error) {
	r, e := openPrivateRoot(path)
	if e != nil {
		return Complete{}, "", e
	}
	defer r.Close()
	var i Intent
	if _, e = readDocument(r, "intent.json", &i); e != nil || i.Root != path {
		return Complete{}, "", fail("DATABASE_PROOF_REQUIRED")
	}
	if e = n.validateIntent(r, i); e != nil {
		return Complete{}, "", e
	}
	complete, raw, e := n.readComplete(r, i)
	if e != nil {
		return complete, "", e
	}
	if _, _, e = readArchive(ctx, r, n.configuration.Policy.MaximumArchiveBytes, complete.ArchiveSHA256); e != nil {
		return Complete{}, "", e
	}
	return complete, sha(raw), nil
}

// CaptureMatchedStores calls all four existing integrity/snapshot owners while
// the original native custody remains live, and proves the database and every
// source tree are unchanged across capture. It grants no runtime access.
func CaptureMatchedStores(ctx context.Context, c *NativeCustody, accounts *accountidentity.Service, configuration config.Config, databaseRoot, output string) (matched MatchedStores, result error) {
	if c == nil || c.mode != SourceMaintenance || validatePlatformRoots(configuration, databaseRoot, output) != nil {
		return matched, fail("STORE_ROOT_OVERLAP")
	}
	database, databaseSHA, e := completeAt(ctx, c.native, databaseRoot)
	if e != nil {
		return matched, e
	}
	state, e := c.Inspect(ctx)
	if e != nil || state != database.Database {
		return matched, fail("DATABASE_CHANGED_BEFORE_STORES")
	}
	owners, e := openNativeOwners(ctx, c, accounts, configuration)
	if e != nil {
		return matched, e
	}
	defer func() {
		if e := owners.Close(); e != nil {
			result = errors.Join(result, fail("DOMAIN_STORE_CLOSE_UNCERTAIN"))
		}
	}()
	before, e := c.storeReadback(ctx, platformRoots(configuration))
	if e != nil {
		return matched, e
	}
	var peak int64 = c.native.configuration.Policy.MaximumArchiveBytes + 4*c.policy.MaximumTreeMetadataBytes
	for _, b := range before {
		peak += b.Bytes
	}
	if e = requireSpace(filepath.Dir(output), peak, c.native.configuration.Policy.MinimumFreeBytes); e != nil {
		return matched, e
	}
	root, e := newRoot(output)
	if e != nil {
		return matched, e
	}
	defer root.Close()
	matched = MatchedStores{FormatVersion: 1, DatabaseRoot: databaseRoot, DatabaseCompleteSHA256: databaseSHA, Database: state, ConfigurationSHA256: nativePlatformConfigurationSHA256(configuration), Original: before, Snapshots: []StoreBinding{}}
	if e = writeNew(root, "matched.intent.json", jsonBytes(matched)); e != nil {
		return matched, e
	}
	exports := []func(context.Context, string) error{owners.model.ExportFiles, owners.data.ExportFiles, owners.train.ExportFiles, owners.preparation.ExportFiles}
	paths := make([]string, 4)
	for i, run := range exports {
		if e = c.CheckFence(ctx); e != nil {
			return matched, e
		}
		paths[i] = filepath.Join(output, storeKinds[i])
		if e = run(ctx, paths[i]); e != nil {
			return matched, fail("DOMAIN_STORE_EXPORT_FAILED")
		}
	}
	snapshots, e := c.storeReadback(ctx, paths)
	if e != nil {
		return matched, e
	}
	after, e := c.storeReadback(ctx, platformRoots(configuration))
	if e != nil || !reflect.DeepEqual(before, after) {
		return matched, fail("SOURCE_STORES_CHANGED")
	}
	if e = owners.Verify(ctx); e != nil {
		return matched, e
	}
	afterDB, e := c.Inspect(ctx)
	if e != nil || afterDB != state {
		return matched, fail("DATABASE_CHANGED_DURING_STORES")
	}
	matched.Snapshots = snapshots
	if e = writeNew(root, "matched.complete.json", jsonBytes(matched)); e != nil {
		return matched, e
	}
	return matched, nil
}

type heldNativeRoot struct {
	root    *os.Root
	file    *os.File
	binding StoreBinding
}
type freshNativeRoots struct{ held []heldNativeRoot }

func lockNativeRoots(paths []string) (*freshNativeRoots, error) {
	if len(paths) != 4 {
		return nil, fail("FOUR_FRESH_STORES_REQUIRED")
	}
	out := &freshNativeRoots{}
	for i, path := range paths {
		root, e := openPrivateRoot(path)
		if e != nil {
			out.Close()
			return nil, e
		}
		file, e := root.Open(".")
		if e != nil {
			root.Close()
			out.Close()
			return nil, fail("STORE_ROOT_LOCK_FAILED")
		}
		if syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil {
			file.Close()
			root.Close()
			out.Close()
			return nil, fail("STORE_ROOT_LOCK_FAILED")
		}
		info, e := file.Stat()
		if e != nil {
			file.Close()
			root.Close()
			out.Close()
			return nil, fail("STORE_ROOT_IDENTITY_INVALID")
		}
		out.held = append(out.held, heldNativeRoot{root, file, StoreBinding{Kind: storeKinds[i], Path: path, Identity: identity(info)}})
	}
	if e := out.Check(true); e != nil {
		out.Close()
		return nil, e
	}
	return out, nil
}
func (r *freshNativeRoots) Check(empty bool) error {
	if r == nil || len(r.held) != 4 {
		return fail("FOUR_FRESH_STORES_REQUIRED")
	}
	for _, h := range r.held {
		info, e := h.file.Stat()
		named, ne := os.Lstat(h.binding.Path)
		if e != nil || ne != nil || !owned(info, true, true) || identity(info) != h.binding.Identity || !os.SameFile(info, named) || noSymlinks(h.binding.Path) != nil {
			return fail("STORE_ROOT_IDENTITY_CHANGED")
		}
		if empty {
			f, e := h.root.Open(".")
			if e != nil {
				return fail("FRESH_STORES_REQUIRED")
			}
			entries, e := f.ReadDir(1)
			ce := f.Close()
			if len(entries) != 0 || (e != nil && e != io.EOF) || ce != nil {
				return fail("FRESH_STORES_REQUIRED")
			}
		}
	}
	return nil
}
func (r *freshNativeRoots) Close() (result error) {
	if r == nil {
		return nil
	}
	for i := len(r.held) - 1; i >= 0; i-- {
		h := r.held[i]
		if syscall.Flock(int(h.file.Fd()), syscall.LOCK_UN) != nil {
			result = errors.Join(result, fail("STORE_ROOT_UNLOCK_UNCERTAIN"))
		}
		fileError, rootError := h.file.Close(), h.root.Close()
		if fileError != nil || rootError != nil {
			result = errors.Join(result, fail("STORE_ROOT_CLOSE_UNCERTAIN"))
		}
	}
	r.held = nil
	return result
}

// RestoreMatchedStores imports once into exact empty locked target roots using
// domain-owned DB/reference/byte checks. A partial import is retained and cannot
// be retried through an existing original operation root.
func RestoreMatchedStores(ctx context.Context, c *NativeCustody, accounts *accountidentity.Service, configuration config.Config, databaseRoot, source, output string) (proof RestoredStores, result error) {
	if c == nil || c.mode != FreshRestoreMaintenance || validatePlatformRoots(configuration, databaseRoot, source, output) != nil {
		return proof, fail("STORE_ROOT_OVERLAP")
	}
	if e := c.platformIdentity(ctx, accounts, configuration); e != nil {
		return proof, e
	}
	root, e := openPrivateRoot(source)
	if e != nil {
		return proof, e
	}
	defer root.Close()
	var matched MatchedStores
	raw, e := readDocument(root, "matched.complete.json", &matched)
	if e != nil || matched.FormatVersion != 1 || !canonical(matched.DatabaseRoot) || !digest.MatchString(matched.DatabaseCompleteSHA256) || len(matched.Snapshots) != 4 {
		return proof, fail("MATCHED_STORES_PROOF_REQUIRED")
	}
	sourcePaths := make([]string, 4)
	for i, b := range matched.Snapshots {
		if b.Kind != storeKinds[i] || b.Path != filepath.Join(source, storeKinds[i]) {
			return proof, fail("MATCHED_STORES_PATH_CHANGED")
		}
		sourcePaths[i] = b.Path
	}
	readback, e := c.storeReadback(ctx, sourcePaths)
	if e != nil || !reflect.DeepEqual(readback, matched.Snapshots) {
		return proof, fail("MATCHED_STORES_CHANGED")
	}
	database, databaseSHA, e := completeAt(ctx, c.native, databaseRoot)
	if e != nil {
		return proof, e
	}
	if !sameData(database.Database, matched.Database) {
		return proof, fail("MATCHED_DATABASE_CHANGED")
	}
	databaseDirectory, e := openPrivateRoot(databaseRoot)
	if e != nil {
		return proof, e
	}
	var restoreIntent Intent
	_, e = readDocument(databaseDirectory, "intent.json", &restoreIntent)
	databaseDirectory.Close()
	if e != nil || !matchedRestoreLineage(restoreIntent, matched) {
		return proof, fail("MATCHED_DATABASE_LINEAGE_CHANGED")
	}
	current, e := c.Inspect(ctx)
	if e != nil || current != database.Database {
		return proof, fail("DATABASE_CHANGED_BEFORE_STORE_RESTORE")
	}
	targets, e := lockNativeRoots(platformRoots(configuration))
	if e != nil {
		return proof, e
	}
	defer func() {
		if e := targets.Close(); e != nil {
			result = errors.Join(result, e)
		}
	}()
	outputRoot, e := newRoot(output)
	if e != nil {
		return proof, e
	}
	defer outputRoot.Close()
	proof = RestoredStores{FormatVersion: 1, MatchedSHA256: sha(raw), DatabaseCompleteSHA256: databaseSHA, ConfigurationSHA256: nativePlatformConfigurationSHA256(configuration)}
	for _, h := range targets.held {
		proof.Targets = append(proof.Targets, h.binding)
	}
	if e = writeNew(outputRoot, "files.intent.json", jsonBytes(proof)); e != nil {
		return proof, e
	}
	model := &models.Service{Identity: accounts, RootDirectory: configuration.Models.ArtifactRoot}
	data := &datasets.Service{Identity: accounts}
	train := &training.Service{Identity: accounts, Datasets: data}
	preparation := &modelpreparations.Service{Identity: accounts, Datasets: data, Training: train}
	imports := []func() error{func() error { return model.ImportFiles(ctx, sourcePaths[0]) }, func() error { return data.ImportFiles(ctx, sourcePaths[1], configuration.Datasets.ArtifactRoot) }, func() error { return train.ImportFiles(ctx, sourcePaths[2], configuration.Training.ArtifactRoot) }, func() error {
		return preparation.ImportFiles(ctx, sourcePaths[3], configuration.ModelPreparation.ArtifactRoot)
	}}
	for _, run := range imports {
		if e = c.CheckFence(ctx); e != nil {
			return proof, e
		}
		if e = run(); e != nil {
			return proof, fail("DOMAIN_STORE_IMPORT_UNKNOWN")
		}
		if e = targets.Check(false); e != nil {
			return proof, e
		}
	}
	owners, e := openNativeOwners(ctx, c, accounts, configuration)
	if e != nil {
		return proof, e
	}
	if e = owners.Verify(ctx); e != nil {
		owners.Close()
		return proof, e
	}
	if e = owners.Close(); e != nil {
		return proof, fail("DOMAIN_STORE_CLOSE_UNCERTAIN")
	}
	proof.Targets, e = c.storeReadback(ctx, platformRoots(configuration))
	if e != nil {
		return proof, e
	}
	after, e := c.Inspect(ctx)
	if e != nil || after != current {
		return proof, fail("DATABASE_CHANGED_DURING_STORE_RESTORE")
	}
	readback, e = c.storeReadback(ctx, sourcePaths)
	if e != nil || !reflect.DeepEqual(readback, matched.Snapshots) {
		return proof, fail("MATCHED_STORES_CHANGED")
	}
	if e = writeNew(outputRoot, "files.complete.json", jsonBytes(proof)); e != nil {
		return proof, e
	}
	return proof, nil
}

// The restored file set belongs to the original database backup, not just to a
// different export that happened to have identical row contents.
func matchedRestoreLineage(i Intent, m MatchedStores) bool {
	return i.Kind == "restore" && canonical(m.DatabaseRoot) && digest.MatchString(m.DatabaseCompleteSHA256) && i.BackupRoot == m.DatabaseRoot && i.BackupCompleteSHA256 == m.DatabaseCompleteSHA256 && i.Source == m.Database
}
