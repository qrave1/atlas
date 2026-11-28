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
	Token string `env:"TELEGRAM_BOT_TOKEN,required,notEmpty"`
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
	TaskListID      string `env:"GOOGLE_TASK_LIST_ID" envDefault:"@default"`
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

func HTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout}
}
