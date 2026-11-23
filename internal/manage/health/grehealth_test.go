package health

import (
	"testing"
	"time"

	"github.com/backpack/backpack/internal/metrics"
)

func TestGREHealthWithFilteredICMP(t *testing.T) {
	now := time.Now()
	name := t.Name()
	for _, v := range []struct {
		age           int
		in, out       uint64
		peer          string
		online, known bool
	}{
		{0, 4000, 6000, "", false, false},
		{5, 4200, 6500, "", true, true},
		{5, 4200, 6500, "", true, true},
		{10, 4200, 6800, "", true, true},
		{15, 4200, 6800, "192.0.2.10", true, true},
		{20, 0, 0, "", false, true},
		{25, 0, 1000, "", false, true},
		{200, 8000, 10000, "", false, false},
	} {
		taken := now.Add(time.Duration(v.age) * time.Second)
		s := metrics.Snapshot{Taken: taken, BytesIn: v.in, BytesOut: v.out, Peer: v.peer}
		online, known := observeGRE(name, s, taken)
		if online != v.online || known != v.known {
			t.Fatalf("at %ds: (%v,%v), want (%v,%v)", v.age, online, known, v.online, v.known)
		}
	}
	s := metrics.Snapshot{Taken: now, Peer: "192.0.2.10"}
	if online, known := observeGRE(name, s, now.Add(2*datagramPeerWindow)); online || known {
		t.Fatal("stale peer should be unknown")
	}
}
