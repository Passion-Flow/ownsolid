//go:build repository_integration && mysql_native_integration

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/mysqloperator"
)

const nativeVolumeRoot = "/projectone-native/payload"

var nativeVolumeRequestPath = flag.String("native-volume-request", "", "Explicit protected Linux volume measurement request")
var nativeVolumePhase = flag.String("native-volume-phase", "", "Exact preflight or final filesystem measurement")

type nativeVolumeRequest struct {
	Format           string            `json:"format"`
	Volume           string            `json:"volume"`
	UID              int               `json:"uid"`
	GID              int               `json:"gid"`
	PeakBytes        int64             `json:"peak_bytes"`
	MaximumBytes     int64             `json:"maximum_bytes"`
	MaximumFiles     int               `json:"maximum_files"`
	MaximumMetadata  int64             `json:"maximum_metadata"`
	OperationSeconds int               `json:"operation_seconds"`
	Inputs           map[string]string `json:"inputs"`
}
type nativeVolumeEntry struct {
	Path     string `json:"path"`
	Kind     string `json:"kind"`
	Mode     uint32 `json:"mode"`
	UID      uint32 `json:"uid"`
	GID      uint32 `json:"gid"`
	Device   uint64 `json:"linux_device"`
	Inode    uint64 `json:"linux_inode"`
	Nlink    uint64 `json:"nlink"`
	Bytes    int64  `json:"bytes"`
	Modified int64  `json:"linux_modified_ns"`
	SHA256   string `json:"sha256,omitempty"`
}

