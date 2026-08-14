// SilentCut reverse-pressure: SMB is the dominant NAS protocol on Synology /
// QNAP and Windows fileservers, which is where many prosumer-grade SilentCut
// users store master clips.

package nas

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/hirochachacha/go-smb2"
)

type smbHandle struct {
	mu     sync.Mutex
	cfg    Config
	remote smbRemote
	closed bool
}

type smbRemote interface {
	mkdirAll(context.Context, string) error
	create(context.Context, string) (io.WriteCloser, error)
	close() error
}

type smb2Remote struct {
	conn    net.Conn
	session *smb2.Session
	share   *smb2.Share
}

func openSMB(ctx context.Context, cfg Config) (NAS, error) {
	if strings.TrimSpace(cfg.Host) == "" || strings.TrimSpace(cfg.Share) == "" {
		return nil, errors.Join(ErrInvalidConfig, errors.New("smb host and share are required"))
	}
	addr, err := smbAddress(cfg.Host)
	if err != nil {
		return nil, err
	}
	dialCtx, cancel := context.WithTimeout(ctx, timeoutFor(cfg))
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(dialCtx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("smb: dial: %w", err)
	}
	if deadline, ok := dialCtx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	dialer := &smb2.Dialer{Initiator: &smb2.NTLMInitiator{
		User: cfg.Username, Password: cfg.Password, Domain: cfg.Domain,
	}}
	session, err := dialer.Dial(conn)
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("smb: authenticate: %w", err)
	}
	share, err := session.Mount(cfg.Share)
	if err != nil {
		_ = session.Logoff()
		_ = conn.Close()
		return nil, fmt.Errorf("smb: mount share: %w", err)
	}
	_ = conn.SetDeadline(time.Time{})
	return &smbHandle{cfg: cfg, remote: &smb2Remote{conn: conn, session: session, share: share}}, nil
}

func (h *smbHandle) Write(ctx context.Context, name string, r io.Reader) (int64, error) {
	cleaned, err := cleanRemotePath(name)
	if err != nil {
		return 0, err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return 0, ErrClosed
	}
	if dir := path.Dir(cleaned); dir != "." {
		if err := h.remote.mkdirAll(ctx, dir); err != nil {
			return 0, fmt.Errorf("smb: create parent directory: %w", err)
		}
	}
	f, err := h.remote.create(ctx, cleaned)
	if err != nil {
		return 0, fmt.Errorf("smb: create file: %w", err)
	}
	n, copyErr := io.Copy(f, r)
	closeErr := f.Close()
	if err := errors.Join(copyErr, closeErr); err != nil {
		return n, fmt.Errorf("smb: write file: %w", err)
	}
	return n, nil
}

func (h *smbHandle) Close() error {
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

func (r *smb2Remote) mkdirAll(ctx context.Context, name string) error {
	return r.share.WithContext(ctx).MkdirAll(name, 0o750)
}

func (r *smb2Remote) create(ctx context.Context, name string) (io.WriteCloser, error) {
	return r.share.WithContext(ctx).Create(name)
}

func (r *smb2Remote) close() error {
	var errs []error
	for _, err := range []error{r.share.Umount(), r.session.Logoff(), r.conn.Close()} {
		if err != nil && !errors.Is(err, net.ErrClosed) && !strings.Contains(strings.ToLower(err.Error()), "use of closed network connection") {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func smbAddress(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if strings.Contains(raw, "://") {
		u, err := url.Parse(raw)
		if err != nil || u.Scheme != "smb" || u.Host == "" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
			return "", errors.Join(ErrInvalidConfig, errors.New("smb host must be smb://host[:port] without credentials or path"))
		}
		raw = u.Host
	}
	if _, _, err := net.SplitHostPort(raw); err == nil {
		return raw, nil
	}
	if ip := net.ParseIP(strings.Trim(raw, "[]")); ip != nil {
		return net.JoinHostPort(ip.String(), "445"), nil
	}
	if strings.Contains(raw, ":") {
		return "", errors.Join(ErrInvalidConfig, errors.New("smb host has an invalid port"))
	}
	return net.JoinHostPort(raw, "445"), nil
}
