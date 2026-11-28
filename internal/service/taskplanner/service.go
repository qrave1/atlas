package taskplanner

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	googleTasks "github.com/qrave1/atlas/internal/clients/tasks"
	"github.com/qrave1/atlas/internal/model"
	"github.com/qrave1/atlas/internal/store"
)

type Repository interface {
	Load(context.Context) (model.State, error)
	Save(context.Context, model.State) error
}

type Brain interface {
	DecideTasks(context.Context, time.Time, []model.Message, []model.Task, *model.TaskProposal) (model.TaskDecision, error)
}

type TaskStore interface {
	ListOwned(context.Context, map[string]bool) ([]model.Task, error)
	GetOwned(context.Context, string) (model.Task, error)
	Apply(context.Context, model.TaskAction, string, string) (string, error)
}

type Service struct {
	repo     Repository
	brain    Brain
	tasks    TaskStore
	location *time.Location
	now      func() time.Time
}

func New(repo Repository, brain Brain, tasks TaskStore, location *time.Location) *Service {
	return &Service{repo: repo, brain: brain, tasks: tasks, location: location, now: time.Now}
}

func addHistory(state *model.State, role, content string) {
	state.History = append(state.History, model.Message{Role: role, Content: content})
	if len(state.History) > 24 {
		state.History = state.History[len(state.History)-24:]
	}
}

func (s *Service) Invalidate(ctx context.Context) error {
	state, err := s.repo.Load(ctx)
	if err != nil || state.TaskPending == nil {
		return err
	}
	if state.TaskPending.Status == "applying" {
		return errors.New("запись уже началась: используйте /resume или /discard")
	}
	state.TaskPending.Status = "superseded"
	return s.repo.Save(ctx, state)
}

func (s *Service) Text(ctx context.Context, text string) (model.Reply, error) {
	if utf8.RuneCountInString(text) > 12000 {
		return model.Reply{Text: "Сообщение слишком длинное. Разбейте его на несколько частей."}, nil
	}
	if err := s.Invalidate(ctx); err != nil {
		return model.Reply{}, err
	}
	state, err := s.repo.Load(ctx)
	if err != nil {
		return model.Reply{}, err
	}
	addHistory(&state, "user", text)
	if err = s.repo.Save(ctx, state); err != nil {
		return model.Reply{}, err
	}
	owned, err := s.tasks.ListOwned(ctx, state.TaskOwned)
	if err != nil {
		return model.Reply{}, err
	}
	if len(owned) > 300 {
		return model.Reply{Text: "Слишком много задач для одного диалога. Используйте /tasks и удалите ненужные задачи."}, nil
	}
	decision, err := s.brain.DecideTasks(ctx, s.now().In(s.location), state.History, owned, state.TaskPending)
	if err != nil {
		return model.Reply{}, err
	}
	if len(decision.Actions) == 0 {
		if strings.TrimSpace(decision.Reply) == "" {
			return model.Reply{}, errors.New("ai returned empty reply")
		}
		addHistory(&state, "assistant", decision.Reply)
		state.TaskPending = nil
		return model.Reply{Text: decision.Reply}, s.repo.Save(ctx, state)
	}
	p := &model.TaskProposal{ID: store.NewID(), CreatedAt: s.now(), Status: "ready", Actions: decision.Actions}
	if err = s.prepare(ctx, &state, p); err != nil {
		reply := "Нужно уточнить план: " + err.Error()
		addHistory(&state, "assistant", reply)
		state.TaskPending = nil
		return model.Reply{Text: reply}, s.repo.Save(ctx, state)
	}
	state.TaskPending = p
	preview := s.render(p)
	addHistory(&state, "assistant", preview)
	return model.Reply{Text: preview, ProposalID: p.ID}, s.repo.Save(ctx, state)
}

