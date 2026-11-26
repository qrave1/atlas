package bot

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/qrave1/atlas/internal/config"
	"github.com/qrave1/atlas/internal/planner"
)

type User struct {
	ID int64 `json:"id"`
}
type Chat struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
}
type Voice struct {
	FileID   string `json:"file_id"`
	Duration int    `json:"duration"`
	FileSize int64  `json:"file_size"`
}
type Message struct {
	From  *User  `json:"from"`
	Chat  Chat   `json:"chat"`
	Text  string `json:"text"`
	Voice *Voice `json:"voice"`
}
type Callback struct {
	ID      string   `json:"id"`
	From    User     `json:"from"`
	Message *Message `json:"message"`
	Data    string   `json:"data"`
}
type Update struct {
	ID       int64     `json:"update_id"`
	Message  *Message  `json:"message"`
	Callback *Callback `json:"callback_query"`
}

func Authorized(u Update) bool {
	if u.Message != nil {
		return u.Message.From != nil && u.Message.From.ID == config.OwnerID && u.Message.Chat.ID == config.OwnerID && u.Message.Chat.Type == "private"
	}
	if u.Callback != nil {
		return u.Callback.From.ID == config.OwnerID && u.Callback.Message != nil && u.Callback.Message.Chat.ID == config.OwnerID && u.Callback.Message.Chat.Type == "private"
	}
	return false
}

type Telegram struct {
	client         *http.Client
	baseURL, token string
}

func NewTelegram(client *http.Client, token string) *Telegram {
	return &Telegram{client, "https://api.telegram.org", token}
}

func (t *Telegram) call(ctx context.Context, method string, input, output any) error {
	body, err := json.Marshal(input)
	if err != nil {
		return err
	}
	for attempt := 0; attempt < 4; attempt++ {
		req, err := http.NewRequestWithContext(ctx, "POST", t.baseURL+"/bot"+t.token+"/"+method, bytes.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := t.client.Do(req)
		if err != nil {
			return fmt.Errorf("telegram network error")
		}
		var result struct {
			OK         bool            `json:"ok"`
			Result     json.RawMessage `json:"result"`
			ErrorCode  int             `json:"error_code"`
			Parameters struct {
				RetryAfter int `json:"retry_after"`
			} `json:"parameters"`
		}
		err = json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&result)
		resp.Body.Close()
		if (resp.StatusCode == 429 || result.ErrorCode == 429) && result.Parameters.RetryAfter > 0 && result.Parameters.RetryAfter <= 60 {
			timer := time.NewTimer(time.Duration(result.Parameters.RetryAfter) * time.Second)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
			continue
		}
		if resp.StatusCode != 200 {
			return fmt.Errorf("telegram http %d", resp.StatusCode)
		}
		if err != nil {
			return err
		}
		if !result.OK {
			return fmt.Errorf("telegram api error %d", result.ErrorCode)
		}
		if output != nil {
			return json.Unmarshal(result.Result, output)
		}
		return nil
	}
	return fmt.Errorf("telegram rate limit retry budget exhausted")
}

func (t *Telegram) Poll(ctx context.Context, offset int64) ([]Update, error) {
	var updates []Update
	err := t.call(ctx, "getUpdates", map[string]any{"offset": offset, "timeout": 40, "limit": 10, "allowed_updates": []string{"message", "callback_query"}}, &updates)
	return updates, err
}

func (t *Telegram) Start(ctx context.Context) error {
	return t.call(ctx, "deleteWebhook", map[string]any{"drop_pending_updates": false}, nil)
}
func (t *Telegram) Answer(ctx context.Context, id string) error {
	return t.call(ctx, "answerCallbackQuery", map[string]string{"callback_query_id": id}, nil)
}

