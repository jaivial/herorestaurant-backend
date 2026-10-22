// Package diag is the reusable, cross-cutting diagnostic channel of the
// backend (coordination id: wa_diag_pkg_v1).
//
// Every observation goes through ONE emission point (Record / Checkpoint /
// Recordf) and lands in a bounded in-memory ring buffer that an operator can
// dump later (admin endpoint or logs) to reconstruct a full turn by its
// correlation id. The package depends on nothing internal and never writes to
// the log itself: the host application installs a Sink (see SetSink) that
// decides how lines are emitted (stdlib log, structured logger, no-op in
// tests...).
//
// Usage from any package:
//
//	diag.Checkpoint("wa-my-corr-id", "my_checkpoint_v1", "key", "value")
//	diag.CheckpointCtx(ctx, "my_checkpoint_v1", "key", "value") // corr id from ctx
//	diag.Recordf("bot", corrID, "my_checkpoint_v1", "x=%d", 3)
package diag

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// DefaultCapacity is the ring size used when no other capacity is set: big
// enough to hold dozens of full WhatsApp turns, small enough to stay flat.
const DefaultCapacity = 2000

// Event is one diagnostic observation. CoordinationID correlates events that
// belong to the same logical unit of work (a bot turn, an HTTP request...);
// the repo reuses existing identifiers for it (bot session id, x-correlation-id).
type Event struct {
	Time           time.Time `json:"time"`
	CoordinationID string    `json:"coordination_id,omitempty"`
	Checkpoint     string    `json:"checkpoint"`
	// Component labels the emitter ("bot", ...) and is rendered as the
	// "[bot]" prefix operators already grep for.
	Component string            `json:"component,omitempty"`
	Message   string            `json:"message,omitempty"`
	Fields    map[string]string `json:"fields,omitempty"`
}

// Line renders the event as the single greppable log line:
// "[<component>] checkpoint <name> correlation_id=<id> <message> k=v...".
// Structured fields render sorted by key so lines stay deterministic.
func (e Event) Line() string {
	var b strings.Builder
	if e.Component != "" {
		b.WriteString("[")
		b.WriteString(e.Component)
		b.WriteString("] ")
	}
	b.WriteString("checkpoint ")
	b.WriteString(e.Checkpoint)
	if e.CoordinationID != "" {
		b.WriteString(" correlation_id=")
		b.WriteString(e.CoordinationID)
	}
	if e.Message != "" {
		b.WriteString(" ")
		b.WriteString(e.Message)
	}
	if len(e.Fields) > 0 {
		keys := make([]string, 0, len(e.Fields))
		for k := range e.Fields {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			b.WriteString(" ")
			b.WriteString(k)
			b.WriteString("=")
			b.WriteString(e.Fields[k])
		}
	}
	return sanitize(b.String())
}

// sanitize strips control characters so user- or error-derived values cannot
// forge extra log lines (log injection, same rule as internal/api).
func sanitize(v string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, v)
}

// Sink receives every recorded event. Installed by the host application; the
// default sink drops events (buffer only) so nothing is emitted until wired.
type Sink func(Event)

// Buffer is a bounded, concurrency-safe circular buffer of the latest events.
type Buffer struct {
	mu      sync.Mutex
	ring    []Event
	next    int
	wrapped bool
}

// NewBuffer returns a ring that keeps the latest capacity events (<=0 uses
// DefaultCapacity).
func NewBuffer(capacity int) *Buffer {
	if capacity <= 0 {
		capacity = DefaultCapacity
	}
	return &Buffer{ring: make([]Event, 0, capacity)}
}

// Add stores one event, dropping the oldest when full.
func (b *Buffer) Add(e Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.ring) < cap(b.ring) {
		b.ring = append(b.ring, e)
		return
	}
	b.ring[b.next] = e
	b.next = (b.next + 1) % len(b.ring)
	b.wrapped = true
}