func (s *Service) prepare(ctx context.Context, state *model.State, proposal *model.TaskProposal) error {
	if len(proposal.Actions) > 10 {
		return errors.New("не больше 10 изменений за одно подтверждение")
	}
	seen := make(map[string]bool)
	for i := range proposal.Actions {
		action := &proposal.Actions[i]
		switch action.Kind {
		case "create":
			action.TaskID, action.Before = "", nil
			if action.ParentIndex != nil {
				parentIndex := *action.ParentIndex
				if parentIndex < 0 || parentIndex >= i || proposal.Actions[parentIndex].Kind != "create" || proposal.Actions[parentIndex].ParentIndex != nil {
					return errors.New("подзадача должна ссылаться на более раннюю родительскую задачу")
				}
			}
			if err := s.validateTask(action.Title, action.Notes, action.DueDate); err != nil {
				return err
			}
		case "update", "delete", "complete":
			if !state.TaskOwned[action.TaskID] {
				return errors.New("можно менять только задачи, созданные Atlas")
			}
			if seen[action.TaskID] {
				return errors.New("несколько изменений одной задачи нужно объединить")
			}
			seen[action.TaskID] = true
			before, err := s.tasks.GetOwned(ctx, action.TaskID)
			if err != nil {
				return errors.New("не удалось проверить исходную задачу; обновите /tasks")
			}
			action.Before = &before
			if action.Kind == "update" {
				if err := s.validateTask(action.Title, action.Notes, action.DueDate); err != nil {
					return err
				}
			}
		default:
			return errors.New("неизвестный тип действия")
		}
	}
	return nil
}

func (s *Service) validateTask(title, notes, dueDate string) error {
	if strings.TrimSpace(title) == "" || utf8.RuneCountInString(title) > 100 || utf8.RuneCountInString(notes) > 4000 {
		return errors.New("название должно содержать 1–100 символов, заметки — до 4000")
	}
	if dueDate != "" {
		due, err := time.ParseInLocation("2006-01-02", dueDate, s.location)
		if err != nil || due.Format("2006-01-02") != dueDate {
			return errors.New("срок должен быть датой в формате ГГГГ-ММ-ДД")
		}
		if due.After(s.now().In(s.location).AddDate(1, 0, 0)) {
			return errors.New("планируем не дальше чем на год вперёд")
		}
	}
	return nil
}

func (s *Service) render(proposal *model.TaskProposal) string {
	var b strings.Builder
	b.WriteString("План задач Google Tasks (МСК):\n")
	for i, action := range proposal.Actions {
		fmt.Fprintf(&b, "\n%d. ", i+1)
		if action.ParentIndex != nil {
			b.WriteString("↳ ")
		}
		switch action.Kind {
		case "delete":
			fmt.Fprintf(&b, "Удалить «%s»\n", action.Before.Title)
		case "complete":
			fmt.Fprintf(&b, "Завершить «%s»\n", action.Before.Title)
		case "update":
			fmt.Fprintf(&b, "Изменить «%s» → «%s»\n", action.Before.Title, action.Title)
		default:
			fmt.Fprintf(&b, "Создать «%s»\n", action.Title)
		}
		if action.DueDate != "" {
			date, _ := time.ParseInLocation("2006-01-02", action.DueDate, s.location)
			fmt.Fprintf(&b, "Срок: %s\n", date.Format("02.01.2006"))
		}
		if action.Notes != "" {
			fmt.Fprintf(&b, "%s\n", action.Notes)
		}
	}
	b.WriteString("\nПодтвердите кнопкой или отправьте исправления. Пока задачи не записаны.")
	return b.String()
}

func (s *Service) Tasks(ctx context.Context) (model.Reply, error) {
	state, err := s.repo.Load(ctx)
	if err != nil {
		return model.Reply{}, err
	}
	tasks, err := s.tasks.ListOwned(ctx, state.TaskOwned)
	if err != nil {
		return model.Reply{}, err
	}
	sort.Slice(tasks, func(i, j int) bool {
		if tasks[i].DueDate == tasks[j].DueDate {
			return tasks[i].Title < tasks[j].Title
		}
		if tasks[i].DueDate == "" {
			return false
		}
		if tasks[j].DueDate == "" {
			return true
		}
		return tasks[i].DueDate < tasks[j].DueDate
	})
	var b strings.Builder
	b.WriteString("Задачи Atlas в Google Tasks:\n")
	count := 0
	for _, task := range tasks {
		if task.ParentID != "" {
			b.WriteString("  ↳ ")
		} else {
			b.WriteString("\n")
		}
		status := ""
		if task.Status == "completed" {
			status = "✓ "
		}
		fmt.Fprintf(&b, "%s%s\n", status, task.Title)
		if task.DueDate != "" {
			date, _ := time.ParseInLocation("2006-01-02", task.DueDate, s.location)
			fmt.Fprintf(&b, "Срок: %s\n", date.Format("02.01.2006"))
		}
		if task.Notes != "" {
			fmt.Fprintf(&b, "%s\n", task.Notes)
		}
		count++
		if count == 50 {
			b.WriteString("\nПоказаны первые 50 задач.")
			break
		}
	}
	if count == 0 {
		b.WriteString("Пока нет задач.")
	}
	return model.Reply{Text: b.String()}, nil
}

