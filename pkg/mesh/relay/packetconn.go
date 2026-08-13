package relay

import (
	"context"
	"crypto/subtle"
	"fmt"
	"io"
	"net"
	"net/url"
	"strings"
	"time"
)

// Endpoint is the client-side description of a relay session. Static Cloud
// configuration contains only the relay address; Planet adds Token and Side
// when it grants a short-lived route to a Moon/Comet pair.
type Endpoint struct {
	RelayAddr *net.UDPAddr
	Token     Token
	Side      Side
}

// ParseEndpoint accepts the transport URI used by mesh.PathChoice.Endpoint:
//
//	relay+udp://relay-us-west.soyaos.ai:7443?token=...&side=moon
func ParseEndpoint(raw string) (Endpoint, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "relay+udp" || u.Host == "" {
		return Endpoint{}, fmt.Errorf("relay: endpoint must use relay+udp://host:port")
	}
	relayAddr, err := net.ResolveUDPAddr("udp", u.Host)
	if err != nil {
		return Endpoint{}, fmt.Errorf("relay: resolve endpoint: %w", err)
	}
	token, err := ParseToken(u.Query().Get("token"))
	if err != nil {
		return Endpoint{}, err
	}
	if !time.Now().Before(token.ExpiresAt()) {
		return Endpoint{}, ErrInvalidToken
	}
	var side Side
	switch strings.ToLower(u.Query().Get("side")) {
	case "moon":
		side = SideMoon
	case "comet":
		side = SideComet
	default:
		return Endpoint{}, fmt.Errorf("relay: endpoint side must be moon or comet")
	}
	return Endpoint{RelayAddr: relayAddr, Token: token, Side: side}, nil
}

// URI constructs a relay endpoint without exposing the token in logs unless
// the caller explicitly formats the returned string.
func URI(relayAddr string, token Token, side Side) (string, error) {
	if !side.valid() {
		return "", fmt.Errorf("relay: invalid side")
	}
	u := &url.URL{Scheme: "relay+udp", Host: relayAddr}
	q := u.Query()
	q.Set("token", token.String())
	if side == SideMoon {
		q.Set("side", "moon")
	} else {
		q.Set("side", "comet")
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// PacketConn adapts the authenticated relay envelope to net.PacketConn. It is
// intentionally below quic-go: the QUIC stack still performs the end-to-end
// TLS handshake and encrypts every application byte before WriteTo is called.
type PacketConn struct {
	conn      *net.UDPConn
	relayAddr *net.UDPAddr
	token     Token
	side      Side
	peerAddr  *net.UDPAddr
}

// ListenPacket creates an ephemeral local UDP socket and registers it as one
// side of the relay session. Registration contains no application payload.
func ListenPacket(ctx context.Context, endpoint Endpoint) (*PacketConn, error) {
	network := "udp6"
	local := &net.UDPAddr{IP: net.IPv6unspecified, Port: 0}
	if endpoint.RelayAddr.IP.To4() != nil {
		network = "udp4"
		local = &net.UDPAddr{IP: net.IPv4zero, Port: 0}
	}
	conn, err := net.ListenUDP(network, local)
	if err != nil {
		return nil, fmt.Errorf("relay: open client socket: %w", err)
	}
	pc := &PacketConn{
		conn:      conn,
		relayAddr: endpoint.RelayAddr,
		token:     endpoint.Token,
		side:      endpoint.Side,
		peerAddr:  endpoint.RelayAddr,
	}
	if err := pc.Register(ctx); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return pc, nil
}

// Register lets the relay bind this session side to the socket's public UDP
// address before QUIC starts. The deadline bounds setup when routing is down.
func (c *PacketConn) Register(ctx context.Context) error {
	deadline, hasDeadline := ctx.Deadline()
	if !hasDeadline {
		deadline = time.Now().Add(5 * time.Second)
	}
	if err := c.conn.SetWriteDeadline(deadline); err != nil {
		return err
	}
	_, err := c.conn.WriteToUDP(encodeFrame(c.token, c.side, nil), c.relayAddr)
	_ = c.conn.SetWriteDeadline(time.Time{})
	if err != nil {
		return fmt.Errorf("relay: register endpoint: %w", err)
	}
	return nil
}

func (c *PacketConn) ReadFrom(p []byte) (int, net.Addr, error) {
	frame := make([]byte, len(p)+HeaderSize)
	for {
		n, source, err := c.conn.ReadFromUDP(frame)
		if err != nil {
			return 0, nil, err
		}
		if !source.IP.Equal(c.relayAddr.IP) || source.Port != c.relayAddr.Port {
			continue
		}
		token, side, payload, err := decodeFrame(frame[:n])
		if err != nil || side != c.side.other() || len(payload) == 0 {
			continue
		}
		if subtle.ConstantTimeCompare(token.raw[:], c.token.raw[:]) != 1 {
			continue
		}
		if len(payload) > len(p) {
			return 0, nil, io.ErrShortBuffer
		}
		copy(p, payload)
		return len(payload), c.peerAddr, nil
	}
}

func (c *PacketConn) WriteTo(p []byte, _ net.Addr) (int, error) {
	if len(p)+HeaderSize > maxUDPSize {
		return 0, fmt.Errorf("relay: datagram too large: %d", len(p))
	}
	if _, err := c.conn.WriteToUDP(encodeFrame(c.token, c.side, p), c.relayAddr); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (c *PacketConn) Close() error                       { return c.conn.Close() }
func (c *PacketConn) LocalAddr() net.Addr                { return c.conn.LocalAddr() }
func (c *PacketConn) SetReadBuffer(bytes int) error      { return c.conn.SetReadBuffer(bytes) }
func (c *PacketConn) SetWriteBuffer(bytes int) error     { return c.conn.SetWriteBuffer(bytes) }
func (c *PacketConn) SetDeadline(t time.Time) error      { return c.conn.SetDeadline(t) }
func (c *PacketConn) SetReadDeadline(t time.Time) error  { return c.conn.SetReadDeadline(t) }
func (c *PacketConn) SetWriteDeadline(t time.Time) error { return c.conn.SetWriteDeadline(t) }

var _ net.PacketConn = (*PacketConn)(nil)
