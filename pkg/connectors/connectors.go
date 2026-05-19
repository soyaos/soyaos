// Package connectors implements Channel Connectors (DD-006).
//
// A Channel is anything that can deliver a Message to or from a user: chat
// platforms (DingTalk, Feishu, WeChat Work, public WeChat), email, SMS, web
// chat widgets, custom webhooks. The connector framework gives each channel
// a uniform shape via two halves:
//
//   - Inbound Adapter: external event → SoyaOS Message → kernel.
//   - Outbound Adapter: SoyaOS Message → external delivery.
//
// v0.1.0-alpha.0 ships the abstractions only — production adapters (DingTalk,
// Feishu, WeChat) land alongside the NewsBeam milestone (DD-009).
package connectors

import (
	"context"
	"errors"
	"sync"
)

// Kind labels the underlying channel.
type Kind string

const (
	KindDingTalk Kind = "dingtalk"
	KindFeishu   Kind = "feishu"
	KindWework   Kind = "wework"
	KindWechatMP Kind = "wechat-mp"
	KindWechatCS Kind = "wechat-cs"
	KindWebhook  Kind = "webhook"
)

// Message is the canonical channel-agnostic envelope.
type Message struct {
	ChannelKind Kind
	BindingID   string            // identifies the specific bound channel instance
	UserID      string            // external user id on that channel
	Text        string            // primary text content
	Attachments []Attachment      // optional files / cards / artifacts
	Metadata    map[string]string // free-form per-channel hints
}

// Attachment is a non-text payload — a file, image, or card.
type Attachment struct {
	Kind     string // "file" / "image" / "card" / "artifact"
	MIME     string
	URL      string // either URL or Bytes is set
	Bytes    []byte
	Filename string
}

// Inbound delivers external events into SoyaOS.
type Inbound interface {
	Kind() Kind
	// Start begins listening; the adapter pushes Messages into the provided sink.
	Start(ctx context.Context, sink chan<- Message) error
}

// Outbound sends SoyaOS Messages out to the external channel.
type Outbound interface {
	Kind() Kind
	Send(ctx context.Context, m Message) error
}

// Registry tracks the inbound and outbound adapters known to this process.
type Registry struct {
	mu        sync.RWMutex
	inbound   map[Kind]Inbound
	outbound  map[Kind]Outbound
	bindings  map[string]Binding
}

// Binding ties a specific external account / webhook / cred-set to a Kind.
type Binding struct {
	ID       string            // stable identifier for this bound channel
	Kind     Kind              // channel kind
	Display  string            // human-readable label
	Settings map[string]string // adapter-specific configuration
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{
		inbound:  map[Kind]Inbound{},
		outbound: map[Kind]Outbound{},
		bindings: map[string]Binding{},
	}
}

// RegisterInbound mounts an inbound adapter for its Kind.
func (r *Registry) RegisterInbound(a Inbound) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.inbound[a.Kind()] = a
}

// RegisterOutbound mounts an outbound adapter for its Kind.
func (r *Registry) RegisterOutbound(a Outbound) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.outbound[a.Kind()] = a
}

// AddBinding records a channel binding.
func (r *Registry) AddBinding(b Binding) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.bindings[b.ID] = b
}

// ErrNoAdapter is returned when no adapter is registered for the requested Kind.
var ErrNoAdapter = errors.New("connectors: no adapter registered for kind")

// Send routes m to the registered outbound adapter for its Kind.
func (r *Registry) Send(ctx context.Context, m Message) error {
	r.mu.RLock()
	a, ok := r.outbound[m.ChannelKind]
	r.mu.RUnlock()
	if !ok {
		return ErrNoAdapter
	}
	return a.Send(ctx, m)
}
