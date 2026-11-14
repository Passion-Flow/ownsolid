//go:build linux || darwin

package main

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"os"
	"syscall"
)

const siemCursorName = "cursor.json"

type siemCursor struct {
	Version  int    `json:"version"`
	Binding  string `json:"binding"`
	Position int64  `json:"position"`
	Hash     string `json:"hash"`
	Sent     int64  `json:"sent"`
}

type siemCursorStore struct {
	root   *os.Root
	lock   *os.File
	state  siemCursor
	exists bool
}

func openSIEMCursor(directory, binding string, window siemWindow) (*siemCursorStore, error) {
	unavailable := errors.New("AUDIT_SIEM_CURSOR_UNAVAILABLE")
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, unavailable
	}
	lock, err := root.Open(".")
	if err != nil {
		root.Close()
		return nil, unavailable
	}
	store := &siemCursorStore{root: root, lock: lock}
	info, err := lock.Stat()
	if err != nil || !info.IsDir() || !protectedSIEMFile(info) {
		store.close()
		return nil, unavailable
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		store.close()
		return nil, errors.New("AUDIT_SIEM_CURSOR_LOCKED")
	}
	if err := store.load(binding, window); err != nil {
		store.close()
		return nil, err
	}
	return store, nil
}

func (s *siemCursorStore) close() {
	_ = s.lock.Close()
	_ = s.root.Close()
}

func (s *siemCursorStore) load(binding string, window siemWindow) error {
	invalid := errors.New("AUDIT_SIEM_CURSOR_INVALID")
	info, err := s.root.Lstat(siemCursorName)
	if errors.Is(err, os.ErrNotExist) {
		s.state = siemCursor{Version: 1, Binding: binding, Position: window.from, Hash: window.previous}
		return nil
	}
	if err != nil || !info.Mode().IsRegular() || !protectedSIEMFile(info) {
		return invalid
	}
	file, err := s.root.Open(siemCursorName)
	if err != nil {
		return invalid
	}
	defer file.Close()
	raw, err := readSIEMMaterial(file, 4096)
	if err != nil || !exactSIEMJSON(raw, &s.state, []string{"version", "binding", "position", "hash", "sent"}) {
		return invalid
	}
	_, hashOK := hashBytes(s.state.Hash)
	if s.state.Version != 1 || !hashOK || s.state.Position < 0 ||
		(s.state.Position == 0) != (s.state.Hash == zeroHash) || s.state.Sent < 0 || s.state.Sent > s.state.Position {
		return invalid
	}
	if s.state.Binding != binding {
		return errors.New("AUDIT_SIEM_CURSOR_GRANT_MISMATCH")
	}
	s.exists = true
	return nil
}

func (s *siemCursorStore) save(next siemCursor) error {
	unavailable := errors.New("AUDIT_SIEM_CURSOR_WRITE_FAILED")
	name := "cursor-" + rand.Text() + ".tmp"
	file, err := s.root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return unavailable
	}
	defer s.root.Remove(name)
	err = json.NewEncoder(file).Encode(next)
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil || closeErr != nil || s.root.Rename(name, siemCursorName) != nil || s.lock.Sync() != nil {
		return unavailable
	}
	s.state, s.exists = next, true
	return nil
}