// SetCapacity resizes the ring, keeping the newest events. The surviving
// events are rebuilt in chronological order, so the ring indices reset.
func (b *Buffer) SetCapacity(capacity int) {
	if capacity <= 0 {
		capacity = DefaultCapacity
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	chron := b.snapshotLocked()
	if len(chron) > capacity {
		chron = chron[len(chron)-capacity:]
	}
	b.ring = append(make([]Event, 0, capacity), chron...)
	b.next, b.wrapped = 0, false
}

// Capacity reports the configured ring size.
func (b *Buffer) Capacity() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return cap(b.ring)
}

// Snapshot returns a copy of the buffered events, oldest first.
func (b *Buffer) Snapshot() []Event {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.snapshotLocked()
}

// snapshotLocked returns the chronological view; b.mu must be held.
func (b *Buffer) snapshotLocked() []Event {
	if !b.wrapped {
		return append([]Event(nil), b.ring...)
	}
	out := make([]Event, 0, len(b.ring))
	out = append(out, b.ring[b.next:]...)
	out = append(out, b.ring[:b.next]...)
	return out
}

// Coordination returns the buffered events whose CoordinationID matches,
// oldest first: the reconstruction of one unit of work.
func (b *Buffer) Coordination(id string) []Event {
	if id == "" {
		return nil
	}
	var out []Event
	for _, e := range b.Snapshot() {
		if e.CoordinationID == id {
			out = append(out, e)
		}
	}
	return out
}

var (
	defaultBuffer      = NewBuffer(DefaultCapacity)
	sink          Sink = func(Event) {}
)

// SetSink installs the emission sink (call once at start-up, before traffic).
func SetSink(s Sink) {
	if s != nil {
		sink = s
	}
}

// SetCapacity resizes the default ring.
func SetCapacity(n int) { defaultBuffer.SetCapacity(n) }

// Capacity reports the default ring size.
func Capacity() int { return defaultBuffer.Capacity() }

// Record is the single emission point: it fills the timestamp, appends the
// event to the default ring and hands it to the sink.
func Record(e Event) {
	if e.Time.IsZero() {
		e.Time = time.Now()
	}
	defaultBuffer.Add(e)
	sink(e)
}

// Recordf records a rendered message (same formatting rules as the log lines
// the callers already had). component may be empty.
func Recordf(component, coordinationID, name, format string, args ...any) {
	Record(Event{
		CoordinationID: coordinationID,
		Checkpoint:     name,
		Component:      component,
		Message:        fmt.Sprintf(format, args...),
	})
}

// Checkpoint records a key=value observation under an explicit coordination id.
func Checkpoint(coordinationID, name string, kv ...string) {
	Record(Event{CoordinationID: coordinationID, Checkpoint: name, Fields: pairs(kv)})
}

// CheckpointCtx records a key=value observation, taking the coordination id
// from the context (see WithCoordination).
func CheckpointCtx(ctx context.Context, name string, kv ...string) {
	Record(Event{CoordinationID: CoordinationFrom(ctx), Checkpoint: name, Fields: pairs(kv)})
}

// pairs turns an even-length key/value list into a map (odd tail dropped).
func pairs(kv []string) map[string]string {
	if len(kv) == 0 {
		return nil
	}
	m := make(map[string]string, len(kv)/2)
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i]] = kv[i+1]
	}
	return m
}

// Snapshot returns a copy of the default buffer, oldest first.
func Snapshot() []Event { return defaultBuffer.Snapshot() }

// Coordination returns the events of one coordination id from the default buffer.
func Coordination(id string) []Event { return defaultBuffer.Coordination(id) }

type coordinationKey struct{}

// WithCoordination returns a context carrying the correlation id of the unit
// of work, so deep call sites can observe without threading it by hand.
func WithCoordination(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, coordinationKey{}, id)
}

// CoordinationFrom extracts the correlation id carried by the context ("" if none).
func CoordinationFrom(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	id, _ := ctx.Value(coordinationKey{}).(string)
	return id
}
