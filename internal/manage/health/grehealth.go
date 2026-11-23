package health

import (
	"sync"
	"time"

	"github.com/backpack/backpack/internal/app"
	"github.com/backpack/backpack/internal/metrics"
)

// A working GRE peer may decline ICMP echoes. Compare successive fresh
// engine samples to recognize traffic across that same tunnel in both directions.
type greReading struct {
	taken, lastTraffic time.Time
	in, out            uint64
	compared           bool
}

var greHistory = struct {
	sync.Mutex
	byName map[string]greReading
}{byName: make(map[string]greReading)}

func greHealth(name string) (bool, bool) {
	snap, err := metrics.Read(app.ConfigDir, name)
	if err != nil {
		return false, false
	}
	return observeGRE(name, snap, time.Now())
}

func observeGRE(name string, snap metrics.Snapshot, now time.Time) (bool, bool) {
	if now.Sub(snap.Taken) > datagramPeerWindow || snap.Taken.After(now) {
		return false, false
	}
	greHistory.Lock()
	defer greHistory.Unlock()
	for key, old := range greHistory.byName {
		if now.Sub(old.taken) > datagramPeerWindow {
			delete(greHistory.byName, key)
		}
	}
	prev := greHistory.byName[name]
	if snap.Taken.Before(prev.taken) {
		return false, false
	}
	next := prev
	if snap.Taken.After(prev.taken) {
		next = greReading{taken: snap.Taken, in: snap.BytesIn, out: snap.BytesOut,
			lastTraffic: prev.lastTraffic, compared: !prev.taken.IsZero()}
		if snap.BytesIn < prev.in || snap.BytesOut < prev.out {
			next.lastTraffic = time.Time{}
		} else if next.compared && snap.BytesIn > prev.in && snap.BytesOut > prev.out {
			next.lastTraffic = snap.Taken
		}
		greHistory.byName[name] = next
	}
	return snap.Peer != "" || now.Sub(next.lastTraffic) <= datagramPeerWindow, snap.Peer != "" || next.compared
}
