package mysqlbackup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"
)

const maximumTreeDepth = 64

type TreeReadback struct {
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
	Files  int    `json:"files"`
}
type treeObservation struct {
	device, inode uint64
	uid, gid      uint32
	mode          os.FileMode
	size          int64
	modified      time.Time
	changed       [2]int64
}

func observeTree(info os.FileInfo) (treeObservation, error) {
	s, ok := info.Sys().(*syscall.Stat_t)
	if !ok || s.Uid != uint32(os.Geteuid()) || info.Mode()&os.ModeSymlink != 0 || info.IsDir() && info.Mode() != os.ModeDir|0700 || !info.IsDir() && (!info.Mode().IsRegular() || info.Mode().Perm()&0022 != 0 || s.Nlink != 1) {
		return treeObservation{}, errors.New("MYSQL_BACKUP_TREE_PATH_INVALID")
	}
	v := reflect.ValueOf(s).Elem()
	for _, name := range []string{"Ctim", "Ctimespec"} {
		stamp := v.FieldByName(name)
		if stamp.IsValid() && stamp.Kind() == reflect.Struct {
			sec, nano := stamp.FieldByName("Sec"), stamp.FieldByName("Nsec")
			if sec.IsValid() && sec.CanInt() && nano.IsValid() && nano.CanInt() {
				return treeObservation{uint64(s.Dev), uint64(s.Ino), s.Uid, s.Gid, info.Mode(), info.Size(), info.ModTime(), [2]int64{sec.Int(), nano.Int()}}, nil
			}
		}
	}
	return treeObservation{}, errors.New("MYSQL_BACKUP_TREE_PATH_INVALID")
}

// TreeDigest retains only bounded directory names and one streaming file buffer.
// It refuses escapes, links, nonregular files, owner/mode/drift and total limits.
// File bytes and path/type/mode/size records are included; timestamps detect IO
// races but are not portable snapshot identity fields.
func TreeDigest(ctx context.Context, rootPath string, maximumBytes int64, maximumFiles int, maximumMetadata int64) (TreeReadback, error) {
	if ctx == nil || maximumBytes < 1 || maximumFiles < 1 || maximumMetadata < 4096 {
		return TreeReadback{}, errors.New("MYSQL_BACKUP_TREE_LIMIT_INVALID")
	}
	root, e := openPrivateRoot(rootPath)
	if e != nil {
		return TreeReadback{}, e
	}
	defer root.Close()
	h := sha256.New()
	encoder := json.NewEncoder(h)
	result := TreeReadback{}
	metadataRemaining := maximumMetadata
	var walk func(string) error
	walk = func(relative string) error {
		if e := ctx.Err(); e != nil {
			return e
		}
		if strings.Count(relative, "/") > maximumTreeDepth {
			return errors.New("MYSQL_BACKUP_TREE_PATH_INVALID")
		}
		before, e := root.Lstat(relative)
		if e != nil {
			return errors.New("MYSQL_BACKUP_TREE_PATH_INVALID")
		}
		stamp, e := observeTree(before)
		if e != nil {
			return e
		}
		metadataRemaining -= int64(len(relative) + 128)
		if metadataRemaining < 0 {
			return errors.New("MYSQL_BACKUP_TREE_METADATA_LIMIT_REACHED")
		}
		if before.IsDir() {
			file, e := root.Open(relative)
			if e != nil {
				return errors.New("MYSQL_BACKUP_TREE_READ_FAILED")
			}
			opened, e := file.Stat()
			if e != nil || !os.SameFile(before, opened) {
				file.Close()
				return errors.New("MYSQL_BACKUP_TREE_CHANGED")
			}
			names := []string{}
			for {
				if e := ctx.Err(); e != nil {
					file.Close()
					return e
				}
				entries, readError := file.ReadDir(256)
				for _, entry := range entries {
					name := entry.Name()
					if name == "" || name == "." || name == ".." || strings.Contains(name, "/") || !utf8.ValidString(name) || strings.ContainsFunc(name, unicode.IsControl) {
						file.Close()
						return errors.New("MYSQL_BACKUP_TREE_PATH_INVALID")
					}
					metadataRemaining -= int64(len(name) + 32)
					if metadataRemaining < 0 {
						file.Close()
						return errors.New("MYSQL_BACKUP_TREE_METADATA_LIMIT_REACHED")
					}
					names = append(names, name)
				}
				if readError == io.EOF {
					break
				}
				if readError != nil {
					file.Close()
					return errors.New("MYSQL_BACKUP_TREE_READ_FAILED")
				}
			}
			if file.Close() != nil {
				return errors.New("MYSQL_BACKUP_TREE_READ_FAILED")
			}
			sort.Strings(names)
			if encoder.Encode(struct {
				Path, Kind string
				Mode       uint32
			}{relative, "directory", uint32(before.Mode().Perm())}) != nil {
				return errors.New("MYSQL_BACKUP_TREE_READ_FAILED")
			}
			for _, name := range names {
				if e = walk(path.Join(relative, name)); e != nil {
					return e
				}
			}
		} else {
			if result.Files >= maximumFiles || before.Size() < 0 || before.Size() > maximumBytes-result.Bytes {
				return errors.New("MYSQL_BACKUP_TREE_LIMIT_REACHED")
			}
			file, e := root.OpenFile(relative, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
			if e != nil {
				return errors.New("MYSQL_BACKUP_TREE_READ_FAILED")
			}
			opened, e := file.Stat()
			if e != nil || !os.SameFile(before, opened) {
				file.Close()
				return errors.New("MYSQL_BACKUP_TREE_CHANGED")
			}
			digest, bytes, e := hashStream(ctx, nil, file, maximumBytes-result.Bytes)
			after, statError := file.Stat()
			closeError := file.Close()
			if e != nil || statError != nil || closeError != nil {
				return errors.New("MYSQL_BACKUP_TREE_READ_FAILED")
			}
			actual, e := observeTree(after)
			if e != nil || actual != stamp || bytes != before.Size() {
				return errors.New("MYSQL_BACKUP_TREE_CHANGED")
			}
			if encoder.Encode(struct {
				Path, Kind, SHA256 string
				Mode               uint32
				Bytes              int64
			}{relative, "file", digest, uint32(before.Mode().Perm()), bytes}) != nil {
				return errors.New("MYSQL_BACKUP_TREE_READ_FAILED")
			}
			result.Bytes += bytes
			result.Files++
		}
		after, e := root.Lstat(relative)
		if e != nil {
			return errors.New("MYSQL_BACKUP_TREE_CHANGED")
		}
		actual, e := observeTree(after)
		if e != nil || actual != stamp || !os.SameFile(before, after) {
			return errors.New("MYSQL_BACKUP_TREE_CHANGED")
		}
		return nil
	}
	if e = walk("."); e != nil {
		return TreeReadback{}, e
	}
	actual, e := os.Lstat(rootPath)
	opened, oe := root.Stat(".")
	if e != nil || oe != nil || !os.SameFile(actual, opened) || !owned(actual, true) {
		return TreeReadback{}, errors.New("MYSQL_BACKUP_TREE_CHANGED")
	}
	resolved, e := filepath.EvalSymlinks(rootPath)
	if e != nil || resolved != rootPath {
		return TreeReadback{}, errors.New("MYSQL_BACKUP_TREE_CHANGED")
	}
	result.SHA256 = hex.EncodeToString(h.Sum(nil))
	return result, nil
}
