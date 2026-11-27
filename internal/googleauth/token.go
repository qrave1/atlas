package googleauth

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/qrave1/atlas/internal/config"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

func Config(path string) (*oauth2.Config, error) {
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
	return os.Rename(name, path)
}

// Authorize performs desktop OAuth and saves the refresh token.
func Authorize(ctx context.Context, credentialsPath, tokenPath string, output io.Writer) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	cfg, err := Config(credentialsPath)
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", "127.0.0.1:8765")
	if err != nil {
		return err
	}
	defer listener.Close()
	cfg.RedirectURL = "http://127.0.0.1:8765/callback"
	state := rand.Text()
	verifier := oauth2.GenerateVerifier()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	ctx = context.WithValue(ctx, oauth2.HTTPClient, config.DirectHTTPClient(45*time.Second))
	result := make(chan error, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("state") != state {
			http.Error(w, "Invalid state", http.StatusBadRequest)
			return
		}
		if r.URL.Query().Get("error") != "" {
			http.Error(w, "Authorization denied", http.StatusBadRequest)
			select {
			case result <- fmt.Errorf("authorization denied"):
			default:
			}
			return
		}
		token, exchangeErr := cfg.Exchange(ctx, r.URL.Query().Get("code"), oauth2.VerifierOption(verifier))
		if exchangeErr == nil && token.RefreshToken == "" {
			exchangeErr = fmt.Errorf("no refresh token; revoke old consent and authorize again")
		}
		if exchangeErr == nil {
			exchangeErr = SaveToken(tokenPath, token)
		}
		if exchangeErr != nil {
			http.Error(w, "Authorization failed; check terminal", http.StatusBadRequest)
		} else {
			fmt.Fprintln(w, "Google Calendar connected. You can close this tab.")
		}
		select {
		case result <- exchangeErr:
		default:
		}
	})
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second, WriteTimeout: 60 * time.Second}
	go func() {
		if serveErr := server.Serve(listener); serveErr != nil && serveErr != http.ErrServerClosed {
			select {
			case result <- serveErr:
			default:
			}
		}
	}()
	defer server.Close()
	fmt.Fprintln(output, "Откройте ссылку на ЭТОМ компьютере и войдите в нужный Google-аккаунт:")
	fmt.Fprintln(output, cfg.AuthCodeURL(state, oauth2.AccessTypeOffline, oauth2.SetAuthURLParam("prompt", "consent"), oauth2.S256ChallengeOption(verifier)))
	select {
	case err = <-result:
		if err == nil {
			fmt.Fprintln(output, "Токен сохранён:", tokenPath)
		}
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}
