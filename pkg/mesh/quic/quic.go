// Package quic provides the end-to-end mTLS stream transport for SoyaMesh.
//
// Direct and WireGuard addresses use an ordinary UDP socket. relay+udp
// addresses place relay.PacketConn underneath quic-go, so Planet forwards
// encrypted QUIC datagrams without terminating TLS or seeing MP4 plaintext.
package quic

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"sync"

	quicgo "github.com/quic-go/quic-go"
	"github.com/soyaos/soyaos/pkg/mesh"
	"github.com/soyaos/soyaos/pkg/mesh/relay"
)

const alpn = "soyaos/v1"

var (
	ErrClosed       = errors.New("mesh/quic: transport is closed")
	ErrTLSRequired  = errors.New("mesh/quic: verified mutual TLS configuration is required")
	ErrPeerMismatch = errors.New("mesh/quic: authenticated peer does not match requested peer")
)

// Transport is a concurrent mesh.StreamTransport backed by quic-go.
type Transport struct {
	tlsCfg  *tls.Config
	quicCfg *quicgo.Config

	mu        sync.Mutex
	closed    bool
	nextID    uint64
	closeFunc map[uint64]func() error
}

// New constructs a transport with conservative stream and connection limits.
// The tls.Config is cloned for every dial/listen operation.
func New(tlsCfg *tls.Config) *Transport {
	return NewWithConfig(tlsCfg, nil)
}

// NewWithConfig permits callers to tune QUIC timeouts and flow-control while
// preserving SoyaOS's mandatory mTLS and ALPN policy.
func NewWithConfig(tlsCfg *tls.Config, quicCfg *quicgo.Config) *Transport {
	return &Transport{
		tlsCfg:    tlsCfg,
		quicCfg:   quicCfg,
		closeFunc: make(map[uint64]func() error),
	}
}

// TLSConfig returns the caller-owned config for inspection. Operations always
// use a clone, so adding ALPN never mutates this value.
func (t *Transport) TLSConfig() *tls.Config { return t.tlsCfg }

// PeerIDFromCertificate is the canonical authenticated node identity: the
// SHA-256 fingerprint of its leaf X.509 certificate.
func PeerIDFromCertificate(cert *x509.Certificate) mesh.PeerID {
	if cert == nil {
		return ""
	}
	digest := sha256.Sum256(cert.Raw)
	return mesh.PeerID("sha256:" + hex.EncodeToString(digest[:]))
}

func (t *Transport) Dial(ctx context.Context, expectedPeer mesh.PeerID, rawAddr string) (mesh.Conn, error) {
	clientTLS, err := t.clientTLS()
	if err != nil {
		return nil, err
	}
	if err := t.ensureOpen(); err != nil {
		return nil, err
	}

	var (
		qconn *quicgo.Conn
		extra func() error
	)
	if strings.HasPrefix(rawAddr, "relay+udp://") {
		endpoint, err := relay.ParseEndpoint(rawAddr)
		if err != nil {
			return nil, err
		}
		packetConn, err := relay.ListenPacket(ctx, endpoint)
		if err != nil {
			return nil, err
		}
		qtransport := &quicgo.Transport{Conn: packetConn}
		qconn, err = qtransport.Dial(ctx, endpoint.RelayAddr, clientTLS, t.config(true))
		if err != nil {
			_ = qtransport.Close()
			return nil, fmt.Errorf("mesh/quic: dial relay: %w", err)
		}
		extra = qtransport.Close
	} else {
		addr, err := directAddress(rawAddr)
		if err != nil {
			return nil, err
		}
		qconn, err = quicgo.DialAddr(ctx, addr, clientTLS, t.config(false))
		if err != nil {
			return nil, fmt.Errorf("mesh/quic: dial %s: %w", redactAddress(rawAddr), err)
		}
	}

	peerID, err := authenticatedPeer(qconn)
	if err != nil {
		_ = qconn.CloseWithError(1, "peer certificate missing")
		if extra != nil {
			_ = extra()
		}
		return nil, err
	}
	if expectedPeer != "" && peerID != expectedPeer {
		_ = qconn.CloseWithError(1, "peer identity mismatch")
		if extra != nil {
			_ = extra()
		}
		return nil, fmt.Errorf("%w: got %s", ErrPeerMismatch, peerID)
	}
	stream, err := qconn.OpenStreamSync(ctx)
	if err != nil {
		_ = qconn.CloseWithError(1, "open stream failed")
		if extra != nil {
			_ = extra()
		}
		return nil, fmt.Errorf("mesh/quic: open stream: %w", err)
	}

	conn := &connection{stream: stream, conn: qconn, peerID: peerID, extraClose: extra}
	conn.unregister = t.register(conn.closeAll)
	return conn, nil
}

