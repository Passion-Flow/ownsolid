package config

import (
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/liveness"
)

const livenessReplicaMaximumServers = 16

var livenessBucketPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)

// LivenessReplicaConfiguration points gateways at the store they share sightings through
// (ADR 0022). Without it, one gateway keeps liveness alone, which is complete for the single
// node topology.
type LivenessReplicaConfiguration struct {
	// Servers are tls:// URLs of the NATS servers, and ServerName the name their
	// certificates carry.
	Servers    []string `yaml:"servers"`
	ServerName string   `yaml:"server_name"`
	// CAFile is the certificate the servers chain to. NKeySeedFile is a private file with
	// the gateway's NKey seed.
	CAFile       string `yaml:"ca_file"`
	NKeySeedFile string `yaml:"nkey_seed_file"`
	Bucket       string `yaml:"bucket"`
	// Replicas is how many servers keep the bucket: 1, 3 or 5.
	Replicas int `yaml:"replicas"`
}

// Validate accepts an absent section and refuses an incomplete one.
func (c *LivenessReplicaConfiguration) Validate() error {
	if c == nil {
		return nil
	}
	if len(c.Servers) == 0 || len(c.Servers) > livenessReplicaMaximumServers {
		return errors.New("liveness_replica.servers must list 1 to 16 servers")
	}
	for _, server := range c.Servers {
		parsed, e := url.Parse(server)
		if e != nil || parsed.Scheme != "tls" || parsed.Hostname() == "" || parsed.Port() == "" || parsed.User != nil || (parsed.Path != "" && parsed.Path != "/") {
			return errors.New("liveness_replica.servers must be tls://host:port URLs")
		}
	}
	if c.ServerName == "" || strings.ContainsAny(c.ServerName, "/: \t\r\n") {
		return errors.New("liveness_replica.server_name must be a certificate name")
	}
	if !filepath.IsAbs(c.CAFile) || !filepath.IsAbs(c.NKeySeedFile) {
		return errors.New("liveness_replica.ca_file and nkey_seed_file must be absolute paths")
	}
	if !livenessBucketPattern.MatchString(c.Bucket) {
		return errors.New("liveness_replica.bucket must be lowercase letters, digits and hyphens")
	}
	if !slices.Contains([]int{1, 3, 5}, c.Replicas) {
		return errors.New("liveness_replica.replicas must be 1, 3 or 5")
	}
	return nil
}

// Settings reads the referenced files for liveness.NewJetStream. A record lives as long as
// two offline windows, so that a node's record outlasts the time it takes to call it offline.
func (c LivenessReplicaConfiguration) Settings(offline time.Duration) (liveness.JetStreamSettings, error) {
	ca, e := os.ReadFile(c.CAFile)
	if e != nil {
		return liveness.JetStreamSettings{}, errors.New("LIVENESS_REPLICA_CA_INVALID")
	}
	seed, e := Secret(c.NKeySeedFile)
	if e != nil {
		return liveness.JetStreamSettings{}, errors.New("LIVENESS_REPLICA_NKEY_INVALID")
	}
	return liveness.JetStreamSettings{Servers: c.Servers, ServerName: c.ServerName, CA: ca, Seed: []byte(strings.TrimSpace(string(seed))), Bucket: c.Bucket, Replicas: c.Replicas, TTL: 2 * offline}, nil
}
