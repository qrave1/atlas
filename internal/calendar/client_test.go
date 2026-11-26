package calendar

import (
	"context"
	"encoding/json"
	gcalendar "google.golang.org/api/calendar/v3"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/qrave1/atlas/internal/model"
)

func TestCreateRecoveryAfterUncertainOutcome(t *testing.T) {
	var event gcalendar.Event
	posts, created := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			posts++
			if created == 0 {
				json.NewDecoder(r.Body).Decode(&event)
				created++
				w.WriteHeader(503)
			} else {
				w.WriteHeader(409)
			}
			return
		}
		json.NewEncoder(w).Encode(event)
	}))
	defer server.Close()
	client := newTestClient(t, server, "installation")

	a := model.Action{Kind: "create", EventID: "abc123", Title: "Practice", Start: time.Now().Add(time.Hour), End: time.Now().Add(2 * time.Hour)}
	if err := client.Apply(context.Background(), a, "operation"); err == nil {
		t.Fatal("expected uncertain error")
	}
	if err := client.Apply(context.Background(), a, "operation"); err != nil {
		t.Fatal(err)
	}
	if posts != 2 || created != 1 {
		t.Fatalf("posts=%d created=%d", posts, created)
	}
}

func TestForeignMarkerBlocksUpdateAndDelete(t *testing.T) {
	for _, kind := range []string{"update", "delete"} {
		t.Run(kind, func(t *testing.T) {
			mutations := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" {
					mutations++
				}
				json.NewEncoder(w).Encode(gcalendar.Event{Etag: "v1"})
			}))
			defer server.Close()
			client := newTestClient(t, server, "owner")

			err := client.Apply(context.Background(), model.Action{Kind: kind, EventID: "foreign", Before: &model.Event{ETag: "v1"}}, "operation")
			if err != ErrForeign || mutations != 0 {
				t.Fatalf("err=%v mutations=%d", err, mutations)
			}
		})
	}
}

func TestUpdateSendsETagAndPreservesPrivateMetadata(t *testing.T) {
	var incoming gcalendar.Event
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			e := gcalendar.Event{Etag: "v1"}
			e.ExtendedProperties = &gcalendar.EventExtendedProperties{Private: map[string]string{"atlas_owner": "owner", "other": "keep"}}
			json.NewEncoder(w).Encode(e)
			return
		}
		if r.Method != "PATCH" || r.Header.Get("If-Match") != "v1" {
			t.Error("missing conditional patch")
		}
		json.NewDecoder(r.Body).Decode(&incoming)
		json.NewEncoder(w).Encode(incoming)
	}))
	defer server.Close()
	client := newTestClient(t, server, "owner")

	err := client.Apply(context.Background(), model.Action{Kind: "update", EventID: "owned", Before: &model.Event{ETag: "v1"}, Start: time.Now(), End: time.Now().Add(time.Hour)}, "operation")
	if err != nil {
		t.Fatal(err)
	}
	if incoming.ExtendedProperties.Private["other"] != "keep" {
		t.Fatal("private metadata lost")
	}
}

func newTestClient(t *testing.T, server *httptest.Server, owner string) *Client {
	t.Helper()
	client, err := New(context.Background(), server.Client(), "primary", owner, time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	client.api.BasePath = server.URL + "/"
	return client
}

func TestListOwnedUsesSDKPaginationAndLocalRegistry(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Query().Get("privateExtendedProperty") != "atlas_owner=owner" {
			t.Error("missing ownership query")
		}
		event := &gcalendar.Event{Id: "owned", Summary: "Practice", Start: &gcalendar.EventDateTime{DateTime: "2026-11-27T12:00:00+03:00"}, End: &gcalendar.EventDateTime{DateTime: "2026-11-27T13:00:00+03:00"}}
		page := &gcalendar.Events{Items: []*gcalendar.Event{event}}
		if r.URL.Query().Get("pageToken") == "" {
			page.NextPageToken = "next"
		} else {
			event.Id = "unregistered"
		}
		json.NewEncoder(w).Encode(page)
	}))
	defer server.Close()
	client := newTestClient(t, server, "owner")
	events, err := client.ListOwned(context.Background(), map[string]bool{"owned": true})
	if err != nil {
		t.Fatal(err)
	}
	if requests != 2 || len(events) != 1 || events[0].ID != "owned" {
		t.Fatalf("requests=%d events=%v", requests, events)
	}
}

func TestSDKPreconditionFailureAndEmptyDescription(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			event := &gcalendar.Event{Etag: "v1", ExtendedProperties: &gcalendar.EventExtendedProperties{Private: map[string]string{"atlas_owner": "owner"}}}
			json.NewEncoder(w).Encode(event)
			return
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if value, exists := body["description"]; !exists || value != "" {
			t.Error("empty description was omitted")
		}
		if r.Header.Get("If-Match") != "v1" {
			t.Error("missing etag")
		}
		w.WriteHeader(http.StatusPreconditionFailed)
	}))
	defer server.Close()
	client := newTestClient(t, server, "owner")
	err := client.Apply(context.Background(), model.Action{Kind: "update", EventID: "owned", Before: &model.Event{ETag: "v1"}}, "operation")
	if err != ErrChanged {
		t.Fatalf("err=%v", err)
	}
}
