package main

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/qrave1/atlas/internal/config"
	"github.com/urfave/cli/v3"
)

func newCommand(
	run func(context.Context) error,
	authorize func(context.Context, string, string, io.Writer) error,
) *cli.Command {
	return &cli.Command{
		Name:  "atlas",
		Usage: "Personal Telegram AI assistant",
		// Return errors to main; the CLI library must not exit inside actions.
		ExitErrHandler: func(context.Context, *cli.Command, error) {},
		OnUsageError:   func(_ context.Context, _ *cli.Command, err error, _ bool) error { return cli.Exit(err, 2) },
		Action: func(_ context.Context, command *cli.Command) error {
			if command.Args().Present() {
				return cli.Exit(fmt.Sprintf("unknown command: %s", command.Args().First()), 2)
			}
			return cli.ShowRootCommandHelp(command)
		},
		Commands: []*cli.Command{
			{
				Name: "run", Usage: "Start the Telegram bot",
				Action: func(ctx context.Context, command *cli.Command) error {
					if command.Args().Present() {
						return cli.Exit("run does not accept positional arguments", 2)
					}
					return run(ctx)
				},
			},
			{
				Name: "google-auth", Usage: "Connect Google Calendar on this computer",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "credentials", Usage: "Desktop OAuth JSON path (default: GOOGLE_CREDENTIALS_FILE or ./secrets/google-credentials.json)"},
					&cli.StringFlag{Name: "token", Usage: "Token output path (default: GOOGLE_TOKEN_FILE or ./secrets/google-token.json)"},
				},
				Action: func(ctx context.Context, command *cli.Command) error {
					if command.Args().Present() {
						return cli.Exit("google-auth does not accept positional arguments", 2)
					}
					google, err := config.NewGoogle()
					if err != nil {
						return err
					}
					if command.IsSet("credentials") {
						google.CredentialsFile = command.String("credentials")
					}
					if command.IsSet("token") {
						google.TokenFile = command.String("token")
					}
					if strings.TrimSpace(google.CredentialsFile) == "" || strings.TrimSpace(google.TokenFile) == "" {
						return cli.Exit("credentials and token paths must not be empty", 2)
					}
					return authorize(ctx, google.CredentialsFile, google.TokenFile, command.Root().Writer)
				},
			},
		},
	}
}
