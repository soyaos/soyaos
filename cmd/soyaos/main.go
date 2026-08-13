// Command soyaos is the single multi-role SoyaOS binary.
//
// In Solo edition (the only edition wired in v0.1.0-alpha.0) Planet, Moon and
// Comet roles all run in the same Go process and share the same kernel +
// OpenAI-Compat gateway.
//
// CLI surface is locked by soyaos/specs (cli.v0):
//
//	soyaos start                  boot Solo: all-in-one, OpenAI-Compat on :7474, control RPC on :7475
//	soyaos version                print build identification
//	soyaos agent create <name>    scaffold a SoyaPack v0 Agent
//	soyaos agent list             list registered Agents (talks to a running soyaos)
//	soyaos agent run <slug> "..." invoke an Agent once (talks to a running soyaos)
//	soyaos agent build [<path>]   build a canonical SoyaPack v0 .spk archive
//	soyaos agent deploy <pack>    register a built .spk with a running soyaos
//	soyaos pack validate <path>   parse + validate a SoyaPack v0 manifest
//
// Each subcommand has its own flag set parsed with stdlib `flag`.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/soyaos/soyaos/internal/buildinfo"
	"github.com/soyaos/soyaos/internal/studio"
	"github.com/soyaos/soyaos/pkg/auth"
	"github.com/soyaos/soyaos/pkg/connectors/dingtalk"
	"github.com/soyaos/soyaos/pkg/control"
	"github.com/soyaos/soyaos/pkg/kernel"
	"github.com/soyaos/soyaos/pkg/llmcall"
	"github.com/soyaos/soyaos/pkg/openaicompat"
	"github.com/soyaos/soyaos/pkg/orbit"
	"github.com/soyaos/soyaos/pkg/scheduler"
	"github.com/soyaos/soyaos/pkg/scope"
	"github.com/soyaos/soyaos/pkg/soyapack"
	"github.com/soyaos/soyaos/pkg/store"
	"github.com/soyaos/soyaos/pkg/version"
)

// SpecVersion is the CLI surface version this binary implements.
// Locked by soyaos/specs/specs/cli/v0.md.
const SpecVersion = "cli.v0"

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
	case "pack":
		exit(cmdPack(os.Args[2:]))
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
  soyaos agent build [<path>]     build a canonical SoyaPack v0 .spk archive
  soyaos agent deploy <pack> [--rpc URL]
                                  upload a built .spk to a running soyaos
  soyaos pack validate <path>     parse + validate a SoyaPack v0 manifest
                                  (<path> is a directory containing soyapack.yaml
                                  or a path to a .yaml file)
  soyaos help                     show this message

