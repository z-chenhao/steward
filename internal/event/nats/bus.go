// Package nats is a thin JetStream adapter for the event.EventBus boundary.
package nats

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"time"

	nat "github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/z-chenhao/assistant/internal/event"
)

type Options struct {
	URL             string        `yaml:"url"`
	StreamPrefix    string        `yaml:"stream_prefix"`
	Timeout         time.Duration `yaml:"timeout"`
	AckWait         time.Duration `yaml:"ack_wait"`
	RetryInterval   time.Duration `yaml:"retry_interval"`
	TokenRef        string        `yaml:"token_ref"`
	MaxAge          time.Duration `yaml:"max_age"`
	MaxBytes        int64         `yaml:"max_bytes"`
	MaxMessages     int64         `yaml:"max_messages"`
	MaxMessageBytes int32         `yaml:"max_message_bytes"`
	PoisonAttempts  int           `yaml:"poison_attempts"`
}

// Zero values retain the bounded defaults for callers compiled before M7.
func (o Options) Defaults() Options {
	if o.MaxAge == 0 {
		o.MaxAge = 7 * 24 * time.Hour
	}
	if o.MaxBytes == 0 {
		o.MaxBytes = 1 << 30
	}
	if o.MaxMessages == 0 {
		o.MaxMessages = 1000000
	}
	if o.MaxMessageBytes == 0 {
		o.MaxMessageBytes = 1 << 20
	}
	if o.PoisonAttempts == 0 {
		o.PoisonAttempts = 5
	}
	return o
}

type SecretStore interface {
	Get(context.Context, string) ([]byte, error)
}

var namePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

func (o Options) Validate() error {
	o = o.Defaults()
	u, err := url.Parse(o.URL)
	if err != nil || u == nil || (u.Scheme != "nats" && u.Scheme != "tls") || u.Hostname() == "" || u.Port() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" {
		return errors.New("nats.url must be a nats:// or tls:// host:port without credentials, path, query, or fragment")
	}
	if !namePattern.MatchString(o.StreamPrefix) {
		return errors.New("nats.stream_prefix must contain 1-64 letters, digits, underscores, or hyphens")
	}
	ip := net.ParseIP(u.Hostname())
	if (ip == nil || !ip.IsLoopback()) && (u.Scheme != "tls" || o.TokenRef == "") {
		return errors.New("remote NATS requires TLS and a SecretStore token reference")
	}
	if o.MaxAge < time.Hour || o.MaxAge > 90*24*time.Hour || o.MaxBytes < 1<<20 || o.MaxBytes > 1<<40 || o.MaxMessages < 100 || o.MaxMessages > 10000000 || o.MaxMessageBytes < 1024 || o.MaxMessageBytes > 8<<20 || o.PoisonAttempts < 2 || o.PoisonAttempts > 100 {
		return errors.New("invalid NATS retention, capacity or poison threshold")
	}
	if o.Timeout <= 0 || o.Timeout > time.Minute || o.AckWait < 10*time.Millisecond || o.AckWait > 5*time.Minute || o.RetryInterval < time.Millisecond || o.RetryInterval > time.Minute {
		return errors.New("invalid bounded NATS timeout, acknowledgement wait or retry interval")
	}
	return nil
}

type Bus struct {
	conn             *nat.Conn
	js               jetstream.JetStream
	options          Options
	stream, subjects string
}

var _ event.EventBus = (*Bus)(nil)

func Open(ctx context.Context, options Options) (*Bus, error) {
	return OpenWithSecrets(ctx, options, nil)
}
func OpenWithSecrets(ctx context.Context, options Options, secrets SecretStore) (*Bus, error) {
	return open(ctx, options, secrets, false)
}

