package app

import (
	"context"
	"log/slog"
	"time"
	_ "time/tzdata"

	"github.com/qrave1/atlas/internal/clients/ai"
	"github.com/qrave1/atlas/internal/clients/calendar"
	"github.com/qrave1/atlas/internal/config"
	"github.com/qrave1/atlas/internal/service"
	"github.com/qrave1/atlas/internal/service/planner"
	"github.com/qrave1/atlas/internal/store"
)

func RunAtlas(ctx context.Context, logger *slog.Logger) error {
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
	directHTTP := config.DirectHTTPClient(90 * time.Second)
	brain := ai.New(directHTTP, cfg.AI.BaseURL, cfg.AI.APIKey, cfg.AI.ChatModel, cfg.AI.TranscribeModel)
	brain.SetModelsStore(repo)
	cal, err := calendar.New(ctx, directHTTP, cfg.Google, state.InstallationID, location)
	if err != nil {
		return err
	}
	plans := planner.New(repo, brain, cal, location)
	bot, err := service.New(ctx, cfg.Telegram, plans, repo, brain, logger)
	if err != nil {
		return err
	}
	logger.Info("Atlas started", "owner_id", config.OwnerID, "telegram_proxy_enabled", cfg.Telegram.ProxyURL != "")
	return bot.Run(ctx)
}
