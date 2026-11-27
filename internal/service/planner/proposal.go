package planner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/qrave1/atlas/internal/model"
)

func (s *Service) prepare(ctx context.Context, state *model.State, p *model.Proposal) error {
	if len(p.Actions) > 10 {
		return fmt.Errorf("не больше 10 изменений за одно подтверждение")
	}
	seen := map[string]bool{}
	for i := range p.Actions {
		a := &p.Actions[i]
		// Never trust snapshots or IDs supplied by the AI for new events.
		a.Before = nil
		switch a.Kind {
		case "create":
			sum := sha256.Sum256([]byte(p.ID + fmt.Sprint(i)))
			a.EventID = hex.EncodeToString(sum[:])
		case "update", "delete":
			if !state.Owned[a.EventID] {
				return fmt.Errorf("можно менять только события, созданные этим ботом")
			}
			if seen[a.EventID] {
				return fmt.Errorf("несколько изменений одного события нужно объединить")
			}
			seen[a.EventID] = true
			before, err := s.calendar.GetOwned(ctx, a.EventID)
			if err != nil {
				return fmt.Errorf("не удалось проверить исходное событие; обновите /tasks")
			}
			a.Before = &before
		default:
			return fmt.Errorf("неизвестный тип действия")
		}
		if a.Kind == "delete" {
			continue
		}
		if !a.DurationKnown {
			return fmt.Errorf("сколько времени выделить на «%s»?", a.Title)
		}
		if strings.TrimSpace(a.Title) == "" || utf8.RuneCountInString(a.Title) > 100 || utf8.RuneCountInString(a.Description) > 4000 {
			return fmt.Errorf("название должно содержать 1–100 символов, описание — до 4000")
		}
		if a.Start.IsZero() || !a.End.After(a.Start) || a.End.Sub(a.Start) > 24*time.Hour {
			return fmt.Errorf("укажите начало и длительность до 24 часов")
		}
		if a.Kind == "create" && a.Start.Before(s.now()) {
			return fmt.Errorf("время начала уже прошло; укажите будущую дату")
		}
		if a.Start.After(s.now().AddDate(1, 0, 0)) {
			return fmt.Errorf("планируем не дальше чем на год вперёд")
		}
	}
	return s.checkSchedule(ctx, p)
}

func (s *Service) render(p *model.Proposal) string {
	var b strings.Builder
	b.WriteString("План изменений (МСК):\n")
	for i, a := range p.Actions {
		if a.Kind == "delete" {
			fmt.Fprintf(&b, "\n%d. Удалить «%s» — %s\n", i+1, a.Before.Title, a.Before.Start.In(s.location).Format("02.01.2006 15:04"))
			continue
		}
		verb := "Создать"
		if a.Kind == "update" {
			verb = "Изменить"
			fmt.Fprintf(&b, "\nБыло: «%s», %s–%s\n", a.Before.Title, a.Before.Start.In(s.location).Format("02.01.2006 15:04"), a.Before.End.In(s.location).Format("15:04"))
		}
		fmt.Fprintf(&b, "\n%d. %s «%s»\n%s — %s (%d мин)\n", i+1, verb, a.Title, a.Start.In(s.location).Format("02.01.2006 15:04"), a.End.In(s.location).Format("02.01.2006 15:04"), int(a.End.Sub(a.Start).Minutes()))
		if a.Description != "" {
			fmt.Fprintf(&b, "%s\n", a.Description)
		}
	}
	b.WriteString("\nПодтвердите кнопкой или отправьте исправления. Пока изменения не записаны.")
	return b.String()
}
