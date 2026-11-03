package engine

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// Explicit Engine archive double, with fixed synthetic namespace UUIDs.
func storeArchiveTestBytes(t *testing.T, p storeTaskProfile, uid, gid int, fault string) []byte {
	t.Helper()
	var out bytes.Buffer
	tw := tar.NewWriter(&out)
	root := p.role
	prefix := root + "/private"
	ns := StoreNamespace{SchemaVersion: 1, StoreID: "bcd9b6c4-f70a-4e0f-931c-892f5e6a3bed", TenantID: "d8c8f7db-1b2b-45b6-8583-6d72f3f57c38"}
	if p.kind == "training" {
		ns.Kind = "training"
	}
	if p.kind == "preparation" {
		ns.Kind = "model_preparation"
	}
	switch fault {
	case "store-version":
		ns.SchemaVersion++
	case "store-kind":
		ns.Kind = "foreign"
	case "store-tenant":
		ns.TenantID = "invalid"
	case "store-id":
		ns.StoreID = "00000000-0000-0000-0000-000000000000"
	}
	raw, _ := json.Marshal(ns)
	headers := []tar.Header{{Name: root, Typeflag: tar.TypeDir, Mode: 0755}, {Name: prefix, Typeflag: tar.TypeDir, Mode: 0700, Uid: uid, Gid: gid}, {Name: prefix + "/namespace.json", Typeflag: tar.TypeReg, Mode: 0600, Uid: uid, Gid: gid, Size: int64(len(raw))}, {Name: prefix + "/owner.lock", Typeflag: tar.TypeReg, Mode: 0600, Uid: uid, Gid: gid}}
	for _, n := range p.children {
		headers = append(headers, tar.Header{Name: prefix + "/" + n, Typeflag: tar.TypeDir, Mode: 0700, Uid: uid, Gid: gid})
	}
	switch fault {
	case "store-mode":
		headers[2].Mode = 0644
	case "store-owner":
		headers[2].Uid++
	case "store-link":
		headers[2].Linkname = "/foreign"
	case "store-extra":
		headers = append(headers, tar.Header{Name: prefix + "/foreign", Typeflag: tar.TypeReg, Mode: 0600})
	case "store-missing":
		headers = headers[:len(headers)-1]
	case "store-lock":
		headers[3].Size = 1
	case "store-nonempty":
		headers = append(headers, tar.Header{Name: prefix + "/" + p.children[0] + "/foreign", Typeflag: tar.TypeReg, Mode: 0600})
	}
	for _, h := range headers {
		if e := tw.WriteHeader(&h); e != nil {
			t.Fatal(e)
		}
		if h.Name == prefix+"/namespace.json" {
			tw.Write(raw)
		}
		if h.Name == prefix+"/owner.lock" && h.Size > 0 {
			tw.Write([]byte("x"))
		}
	}
	if e := tw.Close(); e != nil {
		t.Fatal(e)
	}
	return out.Bytes()
}
func TestStoreTaskFixedProfilesAndArchiveGuard(t *testing.T) {
	for _, kind := range []string{DatasetStoreInitialize, DatasetStoreCheck, DatasetStoreRecord, TrainingStoreInitialize, TrainingStoreCheck, TrainingStoreRecord, PreparationStoreInitialize, PreparationStoreCheck, PreparationStoreRecord} {
		t.Run(kind, func(t *testing.T) {
			c, task, state, posts, files := databaseTaskFixture(t, "", kind)
			ctx := context.Background()
			p, _ := storeTask(kind)
			expectedRole := "datasets"
			expectedKind := "dataset"
			if strings.Contains(kind, "training") {
				expectedRole, expectedKind = "training", "training"
			}
			if strings.Contains(kind, "preparation") {
				expectedRole, expectedKind = "preparations", "preparation"
			}
			if p.role != expectedRole || p.kind != expectedKind || task.OutputOwner.Role != expectedRole {
				t.Fatal("wrong fixed store mapping")
			}
			args := task.command()
			if !strings.Contains(strings.Join(args, " "), "--"+expectedKind+"-store-root /var/lib/projectone/"+expectedRole+"/private") {
				t.Fatal("wrong fixed command", args)
			}
			object, e := c.CreateDatabaseTask(ctx, task)
			if e != nil {
				t.Fatal(e)
			}
			if _, e = c.startDatabaseTask(ctx, task, object.ID, files, 30, databaseAdmit); e != nil {
				t.Fatal(e)
			}
			state.Store(3)
			r, e := c.InspectStoreTaskResult(ctx, task, object.ID)
			if e != nil || r.Namespace.StoreID == "" || r.NamespaceSHA256 == "" {
				t.Fatal(r, e)
			}
			if e = c.RemoveDatabaseTask(ctx, task, object.ID); e != nil || posts.Load() != 3 {
				t.Fatal("store removal", e)
			}
		})
	}
	for _, fault := range []string{"store-version", "store-kind", "store-tenant", "store-id", "store-mode", "store-owner", "store-link", "store-extra", "store-missing", "store-lock", "store-nonempty"} {
		t.Run(fault, func(t *testing.T) {
			p, _ := storeTask(DatasetStoreCheck)
			if _, e := storeTaskArchive(storeArchiveTestBytes(t, p, 10033, 10034, fault), p, 10033, 10034); e == nil {
				t.Fatal("unsafe store archive accepted")
			}
		})
	}
}
func TestStoreTaskRejectsUnboundReasonOwnershipAndActiveConsumer(t *testing.T) {
	for _, change := range []func(*DatabaseTask){func(t *DatabaseTask) { t.Reason = "" }, func(t *DatabaseTask) { t.Reason = "bad\nreason" }, func(t *DatabaseTask) { t.Reason = strings.Repeat("x", 501) }, func(t *DatabaseTask) { t.OutputOwner.Role = "models" }, func(t *DatabaseTask) { delete(t.Peers, "control") }} {
		c, task, _, posts, _ := databaseTaskFixture(t, "", DatasetStoreInitialize)
		change(&task)
		if _, e := c.CreateDatabaseTask(context.Background(), task); e == nil || posts.Load() != 0 {
			t.Fatal("unbound store context accepted", e)
		}
	}
	c, task, _, posts, _ := databaseTaskFixture(t, "", DatasetStoreCheck)
	task.Reason = "irrelevant"
	if _, e := c.CreateDatabaseTask(context.Background(), task); e == nil || posts.Load() != 0 {
		t.Fatal("readonly command accepted reason")
	}
	c, task, state, posts, files := databaseTaskFixture(t, "control-running", DatasetStoreInitialize)
	obj, e := c.CreateDatabaseTask(context.Background(), task)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = c.startDatabaseTask(context.Background(), task, obj.ID, files, 30, databaseAdmit); e == nil || posts.Load() != 1 {
		t.Fatal("active control allowed start")
	}
	state.Store(3)
	if _, e = c.InspectStoreTaskResult(context.Background(), task, obj.ID); e == nil {
		t.Fatal("active control allowed fresh result readback")
	}
}

func TestStoreResultRejectsChangedTaskPeerAndForeignOutputConsumer(t *testing.T) {
	for _, fault := range []string{"store-state-change", "store-peer-change", "store-foreign-consumer"} {
		t.Run(fault, func(t *testing.T) {
			c, task, state, posts, _ := databaseTaskFixture(t, fault, DatasetStoreCheck)
			obj, e := c.CreateDatabaseTask(context.Background(), task)
			if fault == "store-foreign-consumer" {
				if e == nil || posts.Load() != 0 {
					t.Fatal("foreign output consumer accepted", e)
				}
				return
			}
			if e != nil {
				t.Fatal(e)
			}
			state.Store(3)
			if _, e = c.InspectStoreTaskResult(context.Background(), task, obj.ID); e == nil || posts.Load() != 1 {
				t.Fatal("changed result context accepted", e)
			}
		})
	}
}
