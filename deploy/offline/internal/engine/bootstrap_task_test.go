package engine

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// Explicit untrusted Engine archive double; this is never product credential data.
func bootstrapArchiveTestBytes(t *testing.T, uid, gid int, fault string) []byte {
	t.Helper()
	var b bytes.Buffer
	w := tar.NewWriter(&b)
	headers := []tar.Header{{Name: "bootstrap", Typeflag: tar.TypeDir, Mode: 0755}, {Name: "bootstrap/private", Typeflag: tar.TypeDir, Mode: 0700, Uid: uid, Gid: gid}, {Name: "bootstrap/private/credential.json", Typeflag: tar.TypeReg, Mode: 0600, Uid: uid, Gid: gid}}
	payload := []byte(`{"synthetic_secret":"never-log-this"}`)
	headers[2].Size = int64(len(payload))
	switch fault {
	case "output-mode":
		headers[2].Mode = 0644
	case "output-owner":
		headers[2].Uid++
	case "output-parent":
		headers[1].Mode = 0755
	case "output-link":
		headers[2].Typeflag = tar.TypeSymlink
		headers[2].Linkname = "/foreign"
		headers[2].Size = 0
	case "output-size":
		payload = bytes.Repeat([]byte("x"), maximumBootstrapIntentBytes+1)
		headers[2].Size = int64(len(payload))
	case "output-path":
		headers[2].Name = "bootstrap/private/../credential.json"
	}
	for i := range headers {
		if e := w.WriteHeader(&headers[i]); e != nil {
			t.Fatal(e)
		}
		if headers[i].Typeflag == tar.TypeReg {
			if _, e := w.Write(payload); e != nil {
				t.Fatal(e)
			}
		}
	}
	if fault == "output-extra" {
		w.WriteHeader(&tar.Header{Name: "bootstrap/private/extra", Typeflag: tar.TypeReg, Mode: 0600})
	}
	if fault == "output-duplicate" {
		w.WriteHeader(&headers[1])
	}
	if e := w.Close(); e != nil {
		t.Fatal(e)
	}
	if fault == "output-tail" {
		return append(b.Bytes(), []byte("foreign")...)
	}
	return b.Bytes()
}
func TestBootstrapIntentArchiveRejectsUnsafeInventoryAndDoesNotSerializeSecret(t *testing.T) {
	for _, fault := range []string{"", "output-mode", "output-owner", "output-parent", "output-link", "output-size", "output-path", "output-extra", "output-duplicate", "output-tail"} {
		t.Run(fault, func(t *testing.T) {
			r, e := bootstrapIntentArchive(bootstrapArchiveTestBytes(t, 10033, 10034, fault), 10033, 10034)
			if fault != "" {
				if e == nil {
					t.Fatal("unsafe archive accepted")
				}
				return
			}
			if e != nil || r.Bytes == 0 || len(r.SHA256) != 64 {
				t.Fatal(r, e)
			}
			b, _ := json.Marshal(r)
			if bytes.Contains(b, []byte("never-log-this")) || bytes.Contains(b, []byte("Contents")) {
				t.Fatal("secret serialized")
			}
		})
	}
}
func TestBootstrapTaskRequiresDistinctPrivateStateAndInput(t *testing.T) {
	for _, kind := range []string{DatabaseBootstrap, DatabaseBootstrapReconcile, DatabaseBootstrapResume} {
		t.Run(kind, func(t *testing.T) {
			c, task, state, posts, files := databaseTaskFixture(t, "", kind)
			ctx := context.Background()
			created, e := c.CreateDatabaseTask(ctx, task)
			if e != nil {
				t.Fatal(e)
			}
			if _, e = c.startDatabaseTask(ctx, task, created.ID, files[:2], 30, databaseAdmit); e == nil || posts.Load() != 1 {
				t.Fatal("missing protected request accepted", e)
			}
			if _, e = c.ReadBootstrapIntent(ctx, task, created.ID); e == nil {
				t.Fatal("created task readback accepted")
			}
			state.Store(3)
			r, e := c.ReadBootstrapIntent(ctx, task, created.ID)
			if e != nil || r.Bytes == 0 {
				t.Fatal(r, e)
			}
			if e = c.RemoveDatabaseTask(ctx, task, created.ID); e != nil {
				t.Fatal(e)
			}
			if _, e = c.ReadBootstrapIntent(ctx, task, created.ID); e == nil {
				t.Fatal("removed task readback accepted")
			}
		})
	}
	for _, change := range []func(*DatabaseTask){func(t *DatabaseTask) { t.OutputOwner = nil }, func(t *DatabaseTask) { t.OutputOwner.ScopeID = strings.Repeat("f", 32) }, func(t *DatabaseTask) { *t.OutputOwner = t.VolumeOwner }, func(t *DatabaseTask) { t.OutputOwner.Role = "models" }} {
		c, task, _, posts, _ := databaseTaskFixture(t, "", DatabaseBootstrap)
		change(&task)
		if _, e := c.CreateDatabaseTask(context.Background(), task); e == nil || posts.Load() != 0 {
			t.Fatal("unsafe state accepted", e)
		}
	}
	c, task, _, posts, _ := databaseTaskFixture(t, "")
	task.OutputOwner = &task.VolumeOwner
	if _, e := c.CreateDatabaseTask(context.Background(), task); e == nil || posts.Load() != 0 {
		t.Fatal("output mounted for nonbootstrap command")
	}
}
func TestBootstrapTaskRejectsChangedOutputMountAndReadback(t *testing.T) {
	for _, fault := range []string{"output-mount-rw", "output-actual-rw", "output-nocopy", "output-mount-name", "output-state-change"} {
		t.Run(fault, func(t *testing.T) {
			c, task, state, posts, _ := databaseTaskFixture(t, fault, DatabaseBootstrapReconcile)
			object, e := c.CreateDatabaseTask(context.Background(), task)
			if fault != "output-state-change" {
				if e == nil || posts.Load() != 1 {
					t.Fatal("unsafe mount accepted", e)
				}
				return
			}
			if e != nil {
				t.Fatal(e)
			}
			state.Store(3)
			if _, e = c.ReadBootstrapIntent(context.Background(), task, object.ID); e == nil || posts.Load() != 1 {
				t.Fatal("changed task readback accepted", e)
			}
		})
	}
}
