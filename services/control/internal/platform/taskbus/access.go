package taskbus

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// VerifyPeerIsolation is an explicitly invoked private qualification operation,
// never a human API or a task owner. Both supplied canaries must already exist
// and contain synthetic fixture events. An unexpected allowed probe may append
// the exact existing canary again; callers must retain failed fixture evidence.
// It does not create a stream, broaden permissions or claim a physical task.
func (b *JetStream) VerifyPeerIsolation(ctx context.Context, own Envelope, peer *JetStream, other Envelope) error {
	if b == nil || peer == nil || b == peer || ctx == nil {
		return errors.New("TASK_BUS_ACCESS_PROBE_INVALID")
	}
	if _, bounded := ctx.Deadline(); !bounded {
		return errors.New("TASK_BUS_ACCESS_PROBE_INVALID")
	}
	left, right := b.configuration, peer.configuration
	if left.Endpoint != right.Endpoint || strings.Join(left.DialAddresses, ",") != strings.Join(right.DialAddresses, ",") || left.CAFile != right.CAFile || left.ClientCertificateFile != right.ClientCertificateFile || left.ClientKeyFile != right.ClientKeyFile || left.NamespaceID == right.NamespaceID || b.tenant == peer.tenant || left.Stream == right.Stream || left.Consumer == right.Consumer {
		return errors.New("TASK_BUS_ACCESS_PROBE_INVALID")
	}
	keys := map[string]bool{}
	for _, key := range []string{b.publisherKey, b.consumerKey, peer.publisherKey, peer.consumerKey} {
		if key == "" || keys[key] {
			return errors.New("TASK_BUS_ACCESS_PROBE_INVALID")
		}
		keys[key] = true
	}
	for _, v := range []struct {
		bus   *JetStream
		event Envelope
	}{{b, own}, {peer, other}} {
		if _, found, err := v.bus.Lookup(ctx, v.event); err != nil || !found {
			return errors.New("TASK_BUS_ACCESS_CANARY_UNCONFIRMED")
		}
	}
	for _, v := range []struct {
		bus, otherBus *JetStream
		own, other    Envelope
	}{{b, peer, own, other}, {peer, b, other, own}} {
		ownSubject, err := v.own.Subject()
		if err != nil {
			return errors.New("TASK_BUS_ACCESS_PROBE_INVALID")
		}
		foreignSubject, err := v.other.Subject()
		if err != nil {
			return errors.New("TASK_BUS_ACCESS_PROBE_INVALID")
		}
		ownRaw, _ := v.own.Canonical()
		foreignRaw, _ := v.other.Canonical()
		probes := []accessProbe{
			{role: "publisher", subject: foreignSubject, body: foreignRaw, delivery: v.other.DeliveryID, stream: v.otherBus.configuration.Stream},
			{role: "publisher", subject: "$JS.API.STREAM.INFO." + v.otherBus.configuration.Stream, body: []byte(`{}`)},
			{role: "publisher", subject: "$JS.API.STREAM.MSG.GET." + v.otherBus.configuration.Stream, body: []byte(`{"last_by_subj":` + strconv.Quote(foreignSubject) + `}`)},
			{role: "consumer", subject: ownSubject, body: ownRaw, delivery: v.own.DeliveryID, stream: v.bus.configuration.Stream},
			{role: "publisher", subject: "$JS.API.CONSUMER.MSG.NEXT." + v.bus.configuration.Stream + "." + v.bus.configuration.Consumer, body: []byte(`{"batch":1,"no_wait":true}`)},
			{role: "consumer", subject: "projectone.reply.v1." + v.bus.configuration.NamespaceID + ".publisher.probe", subscribe: true},
			{role: "publisher", subject: "projectone.reply.v1." + v.bus.configuration.NamespaceID + ".consumer.probe", subscribe: true},
		}
		for _, probe := range probes {
			if err = v.bus.deniedProbe(ctx, probe); err != nil {
				return err
			}
		}
	}
	// Positive readback after negatives rules out silently changing or losing the
	// actual peer resource as a substitute for explicit permission denial.
	for _, v := range []struct {
		bus   *JetStream
		event Envelope
	}{{b, own}, {peer, other}} {
		if _, found, err := v.bus.Lookup(ctx, v.event); err != nil || !found {
			return errors.New("TASK_BUS_ACCESS_CANARY_UNCONFIRMED")
		}
	}
	return nil
}

type accessProbe struct {
	role, subject, delivery, stream string
	body                            []byte
	subscribe                       bool
}

func (b *JetStream) deniedProbe(ctx context.Context, p accessProbe) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed || b.publisher == nil || b.subscriber == nil {
		return errors.New("TASK_BUS_CLOSED")
	}
	c := b.configuration
	bounded, cancel := context.WithTimeout(ctx, time.Duration(c.ConnectSeconds+c.RequestSeconds)*time.Second)
	defer cancel()
	tlsConfig, err := tlsConfiguration(c)
	if err != nil {
		return err
	}
	expectedKey := b.publisherKey
	path := c.PublisherSeedFile
	if p.role == "consumer" {
		path = c.ConsumerSeedFile
		expectedKey = b.consumerKey
	}
	key, err := userPublicKey(path)
	if err != nil {
		return err
	}
	if key != expectedKey {
		return errors.New("TASK_BUS_IDENTITY_CHANGED")
	}
	connection, err := connectPrivate(bounded, c, p.role, tlsConfig, nats.Nkey(key, signUserNonce(path, key)))
	if err != nil {
		return safeTransport(err)
	}
	defer connection.Close()
	denied := make(chan struct{}, 1)
	operation := "Publish"
	if p.subscribe {
		operation = "Subscription"
	}
	expected := "Permissions Violation for " + operation + " to " + strconv.Quote(p.subject)
	connection.SetErrorHandler(func(_ *nats.Conn, _ *nats.Subscription, e error) {
		if errors.Is(e, nats.ErrPermissionViolation) && strings.HasSuffix(e.Error(), expected) {
			select {
			case denied <- struct{}{}:
			default:
			}
		}
	})
	if p.subscribe {
		sub, e := connection.SubscribeSync(p.subject)
		if sub != nil {
			defer sub.Unsubscribe()
		}
		if e != nil && !errors.Is(e, nats.ErrPermissionViolation) {
			return errors.New("TASK_BUS_ACCESS_UNCONFIRMED")
		}
	} else {
		message := &nats.Msg{Subject: p.subject, Reply: "projectone.reply.v1." + c.NamespaceID + "." + p.role + ".accessprobe", Data: p.body, Header: nats.Header{}}
		if p.delivery != "" {
			message.Header.Set(jetstream.MsgIDHeader, p.delivery)
			message.Header.Set(jetstream.ExpectedStreamHeader, p.stream)
		}
		if connection.PublishMsg(message) != nil {
			return errors.New("TASK_BUS_ACCESS_UNCONFIRMED")
		}
	}
	// A timeout, no responders, permission to another subject, or a missing
	// stream is never accepted. Only the correlated actual server denial counts.
	if connection.FlushWithContext(bounded) != nil {
		return errors.New("TASK_BUS_ACCESS_UNCONFIRMED")
	}
	select {
	case <-bounded.Done():
		return errors.New("TASK_BUS_ACCESS_UNCONFIRMED")
	case <-denied:
		return nil
	}
}
