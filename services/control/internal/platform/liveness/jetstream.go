package liveness

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/nats-io/nkeys"
)

// jetStreamMaxPending bounds what one gateway publishes without an acknowledgement.
const jetStreamMaxPending = 4096

// JetStreamSettings connects a replica to a NATS JetStream cluster. TLS and an NKey are
// always required: gateways accept what the bucket says about any node.
type JetStreamSettings struct {
	Servers    []string
	ServerName string
	// CA is the PEM certificate the servers chain to, and Seed the gateway's NKey seed.
	CA, Seed []byte
	Bucket   string
	// Replicas is the number of servers that keep the bucket.
	Replicas int
	// TTL drops the record of a node nothing has heard for this long.
	TTL time.Duration
}

// JetStreamReplica keeps sightings in a NATS JetStream key-value bucket held in memory. Losing
// the whole bucket loses nothing durable: the database and the next heartbeats restore it.
type JetStreamReplica struct {
	settings JetStreamSettings
	conn     *nats.Conn
	stream   jetstream.JetStream

	mu     sync.Mutex
	bucket jetstream.KeyValue
}

// NewJetStream checks the settings and starts connecting in the background. A cluster that is
// down does not stop the gateway: Publish and Watch report it until it returns.
func NewJetStream(settings JetStreamSettings) (*JetStreamReplica, error) {
	if len(settings.Servers) == 0 || settings.ServerName == "" || settings.Bucket == "" || settings.Replicas < 1 || settings.TTL <= 0 {
		return nil, errors.New("LIVENESS_REPLICA_CONFIG_INVALID")
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(settings.CA) {
		return nil, errors.New("LIVENESS_REPLICA_CA_INVALID")
	}
	identity, e := nkeys.FromSeed(settings.Seed)
	if e != nil {
		return nil, errors.New("LIVENESS_REPLICA_NKEY_INVALID")
	}
	public, e := identity.PublicKey()
	if e != nil {
		return nil, errors.New("LIVENESS_REPLICA_NKEY_INVALID")
	}
	conn, e := nats.Connect(strings.Join(settings.Servers, ","),
		nats.Name("ownsolid-control-liveness"),
		nats.Secure(&tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots, ServerName: settings.ServerName}),
		nats.Nkey(public, identity.Sign),
		nats.Timeout(5*time.Second),
		nats.RetryOnFailedConnect(true),
		nats.MaxReconnects(-1),
		nats.ReconnectWait(2*time.Second),
		nats.ReconnectJitter(time.Second, time.Second),
	)
	if e != nil {
		return nil, errors.New("LIVENESS_REPLICA_UNAVAILABLE")
	}
	stream, e := jetstream.New(conn, jetstream.WithPublishAsyncMaxPending(jetStreamMaxPending))
	if e != nil {
		conn.Close()
		return nil, errors.New("LIVENESS_REPLICA_UNAVAILABLE")
	}
	return &JetStreamReplica{settings: settings, conn: conn, stream: stream}, nil
}

// Close ends the connection.
func (r *JetStreamReplica) Close() { r.conn.Close() }

// keyValue opens the bucket, creating it on first use so that any gateway can start first. It
// holds no lock while it waits for the servers, so that one caller's deadline holds for all;
// creating the same bucket twice changes nothing.
func (r *JetStreamReplica) keyValue(ctx context.Context) (jetstream.KeyValue, error) {
	r.mu.Lock()
	cached := r.bucket
	r.mu.Unlock()
	if cached != nil {
		return cached, nil
	}
	bucket, e := r.stream.CreateOrUpdateKeyValue(ctx, jetstream.KeyValueConfig{
		Bucket:   r.settings.Bucket,
		History:  1,
		TTL:      r.settings.TTL,
		Storage:  jetstream.MemoryStorage,
		Replicas: r.settings.Replicas,
	})
	if e != nil {
		return nil, errors.New("LIVENESS_REPLICA_UNAVAILABLE")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.bucket = bucket
	return bucket, nil
}

// Publish implements Replica. It waits for the servers to acknowledge every sighting, in one
// pipeline: a round trip for each would limit a gateway to far less than its nodes send.
func (r *JetStreamReplica) Publish(ctx context.Context, seen []Seen) error {
	if _, e := r.keyValue(ctx); e != nil {
		return e
	}
	acknowledgements := make([]jetstream.PubAckFuture, 0, len(seen))
	for _, v := range seen {
		var value [8]byte
		binary.BigEndian.PutUint64(value[:], uint64(v.At.UnixMicro()))
		acknowledgement, e := r.stream.PublishAsync("$KV."+r.settings.Bucket+"."+v.Node, value[:])
		if e != nil {
			return errors.New("LIVENESS_REPLICA_UNAVAILABLE")
		}
		acknowledgements = append(acknowledgements, acknowledgement)
	}
	for _, acknowledgement := range acknowledgements {
		select {
		case <-acknowledgement.Ok():
		case <-acknowledgement.Err():
			return errors.New("LIVENESS_REPLICA_UNAVAILABLE")
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

// Watch implements Replica.
func (r *JetStreamReplica) Watch(ctx context.Context, fn func(Seen)) error {
	bucket, e := r.keyValue(ctx)
	if e != nil {
		return e
	}
	watcher, e := bucket.WatchAll(ctx)
	if e != nil {
		return errors.New("LIVENESS_REPLICA_UNAVAILABLE")
	}
	defer watcher.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case entry, open := <-watcher.Updates():
			if !open {
				return errors.New("LIVENESS_REPLICA_UNAVAILABLE")
			}
			// A nil entry marks the end of what was already stored.
			if entry == nil || entry.Operation() != jetstream.KeyValuePut || len(entry.Value()) != 8 {
				continue
			}
			fn(Seen{Node: entry.Key(), At: time.UnixMicro(int64(binary.BigEndian.Uint64(entry.Value()))).UTC()})
		}
	}
}
