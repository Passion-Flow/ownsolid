package config

import "testing"

func TestTelemetryConfigurationValidate(t *testing.T) {
	var absent *TelemetryConfiguration
	if e := absent.Validate(); e != nil {
		t.Fatalf("an absent section was refused: %v", e)
	}
	valid := TelemetryConfiguration{LogsAddress: "http://172.31.254.4:9428", RequestTimeoutSeconds: 5, QueueCapacity: 1024}
	if e := valid.Validate(); e != nil {
		t.Fatal(e)
	}
	if s := valid.Settings(); s.RequestTimeout.Seconds() != 5 || s.QueueCapacity != 1024 || s.LogsAddress != valid.LogsAddress {
		t.Fatalf("settings: %+v", s)
	}
	for name, mutate := range map[string]func(*TelemetryConfiguration){
		"address":       func(c *TelemetryConfiguration) { c.LogsAddress = "https://x/path" },
		"no address":    func(c *TelemetryConfiguration) { c.LogsAddress = "" },
		"zero timeout":  func(c *TelemetryConfiguration) { c.RequestTimeoutSeconds = 0 },
		"long timeout":  func(c *TelemetryConfiguration) { c.RequestTimeoutSeconds = 61 },
		"zero capacity": func(c *TelemetryConfiguration) { c.QueueCapacity = 0 },
		"huge capacity": func(c *TelemetryConfiguration) { c.QueueCapacity = 65537 },
	} {
		c := valid
		mutate(&c)
		if e := c.Validate(); e == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}
