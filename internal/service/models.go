package service

import (
	"context"
	"fmt"
	"github.com/qrave1/atlas/internal/config"
	tele "gopkg.in/telebot.v4"
	"strconv"
	"strings"
)

const (
	defaultChat   = "deepseek/deepseek-v4.1-flash-1017"
	defaultSpeech = "openai/whisper-large-v3"
)

var chatModels = [...]string{defaultChat, "openai/gpt-5-mini"}
var speechModels = [...]string{defaultSpeech, "openai/gpt-4o-mini-transcribe"}

func (b *Bot) showModels(ctx context.Context) error {
	state, err := b.repo.Load(ctx)
	if err != nil {
		return err
	}
	chat, speech := state.ChatModel, state.TranscribeModel
	fallbackChat, fallbackSpeech := defaultChat, defaultSpeech
	if defaults, ok := b.transcriber.(interface{ DefaultModels() (string, string) }); ok {
		fallbackChat, fallbackSpeech = defaults.DefaultModels()
	}
	if chat == "" {
		chat = fallbackChat
	}
	if speech == "" {
		speech = fallbackSpeech
	}
	markup := &tele.ReplyMarkup{}
	for i, m := range chatModels {
		label := m
		if m == chat {
			label = "✓ " + m
		}
		markup.InlineKeyboard = append(markup.InlineKeyboard, []tele.InlineButton{{Text: label, Data: fmt.Sprintf("model:chat:%d", i)}})
	}
	for i, m := range speechModels {
		label := m
		if m == speech {
			label = "✓ " + m
		}
		markup.InlineKeyboard = append(markup.InlineKeyboard, []tele.InlineButton{{Text: label, Data: fmt.Sprintf("model:speech:%d", i)}})
	}
	return retryTelegram(ctx, func() error {
		_, err := b.telegram.Send(tele.ChatID(config.OwnerID), "Выберите модель. Текущие отмечены галочкой.", markup)
		return err
	})
}

func (b *Bot) selectModel(ctx context.Context, selection string) error {
	kind, index, ok := strings.Cut(selection, ":")
	if !ok {
		return nil
	}
	i, err := strconv.Atoi(index)
	if err != nil || i < 0 {
		return nil
	}
	state, err := b.repo.Load(ctx)
	if err != nil {
		return err
	}
	switch kind {
	case "chat":
		if i >= len(chatModels) {
			return nil
		}
		state.ChatModel = chatModels[i]
	case "speech":
		if i >= len(speechModels) {
			return nil
		}
		state.TranscribeModel = speechModels[i]
	default:
		return nil
	}
	if err = b.repo.Save(ctx, state); err != nil {
		return err
	}
	return b.showModels(ctx)
}
