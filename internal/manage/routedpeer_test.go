package manage

import (
	"reflect"
	"strings"
	"testing"
)

func TestRoutedPeerForwardsPreserveBackPackPortMappings(t *testing.T) {
	got, err := routedForwardPorts("443, 2053=8443, 10000-10001=20000-20001")
	want := []string{"443:443", "2053:8443", "10000:20000", "10001:20001"}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, %v, want %v", got, err, want)
	}
	for _, input := range []string{"443, 443=8443", "443=192.0.2.2:443", "0", "443="} {
		if _, err := routedForwardPorts(input); err == nil {
			t.Fatalf("accepted unsupported mapping %q", input)
		}
	}
}

func TestRoutedPeerSecretPreservesHexKeys(t *testing.T) {
	hexKey := strings.Repeat("A2", 32)
	if routedKey(hexKey) != strings.ToLower(hexKey) || routedKey("") != "" {
		t.Fatal("changed a supplied hex key or replaced an automatic key")
	}
	if a, b := routedKey("custom secret"), routedKey("another secret"); len(a) != 64 || a == b {
		t.Fatal("custom secrets must produce distinct 32-byte keys")
	}
}
