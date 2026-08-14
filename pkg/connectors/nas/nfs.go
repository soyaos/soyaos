// SilentCut reverse-pressure: NFS is the protocol of choice for TrueNAS and
// Linux-native fileservers. This implementation uses userspace NFSv3 and
// AUTH_SYS, so it needs no root mount or host filesystem mutation.

package nas

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"sync"

	nfsclient "github.com/willscott/go-nfs-client/nfs"
	"github.com/willscott/go-nfs-client/nfs/rpc"
)

type nfsHandle struct {
	mu     sync.Mutex
	cfg    Config
	remote nfsRemote
	closed bool
}

type nfsRemote interface {
	mkdirAll(string) error
	openFile(string) (io.WriteCloser, error)
	remove(string) error
	close() error
}

type nfs3Remote struct {
	mount  *nfsclient.Mount
	target *nfsclient.Target
}

func openNFS(ctx context.Context, cfg Config) (NAS, error) {
	host, err := nfsHost(cfg.Host)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(cfg.Share) == "" || !strings.HasPrefix(cfg.Share, "/") {
		return nil, errors.Join(ErrInvalidConfig, errors.New("nfs share must be an absolute export path"))
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	uid, gid := cfg.NFSUID, cfg.NFSGID
	if cfg.NFSUseProcessIDs {
		uid, gid = uint32(os.Getuid()), uint32(os.Getgid())
	}
	machine := cfg.NFSMachine
	if machine == "" {
		machine, _ = os.Hostname()
	}
	if machine == "" {
		machine = "soyaos"
	}
	mount, err := nfsclient.DialMount(host, timeoutFor(cfg))
	if err != nil {
		return nil, fmt.Errorf("nfs: dial mount service: %w", err)
	}
	target, err := mount.Mount(cfg.Share, rpc.NewAuthUnix(machine, uid, gid).Auth())
	if err != nil {
		mount.Client.Close()
		return nil, fmt.Errorf("nfs: mount export: %w", err)
	}
	if err := ctx.Err(); err != nil {
		target.Client.Close()
		_ = mount.Unmount()
		mount.Client.Close()
		return nil, err
	}
	return &nfsHandle{cfg: cfg, remote: &nfs3Remote{mount: mount, target: target}}, nil
}

func (h *nfsHandle) Write(ctx context.Context, name string, r io.Reader) (int64, error) {
	cleaned, err := cleanRemotePath(name)
	if err != nil {
		return 0, err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return 0, ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if dir := path.Dir(cleaned); dir != "." {
		if err := h.remote.mkdirAll(dir); err != nil {
			return 0, fmt.Errorf("nfs: create parent directory: %w", err)
		}
	}
	if err := h.remote.remove(cleaned); err != nil && !errors.Is(err, os.ErrNotExist) {
		return 0, fmt.Errorf("nfs: replace file: %w", err)
	}
	f, err := h.remote.openFile(cleaned)
	if err != nil {
		return 0, fmt.Errorf("nfs: create file: %w", err)
	}
	n, copyErr := io.Copy(f, &contextReader{ctx: ctx, r: r})
	closeErr := f.Close()
	if err := errors.Join(copyErr, closeErr); err != nil {
		return n, fmt.Errorf("nfs: write file: %w", err)
	}
	return n, nil
}

func (h *nfsHandle) Close() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return nil
	}
	h.closed = true
	var err error
	if h.remote != nil {
		err = h.remote.close()
		h.remote = nil
	}
	wipe(&h.cfg)
	return err
}

func (r *nfs3Remote) mkdirAll(name string) error {
	current := ""
	for _, part := range strings.Split(name, "/") {
		if current == "" {
			current = part
		} else {
			current += "/" + part
		}
		if _, _, err := r.target.Lookup(current); err == nil {
			continue
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if _, err := r.target.Mkdir(current, 0o750); err != nil && !errors.Is(err, os.ErrExist) {
			return err
		}
	}
	return nil
}

func (r *nfs3Remote) openFile(name string) (io.WriteCloser, error) {
	return r.target.OpenFile(name, 0o640)
}

func (r *nfs3Remote) remove(name string) error { return r.target.Remove(name) }

func (r *nfs3Remote) close() error {
	// Target and Mount use separate RPC connections.
	r.target.Client.Close()
	err := r.mount.Unmount()
	r.mount.Client.Close()
	return err
}

func nfsHost(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "nfs://") {
		raw = strings.TrimPrefix(raw, "nfs://")
	}
	raw = strings.TrimSuffix(raw, "/")
	if raw == "" || strings.ContainsAny(raw, "/?#@") || strings.Contains(raw, ":") {
		return "", errors.Join(ErrInvalidConfig, errors.New("nfs host must be a hostname or IPv4 address without a port"))
	}
	return raw, nil
}

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r *contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}
