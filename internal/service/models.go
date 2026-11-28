package service

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/qrave1/atlas/internal/config"
	tele "gopkg.in/telebot.v4"
)

const (
	defaultChat   = "deepseek/deepseek-v4.1-flash-1017"
	defaultSpeech = "openai/whisper-large-v3"
)

var chatModels = [...]string{defaultChat, "openai/gpt-oss-20b", "openai/gpt-oss-120b", "openai/gpt-5-nano"}
var speechModels = [...]string{defaultSpeech}

func (b *Bot) currentModels(ctx context.Context) (string, string, error) {
	state, err := b.repo.Load(ctx)
	if err != nil {
		return "", "", err
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
	return chat, speech, nil
}

func (b *Bot) showSettings(ctx context.Context) error {
	chat, speech, err := b.currentModels(ctx)
	if err != nil {
		return err
	}
	markup := &tele.ReplyMarkup{InlineKeyboard: [][]tele.InlineButton{
		{{Text: "Модель генерации", Data: "settings:chat"}},
		{{Text: "Модель транскрибации", Data: "settings:speech"}},
	}}
	return retryTelegram(ctx, func() error {
		_, err := b.telegram.Send(tele.ChatID(config.OwnerID), fmt.Sprintf("Настройки Atlas\n\nГенерация: %s\nТранскрибация: %s", chat, speech), markup)
		return err
	})
}

func (b *Bot) showModelChoices(ctx context.Context, kind string) error {
	chat, speech, err := b.currentModels(ctx)
	if err != nil {
		return err
	}
	models, current, title := chatModels[:], chat, "Выберите модель генерации"
	if kind == "speech" {
		models, current, title = speechModels[:], speech, "Выберите модель транскрибации"
	} else if kind != "chat" {
		return nil
	}
	markup := &tele.ReplyMarkup{}
	for i, m := range models {
		label := m
		if m == current {
			label = "✓ " + m
		}
		markup.InlineKeyboard = append(markup.InlineKeyboard, []tele.InlineButton{{Text: label, Data: fmt.Sprintf("model:%s:%d", kind, i)}})
	}
	markup.InlineKeyboard = append(markup.InlineKeyboard, []tele.InlineButton{{Text: "← Настройки", Data: "settings:open"}})
	return retryTelegram(ctx, func() error {
		_, err := b.telegram.Send(tele.ChatID(config.OwnerID), title+". Текущая отмечена галочкой.", markup)
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
	return b.showModelChoices(ctx, kind)
}
