package calendar

import (
	"context"
	"errors"
	"io"
	"testing"
)

func TestAuthorizeHonorsCancellationBeforeSideEffects(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := Authorize(ctx, "missing-credentials.json", "must-not-be-created.json", io.Discard)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
}
