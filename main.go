package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/qrave1/atlas/internal/app"
	"github.com/qrave1/atlas/internal/config"
	"github.com/qrave1/atlas/internal/googleauth"
	"github.com/urfave/cli/v3"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := newCommand(logger, app.RunAtlas, authorizeGoogle).Run(ctx, os.Args); err != nil && !errors.Is(err, context.Canceled) {
		logger.Error("Atlas stopped", "error", err)
		var exit cli.ExitCoder
		if errors.As(err, &exit) {
			os.Exit(exit.ExitCode())
		}
		os.Exit(1)
	}
}

func newCommand(logger *slog.Logger, run func(context.Context, *slog.Logger) error, authorize func(context.Context, *cli.Command) error) *cli.Command {
	return &cli.Command{
		Name:           "atlas",
		Usage:          "Personal Telegram AI assistant",
		ExitErrHandler: func(context.Context, *cli.Command, error) {},
		OnUsageError:   func(_ context.Context, _ *cli.Command, err error, _ bool) error { return cli.Exit(err, 2) },
		Action: func(ctx context.Context, command *cli.Command) error {
			if command.Args().Present() {
				return cli.Exit(fmt.Sprintf("unknown command: %s", command.Args().First()), 2)
			}
			return run(ctx, logger)
		},
		Commands: []*cli.Command{{
			Name:  "google-auth",
			Usage: "Authorize Google Tasks",
			Flags: []cli.Flag{
				&cli.StringFlag{Name: "credentials"},
				&cli.StringFlag{Name: "token"},
			},
			Action: func(ctx context.Context, command *cli.Command) error {
				if command.Args().Present() {
					return cli.Exit("google-auth does not accept positional arguments", 2)
				}
				return authorize(ctx, command)
			},
		}},
	}
}

func authorizeGoogle(ctx context.Context, command *cli.Command) error {
	cfg, err := config.NewGoogle()
	if err != nil {
		return err
	}
	if command.IsSet("credentials") {
		cfg.CredentialsFile = command.String("credentials")
	}
	if command.IsSet("token") {
		cfg.TokenFile = command.String("token")
	}
	if strings.TrimSpace(cfg.CredentialsFile) == "" || strings.TrimSpace(cfg.TokenFile) == "" {
		return cli.Exit("credentials and token paths must not be empty", 2)
	}
	return googleauth.Authorize(ctx, cfg.CredentialsFile, cfg.TokenFile, command.Root().Writer)
}
