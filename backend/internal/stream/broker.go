package stream

import (
	"context"
	"encoding/json"
	"time"

	"github.com/redis/go-redis/v9"
)

// Broker publishes order events over Redis Pub/Sub.
type Broker struct {
	rdb *redis.Client
}

// NewBroker creates a Broker.
func NewBroker(rdb *redis.Client) *Broker {
	return &Broker{rdb: rdb}
}

// Event is a realtime notification payload.
type Event struct {
	Type        string    `json:"type"`
	OrderID     string    `json:"order_id,omitempty"`
	OrderNumber string    `json:"order_number,omitempty"`
	FromStatus  string    `json:"from_status,omitempty"`
	ToStatus    string    `json:"to_status,omitempty"`
	Message     string    `json:"message,omitempty"`
	At          time.Time `json:"at"`
}

// Publish delivers an event to a user's channel.
func (b *Broker) Publish(ctx context.Context, userID string, ev Event) error {
	payload, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	return b.rdb.Publish(ctx, "user:"+userID+":events", payload).Err()
}

// PublishChannel delivers a raw payload to a named channel (chat sessions, etc).
func (b *Broker) PublishChannel(ctx context.Context, channel string, payload []byte) error {
	return b.rdb.Publish(ctx, channel, payload).Err()
}

// SubscribeChannel streams messages from a named channel.
func (b *Broker) SubscribeChannel(ctx context.Context, channel string) (<-chan []byte, func(), error) {
	sub := b.rdb.Subscribe(ctx, channel)
	ch := make(chan []byte, 64)

	go func() {
		defer close(ch)
		for {
			msg, err := sub.ReceiveMessage(ctx)
			if err != nil {
				return
			}
			select {
			case ch <- []byte(msg.Payload):
			case <-ctx.Done():
				return
			}
		}
	}()

	return ch, func() { _ = sub.Close() }, nil
}

// Subscribe opens a channel receiving events for a user.
// The returned func closes the subscription.
func (b *Broker) Subscribe(ctx context.Context, userID string) (<-chan Event, func(), error) {
	sub := b.rdb.Subscribe(ctx, "user:"+userID+":events")
	ch := make(chan Event, 32)

	go func() {
		defer close(ch)
		for {
			msg, err := sub.ReceiveMessage(ctx)
			if err != nil {
				return
			}
			var ev Event
			if err := json.Unmarshal([]byte(msg.Payload), &ev); err != nil {
				continue
			}
			select {
			case ch <- ev:
			case <-ctx.Done():
				return
			}
		}
	}()

	closeFn := func() {
		_ = sub.Close()
	}
	return ch, closeFn, nil
}
