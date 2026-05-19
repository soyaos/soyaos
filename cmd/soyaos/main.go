// Command soyaos is the single multi-role SoyaOS binary.
//
// In Solo edition (the only edition wired in v0.1.0-alpha.0) Planet, Moon and
// Comet roles all run in the same Go process and share the same kernel +
// OpenAI-Compat gateway.
//
// CLI surface is locked by soyaos/specs (cli.v0):
//
//   soyaos start                  boot Solo: all-in-one, OpenAI-Compat on :7474, control RPC on :7475
//   soyaos version                print build identification
//   soyaos agent create <name>    scaffold a SoyaPack v0 Agent
//   soyaos agent list             list registered Agents (talks to a running soyaos)
//   soyaos agent run <slug> "..." invoke an Agent once (talks to a running soyaos)
//   soyaos agent build [<path>]   build a SoyaPack v0 archive (planned, stub in alpha)
//   soyaos agent deploy <pack>    register a pack with a running soyaos (planned, stub in alpha)
//
// Each subcommand has its own flag set parsed with stdlib `flag`.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/soyaos/soyaos/internal/buildinfo"
	"github.com/soyaos/soyaos/pkg/auth"
	"github.com/soyaos/soyaos/pkg/control"
	"github.com/soyaos/soyaos/pkg/kernel"
	"github.com/soyaos/soyaos/pkg/openaicompat"
	"github.com/soyaos/soyaos/pkg/orbit"
	"github.com/soyaos/soyaos/pkg/scope"
	"github.com/soyaos/soyaos/pkg/store"
	"github.com/soyaos/soyaos/pkg/version"
)

// SpecVersion is the CLI surface version this binary implements.
// Locked by soyaos/specs/specs/cli/v0.md.
const SpecVersion = "cli.v0"

// envBYOKKey is the canonical env var name (see soyaos/specs/specs/cli/v0.md).
const envBYOKKey = "SOYA_MODEL_API_KEY"

func main() {
	if len(os.Args) < 2 {
		usage(os.Stderr)
		os.Exit(2)
	}
	switch os.Args[1] {
	case "start":
		exit(cmdStart(os.Args[2:]))
	case "version", "-v", "--version":
		buildinfo.Print(os.Stdout)
	case "--spec-version":
		fmt.Println(SpecVersion)
	case "agent":
		exit(cmdAgent(os.Args[2:]))
	case "help", "-h", "--help":
		usage(os.Stdout)
	default:
		fmt.Fprintf(os.Stderr, "soyaos: unknown command %q\n\n", os.Args[1])
		usage(os.Stderr)
		os.Exit(2)
	}
}

func usage(w *os.File) {
	fmt.Fprintf(w, `soyaos — Agent Operating System (Solo edition · %s · spec %s)

Usage:
  soyaos start [--listen 127.0.0.1:7474] [--rpc 127.0.0.1:7475] [--data-dir DIR]
                                  boot Solo all-in-one (Planet+Moon+Comet)
  soyaos version                  print build identification
  soyaos --spec-version           print CLI spec version this binary implements
  soyaos agent create <name>      scaffold a SoyaPack v0 Agent in ./<name>/
  soyaos agent list [--rpc URL]   list Agents registered with a running soyaos
  soyaos agent run <slug> "<prompt>" [--listen URL]
                                  invoke an Agent and print its response
  soyaos agent build [<path>]     build a SoyaPack v0 archive (alpha: stub)
  soyaos agent deploy <pack>      register a pack with a running soyaos (alpha: stub)
  soyaos help                     show this message

Environment:
  SOYA_MODEL_API_KEY              upstream LLM API key (BYOK). Recognized in
                                  alpha but no real provider is wired yet.

Pre-release. APIs are unstable. See https://github.com/soyaos/soyaos for docs.
`, version.Version, SpecVersion)
}

func exit(err error) {
	if err == nil {
		return
	}
	fmt.Fprintln(os.Stderr, "soyaos:", err)
	os.Exit(1)
}

