package config

import (
	"io"
	"net"
	"net/http"
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
	client, err := (Telegram{ProxyURL: "socks5://" + listener.Addr().String()}).HTTPClient()
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
	client := DirectHTTPClient(time.Second)
	if client.Transport.(*http.Transport).Proxy != nil {
		t.Fatal("direct client unexpectedly inherited proxy")
	}
}
