package bot

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/qrave1/atlas/internal/config"
	"github.com/qrave1/atlas/internal/planner"
)

func TestAuthorization(t *testing.T) {
	owner := config.OwnerID
	cases := []struct {
		name   string
		update Update
		want   bool
	}{
		{"owner", Update{Message: &Message{From: &User{owner}, Chat: Chat{owner, "private"}}}, true},
		{"other sender", Update{Message: &Message{From: &User{1}, Chat: Chat{owner, "private"}}}, false},
		{"group", Update{Message: &Message{From: &User{owner}, Chat: Chat{-1, "group"}}}, false},
		{"missing sender", Update{Message: &Message{Chat: Chat{owner, "private"}}}, false},
		{"owner callback", Update{Callback: &Callback{From: User{owner}, Message: &Message{Chat: Chat{owner, "private"}}}}, true},
		{"foreign callback", Update{Callback: &Callback{From: User{1}, Message: &Message{Chat: Chat{owner, "private"}}}}, false},
		{"inline callback", Update{Callback: &Callback{From: User{owner}}}, false},
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
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": true})
	}))
	defer server.Close()
	tg := NewTelegram(server.Client(), "test")
	tg.baseURL = server.URL
	if err := tg.Send(context.Background(), planner.Reply{Text: "Тест"}); err != nil {
		t.Fatal(err)
	}
	if attempts != 2 {
		t.Fatalf("attempts=%d", attempts)
	}
}

func TestUnauthorizedUpdateTouchesNoDependencies(t *testing.T) {
	// A runner with nil dependencies would panic if authorization were too late.
	runner := &Runner{}
	u := Update{Message: &Message{From: &User{1}, Chat: Chat{1, "private"}, Voice: &Voice{FileID: "secret"}}}
	if err := runner.handle(context.Background(), u); err != nil {
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
	tg := NewTelegram(server.Client(), "test")
	tg.baseURL = server.URL
	data, err := tg.Download(context.Background(), "id")
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "ogg" || len(paths) != 2 || paths[1] != "/file/bottest/voice/1.oga" {
		t.Fatalf("unexpected paths: %v", paths)
	}
}
