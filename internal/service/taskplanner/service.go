package taskplanner

import (
	"context"
	"errors"
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
	DecideTasks(context.Context, time.Time, []model.Message, []model.Task, *model.TaskProposal) (model.TaskDecision, error)
}

type Service struct {
	repo     Repository
	brain    Brain
	tasks    localTasks
	location *time.Location
	now      func() time.Time
}

func New(repo Repository, brain Brain, location *time.Location) *Service {
	return &Service{repo: repo, brain: brain, tasks: localTasks{repo: repo, location: location}, location: location, now: time.Now}
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
	owned, err := s.tasks.List(ctx)
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
			if action.ParentID != "" && action.ParentIndex != nil {
				return errors.New("подзадача должна ссылаться на один эпик")
			}
			if action.ParentID != "" && state.Tasks[action.ParentID].ID == "" {
				return errors.New("эпик должен быть создан Atlas")
			}
			if action.ParentIndex != nil {
				parentIndex := *action.ParentIndex
				if parentIndex < 0 || parentIndex >= i || proposal.Actions[parentIndex].Kind != "create" || proposal.Actions[parentIndex].ParentIndex != nil {
					return errors.New("подзадача должна ссылаться на более раннюю родительскую задачу")
				}
			}
			if err := s.validateTask(action.Title, action.Notes, action.DueDate); err != nil {
				return err
			}
			if err := s.validateSchedule(action); err != nil {
				return err
			}
			if err := s.validateNoOverlap(state, proposal, i, action); err != nil {
				return err
			}
		case "update", "delete", "complete":
			if state.Tasks[action.TaskID].ID == "" {
				return errors.New("можно менять только задачи, созданные Atlas")
			}
			if seen[action.TaskID] {
				return errors.New("несколько изменений одной задачи нужно объединить")
			}
			seen[action.TaskID] = true
			if action.Kind == "delete" {
				for _, child := range state.Tasks {
					if child.ParentID == action.TaskID {
						return errors.New("сначала удали подзадачи эпика")
					}
				}
			}
			if action.Kind == "complete" {
				for _, child := range state.Tasks {
					if child.ParentID == action.TaskID && child.Status != "completed" {
						return errors.New("сначала заверши все подзадачи эпика")
					}
				}
			}
			before, err := s.tasks.Get(ctx, action.TaskID)
			if err != nil {
				return errors.New("не удалось проверить исходную задачу; обновите /tasks")
			}
			action.Before = &before
			if action.Kind == "update" {
				if err := s.validateTask(action.Title, action.Notes, action.DueDate); err != nil {
					return err
				}
				if err := s.validateSchedule(action); err != nil {
					return err
				}
				if err := s.validateNoOverlap(state, proposal, i, action); err != nil {
					return err
				}
			}
		default:
			return errors.New("неизвестный тип действия")
		}
	}
	return nil
}

func (s *Service) validateSchedule(action *model.TaskAction) error {
	if action.ScheduledAt == "" {
		if action.EstimateMinutes < 0 || action.EstimateMinutes > 480 {
			return errors.New("длительность должна быть от 5 минут до 8 часов")
		}
		return nil
	}
	planned, err := time.Parse(time.RFC3339, action.ScheduledAt)
	if err != nil {
		return errors.New("запланированное время должно включать дату, время и часовой пояс")
	}
	planned = planned.In(s.location)
	if !planned.After(s.now().In(s.location)) {
		return errors.New("запланированное время уже прошло")
	}
	if action.EstimateMinutes == 0 && (action.Before == nil || action.Before.EstimateMinutes == 0) {
		return errors.New("уточни, сколько времени заложить на эту задачу")
	}
	if action.DueDate == "" {
		action.DueDate = planned.Format("2006-01-02")
	}
	if action.DueDate != planned.Format("2006-01-02") {
		return errors.New("дата срока должна совпадать с датой запланированного времени")
	}
	if action.EstimateMinutes != 0 && (action.EstimateMinutes < 5 || action.EstimateMinutes > 480) {
		return errors.New("длительность должна быть от 5 минут до 8 часов")
	}
	action.ScheduledAt = planned.Format(time.RFC3339)
	return nil
}

