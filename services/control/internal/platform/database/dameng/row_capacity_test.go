package dameng

import "testing"

func TestMonitoringLookaheadCapacityKeepsIndependentMemoryGuards(t *testing.T) {
	c := connectorConfigurationFixture()
	c.Policy.MaximumRows = 10001
	if err := c.Validate(); err != nil {
		t.Fatal("required single lookahead row is unavailable", err)
	}
	c.Policy.MaximumRows = 10002
	if c.Validate() == nil {
		t.Fatal("unbounded row capacity admitted")
	}
	c.Policy.MaximumRows = 10001
	c.Policy.MaximumResultBytes = (16 << 20) + 1
	if c.Validate() == nil {
		t.Fatal("row lookahead relaxed scalar memory bound")
	}
	s := SchemaConfiguration{FormatVersion: 1, Schema: "P1_OWNER", RuntimeUser: "P1_RUNTIME", LockWaitSeconds: 1, StepSeconds: 1, TotalSeconds: 2, CleanupSeconds: 1, MaximumMetadataRows: 10001, MaximumMetadataBytes: 16384}
	if err := s.Validate(); err != nil {
		t.Fatal("same runtime/schema row capacity cannot be selected", err)
	}
	s.MaximumMetadataRows++
	if s.Validate() == nil {
		t.Fatal("schema and connector capacity bounds diverged")
	}
}
