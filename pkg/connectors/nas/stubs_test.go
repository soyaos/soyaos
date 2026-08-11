package nas

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// TestStubsReturnErrNotImplemented covers smb/nfs/s3 — Open must succeed (so
// the contract is exercisable in unit tests) but Write must surface
// ErrNotImplemented so the scheduler knows to fall back.
func TestStubsReturnErrNotImplemented(t *testing.T) {
	for _, proto := range []string{"smb", "nfs", "s3"} {
		t.Run(proto, func(t *testing.T) {
			h, err := Open(context.Background(), Config{
				Protocol: proto,
				Host:     "example.invalid",
				Share:    "data",
				Username: "alice",
				Password: "s3cret",
				Bucket:   "b",
				Region:   "r",
			})
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			defer h.Close()
			if _, err := h.Write(context.Background(), "/x", strings.NewReader("y")); !errors.Is(err, ErrNotImplemented) {
				t.Errorf("Write err=%v, want ErrNotImplemented", err)
			}
		})
	}
}

func TestStubsWipeCredentialsOnClose(t *testing.T) {
	for _, proto := range []string{"smb", "nfs", "s3"} {
		t.Run(proto, func(t *testing.T) {
			h, err := Open(context.Background(), Config{
				Protocol: proto,
				Host:     "example.invalid",
				Username: "alice",
				Password: "s3cret",
			})
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			if err := h.Close(); err != nil {
				t.Fatalf("Close: %v", err)
			}
			// Peek into the concrete to verify password wipe.
			switch v := h.(type) {
			case *smbHandle:
				if v.cfg.Password != "" {
					t.Errorf("smb Close did not wipe Password")
				}
			case *nfsHandle:
				if v.cfg.Password != "" {
					t.Errorf("nfs Close did not wipe Password")
				}
			case *s3Handle:
				if v.cfg.Password != "" {
					t.Errorf("s3 Close did not wipe Password")
				}
			default:
				t.Fatalf("unexpected handle type %T", h)
			}
		})
	}
}

func TestStubsSkipRealIntegration(t *testing.T) {
	t.Skip("smb/nfs/s3 real-network integration is an alpha stub; wire clients land in Stage 5")
}
