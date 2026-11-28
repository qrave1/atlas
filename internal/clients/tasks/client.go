package tasks

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/qrave1/atlas/internal/config"
	"github.com/qrave1/atlas/internal/googleauth"
	"github.com/qrave1/atlas/internal/model"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
	gtasks "google.golang.org/api/tasks/v1"
)

var (
	ErrNotFound = errors.New("Google Task not found")
	ErrChanged  = errors.New("Google Task changed outside Atlas")
	ErrForeign  = errors.New("Google Task is not owned by Atlas")
)

const metadataPrefix = "\u2063atlas:"
const metadataSuffix = "\u2063"

type Client struct {
	api            *gtasks.Service
	listID         string
	installationID string
}

func New(ctx context.Context, base *http.Client, cfg config.Google, installationID string) (*Client, error) {
	client, err := googleauth.AuthorizedClient(ctx, base, cfg.CredentialsFile, cfg.TokenFile, googleauth.TasksScope)
	if err != nil {
		return nil, err
	}
	api, err := gtasks.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		return nil, fmt.Errorf("create Google Tasks client")
	}
	return &Client{api: api, listID: cfg.TaskListID, installationID: installationID}, nil
}

func tasksError(err error) error {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	var apiErr *googleapi.Error
	if errors.As(err, &apiErr) {
		if apiErr.Code == http.StatusNotFound || apiErr.Code == http.StatusGone {
			return ErrNotFound
		}
		if apiErr.Code == http.StatusPreconditionFailed {
			return ErrChanged
		}
		return fmt.Errorf("Google Tasks HTTP %d", apiErr.Code)
	}
	return errors.New("Google Tasks request failed")
}

func metadata(notes string) (string, string, string) {
	index := strings.LastIndex(notes, metadataPrefix)
	if index < 0 || !strings.HasSuffix(notes, metadataSuffix) {
		return notes, "", ""
	}
	marker := strings.TrimSuffix(notes[index+len(metadataPrefix):], metadataSuffix)
	owner, operation, ok := strings.Cut(marker, ":")
	if !ok {
		return notes, "", ""
	}
	return notes[:index], owner, operation
}

func notesWithMetadata(notes, owner, operation string) string {
	return notes + metadataPrefix + owner + ":" + operation + metadataSuffix
}

func (c *Client) fromTask(task *gtasks.Task) model.Task {
	notes, _, _ := metadata(task.Notes)
	result := model.Task{ID: task.Id, Title: task.Title, Notes: notes, Status: task.Status, ParentID: task.Parent, ETag: task.Etag}
	if len(task.Due) >= len("2006-01-02") {
		if _, err := time.Parse("2006-01-02", task.Due[:10]); err == nil {
			result.DueDate = task.Due[:10]
		}
	}
	return result
}

func (c *Client) ListOwned(ctx context.Context, owned map[string]bool) ([]model.Task, error) {
	result := make([]model.Task, 0, len(owned))
	err := c.api.Tasks.List(c.listID).MaxResults(100).ShowCompleted(true).Pages(ctx, func(page *gtasks.Tasks) error {
		for _, task := range page.Items {
			if !owned[task.Id] {
				continue
			}
			_, owner, _ := metadata(task.Notes)
			if owner == c.installationID {
				result = append(result, c.fromTask(task))
			}
		}
		return nil
	})
	if err != nil {
		return nil, tasksError(err)
	}
	return result, nil
}

func (c *Client) get(ctx context.Context, id string) (*gtasks.Task, error) {
	task, err := c.api.Tasks.Get(c.listID, id).Context(ctx).Do()
	return task, tasksError(err)
}

func (c *Client) GetOwned(ctx context.Context, id string) (model.Task, error) {
	task, err := c.get(ctx, id)
	if err != nil {
		return model.Task{}, err
	}
	_, owner, _ := metadata(task.Notes)
	if owner != c.installationID {
		return model.Task{}, ErrForeign
	}
	return c.fromTask(task), nil
}

func (c *Client) findOperation(ctx context.Context, operation string) (string, error) {
	var id string
	err := c.api.Tasks.List(c.listID).MaxResults(100).ShowCompleted(true).ShowHidden(true).Pages(ctx, func(page *gtasks.Tasks) error {
		for _, task := range page.Items {
			_, owner, marker := metadata(task.Notes)
			if owner == c.installationID && marker == operation {
				id = task.Id
				return errOperationFound
			}
		}
		return nil
	})
	if errors.Is(err, errOperationFound) {
		return id, nil
	}
	return id, tasksError(err)
}

var errOperationFound = errors.New("operation found")

func dateTime(date string) string {
	if date == "" {
		return ""
	}
	return date + "T00:00:00.000Z"
}

func (c *Client) Apply(ctx context.Context, action model.TaskAction, operation, parentID string) (string, error) {
	if action.Kind == "create" {
		if id, err := c.findOperation(ctx, operation); err != nil || id != "" {
			return id, err
		}
		task := &gtasks.Task{
			Title: action.Title, Notes: notesWithMetadata(action.Notes, c.installationID, operation),
			Due: dateTime(action.DueDate), Status: "needsAction",
		}
		call := c.api.Tasks.Insert(c.listID, task).Context(ctx)
		if parentID != "" {
			call.Parent(parentID)
		}
		created, err := call.Do()
		if err != nil {
			return "", tasksError(err)
		}
		return created.Id, nil
	}

	task, err := c.get(ctx, action.TaskID)
	if errors.Is(err, ErrNotFound) && action.Kind == "delete" {
		return action.TaskID, nil
	}
	if err != nil {
		return "", err
	}
	_, owner, previousOperation := metadata(task.Notes)
	if owner != c.installationID {
		return "", ErrForeign
	}
	if previousOperation == operation {
		return task.Id, nil
	}
	if action.Before == nil || task.Etag != action.Before.ETag {
		return "", ErrChanged
	}
	switch action.Kind {
	case "delete":
		call := c.api.Tasks.Delete(c.listID, task.Id).Context(ctx)
		call.Header().Set("If-Match", action.Before.ETag)
		return task.Id, tasksError(call.Do())
	case "update", "complete":
		notes, _, _ := metadata(task.Notes)
		body := &gtasks.Task{
			Title: task.Title, Notes: notesWithMetadata(notes, c.installationID, operation),
			Due: task.Due, Status: task.Status,
		}
		if action.Kind == "update" {
			body.Title = action.Title
			body.Notes = notesWithMetadata(action.Notes, c.installationID, operation)
			body.Due = dateTime(action.DueDate)
			body.ForceSendFields = []string{"Due"}
		}
		if action.Kind == "complete" {
			body.Status = "completed"
		}
		call := c.api.Tasks.Patch(c.listID, task.Id, body).Context(ctx)
		call.Header().Set("If-Match", action.Before.ETag)
		_, err = call.Do()
		return task.Id, tasksError(err)
	default:
		return "", fmt.Errorf("unknown task action")
	}
}