// Upgrade explicitly adopts bounded retention on the original unbounded v1
// stream. Refuse any change that would immediately discard retained events.
func Upgrade(ctx context.Context, options Options, secrets SecretStore) (*Bus, error) {
	return open(ctx, options, secrets, true)
}
func open(ctx context.Context, options Options, secrets SecretStore, upgrade bool) (*Bus, error) {
	options = options.Defaults()
	if err := options.Validate(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	timeout := options.Timeout
	if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) < timeout {
		timeout = time.Until(deadline)
	}
	// Disable reconnect/buffering: the durable SQLite relay owns retry decisions.
	opts := []nat.Option{nat.Timeout(timeout), nat.NoReconnect(), nat.NoEcho()}
	if options.TokenRef != "" {
		if secrets == nil {
			return nil, errors.New("NATS credential unavailable")
		}
		token, e := secrets.Get(ctx, options.TokenRef)
		if e != nil || len(token) == 0 {
			return nil, errors.New("NATS credential unavailable")
		}
		opts = append(opts, nat.Token(string(token)))
		for i := range token {
			token[i] = 0
		}
	}
	conn, err := nat.Connect(options.URL, opts...)
	if err != nil {
		return nil, brokerError(err, "connect event broker")
	}
	js, err := jetstream.New(conn)
	if err != nil {
		conn.Close()
		return nil, errors.New("initialize JetStream")
	}
	b := &Bus{conn: conn, js: js, options: options, stream: options.StreamPrefix + "_events_v1", subjects: options.StreamPrefix + ".v1.events"}
	ctx, cancel := context.WithTimeout(ctx, options.Timeout)
	defer cancel()
	// Validate existing streams without a create request: JetStream may check
	// a full additional reservation before recognizing an idempotent create.
	streamConfig := jetstream.StreamConfig{
		Name: b.stream, Subjects: []string{b.subjects + ".>"}, Storage: jetstream.FileStorage,
		Retention: jetstream.LimitsPolicy, Discard: jetstream.DiscardNew,
		MaxAge: options.MaxAge, MaxBytes: options.MaxBytes, MaxMsgs: options.MaxMessages, MaxMsgSize: options.MaxMessageBytes,
	}
	if upgrade {
		previous, e := js.Stream(ctx, b.stream)
		if e == nil {
			info, e := previous.Info(ctx)
			if e != nil {
				conn.Close()
				return nil, brokerError(e, "inspect upgrade stream")
			}
			if info.State.Bytes > uint64(options.MaxBytes) || info.State.Msgs > uint64(options.MaxMessages) || info.State.Msgs > 0 && info.State.FirstTime.Before(time.Now().Add(-options.MaxAge)) {
				conn.Close()
				return nil, errors.New("broker upgrade would discard retained events; settle or export them first")
			}
			old := info.Config
			if len(old.Subjects) != 1 || old.Subjects[0] != b.subjects+".>" || old.Storage != jetstream.FileStorage || old.Retention != jetstream.LimitsPolicy || old.Discard != jetstream.DiscardNew || old.MaxConsumers > 0 || old.Sealed || old.Mirror != nil || len(old.Sources) > 0 {
				conn.Close()
				return nil, errors.New("broker stream is not an owned Steward v1 stream")
			}
			old.MaxAge = options.MaxAge
			old.MaxBytes = options.MaxBytes
			old.MaxMsgs = options.MaxMessages
			old.MaxMsgSize = options.MaxMessageBytes
			_, e = js.UpdateStream(ctx, old)
			if e != nil {
				conn.Close()
				return nil, brokerError(e, "upgrade bounded stream")
			}
		} else if !errors.Is(e, jetstream.ErrStreamNotFound) {
			conn.Close()
			return nil, brokerError(e, "inspect upgrade stream")
		}
	}
	err = ensureStream(ctx, js, streamConfig)
	if err != nil {
		conn.Close()
		return nil, brokerError(err, "create or validate event stream")
	}
	err = ensureStream(ctx, js, jetstream.StreamConfig{Name: options.StreamPrefix + "_dlq_v1", Subjects: []string{options.StreamPrefix + ".v1.dlq.>"}, Storage: jetstream.FileStorage, Retention: jetstream.LimitsPolicy, Discard: jetstream.DiscardNew, MaxAge: 30 * 24 * time.Hour, MaxBytes: 64 << 20, MaxMsgs: 10000, MaxMsgSize: 4096})
	if err != nil {
		conn.Close()
		return nil, brokerError(err, "create or validate DLQ")
	}
	return b, nil
}

func ensureStream(ctx context.Context, js jetstream.JetStream, want jetstream.StreamConfig) error {
	stream, err := js.Stream(ctx, want.Name)
	if errors.Is(err, jetstream.ErrStreamNotFound) {
		_, err = js.CreateStream(ctx, want)
		return err
	}
	if err != nil {
		return err
	}
	info, err := stream.Info(ctx)
	if err != nil {
		return err
	}
	// Compare the complete configuration, allowing only JetStream's documented
	// defaults and server-owned metadata. Never update a drifted stream here.
	want.MaxConsumers, want.MaxMsgsPerSubject, want.Replicas = -1, -1, 1
	want.Duplicates = 2 * time.Minute
	got := info.Config
	for _, key := range []string{"_nats.level", "_nats.req.level", "_nats.ver"} {
		delete(got.Metadata, key)
	}
	actual, err := json.Marshal(got)
	if err != nil {
		return err
	}
	expected, err := json.Marshal(want)
	if err != nil {
		return err
	}
	if !bytes.Equal(actual, expected) {
		return errors.New("incompatible existing stream configuration")
	}
	return nil
}

func (b *Bus) Publish(ctx context.Context, e event.CloudEvent) error {
	data, err := event.Encode(e)
	if err != nil {
		return err
	}
	key := sha256.Sum256([]byte(e.Subject()))
	msg := nat.NewMsg(b.subjects + "." + hex.EncodeToString(key[:]))
	msg.Header.Set("Content-Type", event.ContentType)
	msg.Data = data
	// No broker deduplication window is used as a correctness dependency.
	ctx, cancel := context.WithTimeout(ctx, b.options.Timeout)
	defer cancel()
	if _, err := b.js.PublishMsg(ctx, msg); err != nil {
		return brokerError(err, "publish event to JetStream")
	}
	return nil
}