// cmdStart boots the Solo edition all-in-one process.
func cmdStart(args []string) error {
	fs := flag.NewFlagSet("start", flag.ContinueOnError)
	listen := fs.String("listen", openaicompat.DefaultListenAddr, "OpenAI-Compat gateway listen address")
	rpc := fs.String("rpc", control.DefaultListenAddr, "control RPC listen address (loopback only)")
	dataDir := fs.String("data-dir", defaultDataDir(), "on-disk persistence root")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if err := os.MkdirAll(*dataDir, 0o755); err != nil {
		return fmt.Errorf("create data-dir %s: %w", *dataDir, err)
	}

	now := time.Now()
	recorder := scope.NewMemory()
	registry := orbit.NewRegistry()
	registry.SeedSolo(now)

	// Persistent KV under <data-dir>/soyaos.bolt — single file shared by
	// auth / scheduler / memory / artifact namespaces.
	soyaStore, err := store.Open(*dataDir)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer soyaStore.Close()

	keys := auth.NewStoreBacked(soyaStore)
	devKey := keys.SeedDevKey()

	k := kernel.New()
	k.Register(kernel.EchoAgent)

	// --- data plane: OpenAI-Compat gateway on :7474 ---
	gateway := openaicompat.NewServer(k, keys)
	dataMux := http.NewServeMux()
	dataMux.Handle("/v1/", gateway.Handler())
	dataMux.Handle("/v1/models", gateway.Handler())
	dataMux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("format") == "json" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"status":"ok","edition":%q,"version":%q,"agents":%d}`, version.Edition, version.Version, len(k.List()))
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprintln(w, "ok")
	})
	dataMux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Studio placeholder. Real Studio lands in a later milestone.
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, studioPlaceholder, version.Version, version.Edition, len(k.List()))
	})

	dataSrv := &http.Server{
		Addr:              *listen,
		Handler:           dataMux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	// --- control plane: RPC on :7475 (loopback-only) ---
	controlSrv := &http.Server{
		Addr:              *rpc,
		Handler:           control.NewServer(k).Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	byok := os.Getenv(envBYOKKey)

	fmt.Fprintf(os.Stdout, "soyaos %s (edition: %s · spec: %s)\n", version.Version, version.Edition, SpecVersion)
	fmt.Fprintf(os.Stdout, "Nodes (in-process):    %d (planet+moon+comet)\n", len(registry.List()))
	fmt.Fprintf(os.Stdout, "OpenAI-Compat gateway: http://%s   paths: %s\n", *listen, openaicompat.PathsString())
	fmt.Fprintf(os.Stdout, "Studio placeholder:    http://%s/\n", *listen)
	fmt.Fprintf(os.Stdout, "Control RPC:           http://%s/control/v0/   (loopback only)\n", *rpc)
	fmt.Fprintf(os.Stdout, "Data dir:              %s\n", *dataDir)
	fmt.Fprintf(os.Stdout, "Dev API key:           %s\n", devKey)
	fmt.Fprintf(os.Stdout, "Upstream LLM (BYOK):   %s\n", byokStatus(byok))
	fmt.Fprintln(os.Stdout, "Registered agents:")
	for _, a := range k.List() {
		fmt.Fprintf(os.Stdout, "  %-20s %s\n", a.ModelID(), a.Description)
	}
	fmt.Fprintln(os.Stdout)

	recorder.Record(scope.Event{
		Time: now, Kind: "log", Level: "info", Source: "cmd/soyaos",
		Message: "Solo edition started",
		Attrs:   map[string]string{"listen": *listen, "rpc": *rpc, "data_dir": *dataDir, "version": version.Version},
	})

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	errCh := make(chan error, 2)
	go func() { errCh <- dataSrv.ListenAndServe() }()
	go func() { errCh <- controlSrv.ListenAndServe() }()

	select {
	case <-ctx.Done():
		fmt.Fprintln(os.Stdout, "\nshutting down…")
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), control.ShutdownTimeout)
		defer shutdownCancel()
		_ = controlSrv.Shutdown(shutdownCtx)
		if err := dataSrv.Shutdown(shutdownCtx); err != nil {
			return err
		}
		return nil
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		// Best-effort shutdown of the other server.
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), control.ShutdownTimeout)
		defer shutdownCancel()
		_ = dataSrv.Shutdown(shutdownCtx)
		_ = controlSrv.Shutdown(shutdownCtx)
		return err
	}
}

func byokStatus(v string) string {
	if v == "" {
		return "not set (Echo agent only; real providers land in Stage 2)"
	}
	masked := v
	if len(masked) > 12 {
		masked = masked[:6] + "…" + masked[len(masked)-4:]
	}
	return fmt.Sprintf("configured (%s)", masked)
}

// defaultDataDir resolves $XDG_DATA_HOME/soyaos with the canonical fallback.
func defaultDataDir() string {
	if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
		return filepath.Join(xdg, "soyaos")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "soyaos")
	}
	return filepath.Join(home, ".local", "share", "soyaos")
}

// --- agent subcommands ------------------------------------------------------

func cmdAgent(args []string) error {
	if len(args) < 1 {
		return errors.New("agent: missing subcommand (try: list / create / run / build / deploy)")
	}
	switch args[0] {
	case "list":
		return cmdAgentList(args[1:])
	case "create":
		return cmdAgentCreate(args[1:])
	case "run":
		return cmdAgentRun(args[1:])
	case "build":
		return cmdAgentBuild(args[1:])
	case "deploy":
		return cmdAgentDeploy(args[1:])
	default:
		return fmt.Errorf("agent: unknown subcommand %q", args[0])
	}
}

func cmdAgentList(args []string) error {
	fs := flag.NewFlagSet("agent list", flag.ContinueOnError)
	rpc := fs.String("rpc", "http://"+control.DefaultListenAddr, "control RPC base URL")
	if err := fs.Parse(args); err != nil {
		return err
	}
	resp, err := http.Get(*rpc + "/control/v0/agents")
	if err != nil {
		return fmt.Errorf("contact control RPC at %s: %w (is soyaos running?)", *rpc, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("control RPC returned %d: %s", resp.StatusCode, body)
	}
	var out struct {
		Data []struct {
			ID, Slug, Description string
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return err
	}
	for _, a := range out.Data {
		fmt.Printf("%-20s %s\n", a.ID, a.Description)
	}
	return nil
}

func cmdAgentRun(args []string) error {
	fs := flag.NewFlagSet("agent run", flag.ContinueOnError)
	listen := fs.String("listen", "http://"+openaicompat.DefaultListenAddr, "OpenAI-Compat gateway base URL")
	apiKey := fs.String("key", "sk-soya-dev-local", "API key for authentication")
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) < 2 {
		return errors.New("agent run: expected <slug> \"<prompt>\"")
	}
	slug, prompt := rest[0], strings.Join(rest[1:], " ")

	body, _ := json.Marshal(map[string]any{
		"model":    "soya:" + strings.TrimPrefix(slug, "soya:"),
		"messages": []map[string]string{{"role": "user", "content": prompt}},
	})
	req, err := http.NewRequest(http.MethodPost, *listen+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+*apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("contact gateway at %s: %w (is soyaos running?)", *listen, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("gateway returned %d: %s", resp.StatusCode, raw)
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return err
	}
	if len(out.Choices) == 0 {
		return errors.New("agent run: empty response")
	}
	fmt.Println(out.Choices[0].Message.Content)
	return nil
}

func cmdAgentCreate(args []string) error {
	if len(args) < 1 || strings.HasPrefix(args[0], "-") {
		return errors.New("agent create: expected <name>")
	}
	name := args[0]
	if !isValidSlug(name) {
		return fmt.Errorf("agent create: %q is not a valid slug (lowercase, hyphens, 1-48 chars)", name)
	}
	if _, err := os.Stat(name); err == nil {
		return fmt.Errorf("agent create: %s already exists", name)
	}

	dirs := []string{
		name,
		filepath.Join(name, "prompts"),
		filepath.Join(name, "templates"),
		filepath.Join(name, "examples"),
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	files := map[string]string{
		filepath.Join(name, "soyapack.yaml"): fmt.Sprintf(soyapackTemplate, name, name),
		filepath.Join(name, "README.md"):     fmt.Sprintf(readmeTemplate, name),
		filepath.Join(name, "prompts", "main.md"): mainPromptTemplate,
		filepath.Join(name, ".gitignore"):    gitignoreTemplate,
	}
	for path, body := range files {
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			return err
		}
	}
	fmt.Printf("Created Agent scaffold: ./%s/\n\n", name)
	fmt.Println("Next:")
	fmt.Printf("  cd %s\n", name)
	fmt.Println("  $EDITOR soyapack.yaml prompts/main.md")
	fmt.Println("  soyaos agent build .")
	return nil
}

func cmdAgentBuild(args []string) error {
	// alpha.0 stub — full implementation lives in Stage 2 S2-A2 (pkg/soyapack).
	return errors.New("agent build: not implemented in v0.1.0-alpha.0 — see roadmap S2-A2 (pkg/soyapack)")
}

func cmdAgentDeploy(args []string) error {
	return errors.New("agent deploy: not implemented in v0.1.0-alpha.0 — see roadmap S2-A2 (pkg/soyapack)")
}

func isValidSlug(s string) bool {
	if len(s) < 1 || len(s) > 48 {
		return false
	}
	for i, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '-':
			if i == 0 || i == len(s)-1 {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// --- scaffold templates -----------------------------------------------------

const soyapackTemplate = `# SoyaPack v0 manifest. See https://github.com/soyaos/specs
spec_version: soyapack.v0
kind: Agent
name: %s
version: 0.1.0
description: TODO — one-paragraph description of what this Agent does.
authors:
  - name: TODO
    email: you@example.com
