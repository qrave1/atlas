package model

import "time"

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type Reply struct {
	Text       string
	ProposalID string
}

type Event struct {
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	Start       time.Time `json:"start"`
	End         time.Time `json:"end"`
	ETag        string    `json:"etag"`
}

type Action struct {
	Kind          string    `json:"kind"`
	EventID       string    `json:"event_id"`
	Title         string    `json:"title"`
	Description   string    `json:"description"`
	Start         time.Time `json:"start"`
	End           time.Time `json:"end"`
	DurationKnown bool      `json:"duration_known"`
	Before        *Event    `json:"before,omitempty"`
}

type Decision struct {
	Reply   string   `json:"reply"`
	Actions []Action `json:"actions"`
}

type Proposal struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	Status    string    `json:"status"`
	Actions   []Action  `json:"actions"`
	Completed int       `json:"completed"`
}

type Task struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Notes    string `json:"notes,omitempty"`
	DueDate  string `json:"due_date,omitempty"`
	Status   string `json:"status"`
	ParentID string `json:"parent_id,omitempty"`
	ETag     string `json:"etag"`
}

type TaskAction struct {
	Kind        string `json:"kind"`
	TaskID      string `json:"task_id,omitempty"`
	ParentIndex *int   `json:"parent_index,omitempty"`
	Title       string `json:"title,omitempty"`
	Notes       string `json:"notes,omitempty"`
	DueDate     string `json:"due_date,omitempty"`
	Before      *Task  `json:"before,omitempty"`
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
	ChatModel       string          `json:"chat_model,omitempty"`
	TranscribeModel string          `json:"transcribe_model,omitempty"`
	InstallationID  string          `json:"installation_id"`
	Offset          int64           `json:"offset"`
	History         []Message       `json:"history"`
	Owned           map[string]bool `json:"owned"`
	Pending         *Proposal       `json:"pending,omitempty"`
	TaskOwned       map[string]bool `json:"task_owned,omitempty"`
	TaskPending     *TaskProposal   `json:"task_pending,omitempty"`
}
