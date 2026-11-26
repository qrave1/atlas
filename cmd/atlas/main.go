package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"
	_ "time/tzdata"

	"github.com/qrave1/atlas/internal/ai"
	"github.com/qrave1/atlas/internal/bot"
	"github.com/qrave1/atlas/internal/calendar"
	"github.com/qrave1/atlas/internal/config"
	"github.com/qrave1/atlas/internal/planner"
	"github.com/qrave1/atlas/internal/store"
)

func run(ctx context.Context, logger *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	location, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		return err
	}
	repo, err := store.New(ctx, cfg.DatabasePath)
	if err != nil {
		return err
	}
	defer repo.Close()
	state, err := repo.Load(ctx)
	if err != nil {
		return err
	}
	telegramHTTP, err := config.HTTPClient(cfg.TelegramProxy, 70*time.Second)
	if err != nil {
		return err
	}
	directHTTP, err := config.HTTPClient("", 90*time.Second)
	if err != nil {
		return err
	}
	googleHTTP, err := calendar.AuthorizedClient(ctx, directHTTP, cfg.GoogleCredentials, cfg.GoogleToken)
	if err != nil {
		return err
	}
	brain := ai.New(directHTTP, cfg.AIBaseURL, cfg.AIKey, cfg.ChatModel, cfg.TranscribeModel)
	cal, err := calendar.New(ctx, googleHTTP, cfg.CalendarID, state.InstallationID, location)
	if err != nil {
		return err
	}
	service := planner.New(repo, brain, cal, location)
	telegram := bot.NewTelegram(telegramHTTP, cfg.TelegramToken)
	logger.Info("Atlas started", "owner_id", config.OwnerID, "telegram_proxy_enabled", cfg.TelegramProxy != "")
	return bot.New(telegram, service, repo, brain, logger).Run(ctx)
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := run(ctx, logger); err != nil && !errors.Is(err, context.Canceled) {
		logger.Error("Atlas stopped", "error", err)
		os.Exit(1)
	}
}
