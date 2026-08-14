package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestStripJSONFence(t *testing.T) {
	input := "```json\n{\"remotion\":{\"props\":{}}}\n```\n"
	if got, want := stripJSONFence(input), `{"remotion":{"props":{}}}`; got != want {
		t.Fatalf("stripJSONFence=%q, want %q", got, want)
	}
}

func TestChunkReaderPreservesBytes(t *testing.T) {
	chunks := make(chan []byte, 3)
	chunks <- []byte("abc")
	chunks <- []byte("defgh")
	close(chunks)
	got, err := io.ReadAll(channelReader(chunks))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, []byte("abcdefgh")) {
		t.Fatalf("got=%q", got)
	}
}

func TestCopyPortableRemotionProject(t *testing.T) {
	src := filepath.Join(t.TempDir(), "project")
	dst := filepath.Join(t.TempDir(), "export")
	for _, dir := range []string{filepath.Join(src, "src"), filepath.Join(src, "node_modules", "pkg"), filepath.Join(src, "out")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for name, body := range map[string]string{
		filepath.Join(src, "package.json"):                    `{}`,
		filepath.Join(src, "bun.lock"):                        "lock",
		filepath.Join(src, "src", "index.ts"):                 "export {};",
		filepath.Join(src, "node_modules", "pkg", "index.js"): "noise",
		filepath.Join(src, "out", "clip.mp4"):                 "noise",
	} {
		if err := os.WriteFile(name, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	props := []byte(`{"duration_seconds":30}`)
	if err := copyPortableRemotionProject(src, dst, props); err != nil {
		t.Fatalf("copyPortableRemotionProject: %v", err)
	}
	for _, name := range []string{"package.json", "bun.lock", filepath.Join("src", "index.ts"), "props.json"} {
		if _, err := os.Stat(filepath.Join(dst, name)); err != nil {
			t.Errorf("missing %s: %v", name, err)
		}
	}
	for _, name := range []string{"node_modules", "out"} {
		if _, err := os.Stat(filepath.Join(dst, name)); !os.IsNotExist(err) {
			t.Errorf("excluded %s was copied", name)
		}
	}
}
