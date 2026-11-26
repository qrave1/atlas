package main

import (
	"context"
	"errors"
	"github.com/urfave/cli/v3"
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
	cfg, err := config.New()
	if err != nil {
		return err
	}
	location, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		return err
	}
	repo, err := store.New(ctx, cfg.Database.Path)
	if err != nil {
		return err
	}
	defer repo.Close()
	state, err := repo.Load(ctx)
	if err != nil {
		return err
	}
	telegramHTTP, err := config.HTTPClient(cfg.Telegram.ProxyURL, 70*time.Second)
	if err != nil {
		return err
	}
	directHTTP, err := config.HTTPClient("", 90*time.Second)
	if err != nil {
		return err
	}
	googleHTTP, err := calendar.AuthorizedClient(ctx, directHTTP, cfg.Google.CredentialsFile, cfg.Google.TokenFile)
	if err != nil {
		return err
	}
	brain := ai.New(directHTTP, cfg.AI.BaseURL, cfg.AI.APIKey, cfg.AI.ChatModel, cfg.AI.TranscribeModel)
	cal, err := calendar.New(ctx, googleHTTP, cfg.Google.CalendarID, state.InstallationID, location)
	if err != nil {
		return err
	}
	service := planner.New(repo, brain, cal, location)
	telegram := bot.NewTelegram(telegramHTTP, cfg.Telegram.Token)
	logger.Info("Atlas started", "owner_id", config.OwnerID, "telegram_proxy_enabled", cfg.Telegram.ProxyURL != "")
	return bot.New(telegram, service, repo, brain, logger).Run(ctx)
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	command := newCommand(func(ctx context.Context) error { return run(ctx, logger) }, calendar.Authorize)
	if err := command.Run(ctx, os.Args); err != nil && !errors.Is(err, context.Canceled) {
		logger.Error("Atlas stopped", "error", err)
		var exitCoder cli.ExitCoder
		if errors.As(err, &exitCoder) {
			os.Exit(exitCoder.ExitCode())
		}
		os.Exit(1)
	}
}
