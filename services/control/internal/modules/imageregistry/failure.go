package imageregistry

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

type FailedFile struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
}
type FailureReceipt struct {
	PinID       string       `json:"pin_id"`
	Files       []FailedFile `json:"files"`
	Directories []string     `json:"directories"`
	SHA256      string       `json:"sha256"`
}

func failedFile(ctx context.Context, root *os.Root, name string, maximum int64) (FailedFile, error) {
	f, e := root.Open(name)
	if e != nil {
		return FailedFile{}, e
	}
	defer f.Close()
	info, e := f.Stat()
	if e != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() > maximum {
		return FailedFile{}, failure("REGISTRY_CONTENT_INVALID")
	}
	h := sha256.New()
	n, e := io.Copy(h, &contextReader{ctx, io.LimitReader(f, maximum+1)})
	if e != nil || n != info.Size() {
		return FailedFile{}, failure("REGISTRY_CONTENT_INVALID")
	}
	return FailedFile{name, "sha256:" + hex.EncodeToString(h.Sum(nil)), n}, nil
}
func captureFailure(ctx context.Context, c Config, pin string) (FailureReceipt, error) {
	out := FailureReceipt{PinID: pin, Files: []FailedFile{}, Directories: []string{}}
	root, e := privateRoot(c.StoreRoot)
	if e != nil {
		return out, e
	}
	defer root.Close()
	for _, base := range []string{pin, "staging-" + pin} {
		f, e := root.Open(base)
		if os.IsNotExist(e) {
			continue
		}
		if e != nil {
			return FailureReceipt{}, e
		}
		entries, e := f.ReadDir(c.Verification.MaximumArchiveEntries + 1)
		f.Close()
		if e != nil && e != io.EOF || len(entries) > c.Verification.MaximumArchiveEntries {
			return FailureReceipt{}, failure("REGISTRY_RECOVERY_REQUIRED")
		}
		out.Directories = append(out.Directories, base)
		for _, entry := range entries {
			if entry.Name() == "blobs" && entry.IsDir() {
				dir, e := root.Open(base + "/blobs")
				if e != nil {
					return FailureReceipt{}, e
				}
				blobs, e := dir.ReadDir(c.Verification.MaximumArchiveEntries + 1)
				dir.Close()
				if e != nil && e != io.EOF || len(blobs) > c.Verification.MaximumArchiveEntries {
					return FailureReceipt{}, failure("REGISTRY_RECOVERY_REQUIRED")
				}
				out.Directories = append(out.Directories, base+"/blobs")
				for _, blob := range blobs {
					if !validDigest("sha256:"+blob.Name()) || !blob.Type().IsRegular() {
						return FailureReceipt{}, failure("REGISTRY_RECOVERY_REQUIRED")
					}
					proof, e := failedFile(ctx, root, base+"/blobs/"+blob.Name(), c.MaximumBlobBytes)
					if e != nil {
						return FailureReceipt{}, e
					}
					out.Files = append(out.Files, proof)
				}
				continue
			}
			if entry.Name() != "archive.tar" && entry.Name() != "manifest.json" && entry.Name() != "inventory.json" || !entry.Type().IsRegular() {
				return FailureReceipt{}, failure("REGISTRY_RECOVERY_REQUIRED")
			}
			proof, e := failedFile(ctx, root, base+"/"+entry.Name(), c.MaximumArchiveBytes)
			if e != nil {
				return FailureReceipt{}, e
			}
			out.Files = append(out.Files, proof)
		}
	}
	sort.Slice(out.Files, func(i, j int) bool { return out.Files[i].Name < out.Files[j].Name })
	sort.Strings(out.Directories)
	raw, _ := json.Marshal(struct {
		PinID       string
		Files       []FailedFile
		Directories []string
	}{out.PinID, out.Files, out.Directories})
	out.SHA256 = digestBytes(raw)
	return out, nil
}
func preparedFailureWithin(ctx context.Context, q transaction.Executor, p Pin) error {
	if p.ManifestDigest != nil || p.State != "failed" && p.State != "importing" && p.State != "unknown" {
		return failure("REGISTRY_RECOVERY_REQUIRED")
	}
	var phase string
	if e := q.QueryRow(ctx, statement(q, `SELECT phase FROM image_registry_imports WHERE tenant_id=$1 AND workspace_id=$2 AND pin_id=$3 FOR UPDATE`, `SELECT phase FROM image_registry_imports WHERE tenant_id=? AND workspace_id=? AND pin_id=? FOR UPDATE`, p.tenant, p.WorkspaceID, p.ID)).Scan(&phase); e != nil {
		return e
	}
	if phase != "prepared" {
		return failure("REGISTRY_REMOTE_RECONCILIATION_REQUIRED")
	}
	return eligibleWithin(ctx, q, p)
}
func (s *Service) AbandonPrepared(ctx context.Context, a identity.Auth, m Mutation, pin string) (FailureReceipt, error) {
	if m.validate() != nil || !validID(pin) {
		return FailureReceipt{}, failure("VALIDATION_FAILED")
	}
	var out FailureReceipt
	e := s.actor(ctx, a, m.WorkspaceID, true, false, func(ctx context.Context, q transaction.Executor, w workspaces.Workspace) error {
		_, repeat, e := mutationWithin(ctx, q, a, w, "abandon", pin, m, []string{pin, m.Reason})
		if e != nil {
			return e
		}
		p, e := pinWithin(ctx, q, w.TenantID, w.ID, pin)
		if e != nil {
			return e
		}
		if e = preparedFailureWithin(ctx, q, p); e != nil {
			return e
		}
		out, e = captureFailure(ctx, s.config, pin)
		if e != nil {
			return e
		}
		if repeat {
			return nil
		}
		if p.State != "failed" {
			if e = stateWithin(ctx, q, p, "failed"); e != nil {
				return e
			}
		}
		return failureAudit(ctx, q, a, w, m, pin, "preparation.abandoned", out)
	})
	if e != nil {
		return FailureReceipt{}, e
	}
	return out, nil
}
func failureAudit(ctx context.Context, q transaction.Executor, a identity.Auth, w workspaces.Workspace, m Mutation, pin, action string, receipt FailureReceipt) error {
	var total int64
	for _, file := range receipt.Files {
		total += file.Bytes
	}
	meta, _ := json.Marshal(map[string]any{"file_inventory_sha256": receipt.SHA256, "files": len(receipt.Files), "bytes": total})
	_, e := q.Exec(ctx, statement(q, `INSERT INTO audit_events(id,occurred_at,actor_user_id,audience,action,outcome,target_type,target_id,reason,request_id,tenant_id,workspace_id,metadata) VALUES($1,clock_timestamp(),$2,'admin',$3,'success','image_registry_pin',$4,$5,$6,$7,$8,$9)`, `INSERT INTO audit_events(id,occurred_at,actor_user_id,audience,action,outcome,target_type,target_id,reason,request_id,tenant_id,workspace_id,metadata) VALUES(?,UTC_TIMESTAMP(6),?,'admin',?,'success','image_registry_pin',?,?,?,?,?,CAST(? AS JSON))`, uuid.NewString(), a.Me.User.ID, "image_registry."+action, pin, m.Reason, m.RequestID, w.TenantID, w.ID, meta))
	return e
}

