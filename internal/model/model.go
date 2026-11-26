package model

import "time"

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
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

type State struct {
	InstallationID string          `json:"installation_id"`
	Offset         int64           `json:"offset"`
	History        []Message       `json:"history"`
	Owned          map[string]bool `json:"owned"`
	Pending        *Proposal       `json:"pending,omitempty"`
}
