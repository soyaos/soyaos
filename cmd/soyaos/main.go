// Command soyaos is the single multi-role SoyaOS binary.
//
// In Solo edition (the only edition wired in v0.1.0-alpha.0) Planet, Moon and
// Comet roles all run in the same Go process and share the same kernel +
// OpenAI-Compat gateway. The subcommand surface is intentionally small:
//
//   soyaos start                  boot Solo: all-in-one, listens on :6473
//   soyaos version                print build identification
//   soyaos agent list             list registered Agents
//
// Each subcommand has its own flag set parsed with stdlib `flag`.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/soyaos/soyaos/internal/buildinfo"
	"github.com/soyaos/soyaos/pkg/auth"
	"github.com/soyaos/soyaos/pkg/kernel"
	"github.com/soyaos/soyaos/pkg/openaicompat"
	"github.com/soyaos/soyaos/pkg/orbit"
	"github.com/soyaos/soyaos/pkg/scope"
	"github.com/soyaos/soyaos/pkg/version"
)

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
	fmt.Fprintf(w, `soyaos — Agent Operating System (Solo edition · %s · %s)

Usage:
  soyaos start [--listen :6473]   boot Solo all-in-one (Planet+Moon+Comet)
  soyaos version                  print build identification
  soyaos agent list               list registered Agents
  soyaos help                     show this message

Pre-release. APIs are unstable. See https://github.com/soyaos/soyaos for docs.
`, version.Version, version.Edition)
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
	listen := fs.String("listen", openaicompat.DefaultListenAddr, "address for the OpenAI-Compat gateway")
	if err := fs.Parse(args); err != nil {
		return err
	}

	now := time.Now()
	recorder := scope.NewMemory()
	registry := orbit.NewRegistry()
	registry.SeedSolo(now)

	store := auth.NewMemoryStore()
	devKey := store.SeedDevKey()

	k := kernel.New()
	k.Register(kernel.EchoAgent)

	gateway := openaicompat.NewServer(k, store)

	mux := http.NewServeMux()
	mux.Handle("/", gateway.Handler())
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"status":"ok","edition":%q,"version":%q,"agents":%d}`, version.Edition, version.Version, len(k.List()))
	})

	srv := &http.Server{
		Addr:              *listen,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	fmt.Fprintf(os.Stdout, "soyaos %s (%s)\n", version.Version, version.Edition)
	fmt.Fprintf(os.Stdout, "Nodes (in-process): %d\n", len(registry.List()))
	fmt.Fprintf(os.Stdout, "OpenAI-Compat gateway: http://%s%s\n", localAddr(*listen), "  (paths: "+openaicompat.PathsString()+")")
	fmt.Fprintf(os.Stdout, "Dev API key: %s\n", devKey)
	fmt.Fprintln(os.Stdout, "Registered agents:")
	for _, a := range k.List() {
		fmt.Fprintf(os.Stdout, "  %-20s %s\n", a.ModelID(), a.Description)
	}
	fmt.Fprintln(os.Stdout)

	recorder.Record(scope.Event{
		Time: now, Kind: "log", Level: "info", Source: "cmd/soyaos",
		Message: "Solo edition started",
		Attrs:   map[string]string{"listen": *listen, "version": version.Version},
	})

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		fmt.Fprintln(os.Stdout, "\nshutting down…")
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			return err
		}
		return nil
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

// cmdAgent implements the `soyaos agent ...` subcommand tree.
//
// v0.1.0-alpha.0 wires the in-process kernel; once a control-plane API
// exists the agent commands will speak to a running soyaos instead.
func cmdAgent(args []string) error {
	if len(args) < 1 {
		return errors.New("agent: missing subcommand (try: list)")
	}
	switch args[0] {
	case "list":
		k := kernel.New()
		k.Register(kernel.EchoAgent) // mirror what `start` registers
		for _, a := range k.List() {
			fmt.Printf("%-20s %s\n", a.ModelID(), a.Description)
		}
		return nil
	default:
		return fmt.Errorf("agent: unknown subcommand %q", args[0])
	}
}

// localAddr converts a bind expression like ":6473" to a click-friendly URL
// authority. Inputs with an explicit host pass through.
func localAddr(listen string) string {
	if len(listen) > 0 && listen[0] == ':' {
		return "localhost" + listen
	}
	return listen
}
