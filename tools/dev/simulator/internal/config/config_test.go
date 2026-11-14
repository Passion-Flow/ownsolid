package config

import (
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "simulator.json")
	if e := os.WriteFile(path, []byte(content), 0o600); e != nil {
		t.Fatal(e)
	}
	return path
}

func TestLoadAppliesDefaults(t *testing.T) {
	c, e := Load(write(t, `{"control_origin":"https://admin.example:8443","identities_file":"/srv/sim/identities.json","agents":2000}`))
	if e != nil {
		t.Fatal(e)
	}
	if c.AgentsPerConnection != 1 || c.HTTPSConnections != DefaultHTTPSConnections || c.EnrollConcurrency != DefaultEnrollConcurrency ||
		c.RequestSeconds != DefaultRequestSeconds || *c.StartSeconds != DefaultStartSeconds || c.LogSeconds != DefaultLogSeconds {
		t.Fatalf("defaults %+v", c)
	}
	if c.ValidateRun() == nil || c.ValidateEnroll() == nil {
		t.Fatal("run and enroll accepted a configuration without a duration, report or tokens")
	}
}

func TestLoadKeepsAnExplicitZeroStart(t *testing.T) {
	c, e := Load(write(t, `{"control_origin":"https://admin.example","identities_file":"/srv/i.json","agents":1,"start_seconds":0}`))
	if e != nil || *c.StartSeconds != 0 {
		t.Fatalf("%v %+v", e, c)
	}
}

func TestLoadRefusesUnknownAndMalformedInput(t *testing.T) {
	for name, content := range map[string]string{
		"unknown field":  `{"control_origin":"https://a.example","identities_file":"/i","agents":1,"agent":1}`,
		"trailing data":  `{"control_origin":"https://a.example","identities_file":"/i","agents":1} {}`,
		"http origin":    `{"control_origin":"http://a.example","identities_file":"/i","agents":1}`,
		"origin path":    `{"control_origin":"https://a.example/x","identities_file":"/i","agents":1}`,
		"relative file":  `{"control_origin":"https://a.example","identities_file":"i.json","agents":1}`,
		"no agents":      `{"control_origin":"https://a.example","identities_file":"/i","agents":0}`,
		"too many":       `{"control_origin":"https://a.example","identities_file":"/i","agents":100001}`,
		"bad dial":       `{"control_origin":"https://a.example","identities_file":"/i","agents":1,"dial_address":"nowhere"}`,
		"unknown fault":  `{"control_origin":"https://a.example","identities_file":"/i","agents":1,"faults":[{"kind":"melt","at_seconds":1,"percent":5}]}`,
		"no percent":     `{"control_origin":"https://a.example","identities_file":"/i","agents":1,"faults":[{"kind":"disconnect","at_seconds":1}]}`,
		"lasting":        `{"control_origin":"https://a.example","identities_file":"/i","agents":1,"faults":[{"kind":"partition","at_seconds":1,"percent":5}]}`,
		"instant lasts":  `{"control_origin":"https://a.example","identities_file":"/i","agents":1,"faults":[{"kind":"restart","at_seconds":1,"percent":5,"duration_seconds":3}]}`,
		"long task":      `{"control_origin":"https://a.example","identities_file":"/i","agents":1,"long_task_seconds":111}`,
		"negative start": `{"control_origin":"https://a.example","identities_file":"/i","agents":1,"start_seconds":-1}`,
	} {
		if _, e := Load(write(t, content)); e == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

func TestRunNeedsFaultsInsideTheDuration(t *testing.T) {
	c, e := Load(write(t, `{"control_origin":"https://a.example","identities_file":"/i","agents":1,"duration_seconds":60,"report_file":"/r.json",
		"faults":[{"kind":"partition","at_seconds":30,"percent":50,"duration_seconds":30}]}`))
	if e != nil {
		t.Fatal(e)
	}
	if c.ValidateRun() == nil {
		t.Fatal("a partition that heals as the run ends was accepted; its recovery could never be measured")
	}
	c.Faults[0].DurationSeconds = 20
	if e := c.ValidateRun(); e != nil {
		t.Fatal(e)
	}
}
