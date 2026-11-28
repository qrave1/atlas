package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	tele "gopkg.in/telebot.v4"

	"github.com/qrave1/atlas/internal/config"
	"github.com/qrave1/atlas/internal/model"
)

func sendReply(ctx context.Context, api *tele.Bot, reply model.Reply) error {
	runes := []rune(reply.Text)
	if len(runes) == 0 {
		return nil
	}
	for len(runes) > 0 {
		n := len(runes)
		if n > 3500 {
			n = 3500
		}
		text := string(runes[:n])
		runes = runes[n:]
		opts := &tele.SendOptions{}
		if len(runes) == 0 && (reply.ProposalID != "" || len(reply.Buttons) > 0) {
			keyboard := make([][]tele.InlineButton, 0, len(reply.Buttons)+1)
			for _, row := range reply.Buttons {
				buttons := make([]tele.InlineButton, 0, len(row))
				for _, button := range row {
					buttons = append(buttons, tele.InlineButton{Text: button.Text, Data: button.Data})
				}
				keyboard = append(keyboard, buttons)
			}
			if reply.ProposalID != "" {
				keyboard = append(keyboard, []tele.InlineButton{
					{Text: "Подтвердить / повторить", Data: "confirm:" + reply.ProposalID},
					{Text: "Отменить", Data: "cancel:" + reply.ProposalID},
				})
			}
			opts.ReplyMarkup = &tele.ReplyMarkup{InlineKeyboard: keyboard}
		}
		if err := retryTelegram(ctx, func() error {
			_, err := api.Send(tele.ChatID(config.OwnerID), text, opts)
			return err
		}); err != nil {
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
