package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func validLivenessReplica() LivenessReplicaConfiguration {
	return LivenessReplicaConfiguration{Servers: []string{"tls://10.0.0.1:4222", "tls://10.0.0.2:4222"}, ServerName: "liveness.internal", CAFile: "/run/ca.pem", NKeySeedFile: "/run/seed", Bucket: "ownsolid-liveness", Replicas: 3}
}

func TestLivenessReplicaValidation(t *testing.T) {
	var absent *LivenessReplicaConfiguration
	if e := absent.Validate(); e != nil {
		t.Fatal(e)
	}
	valid := validLivenessReplica()
	if e := valid.Validate(); e != nil {
		t.Fatal(e)
	}
	for name, change := range map[string]func(*LivenessReplicaConfiguration){
		"no servers":   func(c *LivenessReplicaConfiguration) { c.Servers = nil },
		"plain text":   func(c *LivenessReplicaConfiguration) { c.Servers = []string{"nats://10.0.0.1:4222"} },
		"no port":      func(c *LivenessReplicaConfiguration) { c.Servers = []string{"tls://10.0.0.1"} },
		"credentials":  func(c *LivenessReplicaConfiguration) { c.Servers = []string{"tls://a:b@10.0.0.1:4222"} },
		"no name":      func(c *LivenessReplicaConfiguration) { c.ServerName = "" },
		"bad name":     func(c *LivenessReplicaConfiguration) { c.ServerName = "tls://x" },
		"relative ca":  func(c *LivenessReplicaConfiguration) { c.CAFile = "ca.pem" },
		"relative key": func(c *LivenessReplicaConfiguration) { c.NKeySeedFile = "seed" },
		"bucket":       func(c *LivenessReplicaConfiguration) { c.Bucket = "Bad.Bucket" },
		"two replicas": func(c *LivenessReplicaConfiguration) { c.Replicas = 2 },
	} {
		c := validLivenessReplica()
		change(&c)
		if e := c.Validate(); e == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestLivenessReplicaSettingsReadTheFiles(t *testing.T) {
	directory := t.TempDir()
	c := validLivenessReplica()
	c.CAFile, c.NKeySeedFile = filepath.Join(directory, "ca.pem"), filepath.Join(directory, "seed")
	if _, e := c.Settings(time.Minute); e == nil || e.Error() != "LIVENESS_REPLICA_CA_INVALID" {
		t.Fatal(e)
	}
	if e := os.WriteFile(c.CAFile, []byte("certificate"), 0o644); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(c.NKeySeedFile, []byte("seed\n"), 0o600); e != nil {
		t.Fatal(e)
	}
	if e := os.Chmod(c.NKeySeedFile, 0o644); e != nil {
		t.Fatal(e)
	}
	if _, e := c.Settings(time.Minute); e == nil || e.Error() != "LIVENESS_REPLICA_NKEY_INVALID" {
		t.Fatalf("a seed others can read was accepted: %v", e)
	}
	if e := os.Chmod(c.NKeySeedFile, 0o600); e != nil {
		t.Fatal(e)
	}
	settings, e := c.Settings(time.Minute)
	if e != nil || string(settings.Seed) != "seed" || settings.TTL != 2*time.Minute || strings.Join(settings.Servers, ",") != strings.Join(c.Servers, ",") {
		t.Fatal(settings, e)
	}
}
