package manage

import (
	"strings"
	"testing"
)

func TestMTProxyForwardOnlyChangesTheManagedRule(t *testing.T) {
	raw := `[backhaul]
native = true
role = "server"
mode = "dagger-https"
ports = ["2083", "2087"] # existing services
[backhaul.mux]
pool = 4
[future]
setting = "keep"
`
	tunnel := Tunnel{Role: "server"}
	rule := "22222=127.0.0.1:32123"
	changed, err := mtProxyForwardBody([]byte(raw), tunnel, rule, false)
	if err != nil || !strings.Contains(changed, rule) || !strings.Contains(changed, "# existing services") {
		t.Fatalf("add forward: %v (%s)", err, changed)
	}
	reverted, err := mtProxyForwardBody([]byte(changed), tunnel, rule, true)
	if err != nil || reverted != raw {
		t.Fatalf("remove forward did not restore the original config: %v (%s)", err, reverted)
	}
}
