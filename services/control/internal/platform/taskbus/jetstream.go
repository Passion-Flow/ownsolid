package taskbus

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/nats-io/nkeys"
)

type Publication struct {
	Identity, Stream string
	Sequence         uint64
	Duplicate        bool
}

type Message struct {
	Body                                   []byte
	Subject, Identity, Stream, Consumer    string
	Sequence, ConsumerSequence, Deliveries uint64
	RecordedAt                             time.Time
	message                                jetstream.Msg
	requestSeconds, retrySeconds           int
}

// Confirm waits for real broker acknowledgment. Only the SQL owner may invoke it
// after its receipt or dead letter and audit have confirmed commit.
func (m *Message) Confirm(ctx context.Context) error {
	if m == nil || m.message == nil || ctx == nil {
		return errors.New("TASK_BUS_MESSAGE_INVALID")
	}
	bounded, cancel := context.WithTimeout(ctx, time.Duration(m.requestSeconds)*time.Second)
	defer cancel()
	if m.message.DoubleAck(bounded) != nil {
		return errors.New("TASK_BUS_ACK_UNKNOWN")
	}
	if bounded.Err() != nil {
		return errors.New("TASK_BUS_ACK_UNKNOWN")
	}
	return nil
}
func (m *Message) Delay(ctx context.Context) error {
	if m == nil || m.message == nil || ctx == nil {
		return errors.New("TASK_BUS_MESSAGE_INVALID")
	}
	if ctx.Err() != nil {
		return safeTransport(ctx.Err())
	}
	if m.message.NakWithDelay(time.Duration(m.retrySeconds)*time.Second) != nil {
		return errors.New("TASK_BUS_ACK_UNKNOWN")
	}
	return nil
}

type JetStream struct {
	mu                        sync.Mutex
	configuration             Configuration
	tenant, identity          string
	publisherKey, consumerKey string
	publisher, subscriber     *nats.Conn
	api                       jetstream.JetStream
	stream                    jetstream.Stream
	consumer                  jetstream.Consumer
	closed                    bool
}

func safeTransport(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) {
		return errors.New("TASK_BUS_CANCELLED")
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, nats.ErrTimeout) {
		return errors.New("TASK_BUS_TIMEOUT")
	}
	return errors.New("TASK_BUS_UNAVAILABLE")
}

type pinnedDialer struct {
	ctx             context.Context
	authority, port string
	addresses       []string
	timeout         time.Duration
}

func (d pinnedDialer) Dial(network, address string) (net.Conn, error) {
	if network != "tcp" && network != "tcp4" && network != "tcp6" || address != d.authority {
		return nil, errors.New("TASK_BUS_DESTINATION_REJECTED")
	}
	ctx, cancel := context.WithTimeout(d.ctx, d.timeout)
	defer cancel()
	for _, ip := range d.addresses {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		connection, err := (&net.Dialer{Timeout: d.timeout}).DialContext(ctx, "tcp", net.JoinHostPort(ip, d.port))
		if err == nil {
			return connection, nil
		}
	}
	return nil, errors.New("TASK_BUS_UNAVAILABLE")
}
func connectPrivate(ctx context.Context, c Configuration, role string, tlsConfig *tls.Config, identity nats.Option) (*nats.Conn, error) {
	u, _ := url.Parse(c.Endpoint)
	selected := tlsConfig.Clone()
	selected.ServerName = u.Hostname()
	options := []nats.Option{nats.Secure(selected), nats.TLSHandshakeFirst(), nats.NoReconnect(), nats.IgnoreDiscoveredServers(), nats.SkipHostLookup(),
		nats.Timeout(time.Duration(c.ConnectSeconds) * time.Second), nats.FlusherTimeout(time.Duration(c.RequestSeconds) * time.Second), nats.NoCallbacksAfterClientClose(),
		nats.CustomInboxPrefix("projectone.reply.v1." + c.NamespaceID + "." + role), nats.Name("projectone-task-events-" + role),
		nats.SetCustomDialer(pinnedDialer{ctx: ctx, authority: u.Host, port: u.Port(), addresses: append([]string(nil), c.DialAddresses...), timeout: time.Duration(c.ConnectSeconds) * time.Second}),
		nats.ErrorHandler(func(*nats.Conn, *nats.Subscription, error) {})}
	if identity != nil {
		options = append(options, identity)
	}
	connection, err := nats.Connect(c.Endpoint, options...)
	if err != nil {
		return nil, err
	}
	if ctx.Err() != nil {
		connection.Close()
		return nil, ctx.Err()
	}
	return connection, nil
}

