package manage

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestPeerSetupAuthenticatesAndUsesObservedAddress(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	var calls atomic.Int32
	info, err := startPeerJoin(ctx, "127.0.0.1", "local-test", "direct", func(_ context.Context, _, peer string) (peerJoinResult, error) {
		calls.Add(1)
		return peerJoinResult{Name: peer}, nil
	}, func() { close(done) })
	if err != nil {
		t.Fatal(err)
	}
	link := FindSetupLink(info.Apply)
	if !info.Pending || !strings.HasPrefix(link, peerJoinScheme) {
		t.Fatal("wait-for-peer setup command was not issued")
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(link, peerJoinScheme))
	if err != nil {
		t.Fatal(err)
	}
	var invalid peerJoinLink
	if err := json.Unmarshal(payload, &invalid); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		change func(*peerJoinLink)
	}{
		{"key", func(l *peerJoinLink) { l.Key = base64.StdEncoding.EncodeToString(make([]byte, 32)) }},
		{"pin", func(l *peerJoinLink) { l.Pin = base64.RawURLEncoding.EncodeToString(make([]byte, 32)) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			tampered := invalid
			test.change(&tampered)
			data, err := json.Marshal(tampered)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := fetchPeerJoin(peerJoinScheme + base64.RawURLEncoding.EncodeToString(data)); err == nil {
				t.Fatal("accepted an invalid setup credential")
			}
			if calls.Load() != 0 {
				t.Fatal("unauthenticated request ran setup")
			}
		})
	}
	for range 2 {
		result, err := fetchPeerJoin(link)
		if err != nil || result.Name != "127.0.0.1" {
			t.Fatalf("observed peer %q: %v", result.Name, err)
		}
	}
	if calls.Load() != 1 {
		t.Fatal("retry launched a second setup")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("temporary setup listener did not shut down")
	}
}
