package config

import (
	"fmt"
	"net/http"
	"time"

	"net/url"
	"strings"

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
	ChatModel       string `env:"AI_CHAT_MODEL" envDefault:"deepseek/deepseek-v4.1-flash-1017"`
	TranscribeModel string `env:"AI_TRANSCRIBE_MODEL" envDefault:"openai/whisper-large-v3"`
}

type Google struct {
	CredentialsFile string `env:"GOOGLE_CREDENTIALS_FILE" envDefault:"./secrets/google-credentials.json"`
	TokenFile       string `env:"GOOGLE_TOKEN_FILE" envDefault:"./secrets/google-token.json"`
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

func DirectHTTPClient(timeout time.Duration) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	return &http.Client{Transport: transport, Timeout: timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func (t Telegram) HTTPClient() (*http.Client, error) {
	client := DirectHTTPClient(70 * time.Second)
	if t.ProxyURL == "" {
		return client, nil
	}
	proxy, err := url.Parse(t.ProxyURL)
	if err != nil || proxy.Host == "" {
		return nil, fmt.Errorf("invalid proxy url")
	}
	switch proxy.Scheme {
	case "http", "https", "socks5", "socks5h":
		client.Transport.(*http.Transport).Proxy = http.ProxyURL(proxy)
	default:
		return nil, fmt.Errorf("unsupported proxy scheme")
	}
	return client, nil
}
