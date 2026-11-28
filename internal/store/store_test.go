package store

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/qrave1/atlas/internal/model"
)

func TestStateSurvivesDatabaseReopen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "atlas.db")
	s, err := New(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	state, err := s.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	id := state.InstallationID
	state.Offset = 123
	state.Tasks["task"] = model.Task{ID: "task", Title: "Practice", Status: "needsAction"}
	state.TaskPending = &model.TaskProposal{ID: "plan", Status: "applying", Completed: 1}
	if err = s.Save(ctx, state); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = New(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	state, err = s.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if state.InstallationID != id || state.Offset != 123 || state.Tasks["task"].Title != "Practice" || state.TaskPending.Completed != 1 {
		t.Fatalf("lost state: %+v", state)
	}
}
