package v1

import (
	"encoding/json"
	"testing"
	"time"
)

func TestRegistryDeliveryCapabilityRejectsNullAndTruthyValues(t *testing.T) {
	for _, raw := range []string{`{"registry_image_delivery":true}`, `{"registry_image_delivery":false}`, `{}`} {
		var value InferenceObservation
		if e := json.Unmarshal([]byte(raw), &value); e != nil {
			t.Fatal("valid capability refused", e)
		}
		if bool(value.RegistryImageDelivery) != (raw == `{"registry_image_delivery":true}`) {
			t.Fatal("capability truthiness")
		}
	}
	for _, raw := range []string{`null`, `"false"`, `"true"`, `0`, `1`, `{}`, `[]`} {
		var value InferenceObservation
		if json.Unmarshal([]byte(`{"registry_image_delivery":`+raw+`}`), &value) == nil {
			t.Fatal("invalid capability accepted", raw)
		}
	}
}

func TestRegistryDeliveryCapabilityCannotEstablishImageReadiness(t *testing.T) {
	o := InferenceObservation{RegistryImageDelivery: true, ProbeErrorCode: InferenceImageMissing, GPUs: []NVIDIAGPU{}, ObservedAt: time.Now().UTC()}
	if e := o.Validate(); e != nil {
		t.Fatal("explicit missing observation refused", e)
	}
	raw, e := json.Marshal(o)
	if e != nil {
		t.Fatal(e)
	}
	var value map[string]any
	if json.Unmarshal(raw, &value) != nil || value["registry_image_delivery"] != true || value["probe_error_code"] != InferenceImageMissing || value["deployment"] != nil {
		t.Fatal("capability became successful deployment")
	}
}
