package compose

import (
	"encoding/json"
	"testing"
)

func TestTypedModelJSONRoundtripPreservesOptionalAbsenceAndFalse(t *testing.T) {
	_, _, model := modelFixture(t)
	service := model.Services["control"]
	service.Command = nil
	service.Environment = nil
	service.Mounts = nil
	service.Ports = nil
	service.Tmpfs = nil
	service.ReadOnly = false
	model.Services["control"] = service
	model.Volumes = nil
	raw, e := json.Marshal(model)
	if e != nil {
		t.Fatal(e)
	}
	decoded, e := DecodeModel(raw, 1048576)
	if e != nil || decoded.Services["control"].ReadOnly {
		t.Fatal("valid typed model failed JSON roundtrip", e)
	}
}
