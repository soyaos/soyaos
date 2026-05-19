// Package scope is SoyaScope — observability: structured logs, distributed
// tracing, replay metadata.
//
// v0.1.0-alpha.0 ships a minimal in-process event recorder used by the
// kernel and OpenAI-Compat gateway. OpenTelemetry export, replay, and audit
// integration arrive in later milestones.
package scope

import (
	"sync"
	"time"
)

// Event is a single observation: log line, trace span boundary, or audit
// record. The shape is intentionally flat so it can be serialized to JSON,
// OTel, or printed to stderr without restructuring.
type Event struct {
	Time     time.Time
	Kind     string            // "log" / "span" / "audit"
	Level    string            // "debug" / "info" / "warn" / "error"
	Source   string            // module name, e.g. "openaicompat"
	Message  string            // free-form
	Attrs    map[string]string // structured attributes
	TraceID  string            // optional correlation id
	SpanID   string            // optional span id
	Duration time.Duration     // for span end events
}

// Recorder accepts Events. The Solo recorder buffers events in memory so they
// can be flushed to stderr by callers that want logs visible.
type Recorder interface {
	Record(Event)
}

// Memory is the Solo / test Recorder.
type Memory struct {
	mu     sync.Mutex
	events []Event
}

// NewMemory returns an empty Memory recorder.
func NewMemory() *Memory { return &Memory{} }

// Record stores an Event.
func (m *Memory) Record(e Event) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append(m.events, e)
}

// Snapshot returns a copy of all currently buffered events.
func (m *Memory) Snapshot() []Event {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Event, len(m.events))
	copy(out, m.events)
	return out
}

// Len reports how many events are buffered.
func (m *Memory) Len() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.events)
}
