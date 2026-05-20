// SilentCut reverse-pressure: NFS is the protocol of choice for TrueNAS /
// Linux-native fileservers. Same contract as smb; wire implementation lands
// in Stage 5.

package nas

import (
	"context"
	"io"
)

// nfsHandle is the alpha stub. Stage 5 will wrap a go-nfs-client mount.
//
// TODO(Stage5): github.com/willscott/go-nfs-client — DialMount(host),
// Mount(share), Create + Write + Commit per artifact.
type nfsHandle struct {
	cfg Config
}

func openNFS(_ context.Context, cfg Config) (NAS, error) {
	return &nfsHandle{cfg: cfg}, nil
}

func (h *nfsHandle) Write(context.Context, string, io.Reader) (int64, error) {
	return 0, ErrNotImplemented
}

func (h *nfsHandle) Close() error {
	wipe(&h.cfg)
	return nil
}