// DiscardFailed requires the exact independently reviewable file inventory digest
// returned by AbandonPrepared. An unknown commit performs no filesystem cleanup.
func (s *Service) discardFailed(ctx context.Context, a identity.Auth, m Mutation, pin, expectedInventorySHA string) error {
	if m.validate() != nil || !validID(pin) || !validDigest(expectedInventorySHA) {
		return failure("VALIDATION_FAILED")
	}
	var proof FailureReceipt
	fresh := false
	e := s.actor(ctx, a, m.WorkspaceID, true, false, func(ctx context.Context, q transaction.Executor, w workspaces.Workspace) error {
		_, repeat, e := mutationWithin(ctx, q, a, w, "discard", pin, m, []string{pin, expectedInventorySHA, m.Reason})
		if e != nil || repeat {
			return e
		}
		p, e := pinWithin(ctx, q, w.TenantID, w.ID, pin)
		if e != nil {
			return e
		}
		if p.State != "failed" {
			return failure("REGISTRY_RECOVERY_REQUIRED")
		}
		if e = preparedFailureWithin(ctx, q, p); e != nil {
			return e
		}
		proof, e = captureFailure(ctx, s.config, pin)
		if e != nil {
			return e
		}
		if proof.SHA256 != expectedInventorySHA {
			return failure("REGISTRY_INTENT_STALE")
		}
		fresh = true
		return failureAudit(ctx, q, a, w, m, pin, "preparation.discarded", proof)
	})
	if e != nil || !fresh {
		return e
	}
	root, e := privateRoot(s.config.StoreRoot)
	if e != nil {
		return e
	}
	defer root.Close()
	for _, file := range proof.Files {
		actual, e := failedFile(ctx, root, file.Name, s.config.MaximumArchiveBytes)
		if e != nil || actual != file {
			return failure("REGISTRY_RECOVERY_REQUIRED")
		}
		if e = root.Remove(file.Name); e != nil {
			return failure("REGISTRY_CLEANUP_REQUIRED")
		}
	}
	sort.Slice(proof.Directories, func(i, j int) bool {
		return strings.Count(proof.Directories[i], "/") > strings.Count(proof.Directories[j], "/")
	})
	for _, dir := range proof.Directories {
		if e = root.Remove(dir); e != nil {
			return failure("REGISTRY_CLEANUP_REQUIRED")
		}
	}
	if e = syncDirectory(root, "."); e != nil {
		return failure("REGISTRY_CLEANUP_REQUIRED")
	}
	return s.actor(ctx, a, m.WorkspaceID, true, false, func(ctx context.Context, q transaction.Executor, w workspaces.Workspace) error {
		p, e := pinWithin(ctx, q, w.TenantID, w.ID, pin)
		if e != nil {
			return e
		}
		if p.State != "failed" {
			return failure("REGISTRY_INTENT_STALE")
		}
		if e = preparedFailureWithin(ctx, q, p); e != nil {
			return e
		}
		if e = stateWithin(ctx, q, p, "deleted"); e != nil {
			return e
		}
		return registryAudit(ctx, q, a, w, "preparation.cleanup_completed", pin, m)
	})
}

func (s *Service) DiscardFailed(ctx context.Context, a identity.Auth, m Mutation, pin, expectedInventorySHA string) error {
	return safeError(s.discardFailed(ctx, a, m, pin, expectedInventorySHA))
}
