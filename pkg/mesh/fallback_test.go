package mesh

import (
	"context"
	"errors"
	"io"
	"reflect"
	"testing"
)

func TestFallbackDialer_WireGuardFailureUsesRelay(t *testing.T) {
	transport := &recordingStreamTransport{
		fail: map[string]error{"10.0.0.5:7443": errors.New("wireguard blocked")},
	}
	dialer := FallbackDialer{Transport: transport}
	result, err := dialer.Dial(context.Background(), PeerCandidate{
		PeerID:      "peer-1",
		OverlayIP:   "10.0.0.5:7443",
		PublicHints: []string{"relay+udp://relay.example:7443?token=test&side=comet"},
	})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	if result.Path.Strategy != "planet-relay" {
		t.Fatalf("strategy=%q, want planet-relay", result.Path.Strategy)
	}
	wantAttempts := []string{
		"10.0.0.5:7443",
		"relay+udp://relay.example:7443?token=test&side=comet",
	}
	if !reflect.DeepEqual(transport.attempts, wantAttempts) {
		t.Fatalf("attempts=%v, want %v", transport.attempts, wantAttempts)
	}
}

func TestFallbackDialer_AllRoutesFail(t *testing.T) {
	transport := &recordingStreamTransport{defaultErr: errors.New("offline")}
	_, err := (FallbackDialer{Transport: transport}).Dial(context.Background(), PeerCandidate{
		PeerID:      "peer-1",
		DirectAddr:  "192.168.1.7:7443",
		SameLAN:     true,
		OverlayIP:   "10.0.0.5:7443",
		PublicHints: []string{"relay.example:7443"},
	})
	if !errors.Is(err, ErrNoPath) {
		t.Fatalf("err=%v, want ErrNoPath", err)
	}
	if len(transport.attempts) != 3 {
		t.Fatalf("attempts=%d, want 3", len(transport.attempts))
	}
}

type recordingStreamTransport struct {
	attempts   []string
	fail       map[string]error
	defaultErr error
}

func (t *recordingStreamTransport) Dial(_ context.Context, _ PeerID, addr string) (Conn, error) {
	t.attempts = append(t.attempts, addr)
	if err := t.fail[addr]; err != nil {
		return nil, err
	}
	if t.defaultErr != nil {
		return nil, t.defaultErr
	}
	return nopConn{}, nil
}

func (*recordingStreamTransport) Listen(context.Context, string) (Listener, error) {
	return nil, errors.New("not used")
}
func (*recordingStreamTransport) Close() error { return nil }

type nopConn struct{}

func (nopConn) Read([]byte) (int, error)    { return 0, io.EOF }
func (nopConn) Write(p []byte) (int, error) { return len(p), nil }
func (nopConn) Close() error                { return nil }
func (nopConn) PeerID() PeerID              { return "peer-1" }
