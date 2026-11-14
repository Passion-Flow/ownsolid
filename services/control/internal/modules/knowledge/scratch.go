package knowledge

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	wire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
)

const scratchMarkerFile = ".projectone-knowledge-operation.json"
const scratchMarkerKind = "projectone-knowledge-operation"
const maximumScratchMarkerBytes = 1024

type scratchMarker struct {
	SchemaVersion int    `json:"schema_version"`
	Kind          string `json:"kind"`
	Directory     string `json:"directory"`
	RootSHA256    string `json:"root_sha256"`
	ParentPID     int    `json:"parent_pid"`
	CreatedAt     string `json:"created_at"`
}

func (p *Processor) createScratch() (string, error) {
	p.scratchMu.Lock()
	defer p.scratchMu.Unlock()
	name := "knowledge-" + uuid.NewString()
	directory := filepath.Join(p.configuration.ScratchRoot, name)
	if e := os.Mkdir(directory, 0700); e != nil {
		return "", failure("KNOWLEDGE_SCRATCH_UNAVAILABLE")
	}
	marker := scratchMarker{SchemaVersion: 1, Kind: scratchMarkerKind, Directory: name, RootSHA256: hashBytes([]byte(p.configuration.ScratchRoot)), ParentPID: os.Getpid(), CreatedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	raw, _ := json.Marshal(marker)
	file, e := os.OpenFile(filepath.Join(directory, scratchMarkerFile), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e == nil {
		_, e = file.Write(raw)
		closeError := file.Close()
		if e == nil {
			e = closeError
		}
	}
	if e != nil {
		_ = os.RemoveAll(directory)
		return "", failure("KNOWLEDGE_SCRATCH_UNAVAILABLE")
	}
	return directory, nil
}
func (p *Processor) removeScratch(path string) {
	p.scratchMu.Lock()
	defer p.scratchMu.Unlock()
	if e := os.RemoveAll(path); e != nil {
		slog.Error("knowledge_scratch_cleanup_failed", "code", "KNOWLEDGE_SCRATCH_UNAVAILABLE")
	}
}

// CleanupScratch preserves live/uncertain owners and every unrecognized path.
// It processes at most the configured cleanup batch, independently of DB retention.
func (p *Processor) CleanupScratch() error {
	if p == nil {
		return failure("KNOWLEDGE_UNAVAILABLE")
	}
	p.scratchMu.Lock()
	defer p.scratchMu.Unlock()
	directory, e := os.Open(p.configuration.ScratchRoot)
	if e != nil {
		return failure("KNOWLEDGE_SCRATCH_UNAVAILABLE")
	}
	defer directory.Close()
	entries, e := directory.ReadDir(p.policy.CleanupBatch)
	if e != nil && len(entries) == 0 && !errors.Is(e, io.EOF) {
		return failure("KNOWLEDGE_SCRATCH_UNAVAILABLE")
	}
	remove := []string{}
	for _, entry := range entries {
		name := entry.Name()
		id := strings.TrimPrefix(name, "knowledge-")
		parsed, e := uuid.Parse(id)
		if e != nil || parsed == uuid.Nil || parsed.String() != id || name != "knowledge-"+id || !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			return failure("KNOWLEDGE_SCRATCH_ROOT_INVALID")
		}
		path := filepath.Join(p.configuration.ScratchRoot, name)
		if protected(path, true, false, false) != nil {
			return failure("KNOWLEDGE_SCRATCH_ROOT_INVALID")
		}
		markerPath := filepath.Join(path, scratchMarkerFile)
		info, e := os.Lstat(markerPath)
		if e != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > maximumScratchMarkerBytes {
			return failure("KNOWLEDGE_SCRATCH_ROOT_INVALID")
		}
		owner, ok := info.Sys().(*syscall.Stat_t)
		if !ok || int(owner.Uid) != os.Geteuid() || owner.Nlink != 1 {
			return failure("KNOWLEDGE_SCRATCH_ROOT_INVALID")
		}
		raw, e := trustedFile(markerPath, maximumScratchMarkerBytes)
		var marker scratchMarker
		decodeError := wire.DecodeStrict(raw, &marker)
		created, timeError := time.Parse(time.RFC3339Nano, marker.CreatedAt)
		if e != nil || decodeError != nil || marker.SchemaVersion != 1 || marker.Kind != scratchMarkerKind || marker.Directory != name || marker.RootSHA256 != hashBytes([]byte(p.configuration.ScratchRoot)) || marker.ParentPID < 1 || marker.ParentPID > 2147483647 || timeError != nil || created.Before(time.Unix(0, 0)) || created.After(time.Now().Add(time.Minute)) {
			return failure("KNOWLEDGE_SCRATCH_ROOT_INVALID")
		}
		if e = syscall.Kill(marker.ParentPID, 0); errors.Is(e, syscall.ESRCH) {
			remove = append(remove, path)
		} else if e != nil && !errors.Is(e, syscall.EPERM) {
			return failure("KNOWLEDGE_SCRATCH_UNAVAILABLE")
		}
	}
	for _, path := range remove {
		if e = os.RemoveAll(path); e != nil {
			return failure("KNOWLEDGE_SCRATCH_UNAVAILABLE")
		}
	}
	return nil
}