// NewJetStream connects actual distinct restricted identities. It never provisions
// broker resources or starts a background task worker. Caller owns Close on success.
func NewJetStream(ctx context.Context, c Configuration, tenant string) (*JetStream, error) {
	if ctx == nil || c.Validate() != nil || !c.Enabled || !ValidID(tenant) {
		return nil, configurationError()
	}
	c.DialAddresses = append([]string(nil), c.DialAddresses...)
	tlsConfig, err := tlsConfiguration(c)
	if err != nil {
		return nil, err
	}
	public, err := userPublicKey(c.PublisherSeedFile)
	if err != nil {
		return nil, err
	}
	receiver, err := userPublicKey(c.ConsumerSeedFile)
	if err != nil {
		return nil, err
	}
	if receiver == public {
		return nil, errors.New("TASK_BUS_IDENTITY_CONFLICT")
	}
	b := &JetStream{configuration: c, tenant: tenant, publisherKey: public, consumerKey: receiver}
	accepted := false
	defer func() {
		if !accepted {
			if b.publisher != nil {
				b.publisher.Close()
			}
			if b.subscriber != nil {
				b.subscriber.Close()
			}
		}
	}()
	b.publisher, err = connectPrivate(ctx, c, "publisher", tlsConfig, nats.Nkey(public, signUserNonce(c.PublisherSeedFile, public)))
	if err != nil {
		return nil, safeTransport(err)
	}
	b.subscriber, err = connectPrivate(ctx, c, "consumer", tlsConfig, nats.Nkey(receiver, signUserNonce(c.ConsumerSeedFile, receiver)))
	if err != nil {
		return nil, safeTransport(err)
	}
	// Use the same verified mTLS identity but no NKey, then an actual fresh
	// unregistered user NKey. Only explicit authentication rejection is proof.
	anonymous, err := connectPrivate(ctx, c, "anonymous-probe", tlsConfig, nil)
	if anonymous != nil {
		anonymous.Close()
	}
	if !errors.Is(err, nats.ErrAuthorization) {
		return nil, errors.New("TASK_BUS_AUTHENTICATION_NOT_ENFORCED")
	}
	unregistered, err := nkeys.CreateUser()
	if err != nil {
		return nil, errors.New("TASK_BUS_AUTHENTICATION_FAILED")
	}
	defer unregistered.Wipe()
	wrongPublic, err := unregistered.PublicKey()
	if err != nil || wrongPublic == public || wrongPublic == receiver {
		return nil, errors.New("TASK_BUS_AUTHENTICATION_FAILED")
	}
	wrong, err := connectPrivate(ctx, c, "unknown-key-probe", tlsConfig, nats.Nkey(wrongPublic, unregistered.Sign))
	if wrong != nil {
		wrong.Close()
	}
	if !errors.Is(err, nats.ErrAuthorization) {
		return nil, errors.New("TASK_BUS_AUTHENTICATION_NOT_ENFORCED")
	}
	b.api, err = jetstream.New(b.publisher, jetstream.WithDefaultTimeout(time.Duration(c.RequestSeconds)*time.Second))
	if err != nil {
		return nil, safeTransport(err)
	}
	subscriberAPI, err := jetstream.New(b.subscriber, jetstream.WithDefaultTimeout(time.Duration(c.RequestSeconds)*time.Second))
	if err != nil {
		return nil, safeTransport(err)
	}
	bounded, cancel := context.WithTimeout(ctx, time.Duration(c.RequestSeconds)*time.Second)
	defer cancel()
	b.stream, err = b.api.Stream(bounded, c.Stream)
	if err != nil {
		return nil, safeTransport(err)
	}
	b.consumer, err = subscriberAPI.Consumer(bounded, c.Stream, c.Consumer)
	if err != nil {
		return nil, safeTransport(err)
	}
	stream, err := b.stream.Info(bounded)
	if err != nil {
		return nil, safeTransport(err)
	}
	consumer, err := b.consumer.Info(bounded)
	if err != nil {
		return nil, safeTransport(err)
	}
	b.identity, err = admittedProfile(c, tenant, stream, consumer)
	if err != nil {
		return nil, err
	}
	if bounded.Err() != nil {
		return nil, safeTransport(bounded.Err())
	}
	accepted = true
	return b, nil
}
func (b *JetStream) Identity() string {
	if b == nil {
		return ""
	}
	return b.identity
}
func (b *JetStream) Tenant() string {
	if b == nil {
		return ""
	}
	return b.tenant
}

