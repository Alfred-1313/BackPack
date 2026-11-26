package manage

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestConnectionTestStopInterruptsPendingHelperRemoval(t *testing.T) {
	control := &ctExtraControl{}
	key, err := ctExtraCipher(base64.RawURLEncoding.EncodeToString(make([]byte, ctPairKeySize)))
	if err != nil {
		t.Fatal(err)
	}
	control.key = key
	s := &ConnTestIran{coord: &ctCoordinator{extra: control}}
	c := &connTestCase{tr: "omni-tcp", name: ctExtraName(), peerStarted: true}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- s.removeExtra(ctx, c) }()
	deadline := time.Now().Add(time.Second)
	for {
		control.mu.Lock()
		pending := control.jobs[0].reply != nil
		control.mu.Unlock()
		if pending {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("cleanup never requested peer removal")
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("pending removal returned %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled cleanup kept waiting for Kharej")
	}
}

func TestConnectionTestPeerWatchesForStopBeforeJoining(t *testing.T) {
	coord, err := startCTCoordinator(0, "secret")
	if err != nil {
		t.Fatal(err)
	}
	defer coord.close()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	peer, stop := context.WithCancelCause(ctx)
	defer stop(nil)
	link := ConnTestLink{Host: "127.0.0.1", Coord: coord.tcp.Addr().(*net.TCPAddr).Port, Tok: "secret"}
	go ctWatchCoordinator(peer, link, stop)
	coord.stop()
	<-peer.Done()
	if !errors.Is(context.Cause(peer), errConnTestStopped) {
		t.Fatalf("peer missed Stop while starting: %v", context.Cause(peer))
	}
	select {
	case <-coord.fetched:
	case <-ctx.Done():
		t.Fatal("coordinator did not see the peer's stop poll")
	}
}

func TestConnectionTestEngineLogRemainsBounded(t *testing.T) {
	f, err := os.Create(filepath.Join(t.TempDir(), "engine.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	w := &ctEngineLog{file: f}
	for _, data := range [][]byte{bytes.Repeat([]byte("debug\n"), ctEngineLogLimit), []byte("latest failure\n")} {
		if n, err := w.Write(data); err != nil || n != len(data) {
			t.Fatalf("writing log got %d, %v", n, err)
		}
	}
	info, err := f.Stat()
	if err != nil || info.Size() > ctEngineLogLimit || ctLastLine(f.Name()) != "latest failure" {
		t.Fatalf("test log has an invalid size or lost its last diagnostic: %v", err)
	}
}