func (s *Service) validateNoOverlap(state *model.State, proposal *model.TaskProposal, actionIndex int, action *model.TaskAction) error {
	if action.ScheduledAt == "" || action.EstimateMinutes == 0 {
		return nil
	}
	start, err := time.Parse(time.RFC3339, action.ScheduledAt)
	if err != nil {
		return nil
	}
	start = start.In(s.location)
	duration := time.Duration(action.EstimateMinutes) * time.Minute
	type slot struct {
		title string
		start time.Time
		end   time.Time
	}
	var slots []slot
	excluded := make(map[string]bool)
	for _, candidate := range proposal.Actions {
		if candidate.Kind == "update" || candidate.Kind == "delete" || candidate.Kind == "complete" {
			excluded[candidate.TaskID] = true
		}
	}
	for _, task := range state.Tasks {
		if excluded[task.ID] || task.Status == "completed" || task.ScheduledAt == nil || task.EstimateMinutes <= 0 {
			continue
		}
		slotStart := task.ScheduledAt.In(s.location)
		slots = append(slots, slot{title: task.Title, start: slotStart, end: slotStart.Add(time.Duration(task.EstimateMinutes) * time.Minute)})
	}
	for i, candidate := range proposal.Actions {
		if i == actionIndex || (candidate.Kind != "create" && candidate.Kind != "update") || candidate.ScheduledAt == "" || candidate.EstimateMinutes <= 0 {
			continue
		}
		slotStart, parseErr := time.Parse(time.RFC3339, candidate.ScheduledAt)
		if parseErr == nil {
			slotStart = slotStart.In(s.location)
			slots = append(slots, slot{title: candidate.Title, start: slotStart, end: slotStart.Add(time.Duration(candidate.EstimateMinutes) * time.Minute)})
		}
	}
	conflict := ""
	candidate := start
	for {
		moved := false
		for _, existing := range slots {
			if candidate.Before(existing.end) && candidate.Add(duration).After(existing.start) {
				if conflict == "" {
					conflict = fmt.Sprintf("«%s» (%s–%s)", existing.title, existing.start.Format("15:04"), existing.end.Format("15:04"))
				}
				candidate = existing.end
				moved = true
				break
			}
		}
		if !moved {
			break
		}
	}
	if conflict == "" {
		return nil
	}
	return fmt.Errorf("в это время запланировано %s. Ближайшее свободное время с учётом длительности — %s. Подойдёт?", conflict, candidate.Format("02.01 в 15:04"))
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
	b.WriteString("Черновик плана задач Atlas (МСК):\n")
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
		if action.ScheduledAt != "" {
			if planned, err := time.Parse(time.RFC3339, action.ScheduledAt); err == nil {
				fmt.Fprintf(&b, "Запланировано: %s\n", planned.In(s.location).Format("02.01.2006 15:04"))
			}
		}
		if action.EstimateMinutes > 0 {
			fmt.Fprintf(&b, "Оценка: %d мин.\n", action.EstimateMinutes)
		}
		if action.RepeatReminder {
			b.WriteString("Напоминать повторно: да\n")
		}
	}
	b.WriteString("\nПодтвердите кнопкой или отправьте исправления. Пока задачи не записаны.")
	return b.String()
}

