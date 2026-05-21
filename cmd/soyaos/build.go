package main

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// canonicalEpoch is the synthetic mtime stamped on every entry in a .spk so
// rebuilding the same source tree produces byte-identical archive bytes
// regardless of when (or by whom) the build runs.
var canonicalEpoch = time.Unix(0, 0).UTC()

// packExclude reports whether a path inside the source tree should be left
// out of the .spk archive. The rules:
//
//   - directory components named .git, dist, node_modules anywhere in the
//     tree are excluded (and so is everything underneath them);
//   - .DS_Store is excluded;
//   - any path component starting with "." is excluded except .gitignore,
//     which we explicitly keep so the unpacked archive still ignores the
//     same noise as the source tree.
func packExclude(rel string) bool {
	rel = filepath.ToSlash(rel)
	if rel == "" || rel == "." {
		return false
	}
	parts := strings.Split(rel, "/")
	for i, p := range parts {
		switch p {
		case ".git", "dist", "node_modules", ".DS_Store":
			return true
		}
		if strings.HasPrefix(p, ".") && p != ".gitignore" {
			return true
		}
		_ = i
	}
	return false
}

// buildSPK walks src, gathers every non-excluded regular file, and writes a
// canonical (reproducible) gzip+tar archive to dst. Returns sha256 hex of the
// final archive file and its size in bytes.
//
// Canonical knobs:
//   - files are sorted by their forward-slash relative path before tarring;
//   - tar.Header has Mode = 0644 (regular files) or 0755 (executable bit
//     preserved), Uid/Gid = 0, Uname/Gname = "", and a fixed epoch ModTime;
//   - AccessTime / ChangeTime are left at the zero value so the tar writer
//     emits PAX-stable headers;
//   - gzip is written at default compression with mtime = 0 (achieved by
//     opening the gzip.Writer in a deterministic way).
func buildSPK(src, dst string) (sha string, size int64, err error) {
	files, err := collectFiles(src)
	if err != nil {
		return "", 0, err
	}

	out, err := os.Create(dst)
	if err != nil {
		return "", 0, err
	}
	defer func() {
		if cerr := out.Close(); err == nil && cerr != nil {
			err = cerr
		}
	}()

	gz, err := gzip.NewWriterLevel(out, gzip.DefaultCompression)
	if err != nil {
		return "", 0, err
	}
	// Strip the gzip header's mtime so the very first bytes of the archive
	// are stable across builds. gzip.Header.ModTime == zero time emits a
	// zero MTIME field per RFC 1952.
	gz.Header.ModTime = time.Time{}
	gz.Header.Name = ""
	gz.Header.Comment = ""
	gz.Header.OS = 255 // "unknown" — avoids encoding the host OS byte.

	tw := tar.NewWriter(gz)

	for _, f := range files {
		if err := writeCanonicalEntry(tw, src, f); err != nil {
			_ = tw.Close()
			_ = gz.Close()
			return "", 0, err
		}
	}
	if err := tw.Close(); err != nil {
		_ = gz.Close()
		return "", 0, err
	}
	if err := gz.Close(); err != nil {
		return "", 0, err
	}

	// Compute sha256 + size by re-reading the finished file. We deliberately
	// hash the on-disk artifact (not a tee writer) so the value matches what
	// any downstream tool will see.
	if err := out.Sync(); err != nil {
		return "", 0, err
	}
	if _, err := out.Seek(0, io.SeekStart); err != nil {
		return "", 0, err
	}
	h := sha256.New()
	n, err := io.Copy(h, out)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

// collectFiles walks src and returns every non-excluded regular file's
// relative path (forward-slash, deterministic order). Symlinks, devices, and
// other special entries are skipped — .spk v0 is file-only.
func collectFiles(src string) ([]string, error) {
	var out []string
	err := filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, rerr := filepath.Rel(src, path)
		if rerr != nil {
			return rerr
		}
		if rel == "." {
			return nil
		}
		if packExclude(rel) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(out)
	return out, nil
}

// writeCanonicalEntry adds one regular file from src/rel to tw with every
// time / owner / permission field normalized to the canonical defaults.
func writeCanonicalEntry(tw *tar.Writer, src, rel string) error {
	full := filepath.Join(src, filepath.FromSlash(rel))
	info, err := os.Lstat(full)
	if err != nil {
		return err
	}
	mode := int64(0o644)
	if info.Mode()&0o111 != 0 {
		mode = 0o755
	}
	hdr := &tar.Header{
		Name:       rel,
		Mode:       mode,
		Size:       info.Size(),
		ModTime:    canonicalEpoch,
		AccessTime: time.Time{},
		ChangeTime: time.Time{},
		Uid:        0,
		Gid:        0,
		Uname:      "",
		Gname:      "",
		Format:     tar.FormatPAX,
		Typeflag:   tar.TypeReg,
	}
	// Use PAXRecords explicitly cleared so the writer doesn't add an
	// {atime, ctime} pair that would smuggle a host-local timestamp into
	// the archive headers.
	hdr.PAXRecords = nil

	if err := tw.WriteHeader(hdr); err != nil {
		return fmt.Errorf("write header %s: %w", rel, err)
	}
	f, err := os.Open(full)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := io.Copy(tw, f); err != nil {
		return fmt.Errorf("copy %s: %w", rel, err)
	}
	return nil
}
