package engine

import (
	"context"
	"encoding/json"
	"testing"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

func nativeRestoreRootsLifecycleFixture(t *testing.T, fault string) (*Client, DatabaseTask, []ConfigurationFile, func() int32) {
	t.Helper()
	task := nativeRestoreRootsFixture()
	raw, _ := json.Marshal(task.NativePostgreSQLRestoreRootsInspection.Request)
	files := []ConfigurationFile{{Name: NativePostgreSQLRestoreRootsRequestFile, Contents: raw}}
	c, task, _, effects, files := nativeFixedNativeTaskLifecycleFixture(t, task, fault, files, nativeRestoreRootsOutputFixture(task), []string{wire.PostgreSQLRestoreRootsInspectCommand, "--config", "/fixture/configuration/runtime.yaml", "--request", DatabaseTaskDestination + "/private/native-roots-request.json"}, "none")
	return c, task, files, effects.Load
}
func TestNativePostgreSQLRestoreRootsUnixNetworkNoneOriginalLifecycle(t *testing.T) {
	c, task, files, effects := nativeRestoreRootsLifecycleFixture(t, "")
	ctx := context.Background()
	created, e := c.CreateDatabaseTask(ctx, task)
	if e != nil {
		t.Fatal(e)
	}
	output, e := c.ObserveDatabaseTaskOutput(ctx, task, created.ID)
	if e != nil {
		t.Fatal(e)
	}
	defer output.Close()
	if _, e = c.startDatabaseTask(ctx, task, created.ID, files, 30, databaseAdmit); e != nil {
		t.Fatal(e)
	}
	got, e := output.Wait(ctx)
	if e != nil || got.NativePostgreSQLRestoreRootsInspection == nil || got.NativePostgreSQLRestoreRootsInspection.Observation.CurrentLockHeld {
		t.Fatal("released original roots observation lost", e)
	}
	if _, e = c.startDatabaseTask(ctx, task, created.ID, files, 30, databaseAdmit); e == nil || effects() != 2 {
		t.Fatal("readonly start replay admitted")
	}
	if e = c.StopDatabaseTask(ctx, task, created.ID, 5); e != nil {
		t.Fatal(e)
	}
	if e = c.RemoveDatabaseTask(ctx, task, created.ID); e != nil || effects() != 4 {
		t.Fatal("normal terminal custody cleanup failed", e)
	}
}
func TestNativePostgreSQLRestoreRootsUnixRejectsForeignConsumersAndLiveControl(t *testing.T) {
	for _, fault := range []string{"foreign-store", "foreign-projection", "running-control", "bridge-actor"} {
		t.Run(fault, func(t *testing.T) {
			c, task, files, effects := nativeRestoreRootsLifecycleFixture(t, fault)
			ctx := context.Background()
			created, e := c.CreateDatabaseTask(ctx, task)
			if e != nil {
				if effects() > 1 {
					t.Fatal("unrelated effects after refused context")
				}
				return
			}
			if _, e = c.startDatabaseTask(ctx, task, created.ID, files, 30, databaseAdmit); e == nil || effects() != 1 {
				t.Fatal("foreign consumer/live peer authorized a start")
			}
		})
	}
}
func TestNativePostgreSQLRestoreRootsUnixLostStartPreservesExistingActor(t *testing.T) {
	c, task, files, effects := nativeRestoreRootsLifecycleFixture(t, "start-lost")
	ctx := context.Background()
	created, e := c.CreateDatabaseTask(ctx, task)
	if e != nil {
		t.Fatal(e)
	}
	output, e := c.ObserveDatabaseTaskOutput(ctx, task, created.ID)
	if e != nil {
		t.Fatal(e)
	}
	defer output.Close()
	if _, e = c.startDatabaseTask(ctx, task, created.ID, files, 30, databaseAdmit); e == nil || effects() != 2 {
		t.Fatal("lost start was reported certain")
	}
	if _, e = c.CreateDatabaseTask(ctx, task); e == nil || effects() != 2 {
		t.Fatal("unknown readonly actor replaced")
	}
	if _, e = c.startDatabaseTask(ctx, task, created.ID, files, 30, databaseAdmit); e == nil || effects() != 2 {
		t.Fatal("unknown readonly start replayed")
	}
	if _, e = output.Wait(ctx); e != nil {
		t.Fatal("original private observation not retained", e)
	}
}
