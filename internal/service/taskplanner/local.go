package taskplanner

import (
	"context"
	"errors"
	"time"

	"github.com/qrave1/atlas/internal/model"
	"github.com/qrave1/atlas/internal/store"
)

var (
	errTaskNotFound = errors.New("task not found")
	errTaskChanged  = errors.New("task changed")
)

type localTasks struct {
	repo     Repository
	location *time.Location
}

func (t localTasks) List(ctx context.Context) ([]model.Task, error) {
	state, err := t.repo.Load(ctx)
	if err != nil {
		return nil, err
	}
	tasks := make([]model.Task, 0, len(state.Tasks))
	for _, task := range state.Tasks {
		tasks = append(tasks, task)
	}
	return tasks, nil
}

func (t localTasks) Get(ctx context.Context, id string) (model.Task, error) {
	state, err := t.repo.Load(ctx)
	if err != nil {
		return model.Task{}, err
	}
	task, ok := state.Tasks[id]
	if !ok {
		return model.Task{}, errTaskNotFound
	}
	return task, nil
}

func (t localTasks) Apply(ctx context.Context, action model.TaskAction, operation, parentID string) (string, error) {
	state, err := t.repo.Load(ctx)
	if err != nil {
		return "", err
	}
	if id := state.TaskOperations[operation]; id != "" {
		return id, nil
	}
	if state.Tasks == nil {
		state.Tasks = make(map[string]model.Task)
	}
	if state.TaskOperations == nil {
		state.TaskOperations = make(map[string]string)
	}
	switch action.Kind {
	case "create":
		id := store.NewID()
		task := model.Task{ID: id, Title: action.Title, Notes: action.Notes, DueDate: action.DueDate,
			EstimateMinutes: action.EstimateMinutes, RepeatReminder: action.RepeatReminder, Status: "needsAction",
			ParentID: parentID, ETag: store.NewID()}
		if action.ScheduledAt != "" {
			planned, parseErr := time.Parse(time.RFC3339, action.ScheduledAt)
			err = parseErr
			if err != nil {
				return "", errors.New("invalid scheduled time")
			}
			planned = planned.In(t.location)
			task.ScheduledAt = &planned
			task.NextReminderAt = planned
		} else if task.DueDate != "" {
			due, _ := time.ParseInLocation("2006-01-02", task.DueDate, t.location)
			task.NextReminderAt = time.Date(due.Year(), due.Month(), due.Day(), 11, 0, 0, 0, t.location)
		}
		state.Tasks[id] = task
		state.TaskOperations[operation] = id
		if err = t.repo.Save(ctx, state); err != nil {
			return "", err
		}
		return id, nil
	case "update", "complete", "delete":
		task, ok := state.Tasks[action.TaskID]
		if !ok {
			return "", errTaskNotFound
		}
		if action.Before == nil || task.ETag != action.Before.ETag {
			return "", errTaskChanged
		}
		if action.Kind == "delete" {
			delete(state.Tasks, task.ID)
			if index := indexOf(state.DailyPlanIDs, task.ID); index >= 0 {
				state.DailyPlanIDs = append(state.DailyPlanIDs[:index], state.DailyPlanIDs[index+1:]...)
			}
		} else if action.Kind == "complete" {
			task.Status = "completed"
			task.NextReminderAt = time.Time{}
			task.ETag = store.NewID()
			state.Tasks[task.ID] = task
		} else {
			task.Title, task.Notes, task.DueDate = action.Title, action.Notes, action.DueDate
			task.EstimateMinutes, task.RepeatReminder = action.EstimateMinutes, action.RepeatReminder
			task.ScheduledAt, task.NextReminderAt = nil, time.Time{}
			if action.ScheduledAt != "" {
				planned, parseErr := time.Parse(time.RFC3339, action.ScheduledAt)
				err = parseErr
				if err != nil {
					return "", errors.New("invalid scheduled time")
				}
				planned = planned.In(t.location)
				task.ScheduledAt = &planned
				task.NextReminderAt = planned
			} else if task.DueDate != "" {
				due, _ := time.ParseInLocation("2006-01-02", task.DueDate, t.location)
				task.NextReminderAt = time.Date(due.Year(), due.Month(), due.Day(), 11, 0, 0, 0, t.location)
			}
			task.ETag = store.NewID()
			state.Tasks[task.ID] = task
		}
		state.TaskOperations[operation] = task.ID
		if err = t.repo.Save(ctx, state); err != nil {
			return "", err
		}
		return task.ID, nil
	default:
		return "", errors.New("unknown task action")
	}
	return "", errors.New("unknown task action")
}

func indexOf(ids []string, id string) int {
	for i, candidate := range ids {
		if candidate == id {
			return i
		}
	}
	return -1
}
