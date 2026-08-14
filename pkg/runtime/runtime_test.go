package runtime

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
)

// TestLocalHelperProcess is reinvoked in a child process by Local.Run tests.
// It gives the tests real exec/network/filesystem side effects while keeping
// them hermetic and portable across the supported macOS/Linux runners.
func TestLocalHelperProcess(t *testing.T) {
	if os.Getenv("SOYA_RUNTIME_HELPER") != "1" {
		return
	}
	fail := func(err error) {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	switch os.Getenv("SOYA_RUNTIME_ACTION") {
	case "noop":
		_, _ = fmt.Fprint(os.Stdout, "started")
	case "http":
		resp, err := http.Get(os.Getenv("SOYA_RUNTIME_URL")) //nolint:gosec // loopback test server
		if err != nil {
			fail(err)
		}
		_ = resp.Body.Close()
	case "read":
		content, err := os.ReadFile(os.Getenv("SOYA_RUNTIME_SOURCE"))
		if err != nil {
			fail(err)
		}
		if err := os.WriteFile(os.Getenv("SOYA_RUNTIME_TARGET"), content, 0o600); err != nil {
			fail(err)
		}
	case "write":
		if err := os.WriteFile(os.Getenv("SOYA_RUNTIME_TARGET"), []byte("written"), 0o600); err != nil {
			fail(err)
		}
	case "all":
		content, err := os.ReadFile(os.Getenv("SOYA_RUNTIME_SOURCE"))
		if err != nil {
			fail(err)
		}
		resp, err := http.Get(os.Getenv("SOYA_RUNTIME_URL")) //nolint:gosec // loopback test server
		if err != nil {
			fail(err)
		}
		_ = resp.Body.Close()
		if err := os.WriteFile(os.Getenv("SOYA_RUNTIME_TARGET"), content, 0o600); err != nil {
			fail(err)
		}
	default:
		fail(errors.New("unknown helper action"))
	}
	os.Exit(0)
}

func localHelperTask(t *testing.T, action string) Task {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	return Task{
		ID:      "test-task",
		Command: []string{executable, "-test.run=^TestLocalHelperProcess$"},
		Access:  &Access{},
		Env: map[string]string{
			"SOYA_RUNTIME_HELPER": "1",
			"SOYA_RUNTIME_ACTION": action,
		},
		Caps: Caps{Exec: []string{filepath.Base(executable)}},
	}
}

func requireDenied(t *testing.T, result Result, err error, capability string) {
	t.Helper()
	if !errors.Is(err, ErrDeniedByCapability) {
		t.Fatalf("Run error = %v, want ErrDeniedByCapability", err)
	}
	if !errors.Is(result.Err, ErrDeniedByCapability) {
		t.Fatalf("Result.Err = %v, want ErrDeniedByCapability", result.Err)
	}
	var denied *DeniedError
	if !errors.As(err, &denied) {
		t.Fatalf("Run error = %#v, want *DeniedError", err)
	}
	if denied.Capability != capability {
		t.Fatalf("denied capability = %q, want %q", denied.Capability, capability)
	}
	if len(result.Stdout) != 0 || len(result.Stderr) != 0 {
		t.Fatalf("denial must not start a child: stdout=%q stderr=%q", result.Stdout, result.Stderr)
	}
}

func TestLocalRunExecDenyAndAllow(t *testing.T) {
	task := localHelperTask(t, "noop")
	task.Caps = Caps{}
	result, err := (Local{}).Run(context.Background(), task)
	requireDenied(t, result, err, "exec")

	task = localHelperTask(t, "noop")
	result, err = (Local{}).Run(context.Background(), task)
	if err != nil {
		t.Fatalf("allowed Run: %v", err)
	}
	if result.TaskID != task.ID || string(result.Stdout) != "started" || result.ExitCode != 0 {
		t.Fatalf("allowed Result = %+v", result)
	}
}

func TestLocalRunNetworkDenyAndAllow(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		requests.Add(1)
	}))
	t.Cleanup(server.Close)
	host, port := serverTarget(t, server.URL)
	access := EgressAccess{Host: host, Port: port, Proto: "http"}

	task := localHelperTask(t, "http")
	task.Env["SOYA_RUNTIME_URL"] = server.URL
	task.Access.NetworkOut = []EgressAccess{access}
	result, err := (Local{}).Run(context.Background(), task)
	requireDenied(t, result, err, "network_out")
	if got := requests.Load(); got != 0 {
		t.Fatalf("denied request reached server %d time(s)", got)
	}

	task.Caps.NetworkOut = []NetRule{{Host: host, Port: port, Proto: "http"}}
	result, err = (Local{}).Run(context.Background(), task)
	if err != nil || result.ExitCode != 0 {
		t.Fatalf("allowed network Run = %+v, err=%v", result, err)
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("allowed request count = %d, want 1", got)
	}
}

