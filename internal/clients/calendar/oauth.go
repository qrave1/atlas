package calendar

import (
	"context"
	"net/http"

	"github.com/qrave1/atlas/internal/googleauth"
)

func authorizedClient(ctx context.Context, base *http.Client, credentialsPath, tokenPath string) (*http.Client, error) {
	return googleauth.AuthorizedClient(ctx, base, credentialsPath, tokenPath, googleauth.CalendarScope)
}
