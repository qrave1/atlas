package bot

import (
	"context"
	"encoding/json"

	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	tele "gopkg.in/telebot.v4"

	"github.com/qrave1/atlas/internal/config"
	"github.com/qrave1/atlas/internal/planner"
)

func TestAuthorization(t *testing.T) {
	owner := config.OwnerID
	cases := []struct {
		name   string
		update tele.Update
		want   bool
	}{
		{"owner", tele.Update{Message: &tele.Message{Sender: &tele.User{ID: owner}, Chat: &tele.Chat{ID: owner, Type: "private"}}}, true},
		{"other sender", tele.Update{Message: &tele.Message{Sender: &tele.User{ID: 1}, Chat: &tele.Chat{ID: owner, Type: "private"}}}, false},
		{"group", tele.Update{Message: &tele.Message{Sender: &tele.User{ID: owner}, Chat: &tele.Chat{ID: -1, Type: "group"}}}, false},
		{"missing sender", tele.Update{Message: &tele.Message{Chat: &tele.Chat{ID: owner, Type: "private"}}}, false},
		{"owner callback", tele.Update{Callback: &tele.Callback{Sender: &tele.User{ID: owner}, Message: &tele.Message{Chat: &tele.Chat{ID: owner, Type: "private"}}}}, true},
		{"foreign callback", tele.Update{Callback: &tele.Callback{Sender: &tele.User{ID: 1}, Message: &tele.Message{Chat: &tele.Chat{ID: owner, Type: "private"}}}}, false},
		{"inline callback", tele.Update{Callback: &tele.Callback{Sender: &tele.User{ID: owner}}}, false},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if got := Authorized(tt.update); got != tt.want {
				t.Fatalf("authorized=%v", got)
			}
		})
	}
}

func TestTelegramHonorsRetryAfter(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts == 1 {
			w.WriteHeader(429)
			json.NewEncoder(w).Encode(map[string]any{"ok": false, "error_code": 429, "parameters": map[string]int{"retry_after": 1}})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": map[string]int{"message_id": 1}})
	}))
	defer server.Close()
	tg, err := NewTelegramClient(t.Context(), tele.Settings{URL: server.URL, Client: server.Client(), Token: "test", Offline: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := sendReply(t.Context(), tg, planner.Reply{Text: "Тест"}); err != nil {
		t.Fatal(err)
	}
	if attempts != 2 {
		t.Fatalf("attempts=%d", attempts)
	}
}

func TestUnauthorizedUpdateTouchesNoDependencies(t *testing.T) {
	// A bot with nil dependencies would panic if authorization were too late.
	assistant := &Bot{}
	u := tele.Update{Message: &tele.Message{Sender: &tele.User{ID: 1}, Chat: &tele.Chat{ID: 1, Type: "private"}, Voice: &tele.Voice{File: tele.File{FileID: "secret"}}}}
	if err := assistant.handleUpdate(context.Background(), u); err != nil {
		t.Fatal(err)
	}
}

func TestDownloadUsesSameClientAsAPI(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if strings.HasSuffix(r.URL.Path, "getFile") {
			json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": map[string]any{"file_path": "voice/1.oga", "file_size": 3}})
		} else {
			w.Write([]byte("ogg"))
		}
	}))
	defer server.Close()
	tg, err := NewTelegramClient(t.Context(), tele.Settings{URL: server.URL, Client: server.Client(), Token: "test", Offline: true})
	if err != nil {
		t.Fatal(err)
	}
	data, err := downloadVoice(t.Context(), tg, tele.File{FileID: "id"})
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "ogg" || len(paths) != 2 || paths[1] != "/file/bottest/voice/1.oga" {
		t.Fatalf("unexpected paths: %v", paths)
	}
}

func TestReplyCarriesProposalButtons(t *testing.T) {
	var payload struct {
		Markup string `json:"reply_markup"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		w.Write([]byte(`{"ok":true,"result":{"message_id":1}}`))
	}))
	defer server.Close()
	api, err := NewTelegramClient(t.Context(), tele.Settings{URL: server.URL, Client: server.Client(), Token: "test", Offline: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := sendReply(t.Context(), api, planner.Reply{Text: "План", ProposalID: "abc"}); err != nil {
		t.Fatal(err)
	}
	var markup tele.ReplyMarkup
	if err := json.Unmarshal([]byte(payload.Markup), &markup); err != nil {
		t.Fatal(err)
	}
	rows := markup.InlineKeyboard
	if len(rows) != 1 || len(rows[0]) != 2 || rows[0][0].Data != "confirm:abc" || rows[0][1].Data != "cancel:abc" {
		t.Fatalf("buttons: %+v", rows)
	}
}

func TestCancelInterruptsPolling(t *testing.T) {
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		close(started)
		<-r.Context().Done()
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	api, err := NewTelegramClient(ctx, tele.Settings{URL: server.URL, Client: server.Client(), Token: "test", Offline: true})
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		select {
		case <-started:
			cancel()
		case <-ctx.Done():
		}
	}()
	if _, err := pollUpdates(ctx, api, 0); !errors.Is(err, context.Canceled) {
		t.Fatalf("poll: %v", err)
	}
}
