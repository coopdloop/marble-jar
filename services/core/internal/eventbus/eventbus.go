// Package eventbus is the durable event backbone (Redpanda) plus the ephemeral
// realtime fanout channel (Redis pub/sub). Both degrade to an in-process
// implementation so the stack runs locally without external brokers.
package eventbus

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/segmentio/kafka-go"
)

// Topics on the durable backbone.
const (
	TopicMarbleLogged   = "marble.logged"
	TopicDispatchIntent = "dispatch.intent"
)

// Channel used for realtime WebSocket fanout.
const ChannelQueue = "marblejar:queue"

// Event is the envelope on every topic and pub/sub channel.
type Event struct {
	ID             string          `json:"id"`
	Type           string          `json:"type"`
	OrganizationID string          `json:"organization_id"`
	OccurredAt     time.Time       `json:"occurred_at"`
	Payload        json.RawMessage `json:"payload"`
	Signature      string          `json:"signature,omitempty"`
}

// Publisher writes to the durable backbone.
type Publisher interface {
	Publish(ctx context.Context, topic string, key string, ev Event) error
	Close() error
}

// Broadcaster handles ephemeral realtime fanout across API replicas.
type Broadcaster interface {
	Broadcast(ctx context.Context, ev Event) error
	Subscribe(ctx context.Context) (<-chan Event, func(), error)
	Close() error
}

// NewEvent builds a signed envelope. The HMAC lets dispatch workers verify
// that an intent genuinely originated from core.
func NewEvent(typ, orgID string, payload any, secret string) (Event, error) {
	b, err := json.Marshal(payload)
	if err != nil {
		return Event{}, err
	}
	ev := Event{
		ID:             uuid.NewString(),
		Type:           typ,
		OrganizationID: orgID,
		OccurredAt:     time.Now().UTC(),
		Payload:        b,
	}
	if secret != "" {
		ev.Signature = Sign(b, secret)
	}
	return ev, nil
}

func Sign(payload []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func Verify(payload []byte, signature, secret string) bool {
	return hmac.Equal([]byte(Sign(payload, secret)), []byte(signature))
}

// ---------- Redpanda / Kafka ----------

type kafkaPublisher struct {
	writers map[string]*kafka.Writer
	brokers []string
	mu      sync.RWMutex
	log     *slog.Logger
}

func NewKafkaPublisher(brokers []string, log *slog.Logger) Publisher {
	return &kafkaPublisher{
		writers: map[string]*kafka.Writer{},
		brokers: brokers,
		log:     log,
	}
}

func (k *kafkaPublisher) writer(topic string) *kafka.Writer {
	k.mu.RLock()
	w, ok := k.writers[topic]
	k.mu.RUnlock()
	if ok {
		return w
	}

	k.mu.Lock()
	defer k.mu.Unlock()
	if w, ok := k.writers[topic]; ok {
		return w
	}
	w = &kafka.Writer{
		Addr:                   kafka.TCP(k.brokers...),
		Topic:                  topic,
		Balancer:               &kafka.Hash{},
		RequiredAcks:           kafka.RequireOne,
		AllowAutoTopicCreation: true,
		BatchTimeout:           10 * time.Millisecond,
		Async:                  false,
	}
	k.writers[topic] = w
	return w
}

func (k *kafkaPublisher) Publish(ctx context.Context, topic, key string, ev Event) error {
	b, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	return k.writer(topic).WriteMessages(ctx, kafka.Message{
		Key:   []byte(key),
		Value: b,
		Time:  ev.OccurredAt,
	})
}

func (k *kafkaPublisher) Close() error {
	k.mu.Lock()
	defer k.mu.Unlock()
	for _, w := range k.writers {
		_ = w.Close()
	}
	return nil
}

// NewKafkaReader builds a consumer-group reader for the dispatch worker.
func NewKafkaReader(brokers []string, topic, group string) *kafka.Reader {
	return kafka.NewReader(kafka.ReaderConfig{
		Brokers:        brokers,
		Topic:          topic,
		GroupID:        group,
		MinBytes:       1,
		MaxBytes:       10 << 20,
		CommitInterval: time.Second,
		StartOffset:    kafka.LastOffset,
	})
}

// ---------- Redis pub/sub ----------

type redisBroadcaster struct {
	client *redis.Client
	log    *slog.Logger
}

func NewRedisBroadcaster(url string, log *slog.Logger) (Broadcaster, error) {
	opts, err := redis.ParseURL(url)
	if err != nil {
		return nil, err
	}
	client := redis.NewClient(opts)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		return nil, err
	}
	return &redisBroadcaster{client: client, log: log}, nil
}

