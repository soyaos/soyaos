// SilentCut reverse-pressure: DD-011 §R wants Moon → Comet artifact bytes
// to never pass through Planet (which costs egress and adds RTT). The
// SoyaOS spec §A1 fixes the wire as "mTLS gRPC bidi-stream over QUIC,
// NOT WebRTC". This file holds the Stage-2 / Stage-5 stable contract:
//
//   - StreamTransport: byte-stream Dial / Listen pair, distinct from the
//     existing message-oriented mesh.Transport (which serves the Solo /
//     in-process dispatch path). The Stage 5 QUIC implementation lives
//     in pkg/mesh/quic and satisfies StreamTransport.
//   - PeerID / Conn / Listener: the minimum surface a bidi-stream needs
//     to be useful to the runtime.
//
// We deliberately keep this file independent of any third-party QUIC dep
// so other packages can take the interface today without dragging in
// quic-go.

package mesh

import (
	"context"
	"io"
)

// PeerID is an opaque, mesh-issued identifier for a node. Callers must not
// parse it. The Stage 5 implementation will derive it from the peer's mTLS
// certificate fingerprint.
type PeerID string

// StreamTransport is the byte-stream cousin of Transport. Where Transport's
// Send-Reply shape models RPC, StreamTransport models direct Moon ↔ Comet
// pipes carrying multi-megabyte MP4 chunks.
//
// Implementations must be safe for concurrent Dial / Listen / Close.
type StreamTransport interface {
	// Dial opens a Conn to peer at addr. addr is opaque to callers — it
	// is the resolved endpoint chosen by the path selector
	// (PathChoice.Endpoint). The Conn is full-duplex; either side may
	// Read / Write in any order.
	Dial(ctx context.Context, peer PeerID, addr string) (Conn, error)

	// Listen binds a Listener at addr (transport-specific format).
	Listen(ctx context.Context, addr string) (Listener, error)

	// Close releases every Listener and Conn this transport owns. After
	// Close, Dial and Listen must return an error.
	Close() error
}

// Conn is one end of a peer-to-peer byte stream. The PeerID method tells
// the caller who they're talking to (this is the authenticated identity
// established at Dial / Accept time).
type Conn interface {
	io.ReadWriteCloser
	// PeerID returns the authenticated identity of the remote end.
	PeerID() PeerID
}

// Listener accepts inbound Conns.
type Listener interface {
	// Accept blocks until a peer connects or ctx is cancelled.
	Accept(ctx context.Context) (Conn, error)
	// Close stops accepting; concurrent Accept calls must unblock with an
	// error.
	Close() error
}
