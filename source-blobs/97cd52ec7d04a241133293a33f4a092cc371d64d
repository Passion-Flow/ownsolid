package objectstore

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"syscall"
)

type StageRecord struct {
	ID        string `json:"id"`
	Namespace string `json:"namespace_id"`
	Bytes     int64  `json:"expected_bytes"`
	SHA256    string `json:"sha256"`
	Device    uint64 `json:"device,string"`
	Inode     uint64 `json:"inode,string"`
}

func (s *Stage) writeRecord(id string, size int64, digest string, info os.FileInfo) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return failure("OBJECT_STAGING_INVALID")
	}
	record := StageRecord{id, s.Namespace(), size, digest, uint64(stat.Dev), uint64(stat.Ino)}
	raw, _ := json.Marshal(record)
	f, e := s.root.OpenFile("records/"+id, os.O_CREATE|os.O_EXCL|os.O_WRONLY|syscall.O_NOFOLLOW, 0600)
	if e != nil {
		return failure("OBJECT_STAGING_CONFLICT")
	}
	_, e = f.Write(raw)
	if e == nil {
		e = f.Sync()
	}
	closed := f.Close()
	if e != nil || closed != nil {
		return failure("OBJECT_STAGING_FAILED")
	}
	return stageSync(s.root, "records")
}
func (s *Stage) readRecord(id string) (StageRecord, error) {
	var record StageRecord
	if _, e := (Ref{s.Tenant(), id, id}).Key(); e != nil {
		return record, e
	}
	f, e := s.root.OpenFile("records/"+id, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if e != nil {
		return record, failure("OBJECT_STAGING_RECORD_MISSING")
	}
	defer f.Close()
	info, e := f.Stat()
	if e != nil || !privateOwned(info, false) {
		return record, failure("OBJECT_STAGING_INVALID")
	}
	raw, e := io.ReadAll(io.LimitReader(f, 4097))
	if e != nil || len(raw) > 4096 {
		return record, failure("OBJECT_STAGING_INVALID")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&record) != nil || decoder.Decode(new(any)) != io.EOF || record.ID != id || record.Namespace != s.Namespace() || record.Bytes < 1 || !ValidDigest(record.SHA256) || record.Inode == 0 {
		return StageRecord{}, failure("OBJECT_STAGING_INVALID")
	}
	canonical, _ := json.Marshal(record)
	if !bytes.Equal(raw, canonical) {
		return StageRecord{}, failure("OBJECT_STAGING_INVALID")
	}
	return record, nil
}
func recordMatches(r StageRecord, i os.FileInfo) bool {
	if !privateOwned(i, false) {
		return false
	}
	stat, ok := i.Sys().(*syscall.Stat_t)
	return ok && uint64(stat.Dev) == r.Device && uint64(stat.Ino) == r.Inode
}
func (s *Stage) removeRecord(id string) error {
	if _, e := s.readRecord(id); e != nil {
		return e
	}
	if s.root.Remove("records/"+id) != nil {
		return failure("OBJECT_STAGING_FAILED")
	}
	return stageSync(s.root, "records")
}

// Records returns only bounded, journal-owned attempts. Unknown files are never
// adopted or pruned. Device/inode fields stay in the operator API, not user APIs.
func (s *Stage) Records(limit int) ([]StageRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || limit < 1 || limit > MaximumMultipartParts {
		return nil, failure("OBJECT_REQUEST_INVALID")
	}
	dir, e := s.root.Open("records")
	if e != nil {
		return nil, failure("OBJECT_STAGING_INVALID")
	}
	defer dir.Close()
	entries, e := dir.ReadDir(limit)
	if e != nil && e != io.EOF {
		return nil, failure("OBJECT_STAGING_FAILED")
	}
	out := []StageRecord{}
	for _, entry := range entries {
		r, e := s.readRecord(entry.Name())
		if e != nil {
			return nil, e
		}
		out = append(out, r)
	}
	return out, nil
}

// RemoveAttempt requires the exact retained namespace/device/inode receipt and
// an exclusive nonblocking file lock. It can remove partial failed writes but
// refuses an active consumer, a replacement inode, or an unjournaled file.
func (s *Stage) RemoveAttempt(expected StageRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return failure("OBJECT_STORAGE_CLOSED")
	}
	current, e := s.readRecord(expected.ID)
	if e != nil {
		return e
	}
	if current != expected {
		return failure("OBJECT_STAGING_CHANGED")
	}
	f, e := s.root.OpenFile("files/"+expected.ID, os.O_RDWR|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if errors.Is(e, os.ErrNotExist) {
		return s.removeRecord(expected.ID)
	}
	if e != nil {
		return failure("OBJECT_STAGING_INVALID")
	}
	defer f.Close()
	info, e := f.Stat()
	if e != nil || !recordMatches(current, info) {
		return failure("OBJECT_STAGING_CHANGED")
	}
	if syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil {
		return failure("OBJECT_STAGING_BUSY")
	}
	path, e := s.root.Lstat("files/" + expected.ID)
	if e != nil || !os.SameFile(path, info) {
		return failure("OBJECT_STAGING_CHANGED")
	}
	if s.root.Remove("files/"+expected.ID) != nil || stageSync(s.root, "files") != nil {
		return failure("OBJECT_STAGING_FAILED")
	}
	return s.removeRecord(expected.ID)
}
