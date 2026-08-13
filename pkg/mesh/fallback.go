package mesh

import (
	"context"
	"errors"
	"fmt"
)

// DialResult records which route actually connected. It is the evidence used
// by metrics and by SilentCut's WireGuard-blocked fallback acceptance test.
type DialResult struct {
	Conn Conn
	Path PathChoice
}

// FallbackDialer attempts the cheapest routes in order and continues after a
// transport failure: direct LAN → WireGuard overlay → Planet relay. This is
// distinct from PathStrategy.Select, which only chooses from advertised data
// and cannot observe a failed dial.
type FallbackDialer struct {
	Transport StreamTransport
}

func (d FallbackDialer) Dial(ctx context.Context, candidate PeerCandidate) (DialResult, error) {
	if d.Transport == nil {
		return DialResult{}, fmt.Errorf("mesh: fallback transport is required")
	}
	paths := candidatePaths(candidate)
	if len(paths) == 0 {
		return DialResult{}, ErrNoPath
	}

	var attempts []error
	for _, path := range paths {
		conn, err := d.Transport.Dial(ctx, candidate.PeerID, path.Endpoint)
		if err == nil {
			return DialResult{Conn: conn, Path: path}, nil
		}
		attempts = append(attempts, fmt.Errorf("%s: %w", path.Strategy, err))
		if ctx.Err() != nil {
			break
		}
	}
	return DialResult{}, fmt.Errorf("%w: %w", ErrNoPath, errors.Join(attempts...))
}

func candidatePaths(candidate PeerCandidate) []PathChoice {
	paths := make([]PathChoice, 0, 2+len(candidate.PublicHints))
	if candidate.SameLAN {
		endpoint := candidate.DirectAddr
		if endpoint == "" {
			endpoint = string(candidate.PeerID)
		}
		if endpoint != "" {
			paths = append(paths, PathChoice{Strategy: "direct-lan", Endpoint: endpoint})
		}
	}
	if candidate.OverlayIP != "" {
		paths = append(paths, PathChoice{Strategy: "overlay-wg", Endpoint: candidate.OverlayIP})
	}
	for _, endpoint := range candidate.PublicHints {
		if endpoint != "" {
			paths = append(paths, PathChoice{Strategy: "planet-relay", Endpoint: endpoint})
		}
	}
	return paths
}
