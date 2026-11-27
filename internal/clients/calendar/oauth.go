package calendar

import (
	"context"
	"encoding/json"
	"github.com/qrave1/atlas/internal/googleauth"
	"golang.org/x/oauth2"
	"net/http"
	"os"
	"sync"
	"time"
)

type tokenSource struct {
	mu     sync.Mutex
	source oauth2.TokenSource
	path   string
	last   string
}

func (s *tokenSource) Token() (*oauth2.Token, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	token, err := s.source.Token()
	if err != nil {
		return nil, err
	}
	if token.AccessToken != s.last {
		if err = googleauth.SaveToken(s.path, token); err != nil {
			return nil, err
		}
		s.last = token.AccessToken
	}
	return token, nil
}

func authorizedClient(ctx context.Context, base *http.Client, credentialsPath, tokenPath string) (*http.Client, error) {
	cfg, err := googleauth.Config(credentialsPath)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(tokenPath)
	if err != nil {
		return nil, err
	}
	var token oauth2.Token
	if err = json.Unmarshal(raw, &token); err != nil {
		return nil, err
	}
	ctx = context.WithValue(ctx, oauth2.HTTPClient, base)
	source := &tokenSource{source: cfg.TokenSource(ctx, &token), path: tokenPath, last: token.AccessToken}
	return &http.Client{Transport: &oauth2.Transport{Source: source, Base: base.Transport}, Timeout: 45 * time.Second, CheckRedirect: base.CheckRedirect}, nil
}