func (s *Service) Confirm(ctx context.Context, id string) (model.Reply, error) {
	state, err := s.repo.Load(ctx)
	if err != nil {
		return model.Reply{}, err
	}
	proposal := state.TaskPending
	if proposal == nil || proposal.ID != id || (proposal.Status != "ready" && proposal.Status != "applying") {
		return model.Reply{Text: "Эта кнопка устарела. Отправьте задачу заново или используйте /tasks."}, nil
	}
	if proposal.Status == "ready" {
		if s.now().Sub(proposal.CreatedAt) > 24*time.Hour {
			proposal.Status = "expired"
			return model.Reply{Text: "План старше суток. Отправьте запрос заново."}, s.repo.Save(ctx, state)
		}
		for _, action := range proposal.Actions {
			if action.Kind == "create" {
				continue
			}
			current, checkErr := s.tasks.GetOwned(ctx, action.TaskID)
			if checkErr != nil || action.Before == nil || current.ETag != action.Before.ETag {
				proposal.Status = "superseded"
				return model.Reply{Text: "Исходная задача изменилась или удалена вне бота. Проверьте /tasks и отправьте новый запрос."}, s.repo.Save(ctx, state)
			}
		}
	}
	return s.applyConfirmed(ctx, state)
}

func (s *Service) applyConfirmed(ctx context.Context, state model.State) (model.Reply, error) {
	proposal := state.TaskPending
	proposal.Status = "applying"
	if state.TaskOwned == nil {
		state.TaskOwned = make(map[string]bool)
	}
	if err := s.repo.Save(ctx, state); err != nil {
		return model.Reply{}, err
	}
	for proposal.Completed < len(proposal.Actions) {
		index := proposal.Completed
		action := proposal.Actions[index]
		parentID := ""
		if action.ParentIndex != nil {
			parentID = proposal.Actions[*action.ParentIndex].TaskID
		}
		taskID, err := s.tasks.Apply(ctx, action, proposal.ID+fmt.Sprintf("-%d", index), parentID)
		if err != nil {
			if errors.Is(err, googleTasks.ErrChanged) || errors.Is(err, googleTasks.ErrForeign) {
				return model.Reply{Text: fmt.Sprintf("Выполнено %d из %d. Задача изменилась вне бота или не принадлежит ему. Используйте /discard, проверьте /tasks и составьте новый план.", proposal.Completed, len(proposal.Actions))}, nil
			}
			return model.Reply{Text: fmt.Sprintf("Выполнено %d из %d. Google не подтвердил следующую операцию. Нажмите повтор или /resume; повтор не создаст дубли.", proposal.Completed, len(proposal.Actions)), ProposalID: proposal.ID}, nil
		}
		if action.Kind == "create" {
			action.TaskID = taskID
			state.TaskOwned[taskID] = true
		}
		if action.Kind == "delete" {
			delete(state.TaskOwned, action.TaskID)
		}
		proposal.Actions[index] = action
		proposal.Completed++
		if err = s.repo.Save(ctx, state); err != nil {
			return model.Reply{}, err
		}
	}
	proposal.Status = "done"
	addHistory(&state, "assistant", "Согласованные изменения записаны в Google Tasks.")
	return model.Reply{Text: fmt.Sprintf("Готово. Изменено задач: %d. /tasks — посмотреть список.", proposal.Completed)}, s.repo.Save(ctx, state)
}

func (s *Service) Resume(ctx context.Context) (model.Reply, error) {
	state, err := s.repo.Load(ctx)
	if err != nil {
		return model.Reply{}, err
	}
	if state.TaskPending == nil || state.TaskPending.Status != "applying" {
		return model.Reply{Text: "Нет незавершённой записи."}, nil
	}
	return s.Confirm(ctx, state.TaskPending.ID)
}

func (s *Service) Cancel(ctx context.Context, id string, discard bool) (model.Reply, error) {
	state, err := s.repo.Load(ctx)
	if err != nil {
		return model.Reply{}, err
	}
	if id != "" && (state.TaskPending == nil || state.TaskPending.ID != id) {
		return model.Reply{Text: "Кнопка устарела."}, nil
	}
	if state.TaskPending != nil && state.TaskPending.Status == "applying" && !discard {
		return model.Reply{Text: "Запись уже началась. /resume — продолжить; /discard — остановить оставшиеся действия. Уже выполненные изменения сохранятся."}, nil
	}
	state.TaskPending = nil
	state.History = nil
	return model.Reply{Text: "Текущий диалог и оставшиеся действия отменены. Уже записанные задачи сохранены. /tasks — проверить список."}, s.repo.Save(ctx, state)
}
