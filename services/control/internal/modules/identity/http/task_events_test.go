package http

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
)

func TestTaskDeliveryPublicPolicyRedactsPrivateConfiguration(t *testing.T) {
	var c config.Config
	c.TaskEvents.Enabled = true
	c.TaskEvents.RequestSeconds = 19
	c.TaskEvents.Endpoint = "tls://private-task-marker.invalid:4422"
	c.TaskEvents.PublisherSeedFile = "/private/task-marker/publisher.nk"
	raw, e := json.Marshal(taskDeliveryPublicPolicy(c))
	if e != nil || strings.Contains(string(raw), "private-task-marker") || strings.Contains(string(raw), "publisher") {
		t.Fatal("private task-event settings entered public configuration", e)
	}
	var values map[string]any
	if json.Unmarshal(raw, &values) != nil || len(values) != 2 || values["enabled"] != true || values["request_timeout_milliseconds"] != float64(19000) {
		t.Fatal("nondefault public deadline or shape changed")
	}
	c.TaskEvents.Enabled = false
	if value := taskDeliveryPublicPolicy(c); value.Enabled || value.RequestMilliseconds != 0 {
		t.Fatal("disabled public policy retained private configuration")
	}
}
