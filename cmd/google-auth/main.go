// google-auth performs a one-time desktop OAuth flow on the owner's machine.
package main

import (
	"context"
	"crypto/rand"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/qrave1/atlas/internal/calendar"
	"github.com/qrave1/atlas/internal/config"
	"golang.org/x/oauth2"
)

func run() error {
	credentials := flag.String("credentials", "secrets/google-credentials.json", "Desktop OAuth client JSON")
	tokenPath := flag.String("token", "data/google-token.json", "Destination token file")
	flag.Parse()
	cfg, err := calendar.OAuthConfig(*credentials)
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
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	base, err := config.HTTPClient("", 45*time.Second)
	if err != nil {
		return err
	}
	ctx = context.WithValue(ctx, oauth2.HTTPClient, base)
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
			exchangeErr = calendar.SaveToken(*tokenPath, token)
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
	fmt.Println("Откройте ссылку на ЭТОМ компьютере и войдите в нужный Google-аккаунт:")
	fmt.Println(cfg.AuthCodeURL(state, oauth2.AccessTypeOffline, oauth2.SetAuthURLParam("prompt", "consent"), oauth2.S256ChallengeOption(verifier)))
	select {
	case err = <-result:
		if err == nil {
			fmt.Println("Токен сохранён:", *tokenPath)
		}
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
