// Package nas implements outbound NAS connectors — the SoyaOS-side half of
// the per-Comet "write your artifact home" workflow.
//
// SilentCut (DD-011) reverse-pressure: SilentCut renders MP4 artifacts that
// can be 100s of MB. The user's storage of record is a home / studio NAS,
// not SoyaOS itself, so we need a connector layer that speaks the four
// protocols those NAS appliances actually expose: SMB (Synology / Windows),
// NFS (Linux / TrueNAS), WebDAV (Nextcloud / generic) and S3
// (MinIO / B2 / R2).
//
// Trust model: credentials arrive on every Provision via a Moon-issued
// short-lived bundle. Comet keeps them in memory only; Close() wipes the
// Password field before returning so a heap dump never contains the
// secret beyond the task lifetime.
//
// The four drivers are real wire implementations. SMB speaks SMB2/3, NFS
// speaks NFSv3 over ONC RPC, WebDAV uses authenticated HTTP PUT, and S3 uses
// SigV4 against AWS or an S3-compatible endpoint.
package nas

import (
	"context"
	"errors"
	"io"
	"path"
	"strings"
	"time"
)

// ErrUnknownProtocol is returned by Open when cfg.Protocol is empty or
// outside the four supported values.
var ErrUnknownProtocol = errors.New("nas: unknown protocol")

// ErrInvalidConfig is returned before dialing when a binding is incomplete or
// unsafe. It never includes credential values.
var ErrInvalidConfig = errors.New("nas: invalid configuration")

// ErrClosed is returned when Write races with, or follows, Close.
var ErrClosed = errors.New("nas: handle closed")

// NAS is the unified write-only handle every protocol must satisfy. Read is
// out of scope — SilentCut only needs to push artifacts outward.
type NAS interface {
	// Write copies r to path on the remote share and returns the number of
	// bytes delivered. Implementations should treat path as opaque and let
	// the protocol decide its semantics (UNC on SMB, mount-relative on NFS,
	// URL-relative on WebDAV, key on S3).
	Write(ctx context.Context, path string, r io.Reader) (int64, error)
	// Close releases the underlying connection and *must* zero out any
	// credential material the implementation retained.
	Close() error
}

// Config carries everything Open needs to instantiate a NAS handle. Only
// the fields relevant to cfg.Protocol are inspected; the others are
// preserved verbatim so callers can populate one struct from a Moon-issued
// bundle without branching.
type Config struct {
	// Protocol is one of "smb", "nfs", "webdav", "s3".
	Protocol string

	// Host is the network address — fileserver hostname for SMB / NFS,
	// the WebDAV server origin (e.g. "https://files.example.com"), or
	// the S3 endpoint (omitted for AWS proper).
	Host string

	// Share is the SMB share name or NFS export path. WebDAV puts its
	// remote root in Host; S3 puts it in Bucket.
	Share string

	// Username and Password are Basic-style credentials. They are wiped
	// by Close() in implementations that store them.
	Username string
	Password string
	Domain   string

	// Bucket and Region are S3-specific.
	Bucket string
	Region string
	// SessionToken carries an optional short-lived S3 credential token.
	SessionToken string

	// Endpoint is the override for S3-compatible services (MinIO / R2).
	Endpoint string

	// NFSUseProcessIDs makes AUTH_SYS use the current process uid/gid. When
	// false the explicit NFSUID/NFSGID values are used (including uid 0).
	NFSUseProcessIDs bool
	NFSUID           uint32
	NFSGID           uint32
	NFSMachine       string

	// Timeout bounds connection establishment where the protocol library
	// exposes a context-aware dialer. Zero selects a conservative default.
	Timeout time.Duration
}

// Open returns a NAS handle for cfg. The returned handle is usable until
// Close is called.
func Open(ctx context.Context, cfg Config) (NAS, error) {
	if ctx == nil {
		return nil, errors.Join(ErrInvalidConfig, errors.New("context is required"))
	}
	switch cfg.Protocol {
	case "smb":
		return openSMB(ctx, cfg)
	case "nfs":
		return openNFS(ctx, cfg)
	case "webdav":
		return openWebDAV(ctx, cfg)
	case "s3":
		return openS3(ctx, cfg)
	default:
		return nil, ErrUnknownProtocol
	}
}

const defaultTimeout = 15 * time.Second

func timeoutFor(cfg Config) time.Duration {
	if cfg.Timeout > 0 {
		return cfg.Timeout
	}
	return defaultTimeout
}

// cleanRemotePath normalizes the write-only relative path shared by SMB, NFS,
// and S3. A leading slash is accepted for compatibility with the original
// connector contract, but traversal, backslashes and an empty path are not.
func cleanRemotePath(raw string) (string, error) {
	if strings.Contains(raw, "\\") {
		return "", errors.Join(ErrInvalidConfig, errors.New("remote path must use forward slashes"))
	}
	cleaned := path.Clean(strings.TrimLeft(raw, "/"))
	if cleaned == "." || cleaned == "" || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", errors.Join(ErrInvalidConfig, errors.New("remote path must be a non-empty relative path"))
	}
	return cleaned, nil
}