license: MIT
runtime:
  compat: ">=0.1.0 <0.2.0"
determinism: read-only
affinity: any

expose:
  openai_compat: chat
  virtual_model_id: soya:%s

prompt:
  scaffold: minimal-input-high-quality

inputs:
  - name: title
    type: string
    optional: true
`

const readmeTemplate = `# %s

A SoyaPack v0 Agent. Scaffolded by ` + "`soyaos agent create`" + `.

## Run locally

` + "```bash" + `
# In one terminal, boot soyaos:
soyaos start

# In another terminal, talk to this Agent:
soyaos agent run %[1]s "hello"
` + "```" + `

## Files

- ` + "`soyapack.yaml`" + ` — the manifest (see [SoyaPack v0 spec](https://github.com/soyaos/specs))
- ` + "`prompts/main.md`" + ` — the system prompt
- ` + "`templates/`" + ` — output templates (HTML / Markdown / etc.)
- ` + "`examples/`" + ` — input / expected-output pairs

## License

MIT
`

const mainPromptTemplate = `# Main prompt

You are a SoyaOS Agent. Replace this body with the actual system prompt.

Guidance:

- Lean on declared capabilities; do not assume any I/O that is not in capabilities.yml.
- Output should match one of the declared artifacts in soyapack.yaml.
`

const gitignoreTemplate = `# Local build output
/dist/
*.spk

# Editor / OS
.idea/
.vscode/
.DS_Store
`

// studioPlaceholder is the HTML served at GET / by the data-plane mux.
// Real SoyaStudio lands in a later milestone; for now it's a static page
// confirming the gateway is up, with quick-start curl commands.
const studioPlaceholder = `<!DOCTYPE html>
<html lang="en"><head>
<meta charset="utf-8">
<title>SoyaOS</title>
<style>
body{font:14px/1.55 ui-sans-serif,system-ui,sans-serif;max-width:720px;margin:48px auto;padding:0 24px;color:#0d0d0d}
code,pre{font:13px/1.4 ui-monospace,SFMono-Regular,Menlo,monospace;background:#f5f5f5;padding:2px 6px;border-radius:4px}
pre{padding:12px 16px;overflow-x:auto}
h1{margin:0 0 8px;font-size:24px}
.tag{font-size:11px;letter-spacing:.6px;text-transform:uppercase;color:#888}
.row{padding:8px 0;border-bottom:1px solid rgba(0,0,0,.05)}
</style>
</head><body>
<div class="tag">SoyaOS · Solo</div>
<h1>SoyaOS %s</h1>
<p>Edition: <code>%s</code> · Registered Agents: <code>%d</code></p>
<p>SoyaStudio is not yet built — this placeholder confirms the gateway is up.</p>
<h2>Quickstart</h2>
<pre>curl http://127.0.0.1:7474/v1/models \
  -H "Authorization: Bearer sk-soya-dev-local"</pre>
<pre>soyaos agent list
soyaos agent run echo "hello"</pre>
<p class="tag">Docs: github.com/soyaos/soyaos · Specs: github.com/soyaos/specs</p>
</body></html>
`