func (s *Service) Tasks(ctx context.Context) (model.Reply, error) {
	tasks, err := s.tasks.List(ctx)
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
	b.WriteString("Задачи Atlas:\n")
	children := make(map[string]int)
	completedChildren := make(map[string]int)
	for _, task := range tasks {
		if task.ParentID != "" {
			children[task.ParentID]++
			if task.Status == "completed" {
				completedChildren[task.ParentID]++
			}
		}
	}
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
		if task.ParentID == "" && children[task.ID] > 0 {
			fmt.Fprintf(&b, "Подзадачи: %d/%d завершено\n", completedChildren[task.ID], children[task.ID])
		}
		if task.DueDate != "" {
			date, _ := time.ParseInLocation("2006-01-02", task.DueDate, s.location)
			fmt.Fprintf(&b, "Срок: %s\n", date.Format("02.01.2006"))
		}
		if task.Notes != "" {
			fmt.Fprintf(&b, "%s\n", task.Notes)
		}
		if task.ScheduledAt != nil {
			fmt.Fprintf(&b, "Запланировано: %s\n", task.ScheduledAt.In(s.location).Format("02.01.2006 15:04"))
		}
		if task.EstimateMinutes > 0 {
			fmt.Fprintf(&b, "Оценка: %d мин.\n", task.EstimateMinutes)
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

// Today starts the daily planning flow. Task selection stays local to Atlas.
func (s *Service) Today(ctx context.Context) (model.Reply, error) {
	state, err := s.repo.Load(ctx)
	if err != nil {
		return model.Reply{}, err
	}
	today := s.now().In(s.location).Format("2006-01-02")
	if state.DailyPlanDate != today {
		state.DailyPlanDate = today
		state.DailyPlanIDs = nil
	}
	tasks, err := s.tasks.List(ctx)
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
	selected := make(map[string]bool, len(state.DailyPlanIDs))
	parents := make(map[string]bool)
	for _, task := range tasks {
		if task.ParentID != "" {
			parents[task.ParentID] = true
		}
	}
	for _, id := range state.DailyPlanIDs {
		selected[id] = true
	}
	reply := model.Reply{Text: "План на " + s.now().In(s.location).Format("02.01") + ":\n"}
	shown := 0
	for _, task := range tasks {
		if task.Status == "completed" || (task.ParentID == "" && parents[task.ID]) {
			continue
		}
		if task.DueDate != "" && task.DueDate > today {
			continue
		}
		mark := "▫"
		action := "add"
		if selected[task.ID] {
			mark, action = "✅", "remove"
		}
		reply.Text += fmt.Sprintf("\n%s %s", mark, task.Title)
		if task.DueDate != "" && task.DueDate < today {
			reply.Text += " (просрочена)"
		}
		reply.Buttons = append(reply.Buttons, []model.Button{{Text: mark + " " + truncate(task.Title, 30), Data: "daily:" + action + ":" + task.ID}})
		shown++
		if shown == 8 {
			break
		}
	}
	if shown == 0 {
		reply.Text += "\nНет задач со сроком сегодня или раньше. Добавьте задачу сообщением, если хотите включить её в план."
	} else {
		reply.Text += "\nНажмите на задачу, чтобы включить или убрать её из плана. Выбранные задачи останутся в плане до вечернего обзора."
	}
	if len(state.DailyPlanIDs) > 0 {
		reply.Text += "\n\nСегодня в плане: " + strings.Join(planTitles(tasks, state.DailyPlanIDs), "; ")
	}
	return reply, s.repo.Save(ctx, state)
}

func (s *Service) DailyAction(ctx context.Context, action, taskID string) (model.Reply, error) {
	state, err := s.repo.Load(ctx)
	if err != nil {
		return model.Reply{}, err
	}
	today := s.now().In(s.location).Format("2006-01-02")
	if state.DailyPlanDate != today {
		state.DailyPlanDate, state.DailyPlanIDs = today, nil
	}
	index := -1
	for i, id := range state.DailyPlanIDs {
		if id == taskID {
			index = i
			break
		}
	}
	switch action {
	case "add":
		if taskID == "" || state.Tasks[taskID].ID == "" {
			return model.Reply{Text: "Эта задача не принадлежит Atlas. Обновите /tasks."}, nil
		}
		if index < 0 {
			if len(state.DailyPlanIDs) >= 8 {
				return model.Reply{Text: "В дневном плане может быть не больше 8 задач."}, nil
			}
			state.DailyPlanIDs = append(state.DailyPlanIDs, taskID)
		}
	case "remove":
		if index >= 0 {
			state.DailyPlanIDs = append(state.DailyPlanIDs[:index], state.DailyPlanIDs[index+1:]...)
		}
	case "tomorrow":
		if state.Tasks[taskID].ID == "" {
			return model.Reply{Text: "Эта задача не принадлежит Atlas."}, nil
		}
		before, getErr := s.tasks.Get(ctx, taskID)
		if getErr != nil {
			return model.Reply{}, getErr
		}
		dueDate := s.now().In(s.location).AddDate(0, 0, 1).Format("2006-01-02")
		scheduledAt := ""
		if before.ScheduledAt != nil {
			scheduledAt = before.ScheduledAt.AddDate(0, 0, 1).Format(time.RFC3339)
		}
		proposal := &model.TaskProposal{ID: store.NewID(), CreatedAt: s.now(), Status: "ready", Actions: []model.TaskAction{{Kind: "update", TaskID: taskID, Title: before.Title, Notes: before.Notes, DueDate: dueDate, ScheduledAt: scheduledAt, EstimateMinutes: before.EstimateMinutes, RepeatReminder: before.RepeatReminder, Before: &before}}}
		state.TaskPending = proposal
		if err = s.repo.Save(ctx, state); err != nil {
			return model.Reply{}, err
		}
		return model.Reply{Text: "План переноса на завтра:\n\n" + s.render(proposal), ProposalID: proposal.ID}, nil
	case "complete":
		return s.Reminder(ctx, "done", taskID)
	default:
		return model.Reply{Text: "Неизвестное действие с планом."}, nil
	}
	if err = s.repo.Save(ctx, state); err != nil {
		return model.Reply{}, err
	}
	return s.Today(ctx)
}

func (s *Service) Scheduled(ctx context.Context) ([]model.Reply, error) {
	state, err := s.repo.Load(ctx)
	if err != nil {
		return nil, err
	}
	now := s.now().In(s.location)
	today := now.Format("2006-01-02")
	if state.DailyPlanDate != today {
		state.DailyPlanDate = today
		state.DailyPlanIDs = nil
		if err = s.repo.Save(ctx, state); err != nil {
			return nil, err
		}
	}
	var replies []model.Reply
	if now.Hour() >= 10 && state.MorningSentDate != today {
		replies = append(replies, model.Reply{Text: "Доброе утро! Пора составить план дня.", Buttons: [][]model.Button{{{Text: "Составить план", Data: "today:open"}}}, Schedule: "morning", ScheduleDate: today})
	}
	if now.Hour() >= 22 && state.EveningSentDate != today {
		tasks, listErr := s.tasks.List(ctx)
		if listErr != nil {
			return nil, listErr
		}
		text := "Вечерний обзор"
		selected := make(map[string]bool, len(state.DailyPlanIDs))
		for _, id := range state.DailyPlanIDs {
			selected[id] = true
		}
		var buttons [][]model.Button
		for _, task := range tasks {
			if selected[task.ID] && task.Status != "completed" {
				text += "\n▫ " + task.Title
				buttons = append(buttons, []model.Button{{Text: "Готово", Data: "daily:complete:" + task.ID}, {Text: "Завтра", Data: "daily:tomorrow:" + task.ID}, {Text: "Оставить", Data: "daily:remove:" + task.ID}})
			}
		}
		if len(buttons) == 0 {
			text += ": незавершённых задач из плана нет. Хороший день!"
		} else {
			text += "\n\nПеренос каждой задачи будет показан отдельно и потребует подтверждения. Если ничего не выбрать, сроки останутся как есть."
		}
		replies = append(replies, model.Reply{Text: text, Buttons: buttons, Schedule: "evening", ScheduleDate: today})
	}
	dueReminders := make([]model.Task, 0)
	for _, task := range state.Tasks {
		if task.Status != "completed" && !task.NextReminderAt.IsZero() && !task.NextReminderAt.After(now) {
			dueReminders = append(dueReminders, task)
		}
	}
	sort.Slice(dueReminders, func(i, j int) bool { return dueReminders[i].NextReminderAt.Before(dueReminders[j].NextReminderAt) })
	for i, task := range dueReminders {
		if i == 10 {
			break
		}
		text := "Напоминание: " + task.Title
		if task.SnoozeCount >= 2 {
			text += fmt.Sprintf("\nТы уже откладывал эту задачу %d раза. Что мешает? Могу помочь уменьшить следующий шаг.", task.SnoozeCount)
		}
		buttons := [][]model.Button{
			{{Text: "Готово", Data: "remind:done:" + task.ID}, {Text: "15 минут", Data: "remind:snooze15:" + task.ID}, {Text: "1 час", Data: "remind:snooze60:" + task.ID}},
			{{Text: "Завтра", Data: "remind:tomorrow:" + task.ID}, {Text: "Перенести…", Data: "remind:reschedule:" + task.ID}, {Text: "Застрял — разбить шаг", Data: "remind:stuck:" + task.ID}},
		}
		replies = append(replies, model.Reply{Text: text, Buttons: buttons, Schedule: "reminder", ScheduleDate: task.ID})
	}
	return replies, nil
}

func (s *Service) ScheduleSent(ctx context.Context, schedule, date string) error {
	state, err := s.repo.Load(ctx)
	if err != nil {
		return err
	}
	switch schedule {
	case "morning":
		state.MorningSentDate = date
	case "evening":
		state.EveningSentDate = date
	case "reminder":
		task, ok := state.Tasks[date]
		if !ok || task.NextReminderAt.IsZero() {
			return nil
		}
		if task.RepeatReminder {
			next := s.now().In(s.location).Add(4 * time.Hour)
			if next.Hour() >= 20 {
				next = time.Date(next.Year(), next.Month(), next.Day()+1, 11, 0, 0, 0, s.location)
			}
			task.NextReminderAt = next
		} else {
			task.NextReminderAt = time.Time{}
		}
		state.Tasks[date] = task
	default:
		return errors.New("unknown daily schedule")
	}
	return s.repo.Save(ctx, state)
}

func (s *Service) Reminder(ctx context.Context, action, taskID string) (model.Reply, error) {
	state, err := s.repo.Load(ctx)
	if err != nil {
		return model.Reply{}, err
	}
	task, ok := state.Tasks[taskID]
	if !ok || task.Status == "completed" {
		return model.Reply{Text: "Задача уже завершена или удалена."}, nil
	}
	now := s.now().In(s.location)
	switch action {
	case "done":
		_, err = s.tasks.Apply(ctx, model.TaskAction{Kind: "complete", TaskID: task.ID, Before: &task}, "reminder-done-"+store.NewID(), "")
		if err != nil {
			return model.Reply{}, err
		}
		return model.Reply{Text: "Отлично, отметил задачу выполненной: " + task.Title}, nil
	case "snooze15", "snooze60":
		delay := 15 * time.Minute
		if action == "snooze60" {
			delay = time.Hour
		}
		task.NextReminderAt = now.Add(delay)
		task.SnoozeCount++
		state.Tasks[task.ID] = task
		return model.Reply{Text: fmt.Sprintf("Отложил на %d минут.", int(delay.Minutes())), Buttons: nil}, s.repo.Save(ctx, state)
	case "tomorrow":
		before := task
		tomorrow := now.AddDate(0, 0, 1)
		if task.DueDate != "" {
			due, _ := time.ParseInLocation("2006-01-02", task.DueDate, s.location)
			task.DueDate = due.AddDate(0, 0, 1).Format("2006-01-02")
		}
		if task.ScheduledAt != nil {
			planned := task.ScheduledAt.AddDate(0, 0, 1)
			task.ScheduledAt = &planned
		} else if task.DueDate != "" {
			due, _ := time.ParseInLocation("2006-01-02", task.DueDate, s.location)
			planned := time.Date(due.Year(), due.Month(), due.Day(), 11, 0, 0, 0, s.location)
			task.ScheduledAt = &planned
		} else {
			task.DueDate = tomorrow.Format("2006-01-02")
			planned := time.Date(tomorrow.Year(), tomorrow.Month(), tomorrow.Day(), 11, 0, 0, 0, s.location)
			task.ScheduledAt = &planned
		}
		scheduledAt := ""
		if task.ScheduledAt != nil {
			scheduledAt = task.ScheduledAt.Format(time.RFC3339)
		}
		_, err = s.tasks.Apply(ctx, model.TaskAction{Kind: "update", TaskID: task.ID, Title: task.Title, Notes: task.Notes, DueDate: task.DueDate, ScheduledAt: scheduledAt, EstimateMinutes: task.EstimateMinutes, RepeatReminder: task.RepeatReminder, Before: &before}, "reminder-move-"+store.NewID(), "")
		if err != nil {
			return model.Reply{}, err
		}
		return model.Reply{Text: "Перенёс задачу на завтра: " + task.Title}, nil
	case "reschedule":
		state.RescheduleTaskID = taskID
		if err := s.repo.Save(ctx, state); err != nil {
			return model.Reply{}, err
		}
		return model.Reply{Text: "На какую дату или время перенести «" + task.Title + "»? Напишите, например: «на пятницу в 14:00»."}, nil
	case "stuck":
		prompt := "Разбей эпик task_id=" + task.ID + " «" + task.Title + "» на один самый маленький конкретный следующий шаг. Создай ровно одну короткую подзадачу с parent_task_id=" + task.ID + ". Не меняй эпик и другие задачи. Если непонятно, что мешает, задай один короткий вопрос."
		return s.Text(ctx, prompt)
	default:
		return model.Reply{Text: "Неизвестное действие с напоминанием."}, nil
	}
}

func truncate(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit-1]) + "…"
}