func (b *Bus) Subscribe(ctx context.Context, consumer string) (event.Subscription, error) {
	if !namePattern.MatchString(consumer) {
		return nil, errors.New("invalid durable consumer name")
	}
	ctx, cancel := context.WithTimeout(ctx, b.options.Timeout)
	defer cancel()
	c, err := b.js.CreateConsumer(ctx, b.stream, jetstream.ConsumerConfig{
		Durable: consumer, AckPolicy: jetstream.AckExplicitPolicy, AckWait: b.options.AckWait,
		DeliverPolicy: jetstream.DeliverAllPolicy, FilterSubject: b.subjects + ".>",
		// Unlimited broker deliveries let us publish a durable quarantine receipt
		// BEFORE acknowledging, even across a DLQ outage or process crash.
		MaxDeliver: -1, MaxAckPending: 32,
	})
	if err != nil {
		return nil, brokerError(err, "create or validate durable consumer")
	}
	messages, err := c.Messages(jetstream.PullMaxMessages(1))
	if err != nil {
		return nil, errors.New("open event subscription")
	}
	return &subscription{messages: messages, timeout: b.options.Timeout, bus: b, consumer: consumer}, nil
}

func (b *Bus) Close() error { b.conn.Close(); return nil }

type subscription struct {
	messages jetstream.MessagesContext
	timeout  time.Duration
	bus      *Bus
	consumer string
}

func (s *subscription) Close() error { s.messages.Stop(); return nil }
func (s *subscription) Next(ctx context.Context) (event.Delivery, error) {
	msg, err := s.messages.Next(jetstream.NextContext(ctx))
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, brokerError(err, "receive event")
	}
	metadata, err := msg.Metadata()
	if err != nil {
		return nil, errors.New("read event delivery metadata")
	}
	if metadata.NumDelivered > uint64(s.bus.options.PoisonAttempts) {
		// Bodies, headers, domain subjects and error text never leave the source
		// stream. A deterministic receipt key makes an ambiguous DLQ ack harmless.
		digest := sha256.Sum256(msg.Data())
		receipt := struct {
			Consumer string `json:"consumer"`
			Sequence uint64 `json:"sequence"`
			Attempts uint64 `json:"attempts"`
			SHA256   string `json:"sha256"`
			Code     string `json:"code"`
		}{s.consumer, metadata.Sequence.Stream, metadata.NumDelivered - 1, hex.EncodeToString(digest[:]), "POISON_THRESHOLD"}
		body, _ := json.Marshal(receipt)
		dlq := nat.NewMsg(s.bus.options.StreamPrefix + ".v1.dlq." + s.consumer + "." + strconv.FormatUint(metadata.Sequence.Stream, 10))
		dlq.Data = body
		bounded, cancel := context.WithTimeout(ctx, s.timeout)
		defer cancel()
		if _, err = s.bus.js.PublishMsg(bounded, dlq); err != nil {
			return nil, brokerError(err, "quarantine event")
		}
		if err = msg.DoubleAck(bounded); err != nil {
			return nil, brokerError(err, "acknowledge quarantined event")
		}
		return nil, event.ErrPoison
	}
	if msg.Headers().Get("Content-Type") != event.ContentType {
		_ = msg.Nak()
		return nil, event.ErrPoison
	}
	e, err := event.Decode(msg.Data())
	if err != nil {
		_ = msg.Nak()
		return nil, event.ErrPoison
	}
	return &delivery{message: msg, event: e, timeout: s.timeout}, nil
}

type delivery struct {
	message jetstream.Msg
	event   event.CloudEvent
	timeout time.Duration
}

func (d *delivery) Event() event.CloudEvent { return d.event.Clone() }
func (d *delivery) Ack(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, d.timeout)
	defer cancel()
	if err := d.message.DoubleAck(ctx); err != nil {
		return brokerError(err, "acknowledge event")
	}
	return nil
}

// Preserve only safe classification, never provider errors/URLs/credentials.
func brokerError(err error, action string) error {
	var network *net.OpError
	var api *jetstream.APIError
	if errors.As(err, &network) ||
		errors.Is(err, nat.ErrNoServers) ||
		errors.Is(err, nat.ErrConnectionClosed) ||
		errors.Is(err, nat.ErrDisconnected) ||
		errors.Is(err, nat.ErrTimeout) ||
		errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, nat.ErrNoResponders) ||
		errors.Is(err, jetstream.ErrNoStreamResponse) ||
		errors.Is(err, jetstream.ErrConnectionClosed) ||
		(errors.As(err, &api) && api.Code == 503) {
		return fmt.Errorf("%s: %w", action, event.ErrUnavailable)
	}
	return errors.New(action)
}
