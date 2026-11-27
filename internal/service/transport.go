package service

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"path"
	"strings"
	"syscall"
	"time"

	tele "gopkg.in/telebot.v4"
)

// Telebot does not accept a context on API calls. Cancel requests when Atlas stops.
func newTelegram(ctx context.Context, settings tele.Settings, logger *slog.Logger) (*tele.Bot, error) {
	client := *settings.Client
	base := client.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	client.Transport = telegramTransport{ctx, base, logger}
	settings.Client = &client
	var api *tele.Bot
	started := time.Now()
	err := retryTelegram(ctx, func() error {
		var err error
		api, err = tele.NewBot(settings)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("Telegram startup check (getMe): %w", err)
	}
	if !settings.Offline {
		if api.Me == nil || api.Me.ID == 0 {
			return nil, errors.New("invalid getMe response")
		}
		logger.Info("Telegram connectivity check passed", "method", "getMe", "duration_ms", time.Since(started).Milliseconds())
	}
	return api, nil
}

type telegramTransport struct {
	ctx    context.Context
	base   http.RoundTripper
	logger *slog.Logger
}

type telegramFailure struct{ reason string }

func (e telegramFailure) Error() string { return e.reason }

func networkFailure(err error) error {
	var tlsErr *tls.CertificateVerificationError
	var dns *net.DNSError
	var netErr net.Error
	switch {
	case errors.As(err, &tlsErr):
		return telegramFailure{"TLS certificate verification failed"}
	case errors.Is(err, syscall.ECONNRESET):
		return telegramFailure{"Telegram connection reset"}
	case errors.As(err, &dns):
		return telegramFailure{"Telegram connection: DNS lookup failed"}
	case errors.As(err, &netErr) && netErr.Timeout():
		return telegramFailure{"Telegram connection timed out"}
	case errors.Is(err, syscall.ECONNREFUSED):
		return telegramFailure{"Telegram connection refused"}
	case strings.Contains(err.Error(), "username/password authentication failed"):
		return telegramFailure{"SOCKS5 authentication failed"}
	default:
		return telegramFailure{"Telegram connection failed during dial, proxy negotiation or TLS"}
	}
}
func (t telegramTransport) RoundTrip(req *http.Request) (result *http.Response, requestErr error) {
	started := time.Now()
	status := 0
	method := "unknown"
	switch name := path.Base(req.URL.Path); name {
	case "getMe", "deleteWebhook", "getUpdates", "sendMessage", "answerCallbackQuery", "getFile":
		method = name
	}
	if req.Method == http.MethodGet {
		method = "downloadFile"
	}
	defer func() {
		if t.ctx.Err() != nil {
			return
		}
		if requestErr != nil || status == http.StatusTooManyRequests {
			reason := "rate limited"
			if requestErr != nil {
				reason = "request interrupted"
				var failure telegramFailure
				if errors.As(requestErr, &failure) {
					reason = failure.reason
				}
			}
			t.logger.Warn("Telegram request failed", "method", method, "status", status, "duration_ms", time.Since(started).Milliseconds(), "reason", reason)
		}
	}()
	ctx, cancel := context.WithCancel(req.Context())
	defer cancel()
	stop := context.AfterFunc(t.ctx, cancel)
	defer stop()
	if err := t.ctx.Err(); err != nil {
		return nil, err
	}
	resp, err := t.base.RoundTrip(req.Clone(ctx))
	if err != nil {
		return nil, networkFailure(err)
	}
	status = resp.StatusCode
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusTooManyRequests {
		return nil, telegramFailure{fmt.Sprintf("Telegram HTTP %d", resp.StatusCode)}
	}
	limit := int64(4 << 20)
	if req.Method == http.MethodGet {
		limit = maxVoiceBytes
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, telegramFailure{"failed to read Telegram response"}
	}
	if int64(len(data)) > limit || (req.Method != http.MethodGet && !json.Valid(data)) {
		return nil, telegramFailure{"invalid Telegram response"}
	}

	resp.Body = io.NopCloser(bytes.NewReader(data))
	return resp, nil
}

func retryTelegram(ctx context.Context, call func() error) error {
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
			var failure telegramFailure
			if errors.As(err, &failure) {
				return failure
			}
			var apiErr *tele.Error
			if errors.As(err, &apiErr) {
				return fmt.Errorf("Telegram API error %d", apiErr.Code)
			}
			return errors.New("Telegram request failed without an API status")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(flood.RetryAfter) * time.Second):
		}
	}
	return errors.New("telegram rate limit retry budget exhausted")
}