func (t *Transport) Listen(ctx context.Context, rawAddr string) (mesh.Listener, error) {
	serverTLS, err := t.serverTLS()
	if err != nil {
		return nil, err
	}
	if err := t.ensureOpen(); err != nil {
		return nil, err
	}

	listener := &listener{owner: t}
	if strings.HasPrefix(rawAddr, "relay+udp://") {
		endpoint, err := relay.ParseEndpoint(rawAddr)
		if err != nil {
			return nil, err
		}
		packetConn, err := relay.ListenPacket(ctx, endpoint)
		if err != nil {
			return nil, err
		}
		qtransport := &quicgo.Transport{Conn: packetConn}
		qlistener, err := qtransport.Listen(serverTLS, t.config(true))
		if err != nil {
			_ = qtransport.Close()
			return nil, fmt.Errorf("mesh/quic: listen via relay: %w", err)
		}
		listener.inner = qlistener
		listener.extraClose = qtransport.Close
	} else {
		addr, err := directAddress(rawAddr)
		if err != nil {
			return nil, err
		}
		qlistener, err := quicgo.ListenAddr(addr, serverTLS, t.config(false))
		if err != nil {
			return nil, fmt.Errorf("mesh/quic: listen %s: %w", redactAddress(rawAddr), err)
		}
		listener.inner = qlistener
	}
	listener.unregister = t.register(listener.closeAll)
	return listener, nil
}

// Close shuts down every listener and connection created by this transport.
// It is idempotent.
func (t *Transport) Close() error {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil
	}
	t.closed = true
	closers := make([]func() error, 0, len(t.closeFunc))
	for _, closeFn := range t.closeFunc {
		closers = append(closers, closeFn)
	}
	t.closeFunc = make(map[uint64]func() error)
	t.mu.Unlock()

	var errs []error
	for _, closeFn := range closers {
		if err := closeFn(); err != nil && !errors.Is(err, net.ErrClosed) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (t *Transport) config(relayed bool) *quicgo.Config {
	var cfg *quicgo.Config
	if t.quicCfg == nil {
		cfg = &quicgo.Config{}
	} else {
		cfg = t.quicCfg.Clone()
	}
	if relayed {
		// The outer routing envelope consumes HeaderSize bytes. Disabling
		// QUIC PMTU probing keeps the resulting UDP packets below common MTUs.
		cfg.DisablePathMTUDiscovery = true
	}
	return cfg
}

func (t *Transport) clientTLS() (*tls.Config, error) {
	if t.tlsCfg == nil || len(t.tlsCfg.Certificates) == 0 || t.tlsCfg.InsecureSkipVerify {
		return nil, ErrTLSRequired
	}
	return withALPN(t.tlsCfg), nil
}

func (t *Transport) serverTLS() (*tls.Config, error) {
	if t.tlsCfg == nil || len(t.tlsCfg.Certificates) == 0 ||
		t.tlsCfg.ClientAuth != tls.RequireAndVerifyClientCert || t.tlsCfg.ClientCAs == nil {
		return nil, ErrTLSRequired
	}
	return withALPN(t.tlsCfg), nil
}

func withALPN(base *tls.Config) *tls.Config {
	cfg := base.Clone()
	for _, proto := range cfg.NextProtos {
		if proto == alpn {
			return cfg
		}
	}
	cfg.NextProtos = append(cfg.NextProtos, alpn)
	return cfg
}

func authenticatedPeer(conn *quicgo.Conn) (mesh.PeerID, error) {
	certs := conn.ConnectionState().TLS.PeerCertificates
	if len(certs) == 0 {
		return "", ErrTLSRequired
	}
	return PeerIDFromCertificate(certs[0]), nil
}

func directAddress(raw string) (string, error) {
	if strings.HasPrefix(raw, "udp://") {
		u, err := url.Parse(raw)
		if err != nil || u.Host == "" || u.RawQuery != "" {
			return "", fmt.Errorf("mesh/quic: invalid udp endpoint")
		}
		return u.Host, nil
	}
	if raw == "" || strings.Contains(raw, "://") {
		return "", fmt.Errorf("mesh/quic: unsupported endpoint scheme")
	}
	return raw, nil
}

func redactAddress(raw string) string {
	if u, err := url.Parse(raw); err == nil && u.Host != "" {
		return u.Scheme + "://" + u.Host
	}
	return raw
}

func (t *Transport) ensureOpen() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return ErrClosed
	}
	return nil
}

