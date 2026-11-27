package planner

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/qrave1/atlas/internal/clients/calendar"
	"github.com/qrave1/atlas/internal/model"
)

func (s *Service) Confirm(ctx context.Context, id string) (Reply, error) {
	state, err := s.repo.Load(ctx)
	if err != nil {
		return Reply{}, err
	}
	p := state.Pending
	if p == nil || p.ID != id || (p.Status != "ready" && p.Status != "applying") {
		return Reply{Text: "Эта кнопка устарела. Отправьте задачу заново или используйте /tasks."}, nil
	}
	if p.Status == "ready" {
		if s.now().Sub(p.CreatedAt) > 24*time.Hour {
			p.Status = "expired"
			return Reply{Text: "План старше суток. Отправьте задачу заново."}, s.repo.Save(ctx, state)
		}
		for _, a := range p.Actions {
			if a.Kind == "create" && a.Start.Before(s.now()) {
				p.Status = "expired"
				return Reply{Text: "Время начала уже прошло. Нужен новый план."}, s.repo.Save(ctx, state)
			}
		}
		// Check every original before the first write to avoid a known partial batch.
		for _, a := range p.Actions {
			if a.Kind == "create" {
				continue
			}
			current, checkErr := s.calendar.GetOwned(ctx, a.EventID)
			if checkErr != nil || a.Before == nil || current.ETag != a.Before.ETag {
				p.Status = "superseded"
				return Reply{Text: "Исходное событие изменилось или удалено вне бота. Проверьте /tasks и отправьте новый запрос."}, s.repo.Save(ctx, state)
			}
		}
	}
	if err = s.checkSchedule(ctx, p); err != nil {
		if p.Status == "ready" {
			p.Status = "superseded"
			history(&state, "assistant", err.Error())
		}
		return Reply{Text: err.Error()}, s.repo.Save(ctx, state)
	}
	return s.applyConfirmed(ctx, state)
}

func (s *Service) applyConfirmed(ctx context.Context, state model.State) (Reply, error) {
	p := state.Pending
	p.Status = "applying"
	// Register IDs before network writes so even uncertain outcomes remain tracked.
	for _, a := range p.Actions {
		if a.Kind == "create" {
			state.Owned[a.EventID] = true
		}
	}
	if err := s.repo.Save(ctx, state); err != nil {
		return Reply{}, err
	}
	for p.Completed < len(p.Actions) {
		i := p.Completed
		a := p.Actions[i]
		if err := s.calendar.Apply(ctx, a, p.ID+fmt.Sprintf("-%d", i)); err != nil {
			if errors.Is(err, calendar.ErrChanged) || errors.Is(err, calendar.ErrForeign) {
				return Reply{Text: fmt.Sprintf("Выполнено %d из %d. Событие изменилось вне бота или не принадлежит ему. Используйте /discard, проверьте /tasks и составьте новый план.", p.Completed, len(p.Actions))}, nil
			}
			return Reply{Text: fmt.Sprintf("Выполнено %d из %d. Google не подтвердил следующую операцию. Нажмите повтор или /resume; повтор не создаст дубли.", p.Completed, len(p.Actions)), ProposalID: p.ID}, nil
		}
		if a.Kind == "delete" {
			delete(state.Owned, a.EventID)
		}
		p.Completed++
		if err := s.repo.Save(ctx, state); err != nil {
			return Reply{}, err
		}
	}
	p.Status = "done"
	history(&state, "assistant", "Согласованные изменения записаны в календарь.")
	return Reply{Text: fmt.Sprintf("Готово. Выполнено изменений: %d. /tasks — посмотреть события.", p.Completed)}, s.repo.Save(ctx, state)
}

func (s *Service) Resume(ctx context.Context) (Reply, error) {
	state, err := s.repo.Load(ctx)
	if err != nil {
		return Reply{}, err
	}
	if state.Pending == nil || state.Pending.Status != "applying" {
		return Reply{Text: "Нет незавершённой записи."}, nil
	}
	return s.Confirm(ctx, state.Pending.ID)
}

func (s *Service) Cancel(ctx context.Context, id string, discard bool) (Reply, error) {
	state, err := s.repo.Load(ctx)
	if err != nil {
		return Reply{}, err
	}
	if id != "" && (state.Pending == nil || state.Pending.ID != id) {
		return Reply{Text: "Кнопка устарела."}, nil
	}
	if state.Pending != nil && state.Pending.Status == "applying" && !discard {
		return Reply{Text: "Запись уже началась. /resume — продолжить; /discard — остановить оставшиеся действия. Уже выполненные изменения сохранятся."}, nil
	}
	state.Pending = nil
	state.History = nil
	return Reply{Text: "Текущий диалог и оставшиеся действия отменены. Уже записанные события сохранены. /tasks — проверить календарь."}, s.repo.Save(ctx, state)
}
