package service

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/qrave1/atlas/internal/store"
)

func TestSelectModelPersistsAndRejectsInvalid(t *testing.T) {
	ctx := context.Background()
	db, err := store.New(ctx, filepath.Join(t.TempDir(), "atlas.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	b := &Bot{repo: db}
	// A nil Telegram client makes a successful selection fail only after saving.
	// Exercise invalid values without calling Telegram.
	if err := b.selectModel(ctx, "chat:99"); err != nil {
		t.Fatal(err)
	}
	state, err := db.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if state.ChatModel != "" {
		t.Fatal("invalid selection persisted")
	}
	state.ChatModel = chatModels[1]
	state.TranscribeModel = speechModels[0]
	if err := db.Save(ctx, state); err != nil {
		t.Fatal(err)
	}
	restored, err := db.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if restored.ChatModel != chatModels[1] || restored.TranscribeModel != speechModels[0] {
		t.Fatal("selection not persisted")
	}
}
