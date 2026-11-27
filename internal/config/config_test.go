package config

import (
	"os"
	"strings"
	"testing"
)

func TestNewEnvTagsDefaultsAndOverrides(t *testing.T) {
	for _, name := range []string{"AI_BASE_URL", "AI_CHAT_MODEL", "AI_TRANSCRIBE_MODEL", "GOOGLE_CREDENTIALS_FILE", "GOOGLE_TOKEN_FILE", "GOOGLE_CALENDAR_ID", "DATABASE_PATH"} {
		value, present := os.LookupEnv(name)
		os.Unsetenv(name)
		t.Cleanup(func() {
			if present {
				os.Setenv(name, value)
			} else {
				os.Unsetenv(name)
			}
		})
	}
	t.Setenv("TELEGRAM_BOT_TOKEN", "test-token")
	t.Setenv("AI_API_KEY", "test-key")
	cfg, err := New()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Database.Path != "data/atlas.db" || cfg.Google.CalendarID != "primary" || cfg.AI.BaseURL != "https://api.proxyapi.ru/v1" {
		t.Fatalf("defaults not parsed: %+v", cfg)
	}
	t.Setenv("DATABASE_PATH", "custom/state.db")
	t.Setenv("TELEGRAM_PROXY_URL", "socks5://user:pass@proxy.example:1080")
	cfg, err = New()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Database.Path != "custom/state.db" || cfg.Telegram.ProxyURL != "socks5://user:pass@proxy.example:1080" {
		t.Fatal("env overrides not parsed")
	}
}

func TestNewRejectsEmptyRequiredSecrets(t *testing.T) {
	t.Setenv("TELEGRAM_BOT_TOKEN", "")
	t.Setenv("AI_API_KEY", "test-key")
	_, err := New()
	if err == nil || !strings.Contains(err.Error(), "TELEGRAM_BOT_TOKEN") {
		t.Fatal("required token not enforced")
	}
}
