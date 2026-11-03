package taskbus

import (
	"context"
	"maps"
	"reflect"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

func TestTaskBusExactProvisioningProfileRejectsPolicyAndCreationSubstitution(t *testing.T) {
	c := syntheticConfiguration()
	tenant := "22222222-2222-4222-8222-222222222222"
	streamConfig, err := ExpectedStreamConfiguration(c, tenant)
	if err != nil {
		t.Fatal(err)
	}
	consumerConfig, err := ExpectedConsumerConfiguration(c, tenant)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 22, 0, 0, 1, 123456789, time.UTC)
	stream := jetstream.StreamInfo{Created: now, Config: streamConfig}
	consumer := jetstream.ConsumerInfo{Stream: c.Stream, Name: c.Consumer, Created: now.Add(time.Second), Config: consumerConfig}
	stream.Config.Metadata = responseMetadata(c, tenant)
	consumer.Config.Metadata = responseMetadata(c, tenant)
	identity, err := admittedProfile(c, tenant, &stream, &consumer)
	if err != nil || len(identity) != 64 {
		t.Fatal("exact synthetic profile refused", err)
	}
	if streamConfig.Storage != jetstream.FileStorage || streamConfig.Retention != jetstream.LimitsPolicy || streamConfig.Discard != jetstream.DiscardNew || streamConfig.MaxMsgSize != MaximumTransportBytes || streamConfig.Replicas != 1 || !streamConfig.DenyDelete || !streamConfig.DenyPurge || consumerConfig.MaxDeliver != -1 || consumerConfig.AckPolicy != jetstream.AckExplicitPolicy || consumerConfig.MaxRequestBatch != 1 || consumerConfig.MaxAckPending != c.MaximumAckPending {
		t.Fatal("frozen bounded delivery profile changed")
	}
	for name, mutate := range map[string]func(*jetstream.StreamInfo, *jetstream.ConsumerInfo){
		"wrong_server_version":          func(s *jetstream.StreamInfo, _ *jetstream.ConsumerInfo) { s.Config.Metadata["_nats.ver"] = "2.15.0" },
		"wrong_server_api_level":        func(_ *jetstream.StreamInfo, c *jetstream.ConsumerInfo) { c.Config.Metadata["_nats.level"] = "4" },
		"unexpected_required_api_level": func(s *jetstream.StreamInfo, _ *jetstream.ConsumerInfo) { s.Config.Metadata["_nats.req.level"] = "1" },
		"missing_server_metadata":       func(s *jetstream.StreamInfo, _ *jetstream.ConsumerInfo) { delete(s.Config.Metadata, "_nats.ver") },
		"unknown_metadata":              func(_ *jetstream.StreamInfo, c *jetstream.ConsumerInfo) { c.Config.Metadata["_nats.unknown"] = "0" },
		"foreign_subject":               func(s *jetstream.StreamInfo, _ *jetstream.ConsumerInfo) { s.Config.Subjects = []string{"foreign.>"} },
		"foreign_metadata": func(s *jetstream.StreamInfo, _ *jetstream.ConsumerInfo) {
			s.Config.Metadata = map[string]string{"projectone_namespace": "foreign"}
		},
		"unbounded_bytes":   func(s *jetstream.StreamInfo, _ *jetstream.ConsumerInfo) { s.Config.MaxBytes = -1 },
		"memory_storage":    func(s *jetstream.StreamInfo, _ *jetstream.ConsumerInfo) { s.Config.Storage = jetstream.MemoryStorage },
		"silent_discard":    func(s *jetstream.StreamInfo, _ *jetstream.ConsumerInfo) { s.Config.Discard = jetstream.DiscardOld },
		"no_publish_ack":    func(s *jetstream.StreamInfo, _ *jetstream.ConsumerInfo) { s.Config.NoAck = true },
		"direct_read":       func(s *jetstream.StreamInfo, _ *jetstream.ConsumerInfo) { s.Config.AllowDirect = true },
		"wrong_consumer":    func(_ *jetstream.StreamInfo, c *jetstream.ConsumerInfo) { c.Name = "foreign" },
		"no_explicit_ack":   func(_ *jetstream.StreamInfo, c *jetstream.ConsumerInfo) { c.Config.AckPolicy = jetstream.AckNonePolicy },
		"native_exhaustion": func(_ *jetstream.StreamInfo, c *jetstream.ConsumerInfo) { c.Config.MaxDeliver = 1 },
		"unbounded_pending": func(_ *jetstream.StreamInfo, c *jetstream.ConsumerInfo) { c.Config.MaxAckPending = -1 },
		"ephemeral":         func(_ *jetstream.StreamInfo, c *jetstream.ConsumerInfo) { c.Config.Durable = "" },
		"push":              func(_ *jetstream.StreamInfo, c *jetstream.ConsumerInfo) { c.Config.DeliverSubject = "foreign" },
		"paused":            func(_ *jetstream.StreamInfo, c *jetstream.ConsumerInfo) { c.Paused = true },
	} {
		t.Run(name, func(t *testing.T) {
			s, r := stream, consumer
			s.Config.Metadata = maps.Clone(stream.Config.Metadata)
			r.Config.Metadata = maps.Clone(consumer.Config.Metadata)
			mutate(&s, &r)
			if got, err := admittedProfile(c, tenant, &s, &r); err == nil || got != "" {
				t.Fatal("incompatible profile published identity")
			}
		})
	}
	replaced := stream
	replaced.Created = replaced.Created.Add(time.Nanosecond)
	changed, err := admittedProfile(c, tenant, &replaced, &consumer)
	if err != nil || changed == identity {
		t.Fatal("recreated stream lost exact creation identity", err)
	}
	changedConsumer := consumer
	changedConsumer.Created = changedConsumer.Created.Add(time.Nanosecond)
	changed, err = admittedProfile(c, tenant, &stream, &changedConsumer)
	if err != nil || changed == identity {
		t.Fatal("recreated consumer lost exact creation identity", err)
	}
	if _, err := admittedProfile(c, tenant, nil, &consumer); err == nil {
		t.Fatal("absent actual profile accepted")
	}
}

