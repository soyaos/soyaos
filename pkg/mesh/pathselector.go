// SilentCut reverse-pressure: DD-011 wants the Moon → Comet artifact path
// to pick the cheapest viable route automatically: same-LAN direct first
// (zero egress, lowest RTT), then the WireGuard overlay (still no Planet
// hop, just a tunnel), and only as a last resort the Planet-relay
// fallback (Planet sees the encrypted bytes but not the plaintext MP4 —
// it's still an mTLS pipe end-to-end between Moon and Comet).
//
// This file owns the decision; the actual transport is whatever satisfies
// mesh.StreamTransport (today: pkg/mesh/quic).

package mesh

import (
	"context"
	"errors"
)

// PathStrategy decides which connectivity path Moon should use to reach
// Comet for a given task. Implementations are stateless and pure — the
// scheduler may call Select repeatedly without coordination. ctx is
// passed through for future implementations that consult an async
// reachability probe.
type PathStrategy interface {
	// Select returns the chosen PathChoice for the given list of peer
	// candidates. The first candidate is the Moon's preferred peer; the
	// selector ranks across the rest if the first is not directly
	// reachable.
	Select(ctx context.Context, peers []PeerCandidate) (PathChoice, error)
}

// PeerCandidate describes one possible way to reach a Comet peer.
// Fields are independent: a peer can be SameLAN *and* expose an
// OverlayIP; the strategy picks the cheapest applicable one.
type PeerCandidate struct {
	// PeerID is the authenticated identity of the candidate.
	PeerID PeerID
	// SameLAN is true when the planner has evidence Moon and Comet are
	// on the same /24 (or equivalent). When true, direct-lan wins.
	SameLAN bool
	// OverlayIP is the WireGuard overlay address, if the peer is on the
	// overlay. Empty means "no overlay reachability".
	OverlayIP string
	// PublicHints are ICE-like server-reflexive addresses Planet
	// advertised for this peer. Used for the relay fallback.
	PublicHints []string
}

// PathChoice is the picked route.
type PathChoice struct {
	// Strategy names the bucket: "direct-lan" | "overlay-wg" |
	// "planet-relay". Callers branch on this to pick metrics labels and
	// failure-handling policy.
	Strategy string
	// Endpoint is the transport-specific address the chosen Conn should
	// Dial. For direct-lan: "udp://<peer-lan-ip>:443" (caller resolves
	// peer-lan-ip out of band). For overlay-wg: the OverlayIP. For
	// planet-relay: the first PublicHint.
	Endpoint string
}

// ErrNoPath is returned by Select when none of the candidates are usable.
var ErrNoPath = errors.New("mesh: no usable path to any candidate")

// DefaultPathStrategy implements the §A1 priority: same-LAN direct →
// WireGuard overlay → Planet relay. The first peer that exposes one of
// those wins; the strategy never traverses the candidate list looking
// for a strictly cheaper route once a candidate matches.
type DefaultPathStrategy struct{}

// Select walks the candidates in order; for each it checks the three
// route classes top-to-bottom. The first match becomes the PathChoice.
func (DefaultPathStrategy) Select(_ context.Context, peers []PeerCandidate) (PathChoice, error) {
	for _, c := range peers {
		if c.SameLAN {
			return PathChoice{Strategy: "direct-lan", Endpoint: string(c.PeerID)}, nil
		}
		if c.OverlayIP != "" {
			return PathChoice{Strategy: "overlay-wg", Endpoint: c.OverlayIP}, nil
		}
		if len(c.PublicHints) > 0 {
			return PathChoice{Strategy: "planet-relay", Endpoint: c.PublicHints[0]}, nil
		}
	}
	return PathChoice{}, ErrNoPath
}
