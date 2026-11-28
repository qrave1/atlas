package planner

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/qrave1/atlas/internal/model"
	"github.com/qrave1/atlas/internal/store"
)

type Repository interface {
	Load(context.Context) (model.State, error)
	Save(context.Context, model.State) error
}
type Brain interface {
	Decide(context.Context, time.Time, []model.Message, []model.Event, *model.Proposal) (model.Decision, error)
}
type Calendar interface {
	ListOwned(context.Context, map[string]bool) ([]model.Event, error)
	GetOwned(context.Context, string) (model.Event, error)
	Busy(context.Context, time.Time, time.Time) ([]model.Event, error)
	Apply(context.Context, model.Action, string) error
}

type Reply = model.Reply
type Service struct {
	repo     Repository
	brain    Brain
	calendar Calendar
	location *time.Location
	now      func() time.Time
}

func New(repo Repository, brain Brain, calendar Calendar, location *time.Location) *Service {
	return &Service{repo, brain, calendar, location, time.Now}
}

func (s *Service) Invalidate(ctx context.Context) error {
	state, err := s.repo.Load(ctx)
	if err != nil {
		return err
	}
	if state.Pending == nil {
		return nil
	}
	if state.Pending.Status == "applying" {
		return fmt.Errorf("запись уже началась: используйте /resume или /discard")
	}
	if state.Pending.Status == "ready" || state.Pending.Status == "superseded" {
		state.Pending.Status = "superseded"
	} else {
		state.Pending = nil
	}
	return s.repo.Save(ctx, state)
}

func history(state *model.State, role, content string) {
	state.History = append(state.History, model.Message{Role: role, Content: content})
	if len(state.History) > 24 {
		state.History = state.History[len(state.History)-24:]
	}
}

func (s *Service) Text(ctx context.Context, text string) (Reply, error) {
	if utf8.RuneCountInString(text) > 12000 {
		return Reply{Text: "Сообщение слишком длинное. Разбейте его на несколько частей."}, nil
	}
	if err := s.Invalidate(ctx); err != nil {
		return Reply{}, err
	}
	state, err := s.repo.Load(ctx)
	if err != nil {
		return Reply{}, err
	}
	history(&state, "user", text)
	if err = s.repo.Save(ctx, state); err != nil {
		return Reply{}, err
	}
	events, err := s.calendar.ListOwned(ctx, state.Owned)
	if err != nil {
		return Reply{}, err
	}
	if len(events) > 300 {
		return Reply{Text: "Слишком много событий для одного диалога. Используйте /tasks и удалите ненужные события."}, nil
	}
	decision, err := s.brain.Decide(ctx, s.now().In(s.location), state.History, events, state.Pending)
	if err != nil {
		return Reply{}, err
	}
	if len(decision.Actions) == 0 {
		if strings.TrimSpace(decision.Reply) == "" {
			return Reply{}, fmt.Errorf("ai returned empty reply")
		}
		history(&state, "assistant", decision.Reply)
		state.Pending = nil
		return Reply{Text: decision.Reply}, s.repo.Save(ctx, state)
	}
	p := &model.Proposal{ID: store.NewID(), CreatedAt: s.now(), Status: "ready", Actions: decision.Actions}
	if err = s.prepare(ctx, &state, p); err != nil {
		reply := "Нужно уточнить план: " + err.Error()
		history(&state, "assistant", reply)
		state.Pending = nil
		return Reply{Text: reply}, s.repo.Save(ctx, state)
	}
	state.Pending = p
	text = s.render(p)
	history(&state, "assistant", text)
	return Reply{Text: text, ProposalID: p.ID}, s.repo.Save(ctx, state)
}

func (s *Service) Tasks(ctx context.Context) (Reply, error) {
	state, err := s.repo.Load(ctx)
	if err != nil {
		return Reply{}, err
	}
	events, err := s.calendar.ListOwned(ctx, state.Owned)
	if err != nil {
		return Reply{}, err
	}
	sort.Slice(events, func(i, j int) bool { return events[i].Start.Before(events[j].Start) })
	var b strings.Builder
	b.WriteString("События этого бота (МСК):\n")
	count := 0
	for _, e := range events {
		if e.End.Before(s.now().AddDate(0, 0, -7)) {
			continue
		}
		fmt.Fprintf(&b, "\n%s\n%s — %s\nID: %s\n", e.Title, e.Start.In(s.location).Format("02.01.2006 15:04"), e.End.In(s.location).Format("02.01.2006 15:04"), e.ID)
		count++
		if count == 50 {
			b.WriteString("\nПоказаны первые 50 событий. Остальные можно найти запросом по названию.")
			break
		}
	}
	if count == 0 {
		b.WriteString("Пока нет событий за последние 7 дней и на будущее.")
	}
	return Reply{Text: b.String()}, nil
}