func TestLocalRunFSReadDenyAndAllow(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "source.txt")
	target := filepath.Join(dir, "copied.txt")
	if err := os.WriteFile(source, []byte("source-content"), 0o600); err != nil {
		t.Fatalf("seed source: %v", err)
	}
	task := localHelperTask(t, "read")
	task.Env["SOYA_RUNTIME_SOURCE"] = source
	task.Env["SOYA_RUNTIME_TARGET"] = target
	task.Access.FSRead = []string{source}
	task.Access.FSWrite = []string{target}
	task.Caps.FSWrite = []string{target}

	result, err := (Local{}).Run(context.Background(), task)
	requireDenied(t, result, err, "fs_read")
	if _, statErr := os.Stat(target); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("denied read created target: %v", statErr)
	}

	task.Caps.FSRead = []string{source}
	result, err = (Local{}).Run(context.Background(), task)
	if err != nil || result.ExitCode != 0 {
		t.Fatalf("allowed read Run = %+v, err=%v", result, err)
	}
	content, err := os.ReadFile(target)
	if err != nil || string(content) != "source-content" {
		t.Fatalf("copied content=%q, err=%v", content, err)
	}
}

func TestLocalRunFSWriteDenyAndAllow(t *testing.T) {
	target := filepath.Join(t.TempDir(), "output.txt")
	task := localHelperTask(t, "write")
	task.Env["SOYA_RUNTIME_TARGET"] = target
	task.Access.FSWrite = []string{target}

	result, err := (Local{}).Run(context.Background(), task)
	requireDenied(t, result, err, "fs_write")
	if _, statErr := os.Stat(target); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("denied write created target: %v", statErr)
	}

	task.Caps.FSWrite = []string{target}
	result, err = (Local{}).Run(context.Background(), task)
	if err != nil || result.ExitCode != 0 {
		t.Fatalf("allowed write Run = %+v, err=%v", result, err)
	}
	if content, readErr := os.ReadFile(target); readErr != nil || string(content) != "written" {
		t.Fatalf("output=%q, err=%v", content, readErr)
	}
}

func TestLocalRunLegalTaskNoRegression(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		requests.Add(1)
	}))
	t.Cleanup(server.Close)
	host, port := serverTarget(t, server.URL)
	dir := t.TempDir()
	source := filepath.Join(dir, "input.txt")
	target := filepath.Join(dir, "output.txt")
	if err := os.WriteFile(source, []byte("legal"), 0o600); err != nil {
		t.Fatalf("seed source: %v", err)
	}

	task := localHelperTask(t, "all")
	task.Env["SOYA_RUNTIME_SOURCE"] = source
	task.Env["SOYA_RUNTIME_TARGET"] = target
	task.Env["SOYA_RUNTIME_URL"] = server.URL
	task.Access = &Access{
		NetworkOut: []EgressAccess{{Host: host, Port: port, Proto: "http"}},
		FSRead:     []string{source},
		FSWrite:    []string{target},
	}
	task.Caps.NetworkOut = []NetRule{{Host: host, Port: port, Proto: "http"}}
	task.Caps.FSRead = []string{source}
	task.Caps.FSWrite = []string{target}

	result, err := (Local{}).Run(context.Background(), task)
	if err != nil || result.Err != nil || result.ExitCode != 0 {
		t.Fatalf("Run legal task = %+v, err=%v", result, err)
	}
	if requests.Load() != 1 {
		t.Fatalf("server requests = %d, want 1", requests.Load())
	}
	if content, readErr := os.ReadFile(target); readErr != nil || string(content) != "legal" {
		t.Fatalf("output=%q, err=%v", content, readErr)
	}
}

func TestLocalRunRejectsMissingCommand(t *testing.T) {
	result, err := (Local{}).Run(context.Background(), Task{ID: "invalid"})
	if !errors.Is(err, ErrInvalidTask) || !errors.Is(result.Err, ErrInvalidTask) {
		t.Fatalf("Run missing command = %+v, err=%v; want ErrInvalidTask", result, err)
	}
}

func TestLocalRunRejectsMissingAccessDeclaration(t *testing.T) {
	task := localHelperTask(t, "noop")
	task.Access = nil
	result, err := (Local{}).Run(context.Background(), task)
	if !errors.Is(err, ErrInvalidTask) || !errors.Is(result.Err, ErrInvalidTask) {
		t.Fatalf("Run missing access = %+v, err=%v; want ErrInvalidTask", result, err)
	}
	if len(result.Stdout) != 0 || len(result.Stderr) != 0 {
		t.Fatalf("missing access declaration started child: %+v", result)
	}
}

func serverTarget(t *testing.T, rawURL string) (string, int) {
	t.Helper()
	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parse server URL: %v", err)
	}
	host, portText, err := net.SplitHostPort(parsed.Host)
	if err != nil {
		t.Fatalf("split server target: %v", err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatalf("parse server port: %v", err)
	}
	return host, port
}
