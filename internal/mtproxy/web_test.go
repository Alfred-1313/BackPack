package mtproxy

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWEBReadinessRejectsDecoyPage(t *testing.T) {
	s, err := NewProxy(ModeWeb, 8443, "127.0.0.1", "proxy.example.com", "203.0.113.10", "")
	if err != nil {
		t.Fatal(err)
	}
	decoy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("<html><a href='/api/v1/session'>ordinary page</a></html>"))
	}))
	defer decoy.Close()
	if err := s.checkWebListener(context.Background(), decoy.Listener.Addr().String()); err == nil {
		t.Fatal("a page mentioning the session route passed WEB authentication")
	}
}

func TestWEBReadinessChecksTheConfiguredHostAndCapability(t *testing.T) {
	s, err := NewProxy(ModeWeb, 8443, "127.0.0.1", "proxy.example.com", "203.0.113.10", "")
	if err != nil {
		t.Fatal(err)
	}
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != s.WebHost || r.URL.Query().Get("bridge") != s.webCapability() ||
			r.Header.Get("Accept") != webBootstrapType {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", webBootstrapType)
		_ = json.NewEncoder(w).Encode(map[string]any{"v": 1, "bootstrap": strings.Repeat("A", 43)})
	}))
	defer proxy.Close()
	if err := s.checkWebListener(context.Background(), proxy.Listener.Addr().String()); err != nil {
		t.Fatal(err)
	}
}
