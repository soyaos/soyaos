package main

import (
	"bytes"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/soyaos/soyaos/pkg/auth"
	"github.com/soyaos/soyaos/pkg/kernel"
)

func TestCmdStartProcessAcceptsPersistedRowToken(t *testing.T) {
	if os.Getenv("SOYAOS_CMD_START_HELPER") == "1" {
		err := cmdStart([]string{
			"--listen", os.Getenv("SOYAOS_TEST_LISTEN"),
			"--rpc", os.Getenv("SOYAOS_TEST_RPC"),
			"--data-dir", os.Getenv("SOYAOS_TEST_DATA_DIR"),
		})
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}

	home := t.TempDir()
	dataDir := filepath.Join(home, "data")
	keyPath := filepath.Join(home, ".local", "share", "soyaos", "rowtoken-key")

	firstURL, stopFirst := startCmdStartProcess(t, home, dataDir)
	signer, err := auth.LoadOrCreateRowTokenSigner(keyPath)
	if err != nil {
		t.Fatalf("load production row-token key: %v", err)
	}
	token, err := signer.Mint("estate-muse", "generate_post", "row-17", "owner-prefix", time.Hour)
	if err != nil {
		t.Fatalf("mint production row token: %v", err)
	}
	if got := postActionStatus(t, firstURL, token, "row-17"); got != http.StatusNotFound {
		t.Fatalf("matching row-token through cmdStart = %d, want 404 unknown_agent", got)
	}
	if got := postActionStatus(t, firstURL, token, "row-99"); got != http.StatusUnauthorized {
		t.Fatalf("mismatched row-token through cmdStart = %d, want 401", got)
	}
	stopFirst()

	secondURL, stopSecond := startCmdStartProcess(t, home, dataDir)
	defer stopSecond()
	if got := postActionStatus(t, secondURL, token, "row-17"); got != http.StatusNotFound {
		t.Fatalf("pre-restart token through restarted cmdStart = %d, want 404 unknown_agent", got)
	}
}

func TestNewDataPlaneGatewayWiresDefaultPersistentRowTokens(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	gateway, keyPath, err := newDataPlaneGateway(kernel.New(), auth.NewMemoryStore(), "")
	if err != nil {
		t.Fatalf("newDataPlaneGateway: %v", err)
	}
	wantPath := filepath.Join(home, ".local", "share", "soyaos", "rowtoken-key")
	if keyPath != wantPath {
		t.Fatalf("row-token key path = %q, want %q", keyPath, wantPath)
	}
	if gateway.RowTokens == nil {
		t.Fatal("production gateway has no RowTokens signer")
	}
	info, err := os.Stat(keyPath)
	if err != nil {
		t.Fatalf("stat row-token key: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("row-token key mode = %o, want 600", got)
	}

	token, err := gateway.RowTokens.Mint("estate-muse", "generate_post", "row-17", "owner-prefix", time.Hour)
	if err != nil {
		t.Fatalf("mint row token: %v", err)
	}
	server := httptest.NewServer(gateway.Handler())
	defer server.Close()

	// The token passed authentication. This kernel deliberately has no
	// EstateMuse pack, so the request reaches the post-auth 404 instead of
	// the pre-fix 401.
	if got := postActionStatus(t, server.URL, token, "row-17"); got != http.StatusNotFound {
		t.Fatalf("matching row-token status = %d, want 404 unknown_agent (auth passed)", got)
	}
	if got := postActionStatus(t, server.URL, token, "row-99"); got != http.StatusUnauthorized {
		t.Fatalf("mismatched row-token status = %d, want 401", got)
	}

	// Reconstruct the production gateway from the persisted key. Tokens
	// minted before this simulated restart must remain valid.
	restarted, restartedPath, err := newDataPlaneGateway(kernel.New(), auth.NewMemoryStore(), "")
	if err != nil {
		t.Fatalf("restart newDataPlaneGateway: %v", err)
	}
	if restartedPath != keyPath {
		t.Fatalf("restart key path = %q, want %q", restartedPath, keyPath)
	}
	restartedServer := httptest.NewServer(restarted.Handler())
	defer restartedServer.Close()
	if got := postActionStatus(t, restartedServer.URL, token, "row-17"); got != http.StatusNotFound {
		t.Fatalf("pre-restart token status after restart = %d, want 404 unknown_agent", got)
	}

	forger := auth.NewRowTokenSigner(bytes.Repeat([]byte{0x42}, 32))
	forged, err := forger.Mint("estate-muse", "generate_post", "row-17", "owner-prefix", time.Hour)
	if err != nil {
		t.Fatalf("mint forged token: %v", err)
	}
	if got := postActionStatus(t, restartedServer.URL, forged, "row-17"); got != http.StatusUnauthorized {
		t.Fatalf("forged row-token status = %d, want 401", got)
	}
}

func TestNewDataPlaneGatewayFailsClosedWhenKeyCannotLoad(t *testing.T) {
	keyPath := filepath.Join(t.TempDir(), "rowtoken-key")
	if err := os.WriteFile(keyPath, []byte("short"), 0o600); err != nil {
		t.Fatalf("write invalid key: %v", err)
	}

	gateway, gotPath, err := newDataPlaneGateway(kernel.New(), auth.NewMemoryStore(), keyPath)
	if err == nil {
		t.Fatal("newDataPlaneGateway accepted an invalid short key")
	}
	if gotPath != keyPath {
		t.Fatalf("key path = %q, want %q", gotPath, keyPath)
	}
	if gateway == nil {
		t.Fatal("gateway should remain available for sk-soya authentication")
	}
	if gateway.RowTokens != nil {
		t.Fatal("gateway must leave RowTokens nil after key setup failure")
	}
}

func postActionStatus(t *testing.T, baseURL, bearer, rowID string) int {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost,
		baseURL+"/v1/agents/estate-muse/actions/generate_post",
		bytes.NewBufferString(`{"row_id":"`+rowID+`"}`))
	if err != nil {
		t.Fatalf("build action request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+bearer)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST action: %v", err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

func startCmdStartProcess(t *testing.T, home, dataDir string) (string, func()) {
	t.Helper()
	listen := freeLoopbackAddress(t)
	rpc := freeLoopbackAddress(t)
	cmd := exec.Command(os.Args[0], "-test.run=^TestCmdStartProcessAcceptsPersistedRowToken$")
	cmd.Env = append(os.Environ(),
		"HOME="+home,
		"SOYAOS_CMD_START_HELPER=1",
		"SOYAOS_TEST_LISTEN="+listen,
		"SOYAOS_TEST_RPC="+rpc,
		"SOYAOS_TEST_DATA_DIR="+dataDir,
	)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start cmdStart helper: %v", err)
	}

	stopped := false
	stop := func() {
		if stopped {
			return
		}
		stopped = true
		if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
			t.Errorf("signal cmdStart helper: %v", err)
			return
		}
		if err := cmd.Wait(); err != nil {
			t.Errorf("wait cmdStart helper: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
		}
	}
	t.Cleanup(stop)

	baseURL := "http://" + listen
	client := &http.Client{Timeout: 200 * time.Millisecond}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := client.Get(baseURL + "/healthz")
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return baseURL, stop
			}
		}
		if cmd.ProcessState != nil {
			t.Fatalf("cmdStart helper exited before ready\nstdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
	stop()
	t.Fatalf("cmdStart helper did not become ready\nstdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	return "", func() {}
}

func freeLoopbackAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve loopback address: %v", err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatalf("release loopback address: %v", err)
	}
	return address
}
