//go:build liveness_integration

package liveness

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nkeys"
)

// The image of the servers this test runs. OWNSOLID_TEST_NATS_IMAGE overrides it.
const natsImage = "nats:2.12.1-alpine"

type natsCluster struct {
	t       *testing.T
	suffix  string
	servers []string
	ca      []byte
	seed    []byte
}

func docker(t *testing.T, args ...string) string {
	t.Helper()
	out, e := exec.Command("docker", args...).CombinedOutput()
	if e != nil {
		t.Fatalf("docker %s: %v\n%s", strings.Join(args, " "), e, out)
	}
	return string(out)
}

func freePort(t *testing.T) int {
	t.Helper()
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port
}

// startCluster runs three servers with TLS, with one account whose only user is the gateway's
// NKey, limited to the subjects the replica needs. It removes only what it created.
func startCluster(t *testing.T, bucket string) *natsCluster {
	t.Helper()
	image := os.Getenv("OWNSOLID_TEST_NATS_IMAGE")
	if image == "" {
		image = natsImage
	}
	directory, e := os.MkdirTemp("", "liveness-nats-")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { os.RemoveAll(directory) })
	if e = os.Chmod(directory, 0o755); e != nil {
		t.Fatal(e)
	}
	suffix := uuid.NewString()[:8]
	var containers []string
	for i := 1; i <= 3; i++ {
		containers = append(containers, fmt.Sprintf("nats-%s-%d", suffix, i))
	}
	ca := serverCertificates(t, directory, containers...)
	user, e := nkeys.CreateUser()
	if e != nil {
		t.Fatal(e)
	}
	seed, e := user.Seed()
	if e != nil {
		t.Fatal(e)
	}
	public, e := user.PublicKey()
	if e != nil {
		t.Fatal(e)
	}
	network := "ownsolid-liveness-" + suffix
	docker(t, "network", "create", network)
	t.Cleanup(func() { exec.Command("docker", "network", "rm", network).Run() })
	cluster := &natsCluster{t: t, suffix: suffix, ca: ca, seed: seed}
	var routes []string
	for i := 1; i <= 3; i++ {
		routes = append(routes, fmt.Sprintf("nats-route://nats-%s-%d:6222", suffix, i))
	}
	for i := 1; i <= 3; i++ {
		config := fmt.Sprintf(`server_name: s%d
listen: 0.0.0.0:4222
jetstream { store_dir: /tmp/js, max_mem: 64M, max_file: 64M }
tls { cert_file: /etc/nats-test/server.pem, key_file: /etc/nats-test/server.key, ca_file: /etc/nats-test/ca.pem }
cluster { name: liveness, listen: 0.0.0.0:6222, no_advertise: true, routes: [%s], tls { cert_file: /etc/nats-test/server.pem, key_file: /etc/nats-test/server.key, ca_file: /etc/nats-test/ca.pem, timeout: 5 } }
accounts {
  GATEWAYS {
    jetstream: enabled
    users: [{ nkey: %s, permissions: { publish: { allow: ["$KV.%s.>", "$JS.API.>", "$JS.ACK.>", "_INBOX.>"] }, subscribe: { allow: ["$KV.%s.>", "_INBOX.>"] } } }]
  }
}
`, i, strings.Join(routes, ","), public, bucket, bucket)
		name := fmt.Sprintf("server%d.conf", i)
		if e = os.WriteFile(filepath.Join(directory, name), []byte(config), 0o644); e != nil {
			t.Fatal(e)
		}
		port := freePort(t)
		container := fmt.Sprintf("nats-%s-%d", suffix, i)
		docker(t, "run", "-d", "--name", container, "--network", network, "--network-alias", container, "-p", fmt.Sprintf("127.0.0.1:%d:4222", port), "-v", directory+":/etc/nats-test:ro", image, "-c", "/etc/nats-test/"+name)
		t.Cleanup(func() {
			if t.Failed() {
				logs, _ := exec.Command("docker", "logs", "--tail", "12", container).CombinedOutput()
				t.Logf("%s:\n%s", container, logs)
			}
			exec.Command("docker", "rm", "-f", "-v", container).Run()
		})
		cluster.servers = append(cluster.servers, fmt.Sprintf("tls://127.0.0.1:%d", port))
	}
	return cluster
}

func (c *natsCluster) settings(bucket string) JetStreamSettings {
	return JetStreamSettings{Servers: c.servers, ServerName: "liveness.internal", CA: c.ca, Seed: c.seed, Bucket: bucket, Replicas: 3, TTL: time.Minute}
}

func (c *natsCluster) replica(t *testing.T, bucket string) *JetStreamReplica {
	t.Helper()
	r, e := NewJetStream(c.settings(bucket))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(r.Close)
	return r
}

