// Command jobalert is a single long-running process that polls JSearch for new
// remote DevOps / React Native jobs, dedups them in SQLite, and pushes new
// matches to a Telegram chat. Configuration is entirely via environment
// variables (see .env.example).
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"hireme/internal/app"
	"hireme/internal/config"
	"hireme/internal/health"
	"hireme/internal/jsearch"
	"hireme/internal/notify"
	"hireme/internal/store"
)

func main() {
	// Self-probe mode for container HEALTHCHECK: hit our own /healthz and exit
	// 0/1. distroless has no shell or curl, so the binary is its own probe.
	if len(os.Args) > 1 && os.Args[1] == "-healthcheck" {
		os.Exit(healthcheck())
	}

	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	if err := run(log); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	// Cancel the context on SIGINT/SIGTERM for graceful shutdown (clean DB
	// close, no half-sent notifications).
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if dir := filepath.Dir(cfg.DBPath); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}

	st, err := store.Open(ctx, cfg.DBPath)
	if err != nil {
		return err
	}
	defer st.Close()

	client := jsearch.New(cfg.JSearchAPIKey)
	tg := notify.New(cfg.TelegramBotToken, cfg.TelegramChatID)
	a := app.New(cfg, client, st, tg, log)

	// Liveness endpoint: report healthy while the loop keeps completing cycles.
	// Stale after two intervals, so a wedged loop is caught without false alarms
	// on a single slow cycle. Pointless for a one-shot run.
	reporter := health.New(2*cfg.PollInterval, time.Now())
	a.SetHeartbeat(reporter.Beat)
	if cfg.HealthAddr != "" && !cfg.RunOnce {
		// Listen synchronously so a bad/used address fails loudly at startup
		// instead of vanishing into a goroutine.
		ln, err := net.Listen("tcp", cfg.HealthAddr)
		if err != nil {
			return fmt.Errorf("health endpoint %q: %w", cfg.HealthAddr, err)
		}
		mux := http.NewServeMux()
		mux.HandleFunc("/healthz", reporter.Handler())
		srv := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
		go func() {
			if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
				log.Error("health server stopped", "err", err)
			}
		}()
		defer func() {
			shCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = srv.Shutdown(shCtx)
		}()
		log.Info("health endpoint listening", "addr", cfg.HealthAddr)
	}

	return a.Run(ctx)
}

// healthcheck performs a one-shot GET of the local /healthz and maps the result
// to a process exit code (0 = healthy). HEALTH_ADDR selects the port, mirroring
// the server; a bare ":8080" resolves to localhost.
func healthcheck() int {
	addr := os.Getenv("HEALTH_ADDR")
	if addr == "" {
		addr = ":8080"
	}
	if strings.HasPrefix(addr, ":") {
		addr = "localhost" + addr
	}
	c := &http.Client{Timeout: 5 * time.Second}
	resp, err := c.Get("http://" + addr + "/healthz")
	if err != nil {
		fmt.Fprintln(os.Stderr, "healthcheck:", err)
		return 1
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintln(os.Stderr, "healthcheck: status", resp.StatusCode)
		return 1
	}
	return 0
}