func (t *Telegram) Send(ctx context.Context, reply planner.Reply) error {
	runes := []rune(reply.Text)
	if len(runes) == 0 {
		return nil
	}
	for len(runes) > 0 {
		n := len(runes)
		if n > 3500 {
			n = 3500
		}
		request := map[string]any{"chat_id": config.OwnerID, "text": string(runes[:n])}
		runes = runes[n:]
		if len(runes) == 0 && reply.ProposalID != "" {
			request["reply_markup"] = map[string]any{"inline_keyboard": [][]map[string]string{{{"text": "Подтвердить / повторить", "callback_data": "confirm:" + reply.ProposalID}, {"text": "Отменить", "callback_data": "cancel:" + reply.ProposalID}}}}
		}
		if err := t.call(ctx, "sendMessage", request, nil); err != nil {
			return err
		}
		if len(runes) > 0 {
			timer := time.NewTimer(1100 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
		}
	}
	return nil
}

const maxVoiceBytes = 20 << 20

func (t *Telegram) Download(ctx context.Context, fileID string) ([]byte, error) {
	var file struct {
		Path string `json:"file_path"`
		Size int64  `json:"file_size"`
	}
	if err := t.call(ctx, "getFile", map[string]string{"file_id": fileID}, &file); err != nil {
		return nil, err
	}
	if file.Size > maxVoiceBytes || file.Path == "" {
		return nil, fmt.Errorf("voice file is too large or missing")
	}
	// getFile returns a relative Telegram path; never accept an arbitrary download host.
	if strings.Contains(file.Path, "..") || strings.ContainsAny(file.Path, "?#\\") || strings.HasPrefix(file.Path, "/") {
		return nil, fmt.Errorf("invalid telegram file path")
	}
	u, err := url.Parse(t.baseURL + "/file/bot" + t.token + "/" + file.Path)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := t.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("telegram file download failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("telegram file http %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxVoiceBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxVoiceBytes {
		return nil, fmt.Errorf("voice too large")
	}
	return data, nil
}

type Transcriber interface {
	Transcribe(context.Context, []byte) (string, error)
}
type Runner struct {
	telegram    *Telegram
	service     *planner.Service
	repo        planner.Repository
	transcriber Transcriber
	log         *slog.Logger
}

func New(telegram *Telegram, service *planner.Service, repo planner.Repository, transcriber Transcriber, logger *slog.Logger) *Runner {
	return &Runner{telegram, service, repo, transcriber, logger}
}

func (b *Runner) Run(ctx context.Context) error {
	if err := b.telegram.Start(ctx); err != nil {
		return err
	}
	for ctx.Err() == nil {
		state, err := b.repo.Load(ctx)
		if err != nil {
			return err
		}
		updates, err := b.telegram.Poll(ctx, state.Offset)
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
			if u.ID < state.Offset {
				continue
			}
			// Authorization precedes acknowledgements, voice downloads, AI, and Google.
			if Authorized(u) {
				opCtx, cancel := context.WithTimeout(ctx, 4*time.Minute)
				err = b.handle(opCtx, u)
				cancel()
				if err != nil {
					return err
				}
			}
			state, err = b.repo.Load(ctx)
			if err != nil {
				return err
			}
			state.Offset = u.ID + 1
			if err = b.repo.Save(ctx, state); err != nil {
				return err
			}
		}
	}
	return ctx.Err()
}

func (b *Runner) handle(ctx context.Context, u Update) error {
	if !Authorized(u) {
		return nil
	}
	var reply planner.Reply
	var err error
	if u.Callback != nil {
		if err = b.telegram.Answer(ctx, u.Callback.ID); err != nil {
			b.log.Warn("Callback acknowledgement failed")
		}
		kind, id, ok := strings.Cut(u.Callback.Data, ":")
		if !ok {
			return nil
		}
		switch kind {
		case "confirm":
			reply, err = b.service.Confirm(ctx, id)
		case "cancel":
			reply, err = b.service.Cancel(ctx, id, false)
		default:
			return nil
		}
	} else {
		message := u.Message
		text := strings.TrimSpace(message.Text)
		if message.Voice != nil {
			if err = b.service.Invalidate(ctx); err != nil {
				return b.telegram.Send(ctx, planner.Reply{Text: err.Error()})
			}
			if message.Voice.Duration > 300 || message.Voice.FileSize > maxVoiceBytes {
				return b.telegram.Send(ctx, planner.Reply{Text: "Пришлите голосовое до 5 минут и 20 МБ."})
			}
			if err = b.telegram.Send(ctx, planner.Reply{Text: "Распознаю голосовое…"}); err != nil {
				return err
			}
			var audio []byte
			audio, err = b.telegram.Download(ctx, message.Voice.FileID)
			if err == nil {
				audio, err = convertVoice(ctx, audio)
			}
			if err == nil {
				text, err = b.transcriber.Transcribe(ctx, audio)
			}
			if err != nil {
				b.log.Warn("Voice processing failed", "error", err)
				return b.telegram.Send(ctx, planner.Reply{Text: "Не удалось распознать голосовое. Повторите или напишите текстом. Предыдущий черновик больше не подтверждается."})
			}
			if err = b.telegram.Send(ctx, planner.Reply{Text: "Распознано: " + text}); err != nil {
				return err
			}
		}
		switch text {
		case "/start", "/help":
			reply.Text = "Я Atlas, ваш личный помощник. Отправьте задачу голосом или текстом. Если нет длительности — спрошу. Декомпозиция — только по просьбе. Все изменения — после кнопки подтверждения.\n\n/tasks — мои события\n/cancel — отменить диалог\n/resume — продолжить прерванную запись\n/discard — прекратить оставшиеся действия без отката уже выполненных"
		case "/tasks":
			reply, err = b.service.Tasks(ctx)
		case "/cancel":
			reply, err = b.service.Cancel(ctx, "", false)
		case "/discard":
			reply, err = b.service.Cancel(ctx, "", true)
		case "/resume":
			reply, err = b.service.Resume(ctx)
		default:
			if text == "" {
				reply.Text = "Поддерживаются текст и голосовые сообщения."
			} else {
				reply, err = b.service.Text(ctx, text)
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
	return b.telegram.Send(ctx, reply)
}
