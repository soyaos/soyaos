// Package quic is the QUIC transport for SoyaMesh.
//
// SilentCut reverse-pressure: DD-011 needs Moon → Comet artifact bytes to
// avoid Planet entirely. Spec §A1 fixes mTLS-gRPC-bidi-over-QUIC (NOT
// WebRTC) as the wire. Planet only exchanges ICE-like metadata + a
// short-lived auth token; the MP4 stream itself stays peer-to-peer.
//
// alpha shape: this file exposes the Stage 5 contract (mesh.StreamTransport
// satisfied) but every method returns mesh.ErrNotImplemented so callers can
// branch on errors.Is without pulling quic-go into the dependency tree. The
// Stage 5 implementation will wrap quic-go's quic.Transport with the mTLS
// config the caller supplies here.

package quic

import (
	"context"
	"crypto/tls"
	"errors"

	"github.com/soyaos/soyaos/pkg/mesh"
)

// ErrNotImplemented is the alpha sentinel. It is distinct from
// mesh.ErrNoRoute (which indicates a routing miss) so callers can
// distinguish "this transport isn't built yet" from "we couldn't find a
// path".
var ErrNotImplemented = errors.New("mesh/quic: not implemented in alpha")

// Transport is the QUIC mesh.StreamTransport.
//
// TODO(Stage5): quic-go integration — wrap quic.Transport with the supplied
// tls.Config (mTLS, ALPN "soyaos/v1"). Listen → quic.Listener; Dial →
// quic.Dial + first bidi-stream. PeerID is derived from the peer
// certificate fingerprint at handshake completion. Until that lands this
// stub returns ErrNotImplemented but exposes the contract Stage 5 needs.
type Transport struct {
	tlsCfg *tls.Config
}

// New returns a stub QUIC transport. The tls.Config is held for the Stage 5
// wiring; alpha implementations ignore it.
func New(tlsCfg *tls.Config) *Transport {
	return &Transport{tlsCfg: tlsCfg}
}

// TLSConfig returns the stored config so callers (or tests) can verify the
// transport will pick up the right mTLS material once the QUIC wire lands.
func (t *Transport) TLSConfig() *tls.Config { return t.tlsCfg }

// Dial — alpha stub.
func (t *Transport) Dial(context.Context, mesh.PeerID, string) (mesh.Conn, error) {
	return nil, ErrNotImplemented
}

// Listen — alpha stub.
func (t *Transport) Listen(context.Context, string) (mesh.Listener, error) {
	return nil, ErrNotImplemented
}

// Close — alpha stub. There is no live state to release yet.
func (t *Transport) Close() error { return nil }

// Compile-time assertion that *Transport satisfies the mesh.StreamTransport
// contract — the Stage 5 implementation must keep this check green.
var _ mesh.StreamTransport = (*Transport)(nil)
