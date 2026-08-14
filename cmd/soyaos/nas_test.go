package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/soyaos/soyaos/pkg/kernel"
)

func TestRunNASCheckWebDAVSuccess(t *testing.T) {
	var body []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()
	var out bytes.Buffer
	err := runNASCheck([]string{
		"--protocol", "webdav",
		"--host", srv.URL,
		"--path", "matrix/probe.bin",
		"--payload-bytes", "257",
	}, &out, emptyEnv)
	if err != nil {
		t.Fatalf("runNASCheck: %v\n%s", err, out.String())
	}
	var got nasCheckResult
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if !got.Success || got.Protocol != "webdav" || got.Bytes != 257 || got.RemotePath != "matrix/probe.bin" || len(body) != 257 {
		t.Fatalf("result=%+v body bytes=%d", got, len(body))
	}
}

func TestNASHookForEnvResolvesPackBinding(t *testing.T) {
	var body []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "MKCOL" {
			w.WriteHeader(http.StatusCreated)
			return
		}
		body, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	env := map[string]string{
		"SILENTCUT_NAS_HOST": srv.URL,
		"SILENTCUT_NAS_USER": "trial-user",
		"SILENTCUT_NAS_PASS": "trial-pass",
	}
	hook := nasHookForEnv(context.Background(), func(name string) (string, bool) {
		value, ok := env[name]
		return value, ok
	})
	target, err := hook(kernel.NASBindingSpec{
		ID:       "primary",
		Protocol: "webdav",
		HostRef:  "${SILENTCUT_NAS_HOST}",
		Share:    "/videos/silent-cut",
		Access:   "rw",
		Secrets: map[string]string{
			"username_ref": "${SILENTCUT_NAS_USER}",
			"password_ref": "${SILENTCUT_NAS_PASS}",
		},
	})
	if err != nil {
		t.Fatalf("nasHookForEnv: %v", err)
	}
	if target.ID != "primary" || target.Protocol != "webdav" || target.BasePath != "/videos/silent-cut" {
		t.Fatalf("target=%+v", target)
	}
	if _, err := target.Handle.Write(context.Background(), "videos/silent-cut/probe.bin", strings.NewReader("probe")); err != nil {
		t.Fatalf("target.Write: %v", err)
	}
	if err := target.Handle.Close(); err != nil {
		t.Fatalf("target.Close: %v", err)
	}
	if string(body) != "probe" {
		t.Fatalf("body=%q, want probe", body)
	}
}

func TestRunNASCheckFailureRedactsCredentialsAndEndpoint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()
	secrets := map[string]string{"NAS_USER": "private-user", "NAS_PASS": "private-password"}
	var out bytes.Buffer
	err := runNASCheck([]string{
		"--protocol", "webdav",
		"--host", srv.URL,
		"--username-env", "NAS_USER",
		"--password-env", "NAS_PASS",
	}, &out, func(name string) (string, bool) { value, ok := secrets[name]; return value, ok })
	if !errors.Is(err, errNASCheckFailed) {
		t.Fatalf("err=%v, want errNASCheckFailed", err)
	}
	text := out.String()
	for _, forbidden := range []string{"private-user", "private-password", srv.URL} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("JSON leaked %q: %s", forbidden, text)
		}
	}
	var got nasCheckResult
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if got.Success || got.ErrorCode != "authorization" {
		t.Fatalf("result=%+v", got)
	}
}

func TestRunNASCheckRequiresNamedCredentialEnvironment(t *testing.T) {
	var out bytes.Buffer
	err := runNASCheck([]string{
		"--protocol", "webdav", "--host", "https://nas.invalid", "--password-env", "MISSING",
	}, &out, emptyEnv)
	if err == nil || !strings.Contains(err.Error(), "MISSING") {
		t.Fatalf("err=%v", err)
	}
	if out.Len() != 0 {
		t.Fatalf("unexpected output: %s", out.String())
	}
}

func emptyEnv(string) (string, bool) { return "", false }
