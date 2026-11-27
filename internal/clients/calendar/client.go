package calendar

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	gcalendar "google.golang.org/api/calendar/v3"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"

	"github.com/qrave1/atlas/internal/config"
	"github.com/qrave1/atlas/internal/model"
)

var ErrNotFound = errors.New("calendar: event not found")
var ErrChanged = errors.New("calendar: event changed outside bot")
var ErrForeign = errors.New("calendar: event not owned by this bot")

type Client struct {
	api            *gcalendar.Service
	calendarID     string
	installationID string
	location       *time.Location
}

func New(ctx context.Context, base *http.Client, cfg config.Google, installationID string, loc *time.Location) (*Client, error) {
	client, err := authorizedClient(ctx, base, cfg.CredentialsFile, cfg.TokenFile)
	if err != nil {
		return nil, err
	}
	return newClient(ctx, client, cfg.CalendarID, installationID, loc)
}

func newClient(ctx context.Context, client *http.Client, calendarID, installationID string, location *time.Location) (*Client, error) {
	api, err := gcalendar.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		return nil, fmt.Errorf("create calendar client: %w", err)
	}
	return &Client{api: api, calendarID: calendarID, installationID: installationID, location: location}, nil
}

func apiStatus(err error, code int) bool {
	var apiErr *googleapi.Error
	return errors.As(err, &apiErr) && apiErr.Code == code
}

func calendarError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	var apiErr *googleapi.Error
	if errors.As(err, &apiErr) {
		switch apiErr.Code {
		case http.StatusNotFound, http.StatusGone:
			return ErrNotFound
		case http.StatusPreconditionFailed:
			return ErrChanged
		default:
			return fmt.Errorf("calendar http %d", apiErr.Code)
		}
	}
	// The SDK error may contain request URLs or response data; keep those private.
	return fmt.Errorf("calendar request failed")
}

func (c *Client) fromEvent(event *gcalendar.Event) (model.Event, error) {
	parse := func(d *gcalendar.EventDateTime) (time.Time, error) {
		if d == nil {
			return time.Time{}, fmt.Errorf("calendar event time missing")
		}
		if d.DateTime != "" {
			return time.Parse(time.RFC3339, d.DateTime)
		}
		return time.ParseInLocation("2006-01-02", d.Date, c.location)
	}
	start, err := parse(event.Start)
	if err != nil {
		return model.Event{}, err
	}
	end, err := parse(event.End)
	if err != nil {
		return model.Event{}, err
	}
	return model.Event{ID: event.Id, Title: event.Summary, Description: event.Description, Start: start, End: end, ETag: event.Etag}, nil
}

func (c *Client) get(ctx context.Context, id string) (*gcalendar.Event, error) {
	event, err := c.api.Events.Get(c.calendarID, id).Context(ctx).Do()
	return event, calendarError(err)
}

func private(event *gcalendar.Event, key string) string {
	if event.ExtendedProperties == nil {
		return ""
	}
	return event.ExtendedProperties.Private[key]
}

func (c *Client) GetOwned(ctx context.Context, id string) (model.Event, error) {
	event, err := c.get(ctx, id)
	if err != nil {
		return model.Event{}, err
	}
	if event.Status == "cancelled" {
		return model.Event{}, ErrNotFound
	}
	if private(event, "atlas_owner") != c.installationID {
		return model.Event{}, ErrForeign
	}
	return c.fromEvent(event)
}

func (c *Client) ListOwned(ctx context.Context, owned map[string]bool) ([]model.Event, error) {
	query := c.api.Events.List(c.calendarID).PrivateExtendedProperty("atlas_owner=" + c.installationID)
	return c.listEvents(ctx, query, func(e *gcalendar.Event) bool { return owned[e.Id] })
}

func (c *Client) Busy(ctx context.Context, start, end time.Time) ([]model.Event, error) {
	query := c.api.Events.List(c.calendarID).TimeMin(start.Format(time.RFC3339)).TimeMax(end.Format(time.RFC3339)).SingleEvents(true)
	return c.listEvents(ctx, query, func(e *gcalendar.Event) bool { return e.Transparency != "transparent" })
}

func (c *Client) listEvents(ctx context.Context, query *gcalendar.EventsListCall, include func(*gcalendar.Event) bool) ([]model.Event, error) {
	events := []model.Event{}
	count := 0
	err := query.MaxResults(2500).Pages(ctx, func(page *gcalendar.Events) error {
		count += len(page.Items)
		if count > 10000 {
			return fmt.Errorf("calendar result exceeds safety limit")
		}
		for _, event := range page.Items {
			if event.Status == "cancelled" || !include(event) {
				continue
			}
			item, err := c.fromEvent(event)
			if err != nil {
				return err
			}
			events = append(events, item)
		}
		return nil
	})
	if err != nil {
		return nil, calendarError(err)
	}
	return events, nil
}
func (c *Client) desired(action model.Action, operation string) *gcalendar.Event {
	return &gcalendar.Event{
		Id: action.EventID, Summary: action.Title, Description: action.Description,
		Start:              &gcalendar.EventDateTime{DateTime: action.Start.Format(time.RFC3339), TimeZone: "Europe/Moscow"},
		End:                &gcalendar.EventDateTime{DateTime: action.End.Format(time.RFC3339), TimeZone: "Europe/Moscow"},
		ExtendedProperties: &gcalendar.EventExtendedProperties{Private: map[string]string{"atlas_owner": c.installationID, "atlas_operation": operation}},
		// An empty description in a PATCH must actually clear the old description.
		ForceSendFields: []string{"Description"},
	}
}

// Apply is replay-safe for a persisted operation ID. ETags protect external edits.
func (c *Client) Apply(ctx context.Context, action model.Action, operation string) error {
	if action.Kind == "create" {
		_, err := c.api.Events.Insert(c.calendarID, c.desired(action, operation)).Context(ctx).Do()
		if err == nil {
			return nil
		}
		if !apiStatus(err, http.StatusConflict) {
			return calendarError(err)
		}
		event, getErr := c.get(ctx, action.EventID)
		if getErr != nil {
			return getErr
		}
		if private(event, "atlas_owner") == c.installationID && private(event, "atlas_operation") == operation {
			return nil
		}
		return ErrForeign
	}
	event, err := c.get(ctx, action.EventID)
	if errors.Is(err, ErrNotFound) && action.Kind == "delete" {
		return nil
	}
	if err != nil {
		return err
	}
	if private(event, "atlas_owner") != c.installationID {
		return ErrForeign
	}
	if action.Kind == "update" && private(event, "atlas_operation") == operation {
		return nil
	}
	if action.Before == nil || event.Etag != action.Before.ETag {
		return ErrChanged
	}
	switch action.Kind {
	case "delete":
		call := c.api.Events.Delete(c.calendarID, action.EventID).Context(ctx)
		call.Header().Set("If-Match", action.Before.ETag)
		return calendarError(call.Do())
	case "update":
		patch := c.desired(action, operation)
		for key, value := range event.ExtendedProperties.Private {
			if _, exists := patch.ExtendedProperties.Private[key]; !exists {
				patch.ExtendedProperties.Private[key] = value
			}
		}
		call := c.api.Events.Patch(c.calendarID, action.EventID, patch).Context(ctx)
		call.Header().Set("If-Match", action.Before.ETag)
		_, err = call.Do()
		return calendarError(err)
	default:
		return fmt.Errorf("unknown action kind")
	}
}