// register returns an idempotent removal callback. Close copies callbacks
// before invoking them so these removals never deadlock the transport mutex.
func (t *Transport) register(closeFn func() error) func() {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		_ = closeFn()
		return func() {}
	}
	id := t.nextID
	t.nextID++
	t.closeFunc[id] = closeFn
	t.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			t.mu.Lock()
			delete(t.closeFunc, id)
			t.mu.Unlock()
		})
	}
}

type connection struct {
	stream     *quicgo.Stream
	conn       *quicgo.Conn
	peerID     mesh.PeerID
	extraClose func() error
	unregister func()
	closeOnce  sync.Once
	closeErr   error
}

func (c *connection) Read(p []byte) (int, error)  { return c.stream.Read(p) }
func (c *connection) Write(p []byte) (int, error) { return c.stream.Write(p) }
func (c *connection) PeerID() mesh.PeerID         { return c.peerID }

func (c *connection) Close() error {
	c.closeOnce.Do(func() {
		c.closeErr = c.closeAll()
		if c.unregister != nil {
			c.unregister()
		}
	})
	return c.closeErr
}

func (c *connection) closeAll() error {
	var errs []error
	if c.stream != nil {
		if err := c.stream.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if c.conn != nil {
		if err := c.conn.CloseWithError(0, "closed"); err != nil {
			errs = append(errs, err)
		}
	}
	if c.extraClose != nil {
		if err := c.extraClose(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

type listener struct {
	owner      *Transport
	inner      *quicgo.Listener
	extraClose func() error
	unregister func()
	closeOnce  sync.Once
	closeErr   error
}

func (l *listener) Accept(ctx context.Context) (mesh.Conn, error) {
	qconn, err := l.inner.Accept(ctx)
	if err != nil {
		return nil, err
	}
	peerID, err := authenticatedPeer(qconn)
	if err != nil {
		_ = qconn.CloseWithError(1, "peer certificate missing")
		return nil, err
	}
	stream, err := qconn.AcceptStream(ctx)
	if err != nil {
		_ = qconn.CloseWithError(1, "accept stream failed")
		return nil, fmt.Errorf("mesh/quic: accept stream: %w", err)
	}
	conn := &connection{stream: stream, conn: qconn, peerID: peerID}
	conn.unregister = l.owner.register(conn.closeAll)
	return conn, nil
}

func (l *listener) Close() error {
	l.closeOnce.Do(func() {
		l.closeErr = l.closeAll()
		if l.unregister != nil {
			l.unregister()
		}
	})
	return l.closeErr
}

func (l *listener) closeAll() error {
	var errs []error
	if l.inner != nil {
		if err := l.inner.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if l.extraClose != nil {
		if err := l.extraClose(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// Addr exposes the bound direct address for integration tests and operators.
// Relay listeners return their local relay-client socket, not a public route.
func (l *listener) Addr() net.Addr { return l.inner.Addr() }

var _ mesh.StreamTransport = (*Transport)(nil)
var _ mesh.Conn = (*connection)(nil)
var _ mesh.Listener = (*listener)(nil)
