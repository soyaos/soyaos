package relay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync/atomic"
	"time"
)

// ServerConfig controls authentication, free-quota rate limiting, and stale
// routing state. Payloads are never retained or included in metrics.
type ServerConfig struct {
	Secret             []byte
	RateBytesPerSecond int64
	BurstBytes         int64
	SessionIdleTimeout time.Duration
	Now                func() time.Time
}

// Stats is a payload-free snapshot safe to expose from /healthz.
type Stats struct {
	ActiveSessions  int    `json:"active_sessions"`
	ForwardedBytes  uint64 `json:"forwarded_bytes"`
	ForwardedFrames uint64 `json:"forwarded_frames"`
	DroppedFrames   uint64 `json:"dropped_frames"`
}

type counters struct {
	forwardedBytes  atomic.Uint64
	forwardedFrames atomic.Uint64
	droppedFrames   atomic.Uint64
	activeSessions  atomic.Int64
}

type session struct {
	addresses [2]*net.UDPAddr
	expiresAt time.Time
	lastSeen  time.Time
	bucket    tokenBucket
}

type tokenBucket struct {
	tokens float64
	last   time.Time
}

// Server forwards authenticated UDP envelopes without terminating or parsing
// the QUIC connection contained inside them.
type Server struct {
	conn     net.PacketConn
	secret   []byte
	rate     int64
	burst    int64
	idle     time.Duration
	now      func() time.Time
	counters counters
}

func NewServer(conn net.PacketConn, cfg ServerConfig) (*Server, error) {
	if conn == nil {
		return nil, fmt.Errorf("relay: packet connection is required")
	}
	if len(cfg.Secret) < 32 {
		return nil, ErrSecretTooShort
	}
	if cfg.RateBytesPerSecond <= 0 {
		cfg.RateBytesPerSecond = 10 * 1024 * 1024 / 8 // 10 Mbit/s free quota.
	}
	if cfg.BurstBytes <= 0 {
		cfg.BurstBytes = 4 * 1024 * 1024
	}
	if cfg.SessionIdleTimeout <= 0 {
		cfg.SessionIdleTimeout = 10 * time.Minute
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Server{
		conn:   conn,
		secret: append([]byte(nil), cfg.Secret...),
		rate:   cfg.RateBytesPerSecond,
		burst:  cfg.BurstBytes,
		idle:   cfg.SessionIdleTimeout,
		now:    cfg.Now,
	}, nil
}

// Serve runs until the context is cancelled, the socket is closed, or an
// unrecoverable read error occurs. A single loop owns the routing map, so
// address binding and quota decisions are deterministic without lock races.
func (s *Server) Serve(ctx context.Context) error {
	sessions := make(map[[sessionIDSize]byte]*session)
	buffer := make([]byte, maxUDPSize)
	nextCleanup := s.now()
	cleanupInterval := minDuration(time.Minute, s.idle/2)
	if cleanupInterval <= 0 {
		cleanupInterval = time.Minute
	}

	go func() {
		<-ctx.Done()
		_ = s.conn.Close()
	}()

	for {
		n, source, err := s.conn.ReadFrom(buffer)
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return fmt.Errorf("relay: read datagram: %w", err)
		}
		now := s.now()
		if !now.Before(nextCleanup) {
			for id, sess := range sessions {
				if !now.Before(sess.expiresAt) || now.Sub(sess.lastSeen) > s.idle {
					delete(sessions, id)
					s.counters.activeSessions.Add(-1)
				}
			}
			nextCleanup = now.Add(cleanupInterval)
		}
		token, side, payload, err := decodeFrame(buffer[:n])
		if err != nil || !token.Valid(s.secret, now) {
			s.counters.droppedFrames.Add(1)
			continue
		}

		id := token.sessionID()
		sess := sessions[id]
		if sess == nil {
			sess = &session{
				expiresAt: token.ExpiresAt(),
				lastSeen:  now,
				bucket: tokenBucket{
					tokens: float64(s.burst),
					last:   now,
				},
			}
			sessions[id] = sess
			s.counters.activeSessions.Add(1)
		}
		sess.lastSeen = now

		sideIndex := int(side)
		if sess.addresses[sideIndex] == nil {
			sess.addresses[sideIndex] = cloneUDPAddr(source)
		} else if !sameUDPAddr(sess.addresses[sideIndex], source) {
			// First writer wins for each side. A stolen bearer token cannot
			// replace an endpoint that already registered the session.
			s.counters.droppedFrames.Add(1)
			continue
		}
		if len(payload) == 0 {
			continue // Registration frame.
		}
		if !sess.bucket.allow(int64(len(payload)), s.rate, s.burst, now) {
			s.counters.droppedFrames.Add(1)
			continue
		}
		destination := sess.addresses[int(side.other())]
		if destination == nil {
			s.counters.droppedFrames.Add(1)
			continue
		}
		if _, err := s.conn.WriteTo(buffer[:n], destination); err != nil {
			s.counters.droppedFrames.Add(1)
			continue
		}
		s.counters.forwardedFrames.Add(1)
		s.counters.forwardedBytes.Add(uint64(len(payload)))
	}
}

func (s *Server) Close() error { return s.conn.Close() }

func (s *Server) Stats() Stats {
	return Stats{
		ActiveSessions:  int(s.counters.activeSessions.Load()),
		ForwardedBytes:  s.counters.forwardedBytes.Load(),
		ForwardedFrames: s.counters.forwardedFrames.Load(),
		DroppedFrames:   s.counters.droppedFrames.Load(),
	}
}

// HealthHandler reports only counters and never session tokens, addresses, or
// payloads. It is suitable for a loopback health endpoint and uptime probes.
func (s *Server) HealthHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(struct {
			Status string `json:"status"`
			Stats
		}{Status: "ok", Stats: s.Stats()})
	})
}

func (b *tokenBucket) allow(size, rate, burst int64, now time.Time) bool {
	elapsed := now.Sub(b.last).Seconds()
	if elapsed > 0 {
		b.tokens += elapsed * float64(rate)
		if b.tokens > float64(burst) {
			b.tokens = float64(burst)
		}
		b.last = now
	}
	if float64(size) > b.tokens {
		return false
	}
	b.tokens -= float64(size)
	return true
}

func cloneUDPAddr(addr net.Addr) *net.UDPAddr {
	u, ok := addr.(*net.UDPAddr)
	if !ok {
		return nil
	}
	return &net.UDPAddr{IP: append(net.IP(nil), u.IP...), Port: u.Port, Zone: u.Zone}
}

func sameUDPAddr(a *net.UDPAddr, b net.Addr) bool {
	other, ok := b.(*net.UDPAddr)
	return ok && a.Port == other.Port && a.Zone == other.Zone && a.IP.Equal(other.IP)
}

func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}
