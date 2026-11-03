package engine

import (
	"context"
	"encoding/json"
	"io"
	"math"
	"os"
	"sort"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// These are immutable artifact facts, not an arbitrary extraction program.
type StoreRestoreArtifact struct {
	Role           string `json:"role"`
	UID            int    `json:"uid"`
	GID            int    `json:"gid"`
	Bytes          int64  `json:"bytes"`
	SHA256         string `json:"sha256"`
	MaximumEntries int    `json:"maximum_entries"`
}

type StoreRestoreProfile struct {
	Artifact          StoreRestoreArtifact `json:"artifact"`
	FilesSHA256       string               `json:"files_sha256"`
	DirectoriesSHA256 string               `json:"directories_sha256"`
}

func (r StoreRestoreArtifact) valid() bool {
	role := r.Role == "models" || r.Role == "datasets" || r.Role == "training" || r.Role == "preparations"
	return role && r.UID > 0 && r.GID > 0 && r.UID <= math.MaxInt32 && r.GID <= math.MaxInt32 && r.Bytes >= tarEndBytes && r.Bytes <= maximumBackupArchiveBytes && wire.ValidSHA256(r.SHA256) && r.MaximumEntries >= 2 && r.MaximumEntries <= maximumStoreArchiveEntries
}

func (r StoreRestoreProfile) valid() bool {
	return r.Artifact.valid() && wire.ValidSHA256(r.FilesSHA256) && wire.ValidSHA256(r.DirectoriesSHA256)
}

func storeRestoreInventoryDigests(files []BackupArchiveFile, directories []string, modes map[string]int64) (string, string) {
	files = append([]BackupArchiveFile(nil), files...)
	directories = append([]string(nil), directories...)
	sort.Slice(files, func(i, j int) bool { return files[i].Name < files[j].Name })
	sort.Strings(directories)
	type fileEntry struct {
		BackupArchiveFile
		Mode int64 `json:"mode"`
	}
	entries := make([]fileEntry, 0, len(files))
	for _, file := range files {
		entries = append(entries, fileEntry{file, modes[file.Name]})
	}
	f, _ := json.Marshal(entries)
	d, _ := json.Marshal(directories)
	return wire.Digest(f), wire.Digest(d)
}

func readStoreRestoreSnapshot(source io.Reader, r StoreRestoreArtifact) (StoreBackupArchive, StoreRestoreProfile, error) {
	maximum := r.Bytes + archiveReadbackReserveBytes
	if maximum > maximumBackupArchiveBytes {
		maximum = maximumBackupArchiveBytes
	}
	if maximum < archiveReadbackReserveBytes {
		maximum = archiveReadbackReserveBytes
	}
	directories := []string{}
	modes := map[string]int64{}
	archive, e := readStoreSnapshotArchive(source, io.Discard, r.Role, r.UID, r.GID, maximum, r.MaximumEntries, &directories, modes, "")
	if e != nil {
		return archive, StoreRestoreProfile{}, e
	}
	f, d := storeRestoreInventoryDigests(archive.Files, directories, modes)
	return archive, StoreRestoreProfile{Artifact: r, FilesSHA256: f, DirectoriesSHA256: d}, nil
}

// DescribeStoreRestoreArchive requires the independently verified complete
// backup descriptor. It reads no database and never mutates the host artifact.
func DescribeStoreRestoreArchive(ctx context.Context, input *os.File, r StoreRestoreArtifact) (StoreRestoreProfile, error) {
	if !r.valid() {
		return StoreRestoreProfile{}, failure("OFFLINE_ENGINE_STORE_RESTORE_ARTIFACT_INVALID")
	}
	before, e := restorePrivateFile(input, r.Bytes)
	if e != nil {
		return StoreRestoreProfile{}, e
	}
	if _, e = input.Seek(0, io.SeekStart); e != nil {
		return StoreRestoreProfile{}, failure("OFFLINE_ENGINE_RESTORE_ARCHIVE_SEEK_FAILED")
	}
	archive, profile, e := readStoreRestoreSnapshot(restoreContextReader{ctx, input}, r)
	if e != nil || archive.Bytes != r.Bytes || archive.SHA256 != r.SHA256 {
		return StoreRestoreProfile{}, failure("OFFLINE_ENGINE_STORE_RESTORE_ARCHIVE_INVALID")
	}
	after, e := restorePrivateFile(input, r.Bytes)
	if e != nil || !os.SameFile(before, after) || before.ModTime() != after.ModTime() {
		return StoreRestoreProfile{}, failure("OFFLINE_ENGINE_RESTORE_ARCHIVE_CHANGED")
	}
	return profile, nil
}
