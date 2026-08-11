package quic

import (
	"context"
	"crypto/tls"
	"errors"
	"testing"
)

func TestTransport_NewKeepsTLSConfig(t *testing.T) {
	cfg := &tls.Config{ServerName: "moon.example"}
	tr := New(cfg)
	if tr.TLSConfig() != cfg {
		t.Fatal("tls.Config should be stored verbatim")
	}
}

func TestTransport_StubsReturnErrNotImplemented(t *testing.T) {
	tr := New(nil)
	ctx := context.Background()
	if _, err := tr.Dial(ctx, "peer", "udp/example:443"); !errors.Is(err, ErrNotImplemented) {
		t.Errorf("Dial err=%v, want ErrNotImplemented", err)
	}
	if _, err := tr.Listen(ctx, ":0"); !errors.Is(err, ErrNotImplemented) {
		t.Errorf("Listen err=%v, want ErrNotImplemented", err)
	}
	if err := tr.Close(); err != nil {
		t.Errorf("Close on idle transport must succeed, got %v", err)
	}
}

func TestTransport_SkippedIntegration(t *testing.T) {
	t.Skip("QUIC wire is an alpha stub; quic-go integration lands in Stage 5")
}
