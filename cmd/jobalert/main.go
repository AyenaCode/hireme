// Command jobalert is a single long-running process that polls JSearch for new
// remote DevOps / React Native jobs, dedups them in SQLite, and pushes new
// matches to a Telegram chat. Configuration is entirely via environment
// variables (see .env.example).
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"hireme/internal/app"
	"hireme/internal/config"
	"hireme/internal/filter"
	"hireme/internal/jsearch"
	"hireme/internal/notify"
	"hireme/internal/store"
)

func main() {
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
	flt := filter.New(cfg.Keywords)
	tg := notify.New(cfg.TelegramBotToken, cfg.TelegramChatID)

	return app.New(cfg, client, st, flt, tg, log).Run(ctx)
}