func TestTaskBusUnadmittedCallsReturnZeroAndSequenceDoesNotTruncate(t *testing.T) {
	var transport *JetStream
	publication, err := transport.Publish(context.Background(), syntheticEnvelope())
	if err == nil || publication != (Publication{}) {
		t.Fatal("unadmitted publish produced result")
	}
	publication, found, err := transport.Lookup(context.Background(), syntheticEnvelope())
	if err == nil || found || publication != (Publication{}) {
		t.Fatal("unadmitted lookup produced result")
	}
	if message, err := transport.Next(context.Background()); err == nil || message != nil {
		t.Fatal("unadmitted pull produced result")
	}
	if transport.Verify(context.Background()) == nil {
		t.Fatal("unadmitted verification succeeded")
	}
	if (new(Message)).Confirm(context.Background()) == nil {
		t.Fatal("synthetic message could ACK")
	}
	if (new(Message)).Delay(context.Background()) == nil {
		t.Fatal("synthetic message could NAK")
	}
	if SequenceText(^uint64(0)) != "18446744073709551615" {
		t.Fatal("unsigned broker sequence truncated")
	}
	c := syntheticConfiguration()
	same := c
	if !configurationEqual(c, same) {
		t.Fatal("same typed configuration changed")
	}
	same.MaximumDeliveries++
	if configurationEqual(c, same) || reflect.DeepEqual(c, same) {
		t.Fatal("adjusted policy escaped configuration correlation")
	}
}

func TestTaskBusUnconstructedRecoveryCannotAdmitOrPanic(t *testing.T) {
	for _, b := range []*JetStream{nil, {}} {
		if b.Verify(context.Background()) == nil || b.Reconnect(context.Background()) == nil {
			t.Fatal("unconstructed transport admitted readiness/recovery")
		}
		if v, e := b.Next(context.Background()); e == nil || v != nil {
			t.Fatal("unconstructed transport returned message")
		}
		if e := b.Close(); e != nil {
			t.Fatal("unconstructed close failed", e)
		}
	}
}
