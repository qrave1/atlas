package planner

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/qrave1/atlas/internal/model"
)

func overlaps(aStart, aEnd, bStart, bEnd time.Time) bool {
	return aStart.Before(bEnd) && bStart.Before(aEnd)
}

func (s *Service) checkSchedule(ctx context.Context, p *model.Proposal) error {
	exclude := map[string]bool{}
	for _, a := range p.Actions {
		exclude[a.EventID] = true
	}
	for i, a := range p.Actions {
		if a.Kind == "delete" || i < p.Completed {
			continue
		}
		for j, b := range p.Actions {
			if j >= i || b.Kind == "delete" {
				continue
			}
			if overlaps(a.Start, a.End, b.Start, b.End) {
				return fmt.Errorf("шаги плана пересекаются: «%s» и «%s»", b.Title, a.Title)
			}
		}
		busy, err := s.calendar.Busy(ctx, a.Start, a.End.AddDate(0, 0, 7))
		if err != nil {
			return fmt.Errorf("не удалось проверить занятость календаря; попробуйте ещё раз")
		}
		conflict := false
		for _, b := range busy {
			if !exclude[b.ID] && overlaps(a.Start, a.End, b.Start, b.End) {
				conflict = true
				break
			}
		}
		if !conflict {
			continue
		}
		// Offer alternatives only. Never move a confirmed event automatically.
		for j, b := range p.Actions {
			if j != i && b.Kind != "delete" {
				busy = append(busy, model.Event{Start: b.Start, End: b.End})
			}
		}
		options := s.alternatives(a, busy, exclude)
		if len(options) == 0 {
			return fmt.Errorf("время для «%s» занято. За ближайшие 7 дней не найдено свободных окон 09:00–21:00; предложите другое время", a.Title)
		}
		return fmt.Errorf("время для «%s» занято. Свободные варианты (МСК): %s. Какой выбрать?", a.Title, strings.Join(options, "; "))
	}
	return nil
}

func (s *Service) alternatives(a model.Action, busy []model.Event, exclude map[string]bool) []string {
	var options []string
	duration := a.End.Sub(a.Start)
	start := a.Start.Truncate(30 * time.Minute).Add(30 * time.Minute)
	for limit := a.Start.AddDate(0, 0, 7); start.Add(duration).Before(limit); start = start.Add(30 * time.Minute) {
		local, end := start.In(s.location), start.Add(duration).In(s.location)
		if start.Before(s.now()) || local.Hour() < 9 || end.Hour() > 21 || (end.Hour() == 21 && end.Minute() > 0) || local.Day() != end.Day() {
			continue
		}
		free := true
		for _, b := range busy {
			if !exclude[b.ID] && overlaps(start, start.Add(duration), b.Start, b.End) {
				free = false
				break
			}
		}
		if free {
			options = append(options, local.Format("02.01.2006 15:04")+"–"+end.Format("15:04"))
			if len(options) == 3 {
				break
			}
		}
	}
	return options
}
