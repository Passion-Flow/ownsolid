package settings

import "testing"

func TestPolicyValidationAndDefaults(t *testing.T) {
	if err := Default().Validate(); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*Policy){func(p *Policy) { p.ControlRequestSeconds = 0 }, func(p *Policy) { p.BackoffInitialSeconds = 5; p.BackoffMaxSeconds = 3 }, func(p *Policy) { p.DockerSeconds = 1 }, func(p *Policy) { p.DockerStopSeconds = p.DockerHeaderSeconds }, func(p *Policy) { p.ModelLoadSeconds = p.InferenceTaskSeconds }, func(p *Policy) { p.MetricsSeconds = 1 }} {
		p := Default()
		mutate(&p)
		if p.Validate() == nil {
			t.Fatal("invalid policy accepted")
		}
	}
	if Resolve(nil) != Default() {
		t.Fatal("legacy default changed")
	}
}
