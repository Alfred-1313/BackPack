package manage

import (
	"context"
	"testing"
	"time"

	"github.com/backpack/backpack/internal/tunnel/l3"
)

func TestConnectionTestStartsDirectAndSpoofWhileReverseIsConnecting(t *testing.T) {
	previousBinary := connTestBinary
	connTestBinary = func() (string, error) { return "/bin/true", nil }
	defer func() { connTestBinary = previousBinary }()

	coord := &ctCoordinator{spoofReady: make(chan struct{})}
	s := &ConnTestIran{dir: t.TempDir(), coord: coord}
	defer s.closeTestEngines()
	s.cases = []*connTestCase{
		{kind: "reverse", tr: "tcp", entry: ctPickPort(map[int]bool{}, false)},
		{kind: "direct", tr: "gre", name: "test-direct", l3: l3Spec{Carrier: l3.CarrierGRE}},
		{kind: "spoof", tr: ctSpoofToIran},
	}
	rows := make([]ConnTestResult, len(s.cases))
	for i, c := range s.cases {
		rows[i] = ConnTestResult{Kind: c.kind, Transport: c.tr, Status: ctTesting}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	direct := make(chan struct{}, 1)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		s.runStability(ctx, "127.0.0.1", 1, true, rows, func(i int, r ConnTestResult) {
			if i == 1 && r.Phase == ctPhaseTransportConnect {
				direct <- struct{}{}
			}
		})
	}()
	defer func() { cancel(); <-finished }()
	select {
	case <-direct:
	case <-ctx.Done():
		t.Fatal("Direct waited for Reverse to finish")
	}
	for ctx.Err() == nil {
		coord.mu.Lock()
		open := coord.spoofOpen
		coord.mu.Unlock()
		if open {
			return
		}
		ctSleep(ctx, 10*time.Millisecond)
	}
	t.Fatal("Spoof waited for Reverse to finish")
}
