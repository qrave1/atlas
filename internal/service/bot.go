package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	tele "gopkg.in/telebot.v4"

	"github.com/qrave1/atlas/internal/config"
	"github.com/qrave1/atlas/internal/service/planner"
)

type Transcriber interface {
	Transcribe(context.Context, []byte) (string, error)
}
type Bot struct {
	telegram    *tele.Bot
	planner     *planner.Service
	repo        planner.Repository
	transcriber Transcriber
	log         *slog.Logger
}

func New(ctx context.Context, cfg config.Telegram, service *planner.Service, repo planner.Repository, transcriber Transcriber, logger *slog.Logger) (*Bot, error) {
	client, err := cfg.HTTPClient()
	if err != nil {
		return nil, err
	}
	api, err := newTelegram(ctx, tele.Settings{Token: cfg.Token, Client: client, Updates: 1}, logger.With("proxy_enabled", cfg.ProxyURL != ""))
	if err != nil {
		return nil, err
	}
	return &Bot{api, service, repo, transcriber, logger}, nil
}

func (b *Bot) Run(ctx context.Context) error {

	if err := retryTelegram(ctx, func() error { return b.telegram.RemoveWebhook(false) }); err != nil {
		return fmt.Errorf("deleteWebhook: %w", err)
	}
	for ctx.Err() == nil {
		state, err := b.repo.Load(ctx)
		if err != nil {
			return err
		}
		updates, err := pollUpdates(ctx, b.telegram, state.Offset)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			b.log.Warn("Telegram polling failed", "error", err)
			timer := time.NewTimer(5 * time.Second)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
			continue
		}
		for _, u := range updates {
			if int64(u.ID) < state.Offset {
				continue
			}
			// Authorization precedes acknowledgements, voice downloads, AI, and Google.
			if Authorized(u) {
				opCtx, cancel := context.WithTimeout(ctx, 4*time.Minute)
				err = b.handleUpdate(opCtx, u)
				cancel()
				if err != nil {
					return err
				}
			}
			state, err = b.repo.Load(ctx)
			if err != nil {
				return err
			}
			state.Offset = int64(u.ID) + 1
			if err = b.repo.Save(ctx, state); err != nil {
				return err
			}
		}
	}
	return ctx.Err()
}

func (b *Bot) handleUpdate(ctx context.Context, u tele.Update) error {
	if !Authorized(u) {
		return nil
	}

	var reply planner.Reply
	var err error

	if u.Callback != nil {
		if err = retryTelegram(ctx, func() error { return b.telegram.Respond(u.Callback) }); err != nil {
			b.log.Warn("Callback acknowledgement failed", "error", err)
		}
		kind, id, ok := strings.Cut(u.Callback.Data, ":")
		if !ok {
			return nil
		}
		switch kind {
		case "model":
			return b.selectModel(ctx, id)
		case "confirm":
			reply, err = b.planner.Confirm(ctx, id)
		case "cancel":
			reply, err = b.planner.Cancel(ctx, id, false)
		default:
			return nil
		}
	} else {
		message := u.Message
		text := strings.TrimSpace(message.Text)
		if message.Voice != nil {
			if err = b.planner.Invalidate(ctx); err != nil {
				return sendReply(ctx, b.telegram, planner.Reply{Text: err.Error()})
			}
			if message.Voice.Duration > 300 || message.Voice.FileSize > maxVoiceBytes {
				return sendReply(ctx, b.telegram, planner.Reply{Text: "Пришлите голосовое до 5 минут и 20 МБ."})
			}
			if err = sendReply(ctx, b.telegram, planner.Reply{Text: "Распознаю голосовое…"}); err != nil {
				return err
			}
			var audio []byte
			audio, err = downloadVoice(ctx, b.telegram, message.Voice.File)
			if err == nil {
				audio, err = convertVoice(ctx, audio)
			}
			if err == nil {
				text, err = b.transcriber.Transcribe(ctx, audio)
			}
			if err != nil {
				b.log.Warn("Voice processing failed", "error", err)
				return sendReply(ctx, b.telegram, planner.Reply{Text: "Не удалось распознать голосовое. Повторите или напишите текстом. Предыдущий черновик больше не подтверждается."})
			}
			if err = sendReply(ctx, b.telegram, planner.Reply{Text: "Распознано: " + text}); err != nil {
				return err
			}
		}
		switch text {
		case "/start", "/help":
			reply.Text = "Я Atlas, ваш личный помощник. Отправьте задачу голосом или текстом. Если нет длительности — спрошу. Декомпозиция — только по просьбе. Все изменения — после кнопки подтверждения.\n\n/models — выбор моделей\n/tasks — мои события\n/cancel — отменить диалог\n/resume — продолжить прерванную запись\n/discard — прекратить оставшиеся действия без отката уже выполненных"
		case "/models":
			return b.showModels(ctx)
		case "/tasks":
			reply, err = b.planner.Tasks(ctx)
		case "/cancel":
			reply, err = b.planner.Cancel(ctx, "", false)
		case "/discard":
			reply, err = b.planner.Cancel(ctx, "", true)
		case "/resume":
			reply, err = b.planner.Resume(ctx)
		default:
			if text == "" {
				reply.Text = "Поддерживаются текст и голосовые сообщения."
			} else {
				reply, err = b.planner.Text(ctx, text)
			}
		}
	}
	if err != nil {
		// Never send upstream response bodies, URLs, API keys, or calendar tokens.
		b.log.Error("Request processing failed", "error", err)
		state, loadErr := b.repo.Load(ctx)
		if loadErr != nil {
			return loadErr
		}
		if state.Pending != nil && state.Pending.Status == "applying" {
			reply.Text = "Запись началась. Используйте /resume для продолжения или /discard для остановки оставшихся действий."
		} else {
			reply.Text = "Не удалось обработать запрос. Повторите сообщение. Если идёт запись, используйте /resume."
		}
	}
	return sendReply(ctx, b.telegram, reply)
}

func Authorized(u tele.Update) bool {
	var sender *tele.User
	message := u.Message
	if u.Callback != nil {
		sender = u.Callback.Sender
		message = u.Callback.Message
	} else if message != nil {
		sender = message.Sender
	}
	return sender != nil && sender.ID == config.OwnerID && message != nil && message.Chat != nil &&
		message.Chat.ID == config.OwnerID && message.Chat.Type == tele.ChatPrivate
}

// Poll uses the persisted offset. Telebot exposes batch getUpdates only through Raw.
func pollUpdates(ctx context.Context, api *tele.Bot, offset int64) ([]tele.Update, error) {
	var response struct{ Result []tele.Update }
	err := retryTelegram(ctx, func() error {
		data, err := api.Raw("getUpdates", map[string]any{"offset": offset, "timeout": 40, "limit": 10, "allowed_updates": []string{"message", "callback_query"}})
		if err != nil {
			return err
		}
		return json.Unmarshal(data, &response)
	})
	return response.Result, err
}
