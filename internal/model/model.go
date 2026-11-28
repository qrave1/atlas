package model

import "time"

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type Reply struct {
	Text         string
	ProposalID   string
	Buttons      [][]Button
	Schedule     string
	ScheduleDate string
}

type Button struct {
	Text string
	Data string
}

type Task struct {
	ID              string     `json:"id"`
	Title           string     `json:"title"`
	Notes           string     `json:"notes,omitempty"`
	DueDate         string     `json:"due_date,omitempty"`
	ScheduledAt     *time.Time `json:"scheduled_at,omitempty"`
	EstimateMinutes int        `json:"estimate_minutes,omitempty"`
	RepeatReminder  bool       `json:"repeat_reminder,omitempty"`
	NextReminderAt  time.Time  `json:"next_reminder_at,omitempty"`
	SnoozeCount     int        `json:"snooze_count,omitempty"`
	Status          string     `json:"status"`
	ParentID        string     `json:"parent_id,omitempty"`
	ETag            string     `json:"etag"`
}

type TaskAction struct {
	Kind            string `json:"kind"`
	TaskID          string `json:"task_id,omitempty"`
	ParentID        string `json:"parent_task_id,omitempty"`
	ParentIndex     *int   `json:"parent_index,omitempty"`
	Title           string `json:"title,omitempty"`
	Notes           string `json:"notes,omitempty"`
	DueDate         string `json:"due_date,omitempty"`
	ScheduledAt     string `json:"scheduled_at,omitempty"`
	EstimateMinutes int    `json:"estimate_minutes,omitempty"`
	RepeatReminder  bool   `json:"repeat_reminder,omitempty"`
	Before          *Task  `json:"before,omitempty"`
}

type TaskDecision struct {
	Reply   string       `json:"reply"`
	Actions []TaskAction `json:"actions"`
}

type TaskProposal struct {
	ID        string       `json:"id"`
	CreatedAt time.Time    `json:"created_at"`
	Status    string       `json:"status"`
	Actions   []TaskAction `json:"actions"`
	Completed int          `json:"completed"`
}

type State struct {
	ChatModel          string            `json:"chat_model,omitempty"`
	TranscribeModel    string            `json:"transcribe_model,omitempty"`
	InstallationID     string            `json:"installation_id"`
	TaskStorageVersion int               `json:"task_storage_version"`
	Offset             int64             `json:"offset"`
	History            []Message         `json:"history"`
	Tasks              map[string]Task   `json:"tasks,omitempty"`
	TaskOperations     map[string]string `json:"task_operations,omitempty"`
	TaskPending        *TaskProposal     `json:"task_pending,omitempty"`
	DailyPlanDate      string            `json:"daily_plan_date,omitempty"`
	DailyPlanIDs       []string          `json:"daily_plan_ids,omitempty"`
	MorningSentDate    string            `json:"morning_sent_date,omitempty"`
	EveningSentDate    string            `json:"evening_sent_date,omitempty"`
	RescheduleTaskID   string            `json:"reschedule_task_id,omitempty"`
}
