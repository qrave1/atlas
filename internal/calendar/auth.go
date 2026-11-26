package calendar

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

func OAuthConfig(path string) (*oauth2.Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return google.ConfigFromJSON(raw, "https://www.googleapis.com/auth/calendar.events")
}

func SaveToken(path string, token *oauth2.Token) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	raw, err := json.Marshal(token)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".token-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if err = f.Chmod(0600); err != nil {
		f.Close()
		return err
	}
	if _, err = f.Write(raw); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	// Linux cannot rename over an individual Docker bind mount.
	if err := os.Rename(name, path); !errors.Is(err, syscall.EBUSY) {
		return err
	}
	mounted, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	if _, err = mounted.Write(raw); err == nil {
		err = mounted.Truncate(int64(len(raw)))
	}
	if err == nil {
		err = mounted.Sync()
	}
	return errors.Join(err, mounted.Close())
}

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
		if err = SaveToken(s.path, token); err != nil {
			return nil, err
		}
		s.last = token.AccessToken
	}
	return token, nil
}

func AuthorizedClient(ctx context.Context, base *http.Client, credentialsPath, tokenPath string) (*http.Client, error) {
	cfg, err := OAuthConfig(credentialsPath)
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