// This separate fixture control never opens SQL. It measures the same Go stat
// owner predicates that rejected VirtioFS; exported host inode identity is separate.
func TestActualMySQLNativeVolumeTransfer(t *testing.T) {
	if *nativeVolumeRequestPath == "" {
		t.Fatal("NATIVE_VOLUME_EXPLICIT_REQUEST_REQUIRED")
	}
	var input nativeVolumeRequest
	if _, e := mysqloperator.ReadDocument(*nativeVolumeRequestPath, 1<<20, &input); e != nil {
		t.Fatal("NATIVE_VOLUME_REQUEST_INVALID", e)
	}
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" || input.Format != "projectone.mysql.native-volume-request.v1" || !strings.HasPrefix(input.Volume, "projectone-m20-native-volume-") || input.UID != os.Geteuid() || input.GID != os.Getegid() || input.UID <= 0 || input.GID <= 0 || input.PeakBytes < 1 || input.MaximumBytes < 1 || input.MaximumFiles < 9 || input.MaximumMetadata < 4096 || input.OperationSeconds < 1 || (*nativeVolumePhase != "preflight" && *nativeVolumePhase != "final") || *nativeVolumeRequestPath != nativeVolumeRoot+"/request.json" {
		t.Fatal("NATIVE_VOLUME_EXACT_REQUEST_REQUIRED")
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Duration(input.OperationSeconds)*time.Second)
	defer cancel()
	observe := func(path, relative string) nativeVolumeEntry {
		t.Helper()
		canonical, e := filepath.EvalSymlinks(path)
		if e != nil || canonical != path {
			t.Fatal("NATIVE_VOLUME_CANONICAL_PATH_REQUIRED")
		}
		info, e := os.Lstat(path)
		if e != nil {
			t.Fatal("NATIVE_VOLUME_STAT_REQUIRED")
		}
		s, ok := info.Sys().(*syscall.Stat_t)
		if !ok || s.Uid != uint32(input.UID) || s.Gid != uint32(input.GID) || info.Mode()&os.ModeSymlink != 0 || info.IsDir() && info.Mode() != os.ModeDir|0700 || !info.IsDir() && (!info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || s.Nlink != 1) {
			t.Fatal("NATIVE_VOLUME_REAL_OWNER_MODE_REQUIRED")
		}
		kind := "file"
		if info.IsDir() {
			kind = "directory"
		}
		return nativeVolumeEntry{Path: relative, Kind: kind, Mode: uint32(info.Mode().Perm()), UID: s.Uid, GID: s.Gid, Device: uint64(s.Dev), Inode: uint64(s.Ino), Nlink: uint64(s.Nlink), Bytes: info.Size(), Modified: info.ModTime().UnixNano()}
	}
	_ = observe(nativeVolumeRoot, ".")
	var space syscall.Statfs_t
	if syscall.Statfs(nativeVolumeRoot, &space) != nil {
		t.Fatal("NATIVE_VOLUME_FILESYSTEM_REQUIRED")
	}
	// Linux local filesystem magic values: ext4, XFS, Btrfs. FUSE/VirtioFS,
	// network mounts and arbitrary local-driver options are not this profile.
	if uint64(space.Type) != 0xef53 && uint64(space.Type) != 0x58465342 && uint64(space.Type) != 0x9123683e {
		t.Fatal("NATIVE_VOLUME_LOCAL_LINUX_FILESYSTEM_REQUIRED")
	}
	available, total := uint64(space.Bavail)*uint64(space.Bsize), uint64(space.Blocks)*uint64(space.Bsize)
	additional := uint64(0)
	if *nativeVolumePhase == "preflight" {
		additional = uint64(input.PeakBytes)
	}
	if total == 0 || available < additional || available-additional < (total*3+9)/10 {
		t.Fatal("NATIVE_VOLUME_VM_HEADROOM_REQUIRED")
	}
	inputNames := []string{"inputs/operator-password", "inputs/runtime-password", "inputs/ca.pem", "inputs/source.json", "inputs/target.json", "inputs/runtime.json", "inputs/case.json", "runtime/tests", "artifacts/artifacts.json"}
	if len(input.Inputs) != len(inputNames) {
		t.Fatal("NATIVE_VOLUME_EXACT_INPUT_SET_REQUIRED")
	}
	measure := func(path, relative string) nativeVolumeEntry {
		t.Helper()
		before := observe(path, relative)
		if before.Kind != "file" || before.Bytes > input.MaximumBytes || ctx.Err() != nil {
			t.Fatal("NATIVE_VOLUME_BOUNDED_FILE_REQUIRED")
		}
		file, e := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
		if e != nil {
			t.Fatal("NATIVE_VOLUME_FILE_READ_REQUIRED")
		}
		opened, e := file.Stat()
		hash := sha256.New()
		n, readError := io.Copy(hash, io.LimitReader(file, input.MaximumBytes+1))
		after, statError := file.Stat()
		closeError := file.Close()
		if e != nil || readError != nil || statError != nil || closeError != nil || !os.SameFile(opened, after) || opened.Size() != after.Size() || opened.Mode() != after.Mode() || !opened.ModTime().Equal(after.ModTime()) || n != before.Bytes || !reflect.DeepEqual(before, observe(path, relative)) {
			t.Fatal("NATIVE_VOLUME_FILE_CHANGED")
		}
		before.SHA256 = hex.EncodeToString(hash.Sum(nil))
		return before
	}
	inputs := []nativeVolumeEntry{}
	for _, relative := range inputNames {
		entry := measure(nativeVolumeRoot+"/"+relative, relative)
		expectedMode := uint32(0600)
		if relative == "runtime/tests" {
			expectedMode = 0700
		}
		if entry.SHA256 != input.Inputs[relative] || entry.Mode != expectedMode {
			t.Fatal("NATIVE_VOLUME_INPUT_CHANGED")
		}
		inputs = append(inputs, entry)
	}
	if names, e := os.ReadDir(nativeVolumeRoot + "/inputs"); e != nil || len(names) != 7 {
		t.Fatal("NATIVE_VOLUME_EXACT_SEVEN_INPUTS_REQUIRED")
	}
	if *nativeVolumePhase == "preflight" {
		names, e := os.ReadDir(nativeVolumeRoot + "/output")
		temporary, te := os.ReadDir(nativeVolumeRoot + "/output/temporary")
		if e != nil || te != nil || len(names) != 1 || names[0].Name() != "temporary" || len(temporary) != 0 {
			t.Fatal("NATIVE_VOLUME_EMPTY_OUTPUT_REQUIRED")
		}
	}
	entries := []nativeVolumeEntry{}
	remaining := input.MaximumMetadata
	var bytes int64
	files := 0
	e := filepath.WalkDir(nativeVolumeRoot+"/output", func(path string, d fs.DirEntry, e error) error {
		if e != nil || ctx.Err() != nil {
			return fmt.Errorf("NATIVE_VOLUME_TREE_READ_FAILED")
		}
		relative, e := filepath.Rel(nativeVolumeRoot+"/output", path)
		if e != nil || strings.Count(relative, "/") > 64 {
			return fmt.Errorf("NATIVE_VOLUME_TREE_PATH_INVALID")
		}
		remaining -= int64(len(relative) + 512)
		if remaining < 0 {
			return fmt.Errorf("NATIVE_VOLUME_TREE_METADATA_LIMIT")
		}
		entry := observe(path, filepath.ToSlash(relative))
		if !d.IsDir() {
			entry = measure(path, filepath.ToSlash(relative))
			if files >= input.MaximumFiles || entry.Bytes > input.MaximumBytes-bytes {
				return fmt.Errorf("NATIVE_VOLUME_TREE_LIMIT")
			}
			files++
			bytes += entry.Bytes
		}
		entries = append(entries, entry)
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
	for _, entry := range entries {
		before := entry
		before.SHA256 = ""
		if !reflect.DeepEqual(before, observe(filepath.Join(nativeVolumeRoot+"/output", entry.Path), entry.Path)) {
			t.Fatal("NATIVE_VOLUME_TREE_CHANGED")
		}
	}
	request, e := os.ReadFile(*nativeVolumeRequestPath)
	if e != nil {
		t.Fatal("NATIVE_VOLUME_REQUEST_READ_FAILED")
	}
	requestHash := sha256.Sum256(request)
	receipt := struct {
		Format, Phase, Volume, RequestSHA256 string
		UID, GID                             int
		Filesystem, Available, Total         uint64
		Inputs, Output                       []nativeVolumeEntry
	}{"projectone.mysql.native-volume-measurement.v1", *nativeVolumePhase, input.Volume, hex.EncodeToString(requestHash[:]), input.UID, input.GID, uint64(space.Type), available, total, inputs, entries}
	raw, e := json.Marshal(receipt)
	if e != nil || int64(len(raw)) > input.MaximumMetadata {
		t.Fatal("NATIVE_VOLUME_RECEIPT_LIMIT")
	}
	_ = observe(nativeVolumeRoot+"/receipts", "receipts")
	file, e := os.OpenFile(nativeVolumeRoot+"/receipts/"+*nativeVolumePhase+".json", os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0600)
	if e != nil {
		t.Fatal("NATIVE_VOLUME_RECEIPT_EXISTS")
	}
	if _, e = file.Write(raw); e != nil || file.Sync() != nil || file.Close() != nil {
		t.Fatal("NATIVE_VOLUME_RECEIPT_WRITE_FAILED")
	}
	dir, e := os.Open(nativeVolumeRoot + "/receipts")
	if e != nil || dir.Sync() != nil || dir.Close() != nil {
		t.Fatal("NATIVE_VOLUME_RECEIPT_SYNC_FAILED")
	}
	digest := sha256.Sum256(raw)
	fmt.Printf("NATIVE_VOLUME_RECEIPT %s\n", hex.EncodeToString(digest[:]))
}
