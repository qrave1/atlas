package service

import (
	"context"
	"time"

	tele "gopkg.in/telebot.v4"

	"github.com/qrave1/atlas/internal/config"
	"github.com/qrave1/atlas/internal/service/planner"
)

func sendReply(ctx context.Context, api *tele.Bot, reply planner.Reply) error {
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
		if len(runes) == 0 && reply.ProposalID != "" {
			opts.ReplyMarkup = &tele.ReplyMarkup{InlineKeyboard: [][]tele.InlineButton{{
				{Text: "Подтвердить / повторить", Data: "confirm:" + reply.ProposalID},
				{Text: "Отменить", Data: "cancel:" + reply.ProposalID},
			}}}
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
