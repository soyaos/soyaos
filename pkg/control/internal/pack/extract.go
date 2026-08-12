// Package pack handles the on-disk reverse of cmd/soyaos/build.go::buildSPK —
// it unpacks a canonical .spk archive (gzip-compressed tar) onto a destination
// directory with strict path safety.
//
// The companion build path produces archives whose entries are forward-slash
// relative paths against the Pack source tree. Extract enforces that property
// at unpack time:
//
//   - filepath.Clean(header.Name) must NOT escape the destination root
//     (no ".." segments, no absolute paths, no leading "/");
//   - symlinks, devices, hard-links and other non-regular entries are
//     refused — .spk v0 is file-only;
//   - the on-disk file mode is forced to 0644 (or 0755 if the entry carries
//     the executable bit) regardless of header umask, mirroring buildSPK.
//
// Together these checks close the classic "zip-slip" hole: a malicious
// publisher cannot smuggle ../etc/passwd into a Pack tarball and overwrite
// a sibling file outside the Pack directory.
package pack

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// MaxEntrySize is the upper bound on a single tar entry's declared size. We
// keep it generous (16 MiB) because Pack prompt files are small but template
// HTML / sample images can chew through a few megabytes. A single oversized
// entry that exceeds this bound is refused before any bytes are written.
const MaxEntrySize int64 = 16 << 20

// Result summarizes what Extract wrote.
type Result struct {
	Files int   // number of regular files written
	Bytes int64 // total bytes written
}

// Extract reads a gzip+tar stream from r and writes its contents under dst.
// dst must be an existing, empty (or freshly-created) directory; Extract does
// not delete pre-existing siblings. On any safety violation Extract returns
// an error and the caller should treat dst as untrusted (delete + retry).
//
// The function deliberately consumes from an io.Reader so callers can stream
// from an HTTP body or a temp file equally well.
func Extract(r io.Reader, dst string) (Result, error) {
	absDst, err := filepath.Abs(dst)
	if err != nil {
		return Result{}, fmt.Errorf("pack: resolve dst: %w", err)
	}
	if err := os.MkdirAll(absDst, 0o755); err != nil {
		return Result{}, fmt.Errorf("pack: mkdir dst: %w", err)
	}

	gz, err := gzip.NewReader(r)
	if err != nil {
		return Result{}, fmt.Errorf("pack: gzip open: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)

	res := Result{}
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return res, fmt.Errorf("pack: tar read: %w", err)
		}
		switch hdr.Typeflag {
		case tar.TypeReg, tar.TypeRegA: //nolint:staticcheck // TypeRegA kept for old archives
		case tar.TypeDir:
			// .spk v0 carries no explicit directory entries (every dir is
			// implied by the file paths underneath it) but a stray dir
			// entry would still be safe to skip after path validation.
			if _, err := safeJoin(absDst, hdr.Name); err != nil {
				return res, err
			}
			continue
		default:
			return res, fmt.Errorf("pack: %w: %s (typeflag=%d)", ErrUnsupportedEntry, hdr.Name, hdr.Typeflag)
		}
		if hdr.Size < 0 || hdr.Size > MaxEntrySize {
			return res, fmt.Errorf("pack: %w: %s declares size %d (max %d)", ErrEntryTooLarge, hdr.Name, hdr.Size, MaxEntrySize)
		}

		target, err := safeJoin(absDst, hdr.Name)
		if err != nil {
			return res, err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return res, fmt.Errorf("pack: mkdir %s: %w", filepath.Dir(target), err)
		}
		mode := os.FileMode(0o644)
		if hdr.Mode&0o111 != 0 {
			mode = 0o755
		}
		f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
		if err != nil {
			return res, fmt.Errorf("pack: create %s: %w", target, err)
		}
		// Cap the read at the declared size so a corrupt header cannot
		// make us copy unbounded bytes into a per-file open descriptor.
		n, err := io.Copy(f, io.LimitReader(tr, hdr.Size+1))
		if cerr := f.Close(); err == nil && cerr != nil {
			err = cerr
		}
		if err != nil {
			return res, fmt.Errorf("pack: copy %s: %w", target, err)
		}
		if n > hdr.Size {
			return res, fmt.Errorf("pack: %w: %s has more bytes than its header declared", ErrEntryTooLarge, hdr.Name)
		}
		res.Files++
		res.Bytes += n
	}
	return res, nil
}

// ErrUnsafePath is returned when a tar entry's path would resolve outside
// the destination root after Clean (the canonical zip-slip vector).
var ErrUnsafePath = errors.New("pack: tar entry escapes destination (zip-slip)")

// ErrUnsupportedEntry is returned for symlinks / devices / hard-links —
// .spk v0 is regular-file only.
var ErrUnsupportedEntry = errors.New("pack: unsupported tar entry type")

// ErrEntryTooLarge is returned when a single tar entry exceeds MaxEntrySize.
var ErrEntryTooLarge = errors.New("pack: tar entry exceeds size cap")

// safeJoin resolves rel against base and returns the absolute target path,
// rejecting any rel that would escape base after Clean. The check is the
// canonical defense against zip-slip:
//
//  1. Reject absolute paths outright.
//  2. Clean the relative path; if it starts with ".." it escapes.
//  3. Re-derive the absolute path under base and verify it still has base
//     as a proper prefix (Rel + filepath.IsLocal-style check, but without
//     depending on Go 1.20 IsLocal so older toolchains still build).
func safeJoin(base, rel string) (string, error) {
	rel = filepath.ToSlash(rel)
	if rel == "" {
		return "", fmt.Errorf("%w: empty name", ErrUnsafePath)
	}
	if strings.HasPrefix(rel, "/") || filepath.IsAbs(rel) {
		return "", fmt.Errorf("%w: %s is absolute", ErrUnsafePath, rel)
	}
	cleaned := filepath.Clean(rel)
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") || strings.HasPrefix(cleaned, `..\`) {
		return "", fmt.Errorf("%w: %s", ErrUnsafePath, rel)
	}
	full := filepath.Join(base, cleaned)
	// Final sanity: full must be base or live underneath base.
	relToBase, err := filepath.Rel(base, full)
	if err != nil {
		return "", fmt.Errorf("%w: %s", ErrUnsafePath, rel)
	}
	if relToBase == ".." || strings.HasPrefix(relToBase, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%w: %s", ErrUnsafePath, rel)
	}
	return full, nil
}
