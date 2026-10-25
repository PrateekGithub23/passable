package live

import (
	"context"
	"encoding/json"
	"log"

	"github.com/PrateekGithub23/passable/internal/reports"
	"github.com/redis/go-redis/v9"
)

// channel is the Redis pub/sub channel every API instance publishes to and subscribes on.
const channel = "passable:hazard-events"

// Bus fans committed hazard changes out to every API instance through Redis pub/sub.
//
// Flow for a report created on instance A:
//
//	A: commit in Postgres -> apply to A's overlay -> PUBLISH
//	Redis -> every subscriber (A included) -> apply to overlay (A: already applied, ignored)
//	                                        -> push to that instance's WebSocket clients
//
// Pub/sub is fire-and-forget: a message sent while an instance is disconnected is lost. That's
// acceptable because Postgres is the source of truth: the overlay's gap detection and the
// periodic catch-up (reports.catchUp) resync from the event log.
type Bus struct {
	rdb *redis.Client
	sub *redis.PubSub
}

// NewBus subscribes to the channel and waits for Redis to confirm the subscription, so no
// message published after NewBus returns can be missed by this instance.
func NewBus(ctx context.Context, rdb *redis.Client) (*Bus, error) {
	sub := rdb.Subscribe(ctx, channel)
	if _, err := sub.Receive(ctx); err != nil { // the subscription confirmation
		sub.Close()
		return nil, err
	}
	return &Bus{rdb: rdb, sub: sub}, nil
}

// Publish sends a change to all instances.
func (b *Bus) Publish(ctx context.Context, ch reports.Change) error {
	data, err := json.Marshal(ch)
	if err != nil {
		return err
	}
	return b.rdb.Publish(ctx, channel, data).Err()
}

// Run delivers every received change to handle until ctx is cancelled. go-redis reconnects
// and resubscribes on its own if the Redis connection drops.
func (b *Bus) Run(ctx context.Context, handle func(reports.Change)) {
	defer b.sub.Close()
	msgs := b.sub.Channel()
	for {
		select {
		case <-ctx.Done():
			return
		case m, ok := <-msgs:
			if !ok {
				return
			}
			var ch reports.Change
			if err := json.Unmarshal([]byte(m.Payload), &ch); err != nil {
				log.Printf("live: bad bus message: %v", err)
				continue
			}
			handle(ch)
		}
	}
}
