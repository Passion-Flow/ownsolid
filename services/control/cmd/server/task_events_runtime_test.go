package main

import (
	"context"
	"os"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/taskbus"
)

func TestTaskEventsRuntimeAdmissionFailsClosedWithoutOwner(t *testing.T) {
	var c config.Config
	if s, e := openTaskDeliveries(context.Background(), c, nil, nil); e != nil || s != nil {
		t.Fatal("disabled created a transport owner", e)
	}
	raw, e := os.ReadFile("../../config/examples/task-events/control.example.yaml")
	if e != nil {
		t.Fatal(e)
	}
	c.TaskEvents, e = taskbus.DecodeYAMLConfiguration(raw)
	if e != nil {
		t.Fatal(e)
	}
	for _, ctx := range []context.Context{nil, context.Background()} {
		if s, e := openTaskDeliveries(ctx, c, nil, nil); s != nil || e == nil {
			t.Fatal("enabled missing owner/schema admitted", e)
		}
	}
	if e := validateUnbootstrappedFeatures(c); e == nil || e.Error() != "BOOTSTRAP_REQUIRED_FOR_CONFIGURED_FEATURES" {
		t.Fatal("enabled unbootstrapped feature admitted", e)
	}
	c.TaskEvents.RequestSeconds = 0
	if s, e := openTaskDeliveries(context.Background(), c, nil, nil); s != nil || e == nil || e.Error() != "CONFIG_TASK_EVENTS_INVALID" {
		t.Fatal("invalid policy reached runtime admission", e)
	}
}
