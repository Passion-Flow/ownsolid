package taskbus

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

const MaximumTransportBytes = MaximumEnvelopeBytes + 1024
const ClientCandidateVersion = "v1.54.0"
const ServerCandidateVersion = "v2.15.1"
const serverAPILevel = "5"
const requiredProfileAPILevel = "0"

// NATS2.15.1 server/jetstream_versioning.go adds these exact static/dynamic
// response metadata fields. Provisioning requests retain application fields only.
func responseMetadata(c Configuration, tenant string) map[string]string {
	metadata := profileMetadata(c, tenant)
	metadata["_nats.req.level"] = requiredProfileAPILevel
	metadata["_nats.level"] = serverAPILevel
	metadata["_nats.ver"] = strings.TrimPrefix(ServerCandidateVersion, "v")
	return metadata
}

func profileMetadata(c Configuration, tenant string) map[string]string {
	return map[string]string{"projectone_namespace": c.NamespaceID, "projectone_tenant": tenant, "projectone_schema": "1"}
}

// ExpectedStreamConfiguration is an operator provisioning contract, not an
// automatic create/update path. Runtime admits an already provisioned exact match.
func ExpectedStreamConfiguration(c Configuration, tenant string) (jetstream.StreamConfig, error) {
	if c.Validate() != nil || !c.Enabled || !ValidID(tenant) {
		return jetstream.StreamConfig{}, configurationError()
	}
	subject, _ := FilterSubject(c.NamespaceID, tenant)
	return jetstream.StreamConfig{Name: c.Stream, Subjects: []string{subject}, Retention: jetstream.LimitsPolicy,
		MaxConsumers: 1, MaxMsgs: c.StreamMaximumMessages, MaxBytes: c.StreamMaximumBytes, Discard: jetstream.DiscardNew,
		MaxAge: time.Duration(c.StreamRetentionSeconds) * time.Second, MaxMsgsPerSubject: -1, MaxMsgSize: MaximumTransportBytes,
		Storage: jetstream.FileStorage, Replicas: 1, Duplicates: time.Duration(c.DuplicateWindowSeconds) * time.Second,
		DenyDelete: true, DenyPurge: true, Metadata: profileMetadata(c, tenant)}, nil
}
func ExpectedConsumerConfiguration(c Configuration, tenant string) (jetstream.ConsumerConfig, error) {
	if c.Validate() != nil || !c.Enabled || !ValidID(tenant) {
		return jetstream.ConsumerConfig{}, configurationError()
	}
	subject, _ := FilterSubject(c.NamespaceID, tenant)
	// One message per pull and the finite stream message envelope jointly bound
	// bytes; zero MaxRequestMaxBytes does not permit an unbounded message/batch.
	return jetstream.ConsumerConfig{Name: c.Consumer, Durable: c.Consumer, DeliverPolicy: jetstream.DeliverAllPolicy,
		AckPolicy: jetstream.AckExplicitPolicy, AckWait: time.Duration(c.AckWaitSeconds) * time.Second, MaxDeliver: NativeMaximumDeliveries,
		FilterSubject: subject, ReplayPolicy: jetstream.ReplayInstantPolicy, MaxWaiting: 1, MaxAckPending: c.MaximumAckPending,
		MaxRequestBatch: 1, MaxRequestExpires: time.Duration(c.PullSeconds) * time.Second, Replicas: 1, Metadata: profileMetadata(c, tenant)}, nil
}

func admittedProfile(c Configuration, tenant string, stream *jetstream.StreamInfo, consumer *jetstream.ConsumerInfo) (string, error) {
	expectedStream, e := ExpectedStreamConfiguration(c, tenant)
	if e != nil {
		return "", e
	}
	expectedConsumer, e := ExpectedConsumerConfiguration(c, tenant)
	if e != nil {
		return "", e
	}
	expectedStream.Metadata = responseMetadata(c, tenant)
	expectedConsumer.Metadata = responseMetadata(c, tenant)
	if stream == nil || consumer == nil || stream.Created.IsZero() || consumer.Created.IsZero() || consumer.Stream != c.Stream || consumer.Name != c.Consumer || !reflect.DeepEqual(stream.Config, expectedStream) || !reflect.DeepEqual(consumer.Config, expectedConsumer) || consumer.Paused {
		return "", errors.New("TASK_BUS_PROFILE_REJECTED")
	}
	// Exact creation identity prevents adopting a recreated stream/consumer after
	// reconnect. SQL retains this identity with publication and receipt evidence.
	raw, _ := json.Marshal(struct{ Namespace, Tenant, Stream, Consumer, StreamCreated, ConsumerCreated string }{c.NamespaceID, tenant, c.Stream, c.Consumer, stream.Created.UTC().Format(time.RFC3339Nano), consumer.Created.UTC().Format(time.RFC3339Nano)})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}