func planTitles(tasks []model.Task, ids []string) []string {
	titles := make(map[string]string, len(tasks))
	for _, task := range tasks {
		titles[task.ID] = task.Title
	}
	result := make([]string, 0, len(ids))
	for _, id := range ids {
		if title := titles[id]; title != "" {
			result = append(result, title)
		}
	}
	return result
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
			current, checkErr := s.tasks.Get(ctx, action.TaskID)
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
	if err := s.repo.Save(ctx, state); err != nil {
		return model.Reply{}, err
	}
	for proposal.Completed < len(proposal.Actions) {
		index := proposal.Completed
		action := proposal.Actions[index]
		parentID := ""
		if action.ParentIndex != nil {
			parentID = proposal.Actions[*action.ParentIndex].TaskID
		} else if action.ParentID != "" {
			parentID = action.ParentID
		}
		taskID, err := s.tasks.Apply(ctx, action, proposal.ID+fmt.Sprintf("-%d", index), parentID)
		if err != nil {
			if errors.Is(err, errTaskChanged) || errors.Is(err, errTaskNotFound) {
				return model.Reply{Text: fmt.Sprintf("Выполнено %d из %d. Задача изменилась вне бота или не принадлежит ему. Используйте /discard, проверьте /tasks и составьте новый план.", proposal.Completed, len(proposal.Actions))}, nil
			}
			return model.Reply{Text: fmt.Sprintf("Выполнено %d из %d. Не удалось записать следующую операцию. Нажмите повтор или /resume; повтор не создаст дубли.", proposal.Completed, len(proposal.Actions)), ProposalID: proposal.ID}, nil
		}
		state, err = s.repo.Load(ctx)
		if err != nil {
			return model.Reply{}, err
		}
		proposal = state.TaskPending
		if proposal == nil || proposal.ID == "" || index >= len(proposal.Actions) {
			return model.Reply{}, errors.New("task proposal disappeared while applying")
		}
		action = proposal.Actions[index]
		if action.Kind == "create" {
			action.TaskID = taskID
		}
		proposal.Actions[index] = action
		proposal.Completed++
		if err = s.repo.Save(ctx, state); err != nil {
			return model.Reply{}, err
		}
	}
	proposal.Status = "done"
	addHistory(&state, "assistant", "Согласованные изменения записаны в Atlas.")
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
	state.RescheduleTaskID = ""
	state.History = nil
	return model.Reply{Text: "Текущий диалог и оставшиеся действия отменены. Уже записанные задачи сохранены. /tasks — проверить список."}, s.repo.Save(ctx, state)
}