Environment (all optional — soyaos boots with zero config):
  SOYA_MODEL_API_KEY              upstream LLM API key (BYOK). When set, the
                                  binary registers a "soya:llm" Agent that
                                  proxies any OpenAI-Compatible endpoint.
  SOYA_MODEL_BASE_URL             upstream base URL.
                                  Default: https://api.openai.com/v1
                                  Examples: https://api.deepseek.com/v1,
                                  https://api.moonshot.cn/v1,
                                  http://localhost:11434/v1 (Ollama)
  SOYA_MODEL_DEFAULT              upstream model id used when the caller
                                  targets a "soya:*" virtual model id.
                                  Default: gpt-4o-mini
  SOYA_MODEL_ENABLE_THINKING      optional true/false vendor extension for
                                  mixed-thinking OpenAI-compatible models.
                                  Unset by default (field omitted upstream).
  SOYAOS_CHROME                   Chrome binary path override for PDF /
                                  long_image artifact renderers.

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
	k.SetLogger(func(format string, args ...any) {
		fmt.Fprintf(os.Stderr, "[kernel] "+format+"\n", args...)
	})

	// When the operator supplies SOYA_MODEL_API_KEY (plus optionally
	// SOYA_MODEL_BASE_URL / SOYA_MODEL_DEFAULT), expose a BYOK Agent at
	// `soya:llm` backed by an OpenAI-Compatible upstream. Otherwise only the
	// echo Agent is registered — the binary still boots cleanly.
	llmCfg := llmcall.LoadConfigFromEnv()
	if llmCfg.Configured() {
		k.Register(kernel.NewLLMAgent("llm", llmCfg))
	}

	// --- Scheduler + channel hooks (APP-552) -----------------------------
	//
	// The scheduler ticks at 1Hz against persisted jobs in soyaStore.
	// Schedules declared in a Pack manifest are registered through the
	// kernel.ScheduleHook below. Channel publishers (DingTalk for the
	// alpha) are resolved on-demand by the ChannelHook, which
	// dereferences ${ENV_NAME} secret refs against the process env.
	tw := scheduler.NewTimeWheel()
	defer func() { _ = tw.Stop(context.Background()) }()
	k.SetScheduleHook(makeScheduleHook(tw, soyaStore))
	k.SetChannelHook(channelHookForEnv())

	// Re-load every Pack previously deployed under <data-dir>/packs/* so
	// `soyaos start` is idempotent: a Pack that was deployed via POST
	// /control/v0/packs in a prior process is still wired up after a
	// restart. Failures are warned but never fatal — one corrupt Pack
	// must not prevent the binary from booting.
	if loaded, warnings := reloadDeployedPacks(*dataDir, k); loaded > 0 || len(warnings) > 0 {
		fmt.Fprintf(os.Stdout, "Re-loaded %d pack(s) from %s\n", loaded, filepath.Join(*dataDir, "packs"))
		for _, w := range warnings {
			fmt.Fprintln(os.Stderr, "  warn:", w)
		}
	}

	// --- data plane: OpenAI-Compat gateway on :7474 ---
	//
	// Row-token setup is deliberately non-fatal: a damaged or unwritable key
	// path disables the narrowly-scoped JWT fallback, while ordinary sk-soya
	// authentication and the rest of the Solo runtime remain available.
	gateway, rowTokenKeyPath, rowTokenErr := newDataPlaneGateway(k, keys, "")
	if rowTokenErr != nil {
		fmt.Fprintln(os.Stderr, "  warn: row-token authentication disabled:", rowTokenErr)
	}
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
	// SoyaOS Studio — the embedded SPA from soyaos/studio. The handler does
	// its own SPA fallback so client-side routes (/chat, /agents, /keys,
	// /trace) survive a hard reload. /v1/*, /v1/models, /healthz are
	// registered above and take precedence over this catch-all.
	dataMux.Handle("/", studio.Handler())

	dataSrv := &http.Server{
		Addr:              *listen,
		Handler:           dataMux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	// --- control plane: RPC on :7475 (loopback-only) ---
	controlSrv := &http.Server{
		Addr:              *rpc,
		Handler:           control.NewServer(k).WithDataDir(*dataDir).Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	fmt.Fprintf(os.Stdout, "soyaos %s (edition: %s · spec: %s)\n", version.Version, version.Edition, SpecVersion)
	fmt.Fprintf(os.Stdout, "Nodes (in-process):    %d (planet+moon+comet)\n", len(registry.List()))
	fmt.Fprintf(os.Stdout, "OpenAI-Compat gateway: http://%s   paths: %s\n", *listen, openaicompat.PathsString())
	fmt.Fprintf(os.Stdout, "Studio:                http://%s/                          (chat / agents / keys / trace)\n", *listen)
	fmt.Fprintf(os.Stdout, "Control RPC:           http://%s/control/v0/   (loopback only)\n", *rpc)
	fmt.Fprintf(os.Stdout, "Data dir:              %s\n", *dataDir)
	fmt.Fprintf(os.Stdout, "Dev API key:           %s\n", devKey)
	if rowTokenErr == nil {
		fmt.Fprintf(os.Stdout, "Row-token key:         %s\n", rowTokenKeyPath)
	} else {
		fmt.Fprintln(os.Stdout, "Row-token key:         disabled (see warning above)")
	}
	fmt.Fprintf(os.Stdout, "Upstream LLM (BYOK):   %s\n", byokStatus(llmCfg))
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

// newDataPlaneGateway constructs the exact gateway used by `soyaos start`
// and wires its persistent row-token verifier. An empty rowTokenKeyPath uses
// auth.DefaultRowTokenKeyPath. The gateway is returned even on setup failure
// so cmdStart can warn and preserve zero-configuration startup; in that case
// RowTokens remains nil and row-token authentication fails closed.
func newDataPlaneGateway(k *kernel.Kernel, verifier auth.Verifier, rowTokenKeyPath string) (*openaicompat.Server, string, error) {
	gateway := openaicompat.NewServer(k, verifier)
	if rowTokenKeyPath == "" {
		var err error
		rowTokenKeyPath, err = auth.DefaultRowTokenKeyPath()
		if err != nil {
			return gateway, "", err
		}
	}
	signer, err := auth.LoadOrCreateRowTokenSigner(rowTokenKeyPath)
	if err != nil {
		return gateway, rowTokenKeyPath, err
	}
	gateway.RowTokens = signer
	return gateway, rowTokenKeyPath, nil
}

func byokStatus(cfg llmcall.Config) string {
	if !cfg.Configured() {
		return fmt.Sprintf("not set (only soya:echo answers; export %s to enable soya:llm)", llmcall.EnvAPIKey)
	}
	masked := cfg.APIKey
	if len(masked) > 12 {
		masked = masked[:6] + "…" + masked[len(masked)-4:]
	}
	return fmt.Sprintf("%s · model=%s · key=%s", cfg.BaseURL, cfg.Model, masked)
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
	if err := fs.Parse(reorderForFlagSet(fs, args)); err != nil {
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
	if err := fs.Parse(reorderForFlagSet(fs, args)); err != nil {
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
		filepath.Join(name, "soyapack.yaml"):      fmt.Sprintf(soyapackTemplate, name, name),
		filepath.Join(name, "README.md"):          fmt.Sprintf(readmeTemplate, name),
		filepath.Join(name, "prompts", "main.md"): mainPromptTemplate,
		filepath.Join(name, ".gitignore"):         gitignoreTemplate,
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

// cmdAgentBuild produces a canonical (reproducible) .spk archive from a
// SoyaPack source directory. The archive is a gzipped tar of every regular
// file in the source tree (minus a small exclusion set — see packExclude)
// with every per-file timestamp / uid / gid / username normalized so two
// builds of the same tree produce byte-identical bytes.
func cmdAgentBuild(args []string) error {
	src := "."
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			src = a
			break
		}
	}
	info, err := os.Stat(src)
	if err != nil {
		return fmt.Errorf("agent build: stat %s: %w", src, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("agent build: %s is not a directory", src)
	}

	manifestPath := filepath.Join(src, "soyapack.yaml")
	m, err := soyapack.LoadFromFile(manifestPath)
	if err != nil {
		return err
	}
	if err := soyapack.Validate(m); err != nil {
		return err
	}

	distDir := filepath.Join(src, "dist")
	if err := os.MkdirAll(distDir, 0o755); err != nil {
		return fmt.Errorf("agent build: mkdir dist: %w", err)
	}
	spkPath := filepath.Join(distDir, fmt.Sprintf("%s-%s.spk", m.Name, m.Version))
	sumPath := spkPath + ".sha256"

	sum, size, err := buildSPK(src, spkPath)
	if err != nil {
		return fmt.Errorf("agent build: %w", err)
	}

	sumLine := fmt.Sprintf("%s  %s\n", sum, filepath.Base(spkPath))
	if err := os.WriteFile(sumPath, []byte(sumLine), 0o644); err != nil {
		return fmt.Errorf("agent build: write sha256: %w", err)
	}

	relSpk, err := filepath.Rel(src, spkPath)
	if err != nil {
		relSpk = spkPath
	}
	fmt.Printf("built %s · sha256=%s · size=%d\n", relSpk, sum, size)
	return nil
}

// cmdAgentDeploy uploads a built .spk to a running soyaos via the control
// RPC pack-deploy endpoint. The handshake mirrors what `agent build` writes
// to disk: the .spk is the canonical archive, and the .sha256 sidecar is
// authoritative when present (we recompute the digest in any case and pass
// it in the X-Spk-Sha256 header so the server can detect transport
// tampering).
func cmdAgentDeploy(args []string) error {
	fs := flag.NewFlagSet("agent deploy", flag.ContinueOnError)
	rpc := fs.String("rpc", "http://"+control.DefaultListenAddr, "control RPC base URL")
	if err := fs.Parse(reorderForFlagSet(fs, args)); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) < 1 {
		return errors.New("agent deploy: expected <pack.spk>")
	}
	spkPath := rest[0]

	info, err := os.Stat(spkPath)
	if err != nil {
		return fmt.Errorf("agent deploy: stat %s: %w", spkPath, err)
	}
	if info.IsDir() {
		return fmt.Errorf("agent deploy: %s is a directory, expected a .spk file", spkPath)
	}

	digest, err := sha256OfFile(spkPath)
	if err != nil {
		return fmt.Errorf("agent deploy: hash %s: %w", spkPath, err)
	}

	// Build the multipart body in a pipe so we don't hold the whole .spk
	// in RAM. For a 32 MiB cap that's not a real constraint, but matching
	// the server's streaming shape keeps memory profiles flat for any
	// follow-up cap raise.
	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)
	go func() {
		defer pw.Close()
		part, err := mw.CreateFormFile("pack", filepath.Base(spkPath))
		if err != nil {
			_ = pw.CloseWithError(err)
			return
		}
		f, err := os.Open(spkPath)
		if err != nil {
			_ = pw.CloseWithError(err)
			return
		}
		defer f.Close()
		if _, err := io.Copy(part, f); err != nil {
			_ = pw.CloseWithError(err)
			return
		}
		if err := mw.Close(); err != nil {
			_ = pw.CloseWithError(err)
			return
		}
	}()

	req, err := http.NewRequest(http.MethodPost, *rpc+"/control/v0/packs", pr)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("X-Spk-Sha256", digest)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("contact control RPC at %s: %w (is soyaos running?)", *rpc, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("control RPC returned %d: %s", resp.StatusCode, raw)
	}

	var out struct {
		Slug           string `json:"slug"`
		VirtualModelID string `json:"virtual_model_id"`
		Files          int    `json:"files"`
		Size           int64  `json:"size"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return fmt.Errorf("agent deploy: decode response: %w (body=%s)", err, raw)
	}
	fmt.Printf("deployed %s · ready (files=%d, size=%d)\n", out.VirtualModelID, out.Files, out.Size)
	return nil
}

// sha256OfFile streams path through sha256 and returns the lowercase hex
// digest. Mirrors the format `agent build` writes into the .spk.sha256
// sidecar so caller round-trips byte-identically.
func sha256OfFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// reloadDeployedPacks walks <dataDir>/packs/*/soyapack.yaml and registers
// each Pack against k. Returns the count of successfully loaded Packs and
// a list of human-readable warnings for the ones that failed.
//
// The whole step is best-effort: a partially-broken Pack tree must NOT
// prevent `soyaos start` from booting (the operator can fix or remove the
// offending dir and restart). We log warnings rather than returning an
// error.
func reloadDeployedPacks(dataDir string, k *kernel.Kernel) (loaded int, warnings []string) {
	packsRoot := filepath.Join(dataDir, "packs")
	entries, err := os.ReadDir(packsRoot)
	if err != nil {
		// Missing directory is normal on first boot.
		if !errors.Is(err, os.ErrNotExist) {
			warnings = append(warnings, fmt.Sprintf("read %s: %v", packsRoot, err))
		}
		return 0, warnings
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		packDir := filepath.Join(packsRoot, e.Name())
		manifestPath := filepath.Join(packDir, "soyapack.yaml")
		if _, statErr := os.Stat(manifestPath); statErr != nil {
			warnings = append(warnings, fmt.Sprintf("skip %s: no soyapack.yaml", e.Name()))
			continue
		}
		m, err := soyapack.LoadFromFile(manifestPath)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("skip %s: load: %v", e.Name(), err))
			continue
		}
		if err := soyapack.Validate(m); err != nil {
			warnings = append(warnings, fmt.Sprintf("skip %s: validate: %v", e.Name(), err))
			continue
		}
		if err := k.RegisterFromPack(m, packDir); err != nil {
			warnings = append(warnings, fmt.Sprintf("skip %s: register: %v", e.Name(), err))
			continue
		}
		loaded++
	}
	return loaded, warnings
}

// makeScheduleHook returns the kernel.ScheduleHook used by `soyaos
// start`. It translates the kernel-level ScheduleSpec into a
// scheduler.Job and adds it to the time wheel, while also persisting
// the job's spec so a process restart re-hydrates it. Idempotency
// keys are honored across the dedup window (DD-007 §missed-fire).
func makeScheduleHook(tw *scheduler.TimeWheel, s store.Store) kernel.ScheduleHook {
	return func(jobID string, spec kernel.ScheduleSpec, fire func(ctx context.Context)) error {
		policy := scheduler.MissedFirePolicy(spec.MissedFire)
		if policy == "" {
			policy = scheduler.MissedFireSkip
		}
		j := scheduler.Job{
			ID:             jobID,
			Cron:           spec.Cron,
			IdempotencyKey: spec.IdempotencyKey,
			MissedFire:     policy,
			Fire:           fire,
		}
		// SoyaPack v0 only ships cron schedules from the manifest layer;
		// one-shot RunAt support arrives once the spec surfaces it.
		if err := tw.Add(j); err != nil {
			return fmt.Errorf("time wheel add: %w", err)
		}
		if err := scheduler.SavePersistent(context.Background(), s, j, policy); err != nil {
			return fmt.Errorf("scheduler.SavePersistent: %w", err)
		}
		return nil
	}
}

// channelHookForEnv returns the kernel.ChannelHook used by `soyaos
// start`. For the alpha milestone only DingTalk text/markdown push is
// supported; richer message kinds (long_image, actionCard) require
// the OSS upload + chromedp render pipeline scheduled for v0.1.x.
//
// The hook dereferences `${ENV_NAME}` secret refs at resolve time. A
// missing env var is reported as an error so RegisterFromPack can
// log + skip rather than failing registration.
func channelHookForEnv() kernel.ChannelHook {
	return func(decl kernel.ChannelBindingSpec) (kernel.ChannelPublisher, error) {
		switch decl.Kind {
		case "dingtalk":
			tokenRef := decl.Secrets["access_token_ref"]
			secretRef := decl.Secrets["secret_ref"]
			token, err := resolveEnvRef(tokenRef)
			if err != nil {
				return nil, fmt.Errorf("dingtalk access_token_ref: %w", err)
			}
			secret, _ := resolveEnvRef(secretRef) // optional; bare-token robots work without HMAC
			return &dingtalkPublisher{
				out: &dingtalk.Outbound{AccessToken: token, Secret: secret},
			}, nil
		default:
			// Other channel kinds (feishu / wework / ...) aren't wired
			// in alpha — returning an error keeps the Agent reachable
			// over chat while making the gap visible in the operator log.
			return nil, fmt.Errorf("channel kind %q not wired in alpha", decl.Kind)
		}
	}
}

// dingtalkPublisher is the kernel.ChannelPublisher shim around
// pkg/connectors/dingtalk.Outbound. Text bodies are sent as markdown
// when a title is present (so DingTalk renders the digest header) and
// as plain text otherwise.
type dingtalkPublisher struct {
	out *dingtalk.Outbound
}

func (p *dingtalkPublisher) Send(ctx context.Context, title, body string) error {
	if title == "" {
		return p.out.Send(ctx, dingtalk.Message{Kind: dingtalk.KindText, Text: body})
	}
	return p.out.Send(ctx, dingtalk.Message{
		Kind:     dingtalk.KindMarkdown,
		Title:    title,
		Markdown: body,
	})
}

// resolveEnvRef accepts either a `${ENV_NAME}` ref or an empty string.
// Empty input returns ("", nil); a populated ref must resolve to a
// non-empty env value or the call errors. Inline secrets (anything
// that doesn't look like `${...}`) are rejected — soyapack.Validate
// already enforces this, but the runtime double-checks so a manually
// constructed ChannelBindingSpec can't smuggle a literal through.
func resolveEnvRef(ref string) (string, error) {
	if ref == "" {
		return "", nil
	}
	if !strings.HasPrefix(ref, "${") || !strings.HasSuffix(ref, "}") {
		return "", fmt.Errorf("not an ${ENV_NAME} ref: %q", ref)
	}
	name := strings.TrimSuffix(strings.TrimPrefix(ref, "${"), "}")
	if name == "" {
		return "", fmt.Errorf("empty env name")
	}
	v := os.Getenv(name)
	if v == "" {
		return "", fmt.Errorf("env var %s is not set", name)
	}
	return v, nil
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

// --- pack subcommands -------------------------------------------------------

func cmdPack(args []string) error {
	if len(args) < 1 {
		return errors.New("pack: missing subcommand (try: validate)")
	}
	switch args[0] {
	case "validate":
		return cmdPackValidate(args[1:])
	default:
		return fmt.Errorf("pack: unknown subcommand %q", args[0])
	}
}

// cmdPackValidate parses + validates a SoyaPack v0 manifest. The first
// positional argument is either a directory (in which case soyapack.yaml is
// appended) or a path to the manifest file directly.
func cmdPackValidate(args []string) error {
	if len(args) < 1 || strings.HasPrefix(args[0], "-") {
		return errors.New("pack validate: expected <path> (directory or .yaml file)")
	}
	path := args[0]
	yamlPath := path
	if info, err := os.Stat(path); err == nil && info.IsDir() {
		yamlPath = filepath.Join(path, "soyapack.yaml")
	}
	m, err := soyapack.LoadFromFile(yamlPath)
	if err != nil {
		return err
	}
	if err := soyapack.Validate(m); err != nil {
		return err
	}
	fmt.Printf("OK · %s/%s@%s · kind=%s\n", m.SpecVersion, m.Name, m.Version, m.Kind)
	return nil
}
