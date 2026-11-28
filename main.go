package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/qrave1/atlas/internal/app"
	"github.com/urfave/cli/v3"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := newCommand(logger).Run(ctx, os.Args); err != nil && !errors.Is(err, context.Canceled) {
		logger.Error("Atlas stopped", "error", err)
		var exit cli.ExitCoder
		if errors.As(err, &exit) {
			os.Exit(exit.ExitCode())
		}
		os.Exit(1)
	}
}

func newCommand(logger *slog.Logger) *cli.Command {
	return &cli.Command{
		Name:           "atlas",
		Usage:          "Personal Telegram AI assistant",
		ExitErrHandler: func(context.Context, *cli.Command, error) {},
		OnUsageError:   func(_ context.Context, _ *cli.Command, err error, _ bool) error { return cli.Exit(err, 2) },
		Action: func(ctx context.Context, command *cli.Command) error {
			if command.Args().Present() {
				return cli.Exit(fmt.Sprintf("unknown command: %s", command.Args().First()), 2)
			}
			return app.RunAtlas(ctx, logger)
		},
	}
}
