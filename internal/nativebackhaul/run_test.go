package nativebackhaul

import (
	"bytes"
	"context"
	"encoding/base64"
	"io"
	"net"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/backpack/backpack/config"
	"github.com/sirupsen/logrus"
)

func TestNativeSplitHTTPAuthenticatesAndTransfersAcrossBothRequests(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	listener := listenTest(t)
	mode := config.BackhaulConfig{Mode: "dagger-xhttp", Token: base64.StdEncoding.EncodeToString(make([]byte, 32))}
	creds, err := linkTLS(mode)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	sent := make(chan struct{})
	go func() {
		done <- serveSplitHTTP(ctx, listener, mode.Token, creds, func(c net.Conn) {
			close(sent)
			_, _ = io.Copy(c, c)
		})
	}()
	addr := listener.Addr().String()
	wrongKey := mode
	wrongKey.Token = base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32))
	if conn, err := dialLane(ctx, wrongKey, addr, creds); err == nil {
		conn.Close()
		t.Fatal("accepted an invalid request proof")
	}
	conn, err := dialLane(ctx, mode, addr, creds)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	payload := bytes.Repeat([]byte("split-http transfer\n"), 4096)
	writeErr := make(chan error, 1)
	go func() {
		_, err := conn.Write(payload)
		writeErr <- err
	}()
	got := make([]byte, len(payload))
	if _, err := io.ReadFull(conn, got); err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("incorrect echo: %v", err)
	}
	if err := <-writeErr; err != nil {
		t.Fatal(err)
	}
	<-sent
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Split HTTP listener did not stop")
	}
}

func TestNativeBackhaulMuxReplacesOneLaneWithoutResettingOthers(t *testing.T) {
	for _, mode := range []string{"xtcpmux", "xwsmux", "dagger-http", "dagger-https"} {
		t.Run(mode, func(t *testing.T) {
			target := listenTest(t)
			relay := listenTest(t)
			entry := freeTestPort(t)
			port := freeTestPort(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			go func() {
				for {
					conn, err := target.Accept()
					if err != nil {
						return
					}
					go func() { defer conn.Close(); _, _ = io.Copy(conn, conn) }()
				}
			}()
			var mu sync.Mutex
			var lanes []net.Conn
			go func() {
				for {
					front, err := relay.Accept()
					if err != nil {
						return
					}
					back, err := net.Dial("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
					if err != nil {
						front.Close()
						continue
					}
					mu.Lock()
					lanes = append(lanes, front)
					mu.Unlock()
					go func() {
						defer front.Close()
						defer back.Close()
						go func() { _, _ = io.Copy(back, front); back.Close() }()
						_, _ = io.Copy(front, back)
					}()
				}
			}()
			token := base64.StdEncoding.EncodeToString(make([]byte, 32))
			ir := config.BackhaulConfig{Native: true, Mode: mode, Role: "server", IranHost: "127.0.0.1", Port: uint16(port), Token: token,
				Ports: []string{net.JoinHostPort("127.0.0.1", strconv.Itoa(entry)) + "=" + target.Addr().String()}}
			kh := ir
			kh.Role, kh.Port, kh.Ports = "client", uint16(relay.Addr().(*net.TCPAddr).Port), nil
			log := logrus.New()
			log.SetOutput(io.Discard)
			stopped := make(chan error, 2)
			go func() { stopped <- Run(ctx, "test", ir, log) }()
			go func() { stopped <- Run(ctx, "test", kh, log) }()
			defer func() {
				cancel()
				for range 2 {
					select {
					case err := <-stopped:
						if err != nil {
							t.Error(err)
						}
					case <-time.After(3 * time.Second):
						t.Error("native engine did not stop")
					}
				}
			}()
			addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(entry))
			sockets := make([]net.Conn, 0, laneCount)
			defer func() {
				for _, s := range sockets {
					s.Close()
				}
			}()
			waitFor(t, func() bool {
				mu.Lock()
				defer mu.Unlock()
				return len(lanes) >= laneCount
			})
			waitFor(t, func() bool {
				for _, s := range sockets {
					s.Close()
				}
				sockets = sockets[:0]
				for range laneCount {
					s, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
					if err != nil {
						return false
					}
					sockets = append(sockets, s)
					if !echo(s, 350*time.Millisecond) {
						return false
					}
				}
				return true
			})
			mu.Lock()
			_ = lanes[0].Close()
			mu.Unlock()
			waitFor(t, func() bool {
				mu.Lock()
				defer mu.Unlock()
				return len(lanes) > laneCount
			})
			survived := 0
			for _, s := range sockets {
				if echo(s, 750*time.Millisecond) {
					survived++
				}
			}
			if survived != laneCount-1 {
				t.Fatalf("%d of %d existing streams survived, want %d", survived, laneCount, laneCount-1)
			}
			fresh, err := net.Dial("tcp", addr)
			if err != nil {
				t.Fatal(err)
			}
			defer fresh.Close()
			if !echo(fresh, time.Second) {
				t.Fatal("a new stream could not connect after recovery")
			}
		})
	}
}

func echo(s net.Conn, limit time.Duration) bool {
	_ = s.SetDeadline(time.Now().Add(limit))
	var b [4]byte
	if _, err := s.Write([]byte("ping")); err != nil {
		return false
	}
	_, err := io.ReadFull(s, b[:])
	return err == nil && string(b[:]) == "ping"
}

func waitFor(t *testing.T, ready func() bool) {
	t.Helper()
	for deadline := time.Now().Add(7 * time.Second); time.Now().Before(deadline); {
		if ready() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("native BackPack tunnel did not become ready")
}

func listenTest(t *testing.T) net.Listener {
	t.Helper()
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	return l
}

func freeTestPort(t *testing.T) int {
	l := listenTest(t)
	n := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	return n
}
