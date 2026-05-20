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
// alpha shape:
//   - smb, nfs, s3 are deliberate stubs that return ErrNotImplemented; the
//     real clients (go-smb2, go-nfs-client, AWS SDK) land in Stage 5 when
//     SilentCut is wired end-to-end.
//   - webdav is live: net/http PUT with Basic Auth is enough to exercise
//     the contract and to give SilentCut a working end-to-end path for
//     Nextcloud users today.
package nas

import (
	"context"
	"errors"
	"io"
)

// ErrUnknownProtocol is returned by Open when cfg.Protocol is empty or
// outside the four supported values.
var ErrUnknownProtocol = errors.New("nas: unknown protocol")

// ErrNotImplemented is returned by the alpha stubs (smb / nfs / s3). Callers
// use errors.Is to distinguish "tier not yet built" from network errors.
var ErrNotImplemented = errors.New("nas: protocol not implemented in alpha")

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

	// Bucket and Region are S3-specific.
	Bucket   string
	Region   string

	// Endpoint is the override for S3-compatible services (MinIO / R2).
	Endpoint string
}

// Open returns a NAS handle for cfg. The returned handle is usable until
// Close is called.
func Open(ctx context.Context, cfg Config) (NAS, error) {
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
