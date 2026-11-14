package imageregistry

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"syscall"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

type snapshotPin struct {
	ID              string `json:"id"`
	InventorySHA256 string `json:"inventory_sha256"`
}
type fileSnapshot struct {
	Version   int           `json:"version"`
	Tenant    string        `json:"tenant_id"`
	Workspace string        `json:"workspace_id"`
	Pins      []snapshotPin `json:"pins"`
}
type SnapshotReceipt struct {
	SHA256 string `json:"sha256"`
	Pins   int    `json:"pins"`
	Bytes  int64  `json:"bytes"`
}

func imageFiles(pin string, image cachedImage) []string {
	names := []string{pin + "/archive.tar", pin + "/inventory.json", pin + "/manifest.json"}
	seen := map[string]bool{}
	for _, d := range image.Descriptors {
		if !seen[d.Digest] {
			names = append(names, pin+"/blobs/"+d.Digest[7:])
			seen[d.Digest] = true
		}
	}
	return names
}
func copyPrivate(ctx context.Context, source, destination *os.Root, name string) (int64, error) {
	from, e := source.Open(name)
	if e != nil {
		return 0, e
	}
	defer from.Close()
	info, e := from.Stat()
	if e != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
		return 0, failure("REGISTRY_CONTENT_INVALID")
	}
	to, e := destination.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return 0, e
	}
	n, e := io.Copy(to, &contextReader{ctx, io.LimitReader(from, info.Size()+1)})
	if e == nil && n != info.Size() {
		e = failure("REGISTRY_CONTENT_INVALID")
	}
	if e == nil {
		e = to.Sync()
	}
	return n, joinedClose(to, e)
}
func admitBytes(path string, needed int64, percentage int) error {
	var fs syscall.Statfs_t
	if needed < 0 || percentage < 30 || syscall.Statfs(path, &fs) != nil || fs.Blocks == 0 {
		return failure("REGISTRY_STORE_INVALID")
	}
	available := uint64(fs.Bavail) * uint64(fs.Bsize)
	capacity := uint64(fs.Blocks) * uint64(fs.Bsize)
	if available < uint64(needed) || available-uint64(needed) < capacity/100*uint64(percentage) {
		return failure("REGISTRY_STORAGE_CAPACITY_REQUIRED")
	}
	return nil
}

