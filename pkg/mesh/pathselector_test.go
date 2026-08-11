package mesh

import (
	"context"
	"errors"
	"testing"
)

func TestDefaultPathStrategy_PrefersSameLAN(t *testing.T) {
	got, err := DefaultPathStrategy{}.Select(context.Background(), []PeerCandidate{
		{PeerID: "moon-1", SameLAN: true, OverlayIP: "10.0.0.5", PublicHints: []string{"203.0.113.1:443"}},
	})
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if got.Strategy != "direct-lan" {
		t.Errorf("Strategy=%q, want direct-lan", got.Strategy)
	}
}

func TestDefaultPathStrategy_FallsBackToOverlay(t *testing.T) {
	got, err := DefaultPathStrategy{}.Select(context.Background(), []PeerCandidate{
		{PeerID: "moon-1", SameLAN: false, OverlayIP: "10.0.0.5", PublicHints: []string{"203.0.113.1:443"}},
	})
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if got.Strategy != "overlay-wg" {
		t.Errorf("Strategy=%q, want overlay-wg", got.Strategy)
	}
	if got.Endpoint != "10.0.0.5" {
		t.Errorf("Endpoint=%q, want 10.0.0.5", got.Endpoint)
	}
}

func TestDefaultPathStrategy_FallsBackToPlanetRelay(t *testing.T) {
	got, err := DefaultPathStrategy{}.Select(context.Background(), []PeerCandidate{
		{PeerID: "moon-1", SameLAN: false, OverlayIP: "", PublicHints: []string{"203.0.113.1:443"}},
	})
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if got.Strategy != "planet-relay" {
		t.Errorf("Strategy=%q, want planet-relay", got.Strategy)
	}
	if got.Endpoint != "203.0.113.1:443" {
		t.Errorf("Endpoint=%q, want 203.0.113.1:443", got.Endpoint)
	}
}

func TestDefaultPathStrategy_NoUsablePath(t *testing.T) {
	_, err := DefaultPathStrategy{}.Select(context.Background(), []PeerCandidate{
		{PeerID: "moon-1"},
	})
	if !errors.Is(err, ErrNoPath) {
		t.Fatalf("err=%v, want ErrNoPath", err)
	}
}

func TestDefaultPathStrategy_EmptyPeers(t *testing.T) {
	_, err := DefaultPathStrategy{}.Select(context.Background(), nil)
	if !errors.Is(err, ErrNoPath) {
		t.Fatalf("err=%v, want ErrNoPath", err)
	}
}

// Ensure DefaultPathStrategy satisfies the PathStrategy interface at
// compile time.
var _ PathStrategy = DefaultPathStrategy{}
