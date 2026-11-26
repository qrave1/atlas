package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/urfave/cli/v3"
)

func TestHelpDoesNotStartServicesOrRequireSecrets(t *testing.T) {
	t.Setenv("TELEGRAM_BOT_TOKEN", "")
	t.Setenv("AI_API_KEY", "")
	for _, args := range [][]string{{"atlas"}, {"atlas", "--help"}, {"atlas", "run", "--help"}, {"atlas", "google-auth", "--help"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			called := false
			command := newCommand(func(context.Context) error { called = true; return nil }, func(context.Context, string, string, io.Writer) error { called = true; return nil })
			var output bytes.Buffer
			command.Writer = &output
			command.ErrWriter = &output
			if err := command.Run(context.Background(), args); err != nil {
				t.Fatal(err)
			}
			if called || output.Len() == 0 {
				t.Fatal("help started a service or produced no output")
			}
		})
	}
}

func TestRunDispatchPropagatesContextAndErrors(t *testing.T) {
	type key struct{}
	ctx := context.WithValue(context.Background(), key{}, "value")
	expected := errors.New("run failed")
	command := newCommand(func(ctx context.Context) error {
		if ctx.Value(key{}) != "value" {
			t.Error("context not propagated")
		}
		return expected
	}, nil)
	command.Writer = io.Discard
	command.ErrWriter = io.Discard
	if err := command.Run(ctx, []string{"atlas", "run"}); !errors.Is(err, expected) {
		t.Fatalf("err=%v", err)
	}
}

func TestGoogleAuthUsesOnlyGoogleConfigAndFlagsOverrideEnv(t *testing.T) {
	t.Setenv("TELEGRAM_BOT_TOKEN", "")
	t.Setenv("AI_API_KEY", "")
	t.Setenv("GOOGLE_CREDENTIALS_FILE", "from-env.json")
	t.Setenv("GOOGLE_TOKEN_FILE", "token-env.json")
	for _, tt := range []struct {
		name               string
		args               []string
		credentials, token string
	}{
		{"env", []string{"atlas", "google-auth"}, "from-env.json", "token-env.json"},
		{"flags", []string{"atlas", "google-auth", "--credentials", "custom.json", "--token", "custom-token.json"}, "custom.json", "custom-token.json"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			called := false
			var output bytes.Buffer
			command := newCommand(nil, func(_ context.Context, credentials, token string, out io.Writer) error {
				called = true
				if credentials != tt.credentials || token != tt.token {
					t.Fatalf("wrong OAuth paths: %s %s", credentials, token)
				}
				if out != &output {
					t.Error("output writer not propagated")
				}
				return nil
			})
			command.Writer = &output
			command.ErrWriter = &output
			if err := command.Run(context.Background(), tt.args); err != nil {
				t.Fatal(err)
			}
			if !called {
				t.Fatal("OAuth command not called")
			}
		})
	}
}

func TestInvalidArgumentsAreRejectedBeforeServices(t *testing.T) {
	for _, args := range [][]string{
		{"atlas", "unknown"},
		{"atlas", "run", "extra"},
		{"atlas", "google-auth", "extra"},
		{"atlas", "google-auth", "--credentials", ""},
		{"atlas", "--unknown-flag"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			called := false
			command := newCommand(func(context.Context) error { called = true; return nil }, func(context.Context, string, string, io.Writer) error { called = true; return nil })
			command.Writer = io.Discard
			command.ErrWriter = io.Discard
			err := command.Run(context.Background(), args)
			var exitCoder cli.ExitCoder
			if err == nil || !errors.As(err, &exitCoder) || exitCoder.ExitCode() != 2 || called {
				t.Fatalf("err=%v called=%v", err, called)
			}
		})
	}
}
