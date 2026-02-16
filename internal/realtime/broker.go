package realtime

import (
	"sync"
	"sync/atomic"
	"time"
)

// Event is a server-side realtime event pushed to UI clients.
type Event struct {
	ID      int64     `json:"id"`
	Type    string    `json:"type"`
	JobName string    `json:"job_name,omitempty"`
	RunID   string    `json:"run_id,omitempty"`
	Action  string    `json:"action,omitempty"`
	Status  string    `json:"status,omitempty"`
	Trigger string    `json:"trigger,omitempty"`
	At      time.Time `json:"at"`
}

// Broker is an in-memory fan-out event bus for SSE subscribers.
type Broker struct {
	mu     sync.RWMutex
	nextID atomic.Int64
	nextCh int64
	subs   map[int64]chan Event
}

// NewBroker creates a Broker.
func NewBroker() *Broker {
	return &Broker{
		subs: make(map[int64]chan Event),
	}
}

// Publish broadcasts an event to all active subscribers.
// Slow subscribers drop events instead of blocking producers.
func (b *Broker) Publish(evt Event) {
	evt.ID = b.nextID.Add(1)
	if evt.At.IsZero() {
		evt.At = time.Now().UTC()
	}

	b.mu.RLock()
	defer b.mu.RUnlock()
	for _, ch := range b.subs {
		select {
		case ch <- evt:
		default:
		}
	}
}

// Subscribe registers a subscriber and returns an event channel and cancel func.
func (b *Broker) Subscribe() (<-chan Event, func()) {
	id := atomic.AddInt64(&b.nextCh, 1)
	ch := make(chan Event, 32)

	b.mu.Lock()
	b.subs[id] = ch
	b.mu.Unlock()

	cancel := func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		if c, ok := b.subs[id]; ok {
			delete(b.subs, id)
			close(c)
		}
	}

	return ch, cancel
}
