//go:build repository_integration && postgres_restore_roots_inspect_integration

package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/postgresbackup"
	release "github.com/Passion-Flow/ownsolid/api/release/v1"
)

func readRootsInspectionCLISelector(t *testing.T, path string) []byte {
	t.Helper()
	if !strings.HasPrefix(path, "/runtime/") || filepath.Clean(path) != path || filepath.Base(path) != "cli-input.json" {
		t.Fatal("exact private selector path")
	}
	parent := filepath.Dir(path)
	resolved, e := filepath.EvalSymlinks(parent)
	if e != nil || resolved != parent {
		t.Fatal("selector parent")
	}
	root, e := os.OpenRoot(parent)
	if e != nil {
		t.Fatal("selector root")
	}
	defer func() {
		if root.Close() != nil {
			t.Error("selector root close")
		}
	}()
	owner := func(info os.FileInfo, directory bool) bool {
		if info == nil {
			return false
		}
		s, ok := info.Sys().(*syscall.Stat_t)
		return ok && s.Uid == uint32(os.Geteuid()) && (directory && info.Mode() == os.ModeDir|0700 || !directory && info.Mode() == 0600 && s.Nlink == 1)
	}
	info, e := root.Stat(".")
	if e != nil || !owner(info, true) {
		t.Fatal("private selector parent")
	}
	file, e := root.OpenFile(filepath.Base(path), os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if e != nil {
		t.Fatal("selector descriptor")
	}
	defer func() {
		if file.Close() != nil {
			t.Error("selector close")
		}
	}()
	before, e := file.Stat()
	if e != nil || !owner(before, false) || before.Size() < 1 || before.Size() > 65536 {
		t.Fatal("bounded private selector")
	}
	raw, e := io.ReadAll(io.LimitReader(file, 65537))
	after, ae := file.Stat()
	named, ne := root.Lstat(filepath.Base(path))
	if e != nil || ae != nil || ne != nil || !owner(after, false) || !owner(named, false) || !os.SameFile(before, after) || !os.SameFile(before, named) || after.Size() != before.Size() || named.Size() != before.Size() || int64(len(raw)) != before.Size() || !after.ModTime().Equal(before.ModTime()) || !named.ModTime().Equal(before.ModTime()) {
		t.Fatal("selector changed")
	}
	return raw
}

func TestActualPostgresRestoreRootsInspectionCLI(t *testing.T) {
	path := os.Getenv("PROJECT_ONE_TEST_ROOTS_INSPECTION_CLI_INPUT")
	raw := readRootsInspectionCLISelector(t, path)
	var e error
	var input struct {
		Configuration string `json:"configuration"`
		Request       string `json:"request"`
		Receipt       string `json:"receipt"`
	}
	if release.StrictJSON(raw, &input) != nil || input.Request != filepath.Join(filepath.Dir(path), "roots-inspection-request.json") || input.Receipt != filepath.Join(filepath.Dir(path), "cli-observation.json") || input.Configuration == input.Receipt {
		t.Fatal("protected parent fixture required")
	}
	r, _, e := postgresbackup.LoadRestoreRootsInspectionRequest(input.Request, "")
	if e != nil {
		t.Fatal("original request loader", e)
	}
	var output bytes.Buffer
	if e = runPostgresRestoreRootsInspection(postgresRestoreRootsInspectCommand, []string{"--config", input.Configuration, "--request", input.Request}, &output); e != nil {
		t.Fatal("original CLI handler", safeError(e))
	}
	var out release.PostgreSQLRestoreRootsInspectionObservation
	if release.StrictJSON(output.Bytes(), &out) != nil || !out.ValidFor(r) || !bytes.HasSuffix(output.Bytes(), []byte("\n")) {
		t.Fatal("original released-root response")
	}
	for _, root := range out.Roots {
		if input.Receipt == root.Path || strings.HasPrefix(input.Receipt, root.Path+"/") {
			t.Fatal("receipt overlaps inspected root")
		}
		if entries, e := os.ReadDir(root.Path); e != nil || len(entries) != 0 {
			t.Fatal("root was mutated")
		}
	}
	if _, e := json.Marshal(out); e != nil {
		t.Fatal("private original output")
	}
	root, e := os.OpenRoot(filepath.Dir(input.Receipt))
	if e != nil {
		t.Fatal("receipt parent")
	}
	defer func() {
		if root.Close() != nil {
			t.Error("receipt root close")
		}
	}()
	file, e := root.OpenFile(filepath.Base(input.Receipt), os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0600)
	if e != nil {
		t.Fatal("exclusive receipt")
	}
	n, we := file.Write(output.Bytes())
	se := file.Sync()
	ce := file.Close()
	parent, pe := root.Open(".")
	if pe != nil {
		t.Fatal("receipt parent descriptor")
	}
	de := parent.Sync()
	dc := parent.Close()
	if we != nil || se != nil || ce != nil || de != nil || dc != nil || n != output.Len() {
		t.Fatal("durable private receipt")
	}
}
