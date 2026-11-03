package training_test

import (
	"context"
	"encoding/json"
	"testing"

	transport "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/httpfixture"
	wire "github.com/Passion-Flow/ownsolid/api/training/v1"
)

func TestActualTLSRestrictedTrainingStopAfterEpochLossAndStrictReceiptShape(t *testing.T) {
	f := setup(t)
	dispatched(t, f)
	l := claimed(t, f)
	handler := transport.New(f.s.Identity)
	server := httpfixture.NewTLSServer(t, handler)
	defer server.Close()
	client := httpfixture.New(t, server, "admin", httpfixture.Schemas(t, "../../../../../api/openapi/v1/nodes/openapi.json"))
	client.Headers = map[string]string{"Authorization": "Bearer " + f.credential}
	path := "/agent/v1/nodes/" + f.input.NodeID + "/tasks/" + l.TaskID + "/training-stops"
	body := wire.StopSubmission{LeaseToken: l.LeaseToken, Receipt: stopFixture(t, f, l), Outcome: json.RawMessage("null")}
	raw, _ := json.Marshal(body)
	client.Call("POST", path, append([]byte(`{"unknown":true,`), raw[1:]...), 400, "Problem")
	var fields map[string]json.RawMessage
	json.Unmarshal(raw, &fields)
	delete(fields, "outcome")
	missing, _ := json.Marshal(fields)
	client.Call("POST", path, missing, 400, "Problem")
	if _, e := f.s.DB.Exec(context.Background(), "UPDATE users SET auth_epoch=auth_epoch+1 WHERE id=$1", f.portal.Me.User.ID); e != nil {
		t.Fatal(e)
	}
	first := client.Call("POST", path, body, 200, "TrainingStopAcceptanceResponse")
	if first["state"] != "cancelled" || first["gpu_released"] != true {
		t.Fatal("restricted stop incorrectly depended on revoked user authority")
	}
	client.Call("POST", path, body, 200, "TrainingStopAcceptanceResponse")
}
