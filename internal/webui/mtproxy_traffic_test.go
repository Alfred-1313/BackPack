package webui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"github.com/backpack/backpack/internal/mtproxy"
)

func TestMTProxyCountersReconcileWithoutResettingTheOtherMode(t *testing.T) {
	binding := mtProxyTrafficBinding{
		name: "test", counter: "p_live", host: netip.MustParseAddr("127.0.0.1"), port: 8443,
	}
	state := mtProxyNFT{
		exists: true,
		chains: map[string]bool{"rx": true, "tx": true},
		counters: map[string]mtProxyCounter{
			"p_live":  {Bytes: 1000, Handle: 9},
			"p_ended": {Bytes: 400, Handle: 12},
		},
	}
	script := state.reconcile([]mtProxyTrafficBinding{binding}, map[string]uint64{"p_live": 1000})
	for _, want := range []string{
		"delete counter inet backpack_mtproxy handle 12",
		"ip daddr 127.0.0.1 tcp dport 8443 ct direction original",
		"ip saddr 127.0.0.1 tcp sport 8443 ct direction reply",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("missing %q in accounting update", want)
		}
	}
	if strings.Contains(script, "handle 9") || strings.Contains(script, "add counter") {
		t.Fatalf("the remaining proxy's counter would be reset:\n%s", script)
	}
}

func TestMTProxyCounterStartsWithSavedBytesAfterTheKernelTableIsLost(t *testing.T) {
	binding := mtProxyTrafficBinding{
		name: "test", counter: "p_live", host: netip.MustParseAddr("2001:db8::1"), port: 443,
	}
	script := (mtProxyNFT{}).reconcile([]mtProxyTrafficBinding{binding}, map[string]uint64{"p_live": 12345})
	if !strings.Contains(script, "p_live { packets 0 bytes 12345; }") ||
		!strings.Contains(script, "ip6 daddr 2001:db8::1 tcp dport 443") {
		t.Fatalf("saved IPv6 counter could not be restored:\n%s", script)
	}
}

func TestMTProxyAccountingWaitsForTheConfiguredProxy(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()
	s, err := mtproxy.NewProxy(mtproxy.ModeNative, 8443, "127.0.0.1", "www.example.com", "", "")
	if err != nil {
		t.Fatal(err)
	}
	bindings := []mtProxyTrafficBinding{{listen: target.Listener.Addr().String(), setup: s}}
	if ready := verifiedMTProxyBindings(context.Background(), bindings, nil); len(ready) != 0 {
		t.Fatal("accounting started for an unrelated service on the forwarding port")
	}
}
