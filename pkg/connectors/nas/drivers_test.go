package nas

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"reflect"
	"strings"
	"testing"
)

type recordingFile struct {
	bytes.Buffer
	closed bool
}

func (f *recordingFile) Close() error { f.closed = true; return nil }

type fakeSMBRemote struct {
	dirs   []string
	name   string
	file   recordingFile
	closed bool
}

func (r *fakeSMBRemote) mkdirAll(_ context.Context, name string) error {
	r.dirs = append(r.dirs, name)
	return nil
}
func (r *fakeSMBRemote) create(_ context.Context, name string) (io.WriteCloser, error) {
	r.name = name
	return &r.file, nil
}
func (r *fakeSMBRemote) close() error { r.closed = true; return nil }

type fakeNFSRemote struct {
	dirs    []string
	name    string
	removed string
	file    recordingFile
	closed  bool
}

func (r *fakeNFSRemote) mkdirAll(name string) error { r.dirs = append(r.dirs, name); return nil }
func (r *fakeNFSRemote) openFile(name string) (io.WriteCloser, error) {
	r.name = name
	return &r.file, nil
}
func (r *fakeNFSRemote) remove(name string) error { r.removed = name; return os.ErrNotExist }
func (r *fakeNFSRemote) close() error             { r.closed = true; return nil }

type fakeS3Remote struct {
	bucket, object string
	body           []byte
	closed         bool
}

func (r *fakeS3Remote) put(_ context.Context, bucket, object string, body io.Reader) (int64, error) {
	r.bucket, r.object = bucket, object
	r.body, _ = io.ReadAll(body)
	return int64(len(r.body)), nil
}
func (r *fakeS3Remote) close() { r.closed = true }

func TestSMBHandleWritesAndCloses(t *testing.T) {
	remote := &fakeSMBRemote{}
	h := &smbHandle{cfg: secretConfig(), remote: remote}
	n, err := h.Write(context.Background(), "/renders/final.mp4", strings.NewReader("video"))
	if err != nil || n != 5 {
		t.Fatalf("Write = %d, %v", n, err)
	}
	if !reflect.DeepEqual(remote.dirs, []string{"renders"}) || remote.name != "renders/final.mp4" || remote.file.String() != "video" || !remote.file.closed {
		t.Fatalf("remote state = %+v", remote)
	}
	assertClosedAndWiped(t, h, &h.cfg)
	if !remote.closed {
		t.Fatal("remote was not closed")
	}
}

func TestNFSHandleReplacesThenWrites(t *testing.T) {
	remote := &fakeNFSRemote{}
	h := &nfsHandle{cfg: secretConfig(), remote: remote}
	n, err := h.Write(context.Background(), "renders/final.mp4", strings.NewReader("video"))
	if err != nil || n != 5 {
		t.Fatalf("Write = %d, %v", n, err)
	}
	if remote.removed != "renders/final.mp4" || remote.name != remote.removed || remote.file.String() != "video" || !remote.file.closed {
		t.Fatalf("remote state = %+v", remote)
	}
	assertClosedAndWiped(t, h, &h.cfg)
	if !remote.closed {
		t.Fatal("remote was not closed")
	}
}

func TestS3HandleStreamsObject(t *testing.T) {
	remote := &fakeS3Remote{}
	h := &s3Handle{cfg: Config{Bucket: "clips", Username: "access", Password: "secret", SessionToken: "token"}, remote: remote}
	n, err := h.Write(context.Background(), "/renders/final.mp4", strings.NewReader("video"))
	if err != nil || n != 5 {
		t.Fatalf("Write = %d, %v", n, err)
	}
	if remote.bucket != "clips" || remote.object != "renders/final.mp4" || string(remote.body) != "video" {
		t.Fatalf("remote state = %+v", remote)
	}
	assertClosedAndWiped(t, h, &h.cfg)
	if !remote.closed {
		t.Fatal("remote was not closed")
	}
}

func TestDriverConfigurationValidation(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
	}{
		{"smb missing share", Config{Protocol: "smb", Host: "nas.local"}},
		{"nfs relative export", Config{Protocol: "nfs", Host: "nas.local", Share: "export"}},
		{"s3 credentials in endpoint", Config{Protocol: "s3", Host: "http://user:pass@nas.local:9000", Share: "b", Username: "a", Password: "b"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Open(context.Background(), tt.cfg)
			if !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("err=%v, want ErrInvalidConfig", err)
			}
		})
	}
}

func TestCleanRemotePathRejectsTraversal(t *testing.T) {
	for _, value := range []string{"", "/", "../secret", "a/../../secret", `a\\b`} {
		if _, err := cleanRemotePath(value); !errors.Is(err, ErrInvalidConfig) {
			t.Errorf("cleanRemotePath(%q) err=%v", value, err)
		}
	}
	if got, err := cleanRemotePath("/safe/output.mp4"); err != nil || got != "safe/output.mp4" {
		t.Fatalf("cleanRemotePath = %q, %v", got, err)
	}
}

func secretConfig() Config {
	return Config{Username: "alice", Password: "secret", Domain: "studio", SessionToken: "token"}
}

func assertClosedAndWiped(t *testing.T, h NAS, cfg *Config) {
	t.Helper()
	if err := h.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if cfg.Username != "" || cfg.Password != "" || cfg.Domain != "" || cfg.SessionToken != "" {
		t.Fatalf("credentials not wiped: %+v", cfg)
	}
	if _, err := h.Write(context.Background(), "after-close", strings.NewReader("x")); !errors.Is(err, ErrClosed) {
		t.Fatalf("write after Close err=%v, want ErrClosed", err)
	}
	if err := h.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}
