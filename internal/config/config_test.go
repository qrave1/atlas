package config

import (
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

func TestSOCKS5ProxyReceivesHostname(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	domains := make(chan string, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(5 * time.Second))
		var greeting [2]byte
		if _, err = io.ReadFull(conn, greeting[:]); err != nil {
			return
		}
		methods := make([]byte, int(greeting[1]))
		if _, err = io.ReadFull(conn, methods); err != nil {
			return
		}
		if _, err = conn.Write([]byte{5, 0}); err != nil {
			return
		}
		var header [5]byte
		if _, err = io.ReadFull(conn, header[:]); err != nil {
			return
		}
		if header[3] != 3 {
			return
		}
		name := make([]byte, int(header[4]))
		if _, err = io.ReadFull(conn, name); err != nil {
			return
		}
		var port [2]byte
		if _, err = io.ReadFull(conn, port[:]); err != nil {
			return
		}
		domains <- string(name)
		if _, err = conn.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 80}); err != nil {
			return
		}
		buf := make([]byte, 4096)
		if _, err = conn.Read(buf); err != nil {
			return
		}
		conn.Write([]byte("HTTP/1.1 200 OK\r\nContent-Length: 2\r\nConnection: close\r\n\r\nok"))
	}()
	client, err := HTTPClient("socks5://"+listener.Addr().String(), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Get("http://api.telegram.org/ping")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	select {
	case domain := <-domains:
		if domain != "api.telegram.org" {
			t.Fatalf("domain=%s", domain)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("proxy did not receive hostname")
	}
}

func TestDirectClientDoesNotInheritGlobalProxy(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "http://unrelated-proxy.invalid:8080")
	client, err := HTTPClient("", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if client.Transport.(*http.Transport).Proxy != nil {
		t.Fatal("direct client unexpectedly inherited proxy")
	}
}

func TestLoadEnvTagsDefaultsAndOverrides(t *testing.T) {
	for _, name := range []string{"AI_BASE_URL", "AI_CHAT_MODEL", "AI_TRANSCRIBE_MODEL", "GOOGLE_CREDENTIALS_FILE", "GOOGLE_TOKEN_FILE", "GOOGLE_CALENDAR_ID", "DATABASE_PATH"} {
		value, present := os.LookupEnv(name)
		os.Unsetenv(name)
		t.Cleanup(func() {
			if present {
				os.Setenv(name, value)
			} else {
				os.Unsetenv(name)
			}
		})
	}
	t.Setenv("TELEGRAM_BOT_TOKEN", "test-token")
	t.Setenv("AI_API_KEY", "test-key")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DatabasePath != "data/atlas.db" || cfg.CalendarID != "primary" || cfg.AIBaseURL != "https://api.proxyapi.ru/v1" {
		t.Fatalf("defaults not parsed: %+v", cfg)
	}
	t.Setenv("DATABASE_PATH", "custom/state.db")
	t.Setenv("TELEGRAM_PROXY_URL", "socks5://user:pass@proxy.example:1080")
	cfg, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DatabasePath != "custom/state.db" || cfg.TelegramProxy != "socks5://user:pass@proxy.example:1080" {
		t.Fatal("env overrides not parsed")
	}
}

func TestLoadRejectsEmptyRequiredSecrets(t *testing.T) {
	t.Setenv("TELEGRAM_BOT_TOKEN", "")
	t.Setenv("AI_API_KEY", "test-key")
	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "TELEGRAM_BOT_TOKEN") {
		t.Fatal("required token not enforced")
	}
}
