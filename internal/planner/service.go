package planner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/qrave1/atlas/internal/calendar"
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

type Reply struct{ Text, ProposalID string }
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
	p.Status = "applying"
	// Register IDs before network writes so even uncertain outcomes remain tracked.
	for _, a := range p.Actions {
		if a.Kind == "create" {
			state.Owned[a.EventID] = true
		}
	}
	if err = s.repo.Save(ctx, state); err != nil {
		return Reply{}, err
	}
	for p.Completed < len(p.Actions) {
		i := p.Completed
		a := p.Actions[i]
		if err = s.calendar.Apply(ctx, a, p.ID+fmt.Sprintf("-%d", i)); err != nil {
			if errors.Is(err, calendar.ErrChanged) || errors.Is(err, calendar.ErrForeign) {
				return Reply{Text: fmt.Sprintf("Выполнено %d из %d. Событие изменилось вне бота или не принадлежит ему. Используйте /discard, проверьте /tasks и составьте новый план.", p.Completed, len(p.Actions))}, nil
			}
			return Reply{Text: fmt.Sprintf("Выполнено %d из %d. Google не подтвердил следующую операцию. Нажмите повтор или /resume; повтор не создаст дубли.", p.Completed, len(p.Actions)), ProposalID: p.ID}, nil
		}
		if a.Kind == "delete" {
			delete(state.Owned, a.EventID)
		}
		p.Completed++
		if err = s.repo.Save(ctx, state); err != nil {
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
