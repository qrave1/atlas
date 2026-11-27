package planner

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/qrave1/atlas/internal/clients/calendar"
	"github.com/qrave1/atlas/internal/model"
	"github.com/qrave1/atlas/internal/store"
)

type fakeBrain struct{ decision model.Decision }

func (b *fakeBrain) Decide(context.Context, time.Time, []model.Message, []model.Event, *model.Proposal) (model.Decision, error) {
	return b.decision, nil
}

type fakeCalendar struct {
	events     map[string]model.Event
	busy       []model.Event
	writes     int
	failed     bool
	failOnce   bool
	operations map[string]bool
}

func (c *fakeCalendar) ListOwned(context.Context, map[string]bool) ([]model.Event, error) {
	var out []model.Event
	for _, e := range c.events {
		out = append(out, e)
	}
	return out, nil
}
func (c *fakeCalendar) GetOwned(_ context.Context, id string) (model.Event, error) {
	e, ok := c.events[id]
	if !ok {
		return e, calendar.ErrNotFound
	}
	return e, nil
}
func (c *fakeCalendar) Busy(context.Context, time.Time, time.Time) ([]model.Event, error) {
	return c.busy, nil
}
func (c *fakeCalendar) Apply(_ context.Context, a model.Action, op string) error {
	if !c.operations[op] {
		c.operations[op] = true
		c.writes++
		if a.Kind == "delete" {
			delete(c.events, a.EventID)
		} else {
			c.events[a.EventID] = model.Event{ID: a.EventID, Title: a.Title, Start: a.Start, End: a.End, ETag: "new"}
		}
	}
	if c.failOnce && !c.failed {
		c.failed = true
		return errors.New("uncertain network outcome")
	}
	return nil
}

func fixture(t *testing.T) (*Service, *store.Store, *fakeBrain, *fakeCalendar, model.Action) {
	t.Helper()
	loc, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 26, 10, 0, 0, 0, loc)
	repo, err := store.New(context.Background(), filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repo.Close() })
	a := model.Action{Kind: "create", Title: "Практика", Start: now.Add(26 * time.Hour), End: now.Add(27 * time.Hour), DurationKnown: true}
	brain := &fakeBrain{model.Decision{Actions: []model.Action{a}}}
	cal := &fakeCalendar{events: map[string]model.Event{}, operations: map[string]bool{}}
	s := New(repo, brain, cal, loc)
	s.now = func() time.Time { return now }
	return s, repo, brain, cal, a
}