// ExportFiles is an operator storage API, never an HTTP-selected host path. It
// retains exact current Workspace file provenance alongside a logical DB export.
func (s *Service) ExportFiles(ctx context.Context, a identity.Auth, workspace, destination string) (SnapshotReceipt, error) {
	if !filepath.IsAbs(destination) || filepath.Clean(destination) != destination || withinPath(s.config.StoreRoot, destination) || withinPath(destination, s.config.StoreRoot) || !withinPath(s.config.Verification.MountRoot, destination) {
		return SnapshotReceipt{}, failure("REGISTRY_SNAPSHOT_INVALID")
	}
	var receipt SnapshotReceipt
	e := s.actor(ctx, a, workspace, true, false, func(ctx context.Context, q transaction.Executor, w workspaces.Workspace) error {
		source, e := privateRoot(s.config.StoreRoot)
		if e != nil {
			return e
		}
		defer source.Close()
		index := fileSnapshot{Version: 1, Tenant: w.TenantID, Workspace: w.ID, Pins: []snapshotPin{}}
		rows, e := q.Query(ctx, statement(q, "SELECT "+pinPG+" FROM image_registry_pins WHERE tenant_id=$1 AND workspace_id=$2 AND state<>'deleted' ORDER BY id FOR UPDATE", "SELECT "+pinMY+" FROM image_registry_pins WHERE tenant_id=? AND workspace_id=? AND state<>'deleted' ORDER BY id FOR UPDATE", w.TenantID, w.ID))
		if e != nil {
			return e
		}
		pins := []Pin{}
		for rows.Next() {
			p, e := scanPin(rows)
			if e != nil {
				rows.Close()
				return e
			}
			pins = append(pins, p)
			if len(pins) > s.config.MaximumPins {
				rows.Close()
				return failure("REGISTRY_CAPACITY_REQUIRED")
			}
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
		images := make([]cachedImage, len(pins))
		var needed int64
		for i, p := range pins {
			if p.State != "ready" {
				return failure("REGISTRY_RECOVERY_REQUIRED")
			}
			images[i], e = readCached(ctx, s.config, p.ID)
			if e != nil {
				return e
			}
			if e = cachedMatches(p, images[i]); e != nil {
				return e
			}
			for _, name := range imageFiles(p.ID, images[i]) {
				info, e := source.Stat(name)
				if e != nil {
					return e
				}
				needed += info.Size()
			}
			index.Pins = append(index.Pins, snapshotPin{p.ID, *p.inventorySHA})
		}
		parent, e := privateRoot(filepath.Dir(destination))
		if e != nil {
			return e
		}
		parent.Close()
		if e = admitBytes(filepath.Dir(destination), needed, s.config.MinimumHeadroomPercent); e != nil {
			return e
		}
		if e = os.Mkdir(destination, 0700); e != nil {
			return failure("REGISTRY_NEW_SNAPSHOT_REQUIRED")
		}
		target, e := privateRoot(destination)
		if e != nil {
			return e
		}
		defer target.Close()
		for i, p := range pins {
			if e = target.Mkdir(p.ID, 0700); e != nil {
				return e
			}
			if e = target.Mkdir(p.ID+"/blobs", 0700); e != nil {
				return e
			}
			for _, name := range imageFiles(p.ID, images[i]) {
				n, e := copyPrivate(ctx, source, target, name)
				if e != nil {
					return e
				}
				receipt.Bytes += n
			}
			copyConfig := s.config
			copyConfig.StoreRoot = destination
			copy, e := readCached(ctx, copyConfig, p.ID)
			if e != nil || cachedMatches(p, copy) != nil {
				return failure("REGISTRY_SNAPSHOT_INVALID")
			}
			if e = syncDirectory(target, p.ID+"/blobs"); e != nil {
				return e
			}
			if e = syncDirectory(target, p.ID); e != nil {
				return e
			}
		}
		raw, e := json.Marshal(index)
		if e != nil {
			return e
		}
		if e = writePrivate(target, "snapshot.json", raw); e != nil {
			return e
		}
		if e = syncDirectory(target, "."); e != nil {
			return e
		}
		parent, e = privateRoot(filepath.Dir(destination))
		if e != nil {
			return e
		}
		e = syncDirectory(parent, ".")
		parent.Close()
		if e != nil {
			return e
		}
		receipt.SHA256 = digestBytes(raw)
		receipt.Pins = len(pins)
		return nil
	})
	if e != nil {
		return SnapshotReceipt{}, safeError(e)
	}
	return receipt, nil
}

// ImportFiles is called only under the parent's unavailable restore boundary,
// after logical metadata restore. It copies exact DB-bound pins and refuses to
// overwrite/adopt any existing unmatched local directory. VerifyStored and grant
// invalidation are mandatory before the parent can publish readiness again.
func (s *Service) importFiles(ctx context.Context, snapshot, expectedSHA string) error {
	if !validDigest(expectedSHA) || !filepath.IsAbs(snapshot) || filepath.Clean(snapshot) != snapshot || withinPath(s.config.StoreRoot, snapshot) || withinPath(snapshot, s.config.StoreRoot) || !withinPath(s.config.Verification.MountRoot, snapshot) {
		return failure("REGISTRY_SNAPSHOT_INVALID")
	}
	store, e := s.store()
	if e != nil {
		return e
	}
	ctx, cancel := context.WithTimeout(ctx, s.config.OperationTimeout())
	defer cancel()
	source, e := privateRoot(snapshot)
	if e != nil {
		return e
	}
	defer source.Close()
	info, e := source.Stat("snapshot.json")
	if e != nil || info.Size() > s.config.Verification.MaximumMetadataBytes {
		return failure("REGISTRY_SNAPSHOT_INVALID")
	}
	raw, e := source.ReadFile("snapshot.json")
	if e != nil || digestBytes(raw) != expectedSHA {
		return failure("REGISTRY_SNAPSHOT_INVALID")
	}
	var index fileSnapshot
	if json.Unmarshal(raw, &index) != nil || index.Version != 1 || !validID(index.Tenant) || !validID(index.Workspace) || len(index.Pins) > s.config.MaximumPins {
		return failure("REGISTRY_SNAPSHOT_INVALID")
	}
	return safeError(store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
		var installed string
		if e := q.QueryRow(ctx, statement(q, `SELECT tenant_id::text FROM installation_state WHERE id=1 AND bootstrap_complete FOR SHARE`, `SELECT tenant_id FROM installation_state WHERE id=1 AND bootstrap_complete=1 FOR SHARE`)).Scan(&installed); e != nil {
			return e
		}
		if installed != index.Tenant {
			return failure("REGISTRY_SNAPSHOT_INVALID")
		}
		destination, e := privateRoot(s.config.StoreRoot)
		if e != nil {
			return e
		}
		defer destination.Close()
		seen := map[string]bool{}
		for _, entry := range index.Pins {
			if !validID(entry.ID) || seen[entry.ID] {
				return failure("REGISTRY_SNAPSHOT_INVALID")
			}
			seen[entry.ID] = true
			p, e := pinWithin(ctx, q, index.Tenant, index.Workspace, entry.ID)
			if e != nil {
				return e
			}
			if p.State != "ready" || p.inventorySHA == nil || *p.inventorySHA != entry.InventorySHA256 {
				return failure("REGISTRY_SNAPSHOT_INVALID")
			}
			fromConfig := s.config
			fromConfig.StoreRoot = snapshot
			image, e := readCached(ctx, fromConfig, p.ID)
			if e != nil || cachedMatches(p, image) != nil {
				return failure("REGISTRY_SNAPSHOT_INVALID")
			}
			if _, e = destination.Stat(p.ID); e == nil {
				existing, e := readCached(ctx, s.config, p.ID)
				if e != nil || cachedMatches(p, existing) != nil {
					return failure("REGISTRY_RECOVERY_REQUIRED")
				}
				continue
			} else if !os.IsNotExist(e) {
				return e
			}
			var needed int64
			for _, name := range imageFiles(p.ID, image) {
				info, e := source.Stat(name)
				if e != nil {
					return e
				}
				needed += info.Size()
			}
			if e = admitBytes(s.config.StoreRoot, needed, s.config.MinimumHeadroomPercent); e != nil {
				return e
			}
			stage := "staging-" + p.ID
			if e = destination.Mkdir(stage, 0700); e != nil {
				return failure("REGISTRY_RECOVERY_REQUIRED")
			}
			if e = destination.Mkdir(stage+"/blobs", 0700); e != nil {
				return e
			}
			// Preserve exact names while writing into the known unpublished stage root.
			target, e := os.OpenRoot(filepath.Join(s.config.StoreRoot, stage))
			if e != nil {
				return e
			}
			origin, e := os.OpenRoot(filepath.Join(snapshot, p.ID))
			if e != nil {
				target.Close()
				return e
			}
			for _, name := range imageFiles(p.ID, image) {
				relative := name[len(p.ID)+1:]
				if _, e = copyPrivate(ctx, origin, target, relative); e != nil {
					origin.Close()
					target.Close()
					return e
				}
			}
			origin.Close()
			if e = syncDirectory(target, "blobs"); e == nil {
				e = syncDirectory(target, ".")
			}
			target.Close()
			if e != nil {
				return e
			}
			if e = destination.Rename(stage, p.ID); e != nil {
				return e
			}
			if e = syncDirectory(destination, "."); e != nil {
				return e
			}
		}
		return nil
	}))
}

func (s *Service) ImportFiles(ctx context.Context, snapshot, expectedSHA string) error {
	return safeError(s.importFiles(ctx, snapshot, expectedSHA))
}
