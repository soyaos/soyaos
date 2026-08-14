package nas

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestWebDAV_WritePutsBytes(t *testing.T) {
	var gotBody []byte
	var gotAuthUser, gotAuthPass string
	var gotPath string
	var gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		u, p, _ := r.BasicAuth()
		gotAuthUser, gotAuthPass = u, p
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	h, err := Open(context.Background(), Config{
		Protocol: "webdav",
		Host:     srv.URL,
		Username: "alice",
		Password: "s3cret",
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer h.Close()

	n, err := h.Write(context.Background(), "/comet/output.mp4", strings.NewReader("hello mp4"))
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if n != int64(len("hello mp4")) {
		t.Errorf("Write returned %d, want %d", n, len("hello mp4"))
	}
	if gotMethod != http.MethodPut {
		t.Errorf("method=%q, want PUT", gotMethod)
	}
	if gotPath != "/comet/output.mp4" {
		t.Errorf("path=%q, want /comet/output.mp4", gotPath)
	}
	if !bytes.Equal(gotBody, []byte("hello mp4")) {
		t.Errorf("body=%q", gotBody)
	}
	if gotAuthUser != "alice" || gotAuthPass != "s3cret" {
		t.Errorf("basic auth=%q/%q, want alice/s3cret", gotAuthUser, gotAuthPass)
	}
}

func TestWebDAV_CloseWipesCredentials(t *testing.T) {
	h, err := Open(context.Background(), Config{
		Protocol: "webdav",
		Host:     "http://example.invalid",
		Username: "alice",
		Password: "s3cret",
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	// Peek into the concrete type to assert credential wiping. This is the
	// one place the credential-handling contract is testable.
	wh := h.(*webdavHandle)
	if wh.cfg.Password != "s3cret" {
		t.Fatalf("password not stored pre-close")
	}
	if err := h.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if wh.cfg.Password != "" {
		t.Errorf("Close did not wipe Password (got %q)", wh.cfg.Password)
	}
	if wh.cfg.Username != "" {
		t.Errorf("Close did not wipe Username (got %q)", wh.cfg.Username)
	}
	// Double-close is a no-op.
	if err := h.Close(); err != nil {
		t.Errorf("second Close errored: %v", err)
	}
	// Write after Close must fail.
	if _, err := h.Write(context.Background(), "/x", strings.NewReader("y")); err == nil {
		t.Errorf("Write after Close must fail")
	}
}

func TestWebDAV_NonSuccessStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()
	h, _ := Open(context.Background(), Config{Protocol: "webdav", Host: srv.URL})
	defer h.Close()
	_, err := h.Write(context.Background(), "/x", strings.NewReader("y"))
	if err == nil {
		t.Fatal("expected error on 403")
	}
}

func TestWebDAV_ExistingCollectionConfirmedAfterForbiddenMKCOL(t *testing.T) {
	var methods []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.Method)
		switch r.Method {
		case "MKCOL":
			w.WriteHeader(http.StatusForbidden)
		case "PROPFIND":
			w.WriteHeader(http.StatusMultiStatus)
		case http.MethodPut:
			w.WriteHeader(http.StatusCreated)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	defer srv.Close()
	h, err := Open(context.Background(), Config{Protocol: "webdav", Host: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	if _, err := h.Write(context.Background(), "existing/file.bin", strings.NewReader("probe")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	want := []string{"MKCOL", "PROPFIND", http.MethodPut}
	if !bytes.Equal([]byte(strings.Join(methods, ",")), []byte(strings.Join(want, ","))) {
		t.Fatalf("methods=%v, want %v", methods, want)
	}
}

func TestWebDAV_HostRequired(t *testing.T) {
	_, err := Open(context.Background(), Config{Protocol: "webdav"})
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("err=%v, want ErrInvalidConfig", err)
	}
}

func TestWebDAV_CloseWaitsForInFlightWrite(t *testing.T) {
	reachedServer := make(chan struct{})
	releaseServer := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "MKCOL" {
			close(reachedServer)
			<-releaseServer
			w.WriteHeader(http.StatusCreated)
			return
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()
	h, err := Open(context.Background(), Config{Protocol: "webdav", Host: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	writeDone := make(chan error, 1)
	go func() {
		_, err := h.Write(context.Background(), "dir/probe.bin", strings.NewReader("probe"))
		writeDone <- err
	}()
	<-reachedServer
	closeDone := make(chan error, 1)
	go func() { closeDone <- h.Close() }()
	select {
	case err := <-closeDone:
		t.Fatalf("Close returned before the in-flight write completed: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(releaseServer)
	if err := <-writeDone; err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := <-closeDone; err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestOpen_UnknownProtocol(t *testing.T) {
	_, err := Open(context.Background(), Config{Protocol: ""})
	if !errors.Is(err, ErrUnknownProtocol) {
		t.Fatalf("err=%v, want ErrUnknownProtocol", err)
	}
}
