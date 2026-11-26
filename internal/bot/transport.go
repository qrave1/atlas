package bot

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	tele "gopkg.in/telebot.v4"
	"io"
	"net/http"
	"time"
)

// Telebot does not accept a context on API calls. Cancel requests when Atlas stops.
func NewTelegramClient(ctx context.Context, settings tele.Settings) (*tele.Bot, error) {
	client := *settings.Client
	base := client.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	client.Transport = telegramTransport{ctx, base}
	settings.Client = &client
	return tele.NewBot(settings)
}

type telegramTransport struct {
	ctx  context.Context
	base http.RoundTripper
}

func (t telegramTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	ctx, cancel := context.WithCancel(req.Context())
	defer cancel()
	stop := context.AfterFunc(t.ctx, cancel)
	defer stop()
	if err := t.ctx.Err(); err != nil {
		return nil, err
	}
	resp, err := t.base.RoundTrip(req.Clone(ctx))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	limit := int64(4 << 20)
	if req.Method == http.MethodGet {
		limit = maxVoiceBytes
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit || (req.Method != http.MethodGet && !json.Valid(data)) {
		return nil, errors.New("invalid telegram response")
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusTooManyRequests {
		return nil, fmt.Errorf("telegram http %d", resp.StatusCode)
	}
	resp.Body = io.NopCloser(bytes.NewReader(data))
	return resp, nil
}

func retry(ctx context.Context, call func() error) error {
	for attempt := 0; attempt < 4; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := call()
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		var flood tele.FloodError
		if !errors.As(err, &flood) || flood.RetryAfter < 1 || flood.RetryAfter > 60 {
			return errors.New("telegram request failed")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(flood.RetryAfter) * time.Second):
		}
	}
	return errors.New("telegram rate limit retry budget exhausted")
}
