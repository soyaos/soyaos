package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/soyaos/soyaos/pkg/mesh/relay"
)

const defaultRelaySecretEnv = "SOYAOS_RELAY_SECRET"

func cmdRelay(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("relay: expected serve or token")
	}
	switch args[0] {
	case "serve":
		return cmdRelayServe(args[1:])
	case "token":
		return cmdRelayToken(args[1:])
	default:
		return fmt.Errorf("relay: unknown command %q (expected serve or token)", args[0])
	}
}

func cmdRelayServe(args []string) error {
	fs := flag.NewFlagSet("relay serve", flag.ContinueOnError)
	listen := fs.String("listen", "0.0.0.0:7443", "UDP relay listen address")
	healthListen := fs.String("health-listen", "127.0.0.1:7480", "HTTP health listen address")
	secretEnv := fs.String("secret-env", defaultRelaySecretEnv, "environment variable containing the token-signing secret")
	rateMbps := fs.Int("rate-mbps", 10, "per-session free-quota rate in Mbit/s")
	burstMiB := fs.Int("burst-mib", 4, "per-session burst allowance in MiB")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *rateMbps < 1 || *rateMbps > 10000 {
		return fmt.Errorf("relay: --rate-mbps must be between 1 and 10000")
	}
	if *burstMiB < 1 || *burstMiB > 1024 {
		return fmt.Errorf("relay: --burst-mib must be between 1 and 1024")
	}
	secret := []byte(os.Getenv(*secretEnv))
	if len(secret) < 32 {
		return fmt.Errorf("relay: %s must contain at least 32 bytes; generate with `openssl rand -base64 32`", *secretEnv)
	}

	packetConn, err := net.ListenPacket("udp", *listen)
	if err != nil {
		return fmt.Errorf("relay: listen UDP: %w", err)
	}
	server, err := relay.NewServer(packetConn, relay.ServerConfig{
		Secret:             secret,
		RateBytesPerSecond: int64(*rateMbps) * 1024 * 1024 / 8,
		BurstBytes:         int64(*burstMiB) * 1024 * 1024,
	})
	if err != nil {
		_ = packetConn.Close()
		return err
	}

	healthServer := &http.Server{
		Addr:              *healthListen,
		Handler:           server.HealthHandler(),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       30 * time.Second,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	errCh := make(chan error, 2)
	go func() { errCh <- server.Serve(ctx) }()
	go func() {
		err := healthServer.ListenAndServe()
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		errCh <- err
	}()

	fmt.Fprintf(os.Stdout, "SoyaOS relay listening on udp://%s (health http://%s/healthz)\n", *listen, *healthListen)
	select {
	case <-ctx.Done():
	case err := <-errCh:
		if err != nil {
			stop()
			_ = server.Close()
			_ = healthServer.Close()
			return err
		}
	}
	stop()
	_ = server.Close()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return healthServer.Shutdown(shutdownCtx)
}

func cmdRelayToken(args []string) error {
	fs := flag.NewFlagSet("relay token", flag.ContinueOnError)
	endpoint := fs.String("endpoint", "", "public relay host:port used to build the two client URIs")
	ttl := fs.Duration("ttl", 5*time.Minute, "routing token lifetime")
	secretEnv := fs.String("secret-env", defaultRelaySecretEnv, "environment variable containing the token-signing secret")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*endpoint) == "" {
		return fmt.Errorf("relay token: --endpoint host:port is required")
	}
	secret := []byte(os.Getenv(*secretEnv))
	token, err := relay.IssueToken(secret, *ttl, time.Now())
	if err != nil {
		return err
	}
	moon, err := relay.URI(*endpoint, token, relay.SideMoon)
	if err != nil {
		return err
	}
	comet, err := relay.URI(*endpoint, token, relay.SideComet)
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(struct {
		ExpiresAt     time.Time `json:"expires_at"`
		MoonEndpoint  string    `json:"moon_endpoint"`
		CometEndpoint string    `json:"comet_endpoint"`
	}{
		ExpiresAt:     token.ExpiresAt().UTC(),
		MoonEndpoint:  moon,
		CometEndpoint: comet,
	})
}