// eventually retries while a cluster forms or replaces a leader.
func eventually(t *testing.T, what string, fn func() error) {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	var last error
	for time.Now().Before(deadline) {
		if last = fn(); last == nil {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("%s: %v", what, last)
}

// sink collects the largest time seen per node, as a table does.
type sink struct {
	mu   sync.Mutex
	seen map[string]time.Time
}

func (s *sink) take(v Seen) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if v.At.After(s.seen[v.Node]) {
		s.seen[v.Node] = v.At
	}
}

func (s *sink) has(node string, at time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.seen[node].Equal(at)
}

func watching(t *testing.T, r *JetStreamReplica) *sink {
	t.Helper()
	out := &sink{seen: map[string]time.Time{}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		for ctx.Err() == nil {
			r.Watch(ctx, out.take)
			time.Sleep(200 * time.Millisecond)
		}
	}()
	t.Cleanup(func() { cancel(); <-done })
	return out
}

func publish(ctx context.Context, r *JetStreamReplica, seen ...Seen) error {
	bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return r.Publish(bounded, seen)
}

func TestJetStreamReplicaAcrossThreeServers(t *testing.T) {
	const bucket = "ownsolid-liveness"
	cluster := startCluster(t, bucket)
	ctx := context.Background()
	a, b := cluster.replica(t, bucket), cluster.replica(t, bucket)
	first := time.Now().UTC().Truncate(time.Microsecond)
	nodes := []Seen{{uuid.NewString(), first}, {uuid.NewString(), first.Add(time.Second)}, {uuid.NewString(), first.Add(2 * time.Second)}}
	eventually(t, "first publish", func() error { return publish(ctx, a, nodes...) })

	// A gateway started later reads what is already stored, then changes.
	observer := watching(t, b)
	eventually(t, "stored sightings", func() error {
		for _, v := range nodes {
			if !observer.has(v.Node, v.At) {
				return fmt.Errorf("%s not seen", v.Node)
			}
		}
		return nil
	})
	newer := Seen{nodes[0].Node, first.Add(time.Minute)}
	eventually(t, "newer publish", func() error { return publish(ctx, a, newer) })
	eventually(t, "newer sighting", func() error {
		if !observer.has(newer.Node, newer.At) {
			return fmt.Errorf("not seen")
		}
		return nil
	})

	// A full batch goes through one pipeline, far faster than one round trip per sighting.
	large := make([]Seen, 1000)
	for i := range large {
		large[i] = Seen{uuid.NewString(), first}
	}
	started := time.Now()
	eventually(t, "batch publish", func() error {
		bounded, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		return a.Publish(bounded, large)
	})
	t.Logf("published %d sightings in %v", len(large), time.Since(started))

	// Losing one server of three loses neither the data nor the service.
	docker(t, "stop", "-t", "1", fmt.Sprintf("nats-%s-1", cluster.suffix))
	after := Seen{nodes[1].Node, first.Add(2 * time.Minute)}
	eventually(t, "publish with a server down", func() error { return publish(ctx, a, after) })
	eventually(t, "sighting with a server down", func() error {
		if !observer.has(after.Node, after.At) {
			return fmt.Errorf("not seen")
		}
		return nil
	})
	late := watching(t, cluster.replica(t, bucket))
	eventually(t, "stored data with a server down", func() error {
		if !late.has(nodes[2].Node, nodes[2].At) || !late.has(after.Node, after.At) {
			return fmt.Errorf("not replicated")
		}
		return nil
	})
}

func TestJetStreamReplicaRefusesWhatItCannotTrust(t *testing.T) {
	const bucket = "ownsolid-liveness"
	cluster := startCluster(t, bucket)
	good := cluster.replica(t, bucket)
	eventually(t, "cluster up", func() error { return publish(context.Background(), good, Seen{uuid.NewString(), time.Now().UTC()}) })

	stranger, e := nkeys.CreateUser()
	if e != nil {
		t.Fatal(e)
	}
	seed, e := stranger.Seed()
	if e != nil {
		t.Fatal(e)
	}
	settings := cluster.settings(bucket)
	settings.Seed = seed
	unknown, e := NewJetStream(settings)
	if e != nil {
		t.Fatal(e)
	}
	defer unknown.Close()
	if e = publish(context.Background(), unknown, Seen{uuid.NewString(), time.Now().UTC()}); e == nil {
		t.Fatal("a stranger's key could publish")
	}

	otherDirectory := t.TempDir()
	settings = cluster.settings(bucket)
	settings.CA = serverCertificates(t, otherDirectory)
	untrusted, e := NewJetStream(settings)
	if e != nil {
		t.Fatal(e)
	}
	defer untrusted.Close()
	if e = publish(context.Background(), untrusted, Seen{uuid.NewString(), time.Now().UTC()}); e == nil {
		t.Fatal("a server outside the CA was trusted")
	}
}
