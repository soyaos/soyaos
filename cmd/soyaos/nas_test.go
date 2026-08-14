package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