func TestWritesRequireConfirmationAndDuplicateIsSafe(t *testing.T) {
	s, _, _, cal, _ := fixture(t)
	ctx := context.Background()
	reply, err := s.Text(ctx, "Завтра в 12 практика на час")
	if err != nil {
		t.Fatal(err)
	}
	if reply.ProposalID == "" || cal.writes != 0 {
		t.Fatal("draft must not write calendar")
	}
	if _, err = s.Confirm(ctx, reply.ProposalID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Confirm(ctx, reply.ProposalID); err != nil {
		t.Fatal(err)
	}
	if cal.writes != 1 {
		t.Fatalf("writes=%d", cal.writes)
	}
}

func TestCorrectionInvalidatesOldButton(t *testing.T) {
	s, _, brain, cal, a := fixture(t)
	ctx := context.Background()
	old, err := s.Text(ctx, "На час")
	if err != nil {
		t.Fatal(err)
	}
	a.Start = a.Start.Add(time.Hour)
	a.End = a.End.Add(time.Hour)
	brain.decision.Actions = []model.Action{a}
	current, err := s.Text(ctx, "Перенеси на час позже")
	if err != nil {
		t.Fatal(err)
	}
	if old.ProposalID == current.ProposalID {
		t.Fatal("proposal ID reused")
	}
	if _, err = s.Confirm(ctx, old.ProposalID); err != nil {
		t.Fatal(err)
	}
	if cal.writes != 0 {
		t.Fatal("old button wrote calendar")
	}
}

func TestUnknownDurationRequiresQuestion(t *testing.T) {
	s, _, brain, cal, a := fixture(t)
	a.DurationKnown = false
	brain.decision.Actions = []model.Action{a}
	reply, err := s.Text(context.Background(), "Сделать практику завтра в 12")
	if err != nil {
		t.Fatal(err)
	}
	if reply.ProposalID != "" || !strings.Contains(reply.Text, "сколько времени") || cal.writes != 0 {
		t.Fatalf("unexpected reply: %+v", reply)
	}
}

func TestForeignEventCannotBeDeleted(t *testing.T) {
	s, _, brain, cal, _ := fixture(t)
	brain.decision.Actions = []model.Action{{Kind: "delete", EventID: "foreign"}}
	reply, err := s.Text(context.Background(), "Удалить чужое")
	if err != nil {
		t.Fatal(err)
	}
	if reply.ProposalID != "" || cal.writes != 0 {
		t.Fatal("foreign event permitted")
	}
}

func TestConflictOffersAlternativesWithoutWriting(t *testing.T) {
	s, _, _, cal, a := fixture(t)
	cal.busy = []model.Event{{ID: "other", Start: a.Start, End: a.End}}
	reply, err := s.Text(context.Background(), "Практика на час")
	if err != nil {
		t.Fatal(err)
	}
	if reply.ProposalID != "" || !strings.Contains(reply.Text, "Свободные варианты") || cal.writes != 0 {
		t.Fatalf("unexpected reply: %+v", reply)
	}
}

func TestNewConflictAtConfirmationInvalidatesProposal(t *testing.T) {
	s, _, _, cal, a := fixture(t)
	ctx := context.Background()
	reply, err := s.Text(ctx, "Практика на час")
	if err != nil {
		t.Fatal(err)
	}
	cal.busy = []model.Event{{ID: "other", Start: a.Start, End: a.End}}
	if _, err = s.Confirm(ctx, reply.ProposalID); err != nil {
		t.Fatal(err)
	}
	cal.busy = nil
	if _, err = s.Confirm(ctx, reply.ProposalID); err != nil {
		t.Fatal(err)
	}
	if cal.writes != 0 {
		t.Fatal("proposal stayed active after calendar conflict")
	}
}

func TestRetryAfterUncertainWriteAndServiceRestart(t *testing.T) {
	s, repo, brain, cal, _ := fixture(t)
	ctx := context.Background()
	cal.failOnce = true
	reply, err := s.Text(ctx, "Практика на час")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Confirm(ctx, reply.ProposalID); err != nil {
		t.Fatal(err)
	}
	restarted := New(repo, brain, cal, s.location)
	restarted.now = s.now
	if _, err = restarted.Resume(ctx); err != nil {
		t.Fatal(err)
	}
	if cal.writes != 1 {
		t.Fatalf("writes=%d, expected one after retry", cal.writes)
	}
	state, err := repo.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if state.Pending.Status != "done" || state.Pending.Completed != 1 {
		t.Fatal("operation did not complete")
	}
}

func TestExternalEditBeforeConfirmationBlocksWholeBatch(t *testing.T) {
	s, repo, brain, cal, a := fixture(t)
	ctx := context.Background()
	state, err := repo.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	state.Owned["owned"] = true
	if err = repo.Save(ctx, state); err != nil {
		t.Fatal(err)
	}
	cal.events["owned"] = model.Event{ID: "owned", Title: "Old", Start: a.Start.Add(3 * time.Hour), End: a.End.Add(3 * time.Hour), ETag: "v1"}
	brain.decision.Actions = []model.Action{a, {Kind: "delete", EventID: "owned"}}
	reply, err := s.Text(ctx, "Создать практику и удалить old")
	if err != nil {
		t.Fatal(err)
	}
	e := cal.events["owned"]
	e.ETag = "v2"
	cal.events["owned"] = e
	if _, err = s.Confirm(ctx, reply.ProposalID); err != nil {
		t.Fatal(err)
	}
	if cal.writes != 0 {
		t.Fatal("batch began despite external edit")
	}
}

func TestExpirationAndCancelBlockOldConfirmation(t *testing.T) {
	for _, mode := range []string{"expired", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			s, _, _, cal, _ := fixture(t)
			ctx := context.Background()
			reply, err := s.Text(ctx, "Практика на час")
			if err != nil {
				t.Fatal(err)
			}
			if mode == "expired" {
				now := s.now()
				s.now = func() time.Time { return now.Add(25 * time.Hour) }
			} else {
				if _, err = s.Cancel(ctx, "", false); err != nil {
					t.Fatal(err)
				}
			}
			if _, err = s.Confirm(ctx, reply.ProposalID); err != nil {
				t.Fatal(err)
			}
			if cal.writes != 0 {
				t.Fatal("expired/cancelled proposal wrote calendar")
			}
		})
	}
}

func TestUpdateThenDeleteOwnedEvent(t *testing.T) {
	s, repo, brain, cal, a := fixture(t)
	ctx := context.Background()
	created, err := s.Text(ctx, "Практика на час")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Confirm(ctx, created.ProposalID); err != nil {
		t.Fatal(err)
	}
	state, err := repo.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	id := state.Pending.Actions[0].EventID
	a.Kind = "update"
	a.EventID = id
	a.Title = "Практика по математике"
	a.Start = a.Start.Add(2 * time.Hour)
	a.End = a.End.Add(2 * time.Hour)
	brain.decision.Actions = []model.Action{a}
	updated, err := s.Text(ctx, "Перенеси на два часа позже и переименуй")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Confirm(ctx, updated.ProposalID); err != nil {
		t.Fatal(err)
	}
	if cal.events[id].Title != a.Title || !cal.events[id].Start.Equal(a.Start) {
		t.Fatal("event not updated")
	}
	brain.decision.Actions = []model.Action{{Kind: "delete", EventID: id}}
	deleted, err := s.Text(ctx, "Удали практику")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Confirm(ctx, deleted.ProposalID); err != nil {
		t.Fatal(err)
	}
	state, err = repo.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if state.Owned[id] || len(cal.events) != 0 || cal.writes != 3 {
		t.Fatal("delete did not clear event and registry")
	}
}
