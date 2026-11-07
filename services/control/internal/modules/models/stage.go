package models

import (
	"io"
	"os"
	"path/filepath"
	"strconv"
	"syscall"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
)

// stageCondition is what one verification attempt left in its stage directory.
type stageCondition int

const (
	// stageEmpty: the directory is missing or holds nothing, so the attempt wrote no file.
	stageEmpty stageCondition = iota
	// stagePartial: only approved files, at least one of them unfinished. The attempt
	// stopped while extracting, for example because control crashed or was stopped.
	stagePartial
	// stageFinished: every approved file, each written, synced and made read-only.
	stageFinished
	// stageForeign: something extraction never writes. It is left for an operator.
	stageForeign
)

// stagePath is the directory one attempt extracts into. Each attempt has its own, so
// a stopped attempt's files are never mixed with the next attempt's.
func stagePath(id string, attempt int) string {
	return "imports/" + id + "/stage-" + strconv.Itoa(attempt)
}

// isStageName reports a directory name some attempt can have made.
func isStageName(name string) bool {
	for attempt := 1; attempt <= config.ModelImportSchemaMaximumAttempts; attempt++ {
		if name == "stage-"+strconv.Itoa(attempt) {
			return true
		}
	}
	return false
}

// ownedRegular reports a regular, non-executable file of this process's user with
// one link: the only kind of file the import paths write.
func ownedRegular(root *os.Root, path string) (os.FileInfo, bool) {
	info, e := root.Lstat(path)
	if e != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 != 0 {
		return nil, false
	}
	owner, ok := info.Sys().(*syscall.Stat_t)
	return info, ok && owner.Uid == uint32(os.Geteuid()) && owner.Nlink == 1
}

// stageFiles lists the approved model files in one stage directory. Anything else in
// it, or more entries than the approved model has, is MODEL_CLEANUP_CONFLICT.
func stageFiles(root *os.Root, path string) ([]string, error) {
	if e := directory(root, path); e != nil {
		return nil, e
	}
	stage, e := root.Open(path)
	if e != nil {
		return nil, fail("MODEL_CLEANUP_CONFLICT")
	}
	approved := len(wire.ApprovedModel().Files)
	children, e := stage.ReadDir(approved + 1)
	stage.Close()
	if e != nil && e != io.EOF {
		return nil, fail("MODEL_CLEANUP_CONFLICT")
	}
	if len(children) > approved {
		return nil, fail("MODEL_CLEANUP_CONFLICT")
	}
	files := make([]string, 0, len(children))
	for _, child := range children {
		item := filepath.Join(path, child.Name())
		if _, ok := wire.ModelFileForName(child.Name()); !ok {
			return nil, fail("MODEL_CLEANUP_CONFLICT")
		}
		if _, ok := ownedRegular(root, item); !ok {
			return nil, fail("MODEL_CLEANUP_CONFLICT")
		}
		files = append(files, item)
	}
	return files, nil
}

// classifyStage reads a stage without hashing it. modelartifact.ExtractArchive makes
// a file read-only only after writing and syncing all of it, so a read-only file of
// the approved size is finished; the digests are checked later by VerifyDirectory.
func classifyStage(root *os.Root, path string) (stageCondition, error) {
	info, e := root.Lstat(path)
	if os.IsNotExist(e) {
		return stageEmpty, nil
	}
	if e != nil {
		return 0, fail("MODEL_IO_FAILED")
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return stageForeign, nil
	}
	files, e := stageFiles(root, path)
	if e != nil {
		return stageForeign, nil
	}
	if len(files) == 0 {
		return stageEmpty, nil
	}
	finished := 0
	for _, item := range files {
		expected, _ := wire.ModelFileForName(filepath.Base(item))
		if info, ok := ownedRegular(root, item); ok && info.Mode().Perm() == 0444 && info.Size() == expected.SizeBytes {
			finished++
		}
	}
	if finished == len(wire.ApprovedModel().Files) {
		return stageFinished, nil
	}
	return stagePartial, nil
}

// removeStage deletes a stage's approved files and then the stage directory.
func removeStage(root *os.Root, path string) error {
	files, e := stageFiles(root, path)
	if e != nil {
		return e
	}
	for _, item := range files {
		if root.Remove(item) != nil {
			return fail("MODEL_IO_FAILED")
		}
	}
	if root.Remove(path) != nil {
		return fail("MODEL_IO_FAILED")
	}
	return syncDirectory(root, filepath.Dir(path))
}
