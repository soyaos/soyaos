package pack

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// makeTarGz builds an in-memory gzip+tar archive with the entries given.
// Each entry name is stored verbatim — tests pass malicious names directly
// to confirm safeJoin rejects them.
type entry struct {
	name     string
	body     string
	typeflag byte
	mode     int64
}

func makeTarGz(t *testing.T, entries []entry) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		tf := e.typeflag
		if tf == 0 {
			tf = tar.TypeReg
		}
		hdr := &tar.Header{
			Name:     e.name,
			Mode:     e.mode,
			Size:     int64(len(e.body)),
			Typeflag: tf,
			Format:   tar.FormatPAX,
		}
		if hdr.Mode == 0 && tf == tar.TypeReg {
			hdr.Mode = 0o644
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("tar header %s: %v", e.name, err)
		}
		if tf == tar.TypeReg && e.body != "" {
			if _, err := tw.Write([]byte(e.body)); err != nil {
				t.Fatalf("tar body %s: %v", e.name, err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("gz close: %v", err)
	}
	return buf.Bytes()
}

func TestExtract_HappyPath(t *testing.T) {
	dst := t.TempDir()
	body := makeTarGz(t, []entry{
		{name: "soyapack.yaml", body: "spec_version: soyapack.v0\n"},
		{name: "prompts/main.md", body: "system prompt body"},
		{name: "examples/x.txt", body: "x"},
	})
	res, err := Extract(bytes.NewReader(body), dst)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if res.Files != 3 {
		t.Errorf("Files=%d, want 3", res.Files)
	}
	if res.Bytes == 0 {
		t.Errorf("Bytes=0, want >0")
	}
	got, err := os.ReadFile(filepath.Join(dst, "prompts/main.md"))
	if err != nil {
		t.Fatalf("read extracted: %v", err)
	}
	if string(got) != "system prompt body" {
		t.Errorf("body mismatch: %q", got)
	}
}

func TestExtract_RejectsParentTraversal(t *testing.T) {
	dst := t.TempDir()
	body := makeTarGz(t, []entry{
		{name: "../etc/passwd", body: "root:x:0:0"},
	})
	_, err := Extract(bytes.NewReader(body), dst)
	if err == nil {
		t.Fatal("Extract accepted ../etc/passwd")
	}
	if !errors.Is(err, ErrUnsafePath) {
		t.Errorf("err = %v, want ErrUnsafePath", err)
	}
	// And the file must not exist on disk anywhere.
	if _, statErr := os.Stat(filepath.Join(dst, "..", "etc", "passwd")); statErr == nil {
		t.Error("zip-slip wrote ../etc/passwd on disk")
	}
}

func TestExtract_RejectsAbsolutePath(t *testing.T) {
	dst := t.TempDir()
	body := makeTarGz(t, []entry{
		{name: "/tmp/pwned", body: "x"},
	})
	_, err := Extract(bytes.NewReader(body), dst)
	if err == nil || !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("err = %v, want ErrUnsafePath", err)
	}
}

func TestExtract_RejectsSymlink(t *testing.T) {
	dst := t.TempDir()
	body := makeTarGz(t, []entry{
		{name: "link", typeflag: tar.TypeSymlink},
	})
	_, err := Extract(bytes.NewReader(body), dst)
	if err == nil || !errors.Is(err, ErrUnsupportedEntry) {
		t.Fatalf("err = %v, want ErrUnsupportedEntry", err)
	}
}

func TestExtract_RejectsOversizeEntry(t *testing.T) {
	dst := t.TempDir()
	// Build a tar header that declares a > MaxEntrySize body. We write a
	// tiny body so the test stays fast — the cap is checked from the
	// header alone before we copy bytes.
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	hdr := &tar.Header{
		Name:     "big.bin",
		Mode:     0o644,
		Size:     MaxEntrySize + 1,
		Typeflag: tar.TypeReg,
		Format:   tar.FormatPAX,
	}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}
	_ = tw.Close()
	_ = gz.Close()
	_, err := Extract(bytes.NewReader(buf.Bytes()), dst)
	if err == nil || !errors.Is(err, ErrEntryTooLarge) {
		t.Fatalf("err = %v, want ErrEntryTooLarge", err)
	}
}

func TestExtract_PreservesExecutableBit(t *testing.T) {
	dst := t.TempDir()
	body := makeTarGz(t, []entry{
		{name: "scripts/run.sh", body: "#!/bin/sh\n", mode: 0o755},
		{name: "README.md", body: "x", mode: 0o644},
	})
	if _, err := Extract(bytes.NewReader(body), dst); err != nil {
		t.Fatalf("Extract: %v", err)
	}
	info, err := os.Stat(filepath.Join(dst, "scripts/run.sh"))
	if err != nil {
		t.Fatalf("stat run.sh: %v", err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Errorf("scripts/run.sh lost executable bit: %v", info.Mode())
	}
}

func TestSafeJoin_VariousAttacks(t *testing.T) {
	base := t.TempDir()
	cases := []struct {
		name string
		ok   bool
	}{
		{"prompts/main.md", true},
		{"a/b/c.txt", true},
		{"./README.md", true},
		{"../escape", false},
		{"a/../../escape", false},
		{"/abs", false},
		{"", false},
	}
	for _, tc := range cases {
		_, err := safeJoin(base, tc.name)
		if tc.ok && err != nil {
			t.Errorf("safeJoin(%q) err=%v, want ok", tc.name, err)
		}
		if !tc.ok && err == nil {
			t.Errorf("safeJoin(%q) accepted, want reject", tc.name)
		}
		if !tc.ok && err != nil && !strings.Contains(err.Error(), "zip-slip") && !strings.Contains(err.Error(), "empty") {
			t.Errorf("safeJoin(%q) err=%v, want zip-slip/empty mention", tc.name, err)
		}
	}
}
