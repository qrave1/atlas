package config

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/caarlos0/env/v11"
)

const OwnerID int64 = 728328832

type Config struct {
	TelegramToken     string `env:"TELEGRAM_BOT_TOKEN,required,notEmpty"`
	TelegramProxy     string `env:"TELEGRAM_PROXY_URL"`
	AIKey             string `env:"AI_API_KEY,required,notEmpty"`
	AIBaseURL         string `env:"AI_BASE_URL" envDefault:"https://api.proxyapi.ru/v1"`
	ChatModel         string `env:"AI_CHAT_MODEL" envDefault:"openai/gpt-5-mini"`
	TranscribeModel   string `env:"AI_TRANSCRIBE_MODEL" envDefault:"openai/gpt-4o-mini-transcribe"`
	GoogleCredentials string `env:"GOOGLE_CREDENTIALS_FILE" envDefault:"secrets/google-credentials.json"`
	GoogleToken       string `env:"GOOGLE_TOKEN_FILE" envDefault:"data/google-token.json"`
	CalendarID        string `env:"GOOGLE_CALENDAR_ID" envDefault:"primary"`
	DatabasePath      string `env:"DATABASE_PATH" envDefault:"data/atlas.db"`
}

func Load() (Config, error) {
	cfg, err := env.ParseAs[Config]()
	if err != nil {
		return cfg, fmt.Errorf("parse config: %w", err)
	}
	if strings.TrimSpace(cfg.TelegramToken) == "" || strings.TrimSpace(cfg.AIKey) == "" {
		return cfg, fmt.Errorf("set TELEGRAM_BOT_TOKEN and AI_API_KEY")
	}
	u, err := url.Parse(cfg.AIBaseURL)
	if err != nil || u.Host == "" || u.Scheme != "https" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return cfg, fmt.Errorf("AI_BASE_URL must be an https base url")
	}
	return cfg, nil
}

// HTTPClient uses only the explicitly supplied proxy, never global proxy env vars.
func HTTPClient(proxy string, timeout time.Duration) (*http.Client, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	if proxy != "" {
		u, err := url.Parse(proxy)
		if err != nil || u.Host == "" {
			return nil, fmt.Errorf("invalid proxy url")
		}
		switch u.Scheme {
		case "http", "https", "socks5", "socks5h":
		default:
			return nil, fmt.Errorf("unsupported proxy scheme")
		}
		transport.Proxy = http.ProxyURL(u)
	}
	return &http.Client{Transport: transport, Timeout: timeout, CheckRedirect: func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }}, nil
}