func (b *JetStream) MatchesConfiguration(c Configuration) bool {
	return b != nil && c.Validate() == nil && configurationEqual(b.configuration, c)
}
func configurationEqual(left, right Configuration) bool {
	// Canonical component bytes are internal correlation, never structured logs.
	a, _ := json.Marshal(left)
	z, _ := json.Marshal(right)
	return bytes.Equal(a, z)
}
func (b *JetStream) verify(ctx context.Context) error {
	if b.closed || b.publisher == nil || b.subscriber == nil || !b.publisher.IsConnected() || !b.subscriber.IsConnected() {
		return errors.New("TASK_BUS_CLOSED")
	}
	stream, err := b.stream.Info(ctx)
	if err != nil {
		return safeTransport(err)
	}
	consumer, err := b.consumer.Info(ctx)
	if err != nil {
		return safeTransport(err)
	}
	identity, err := admittedProfile(b.configuration, b.tenant, stream, consumer)
	if err != nil {
		return err
	}
	if identity != b.identity {
		return errors.New("TASK_BUS_IDENTITY_CHANGED")
	}
	return nil
}
func (b *JetStream) Verify(ctx context.Context) error {
	if b == nil || ctx == nil {
		return errors.New("TASK_BUS_CLOSED")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	bounded, cancel := context.WithTimeout(ctx, time.Duration(b.configuration.RequestSeconds)*time.Second)
	defer cancel()
	return b.verify(bounded)
}
func (b *JetStream) Publish(ctx context.Context, e Envelope) (Publication, error) {
	if b == nil || ctx == nil {
		return Publication{}, errors.New("TASK_BUS_CLOSED")
	}
	raw, err := e.Canonical()
	if err != nil || e.Event.NamespaceID != b.configuration.NamespaceID || e.Event.TenantID != b.tenant {
		return Publication{}, errors.New("TASK_EVENT_INVALID")
	}
	subject, _ := e.Subject()
	b.mu.Lock()
	defer b.mu.Unlock()
	bounded, cancel := context.WithTimeout(ctx, time.Duration(b.configuration.RequestSeconds)*time.Second)
	defer cancel()
	if err = b.verify(bounded); err != nil {
		return Publication{}, err
	}
	ack, err := b.api.PublishMsg(bounded, &nats.Msg{Subject: subject, Data: raw}, jetstream.WithMsgID(e.DeliveryID), jetstream.WithExpectStream(b.configuration.Stream), jetstream.WithRetryAttempts(0))
	if err != nil || ack == nil || ack.Stream != b.configuration.Stream || ack.Sequence == 0 || bounded.Err() != nil {
		return Publication{}, errors.New("TASK_BUS_PUBLISH_UNKNOWN")
	}
	// A stream-wide duplicate MsgID ACK alone does not bind another subject/body.
	// Independently read the exact immutable subject before projecting publication.
	stored, readErr := b.stream.GetLastMsgForSubject(bounded, subject)
	if errors.Is(readErr, jetstream.ErrMsgNotFound) {
		return Publication{}, errors.New("TASK_BUS_READBACK_CONFLICT")
	}
	if readErr != nil {
		return Publication{}, errors.New("TASK_BUS_PUBLISH_UNKNOWN")
	}
	if stored == nil || stored.Sequence != ack.Sequence || stored.Subject != subject || !bytes.Equal(stored.Data, raw) || stored.Header.Get(jetstream.MsgIDHeader) != e.DeliveryID || stored.Header.Get(jetstream.ExpectedStreamHeader) != b.configuration.Stream {
		return Publication{}, errors.New("TASK_BUS_READBACK_CONFLICT")
	}
	if err = b.verify(bounded); err != nil {
		if err.Error() == "TASK_BUS_IDENTITY_CHANGED" || err.Error() == "TASK_BUS_PROFILE_REJECTED" {
			return Publication{}, err
		}
		return Publication{}, errors.New("TASK_BUS_PUBLISH_UNKNOWN")
	}
	return Publication{Identity: b.identity, Stream: ack.Stream, Sequence: ack.Sequence, Duplicate: ack.Duplicate}, nil
}
func (b *JetStream) Lookup(ctx context.Context, e Envelope) (Publication, bool, error) {
	if b == nil || ctx == nil {
		return Publication{}, false, errors.New("TASK_BUS_CLOSED")
	}
	raw, err := e.Canonical()
	if err != nil || e.Event.NamespaceID != b.configuration.NamespaceID || e.Event.TenantID != b.tenant {
		return Publication{}, false, errors.New("TASK_EVENT_INVALID")
	}
	subject, _ := e.Subject()
	b.mu.Lock()
	defer b.mu.Unlock()
	bounded, cancel := context.WithTimeout(ctx, time.Duration(b.configuration.RequestSeconds)*time.Second)
	defer cancel()
	if err = b.verify(bounded); err != nil {
		return Publication{}, false, err
	}
	value, err := b.stream.GetLastMsgForSubject(bounded, subject)
	if errors.Is(err, jetstream.ErrMsgNotFound) {
		if err = b.verify(bounded); err != nil {
			return Publication{}, false, err
		}
		return Publication{}, false, nil
	}
	if err != nil {
		return Publication{}, false, safeTransport(err)
	}
	if value == nil || value.Sequence == 0 || value.Subject != subject || !bytes.Equal(value.Data, raw) || value.Header.Get(jetstream.MsgIDHeader) != e.DeliveryID || value.Header.Get(jetstream.ExpectedStreamHeader) != b.configuration.Stream {
		return Publication{}, false, errors.New("TASK_BUS_READBACK_CONFLICT")
	}
	if err = b.verify(bounded); err != nil {
		return Publication{}, false, err
	}
	return Publication{Identity: b.identity, Stream: b.configuration.Stream, Sequence: value.Sequence}, true, nil
}
func (b *JetStream) Next(ctx context.Context) (*Message, error) {
	if b == nil || ctx == nil {
		return nil, errors.New("TASK_BUS_CLOSED")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	bounded, cancel := context.WithTimeout(ctx, time.Duration(b.configuration.RequestSeconds)*time.Second)
	defer cancel()
	if err := b.verify(bounded); err != nil {
		return nil, err
	}
	wait := time.Duration(b.configuration.PullSeconds) * time.Second
	if deadline, ok := bounded.Deadline(); ok && time.Until(deadline) < wait {
		wait = time.Until(deadline)
	}
	if wait <= 0 {
		return nil, errors.New("TASK_BUS_TIMEOUT")
	}
	value, err := b.consumer.Next(jetstream.FetchMaxWait(wait))
	if bounded.Err() != nil {
		return nil, safeTransport(bounded.Err())
	}
	if errors.Is(err, nats.ErrTimeout) {
		return nil, nil
	}
	if err != nil {
		return nil, safeTransport(err)
	}
	if value == nil {
		return nil, errors.New("TASK_BUS_MESSAGE_INVALID")
	}
	metadata, err := value.Metadata()
	if err != nil || metadata == nil || metadata.Stream != b.configuration.Stream || metadata.Consumer != b.configuration.Consumer || metadata.Sequence.Stream == 0 || metadata.Sequence.Consumer == 0 || metadata.NumDelivered == 0 || len(value.Data()) > MaximumTransportBytes {
		return nil, errors.New("TASK_BUS_MESSAGE_INVALID")
	}
	if err = b.verify(bounded); err != nil {
		return nil, err
	}
	return &Message{Body: append([]byte(nil), value.Data()...), Subject: value.Subject(), Identity: b.identity, Stream: metadata.Stream, Consumer: metadata.Consumer, Sequence: metadata.Sequence.Stream, ConsumerSequence: metadata.Sequence.Consumer, Deliveries: metadata.NumDelivered, RecordedAt: metadata.Timestamp.UTC(), message: value, requestSeconds: b.configuration.RequestSeconds, retrySeconds: b.configuration.RetrySeconds}, nil
}
func (b *JetStream) Close() error {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil
	}
	b.closed = true
	if b.publisher != nil {
		b.publisher.Close()
	}
	if b.subscriber != nil {
		b.subscriber.Close()
	}
	return nil
}

// SequenceText preserves full unsigned broker sequence values in SQL decimal
// columns without architecture-sized int truncation.
func SequenceText(sequence uint64) string { return strconv.FormatUint(sequence, 10) }