func (r *redisBroadcaster) Broadcast(ctx context.Context, ev Event) error {
	b, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	return r.client.Publish(ctx, ChannelQueue, b).Err()
}

func (r *redisBroadcaster) Subscribe(ctx context.Context) (<-chan Event, func(), error) {
	sub := r.client.Subscribe(ctx, ChannelQueue)
	if _, err := sub.Receive(ctx); err != nil {
		_ = sub.Close()
		return nil, nil, err
	}

	out := make(chan Event, 256)
	go func() {
		defer close(out)
		ch := sub.Channel(redis.WithChannelSize(256))
		for {
			select {
			case <-ctx.Done():
				return
			case msg, ok := <-ch:
				if !ok {
					return
				}
				var ev Event
				if err := json.Unmarshal([]byte(msg.Payload), &ev); err != nil {
					r.log.Warn("bad realtime event", "error", err)
					continue
				}
				select {
				case out <- ev:
				default:
					r.log.Warn("realtime subscriber slow, dropping event", "event_id", ev.ID)
				}
			}
		}
	}()
	return out, func() { _ = sub.Close() }, nil
}

func (r *redisBroadcaster) Close() error { return r.client.Close() }

// ---------- in-process fallbacks ----------

type memPublisher struct {
	mu   sync.Mutex
	subs map[string][]chan Event
	log  *slog.Logger
}

// NewMemoryPublisher lets the stack run without Redpanda; consumers registered
// via Consume receive events in-process.
func NewMemoryPublisher(log *slog.Logger) Publisher {
	return &memPublisher{subs: map[string][]chan Event{}, log: log}
}

func (m *memPublisher) Publish(ctx context.Context, topic, _ string, ev Event) error {
	m.mu.Lock()
	subs := append([]chan Event(nil), m.subs[topic]...)
	m.mu.Unlock()
	for _, ch := range subs {
		select {
		case ch <- ev:
		default:
			m.log.Warn("in-memory consumer lagging", "topic", topic)
		}
	}
	return nil
}

func (m *memPublisher) Consume(topic string) <-chan Event {
	m.mu.Lock()
	defer m.mu.Unlock()
	ch := make(chan Event, 512)
	m.subs[topic] = append(m.subs[topic], ch)
	return ch
}

func (m *memPublisher) Close() error { return nil }

// MemoryConsumable is implemented by the in-process publisher so a single
// binary can host both producer and consumer in dev mode.
type MemoryConsumable interface {
	Consume(topic string) <-chan Event
}

type memBroadcaster struct {
	mu   sync.RWMutex
	subs map[int]chan Event
	next int
}

func NewMemoryBroadcaster() Broadcaster {
	return &memBroadcaster{subs: map[int]chan Event{}}
}

func (m *memBroadcaster) Broadcast(_ context.Context, ev Event) error {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, ch := range m.subs {
		select {
		case ch <- ev:
		default:
		}
	}
	return nil
}

func (m *memBroadcaster) Subscribe(ctx context.Context) (<-chan Event, func(), error) {
	m.mu.Lock()
	id := m.next
	m.next++
	ch := make(chan Event, 256)
	m.subs[id] = ch
	m.mu.Unlock()

	cancel := func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		if c, ok := m.subs[id]; ok {
			delete(m.subs, id)
			close(c)
		}
	}
	go func() {
		<-ctx.Done()
		cancel()
	}()
	return ch, cancel, nil
}

func (m *memBroadcaster) Close() error { return nil }
