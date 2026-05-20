// SilentCut reverse-pressure: SMB is the dominant NAS protocol on Synology /
// Windows fileservers, which is where most prosumer-grade SilentCut users
// store master clips. The contract here is final; the wire implementation
// arrives in Stage 5.

package nas

import (
	"context"
	"io"
)

// smbHandle is the alpha stub. Stage 5 will wrap a go-smb2 session.
//
// TODO(Stage5): github.com/hirochachacha/go-smb2 — Dial(host:445),
// NewSession with NTLM, OpenMount(share), then for each Write
// CreateFile + io.Copy + Close. Persist nothing; reconnect on every
// Open to match the Moon-token-lifetime model.
type smbHandle struct {
	cfg Config
}

func openSMB(_ context.Context, cfg Config) (NAS, error) {
	return &smbHandle{cfg: cfg}, nil
}

func (h *smbHandle) Write(context.Context, string, io.Reader) (int64, error) {
	return 0, ErrNotImplemented
}

func (h *smbHandle) Close() error {
	// Wipe credential bytes even in the stub so callers can rely on the
	// "Close erases secrets" contract during table-driven tests.
	wipe(&h.cfg)
	return nil
}
