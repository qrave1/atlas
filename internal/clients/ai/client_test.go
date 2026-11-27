package ai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	openai "github.com/sashabaranov/go-openai"
)

func TestTranscriptionRequestContract(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/audio/transcriptions" || r.Header.Get("Authorization") != "Bearer test-key" {
			t.Error("wrong endpoint or auth")
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Error(err)
			return
		}
		defer r.MultipartForm.RemoveAll()
		if r.FormValue("model") != "speech-model" || r.FormValue("language") != "ru" || r.FormValue("response_format") != "json" {
			t.Error("wrong transcription options")
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			t.Error(err)
			return
		}
		defer file.Close()
		data, err := io.ReadAll(file)
		if err != nil {
			t.Error(err)
		}
		if header.Filename != "voice.wav" || string(data) != "RIFF" {
			t.Error("wrong audio payload")
		}
		json.NewEncoder(w).Encode(map[string]string{"text": "Практика завтра в 12"})
	}))
	defer server.Close()
	client := New(server.Client(), server.URL+"/v1", "test-key", "chat-model", "speech-model")
	text, err := client.Transcribe(context.Background(), []byte("RIFF"))
	if err != nil {
		t.Fatal(err)
	}
	if text != "Практика завтра в 12" {
		t.Fatalf("text=%q", text)
	}
}

func TestIncompleteDecisionIsRejected(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"finish_reason": "length", "message": map[string]string{"content": `{"reply":"ok","actions":[]}`}}}})
	}))
	defer server.Close()
	client := New(server.Client(), server.URL, "test-key", "chat", "speech")
	if _, err := client.Decide(context.Background(), time.Now(), nil, nil, nil); err == nil {
		t.Fatal("truncated completion accepted")
	}
}

func TestUpstreamErrorBodyIsNotExposed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		io.WriteString(w, "sensitive-upstream-body")
	}))
	defer server.Close()
	client := New(server.Client(), server.URL, "test-key", "chat", "speech")
	_, err := client.Decide(context.Background(), time.Now(), nil, nil, nil)
	if err == nil || strings.Contains(err.Error(), "sensitive") {
		t.Fatalf("unsafe error: %v", err)
	}
}

func TestDecisionRequestContract(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request openai.ChatCompletionRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		if r.URL.Path != "/v1/chat/completions" || request.Model != "openai/gpt-5-mini" || request.MaxCompletionTokens != 8000 || request.ResponseFormat == nil || request.ResponseFormat.Type != openai.ChatCompletionResponseFormatTypeJSONObject || len(request.Messages) != 2 {
			t.Error("unexpected chat request")
		}
		io.WriteString(w, `{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"{\"reply\":\"ok\",\"actions\":[]}"}}]}`)
	}))
	defer server.Close()
	c := New(server.Client(), server.URL+"/v1", "test", "openai/gpt-5-mini", "speech")
	decision, err := c.Decide(t.Context(), time.Now(), nil, nil, nil)
	if err != nil || decision.Reply != "ok" {
		t.Fatalf("decision=%+v error=%v", decision, err)
	}
}
