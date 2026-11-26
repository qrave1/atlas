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
	Telegram Telegram
	AI       AI
	Google   Google
	Database Database
}

type Telegram struct {
	Token    string `env:"TELEGRAM_BOT_TOKEN,required,notEmpty"`
	ProxyURL string `env:"TELEGRAM_PROXY_URL"`
}

type AI struct {
	APIKey          string `env:"AI_API_KEY,required,notEmpty"`
	BaseURL         string `env:"AI_BASE_URL" envDefault:"https://api.proxyapi.ru/v1"`
	ChatModel       string `env:"AI_CHAT_MODEL" envDefault:"openai/gpt-5-mini"`
	TranscribeModel string `env:"AI_TRANSCRIBE_MODEL" envDefault:"openai/gpt-4o-mini-transcribe"`
}

type Google struct {
	CredentialsFile string `env:"GOOGLE_CREDENTIALS_FILE" envDefault:"secrets/google-credentials.json"`
	TokenFile       string `env:"GOOGLE_TOKEN_FILE" envDefault:"data/google-token.json"`
	CalendarID      string `env:"GOOGLE_CALENDAR_ID" envDefault:"primary"`
}

type Database struct {
	Path string `env:"DATABASE_PATH" envDefault:"data/atlas.db"`
}

func New() (*Config, error) {
	cfg, err := env.ParseAs[Config]()
	if err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if strings.TrimSpace(cfg.Telegram.Token) == "" || strings.TrimSpace(cfg.AI.APIKey) == "" {
		return nil, fmt.Errorf("set TELEGRAM_BOT_TOKEN and AI_API_KEY")
	}
	u, err := url.Parse(cfg.AI.BaseURL)
	if err != nil || u.Host == "" || u.Scheme != "https" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return nil, fmt.Errorf("AI_BASE_URL must be an https base url")
	}
	return &cfg, nil
}

// NewGoogle parses only OAuth settings, without requiring bot or AI credentials.
func NewGoogle() (*Google, error) {
	cfg, err := env.ParseAs[Google]()
	if err != nil {
		return nil, fmt.Errorf("parse google config: %w", err)
	}
	return &cfg, nil
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
