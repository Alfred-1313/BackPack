package mtproxy

import (
	"net"
	"testing"
)

func TestMTProxyPortChecksTheExactExitAddress(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	check := PortCheck{Bind: "127.0.0.1", Port: listener.Addr().(*net.TCPAddr).Port}
	busy, err := check.Check()
	if err != nil || !busy.Busy || check.ManualResult(check.ResultLine(busy)) {
		t.Fatalf("occupied exit: %+v %v", busy, err)
	}
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	free, err := check.Check()
	if err != nil || free.Busy || !check.ManualResult(check.ResultLine(free)) {
		t.Fatalf("released exit: %+v %v", free, err)
	}
	random, err := (PortCheck{Bind: check.Bind}).Check()
	if err != nil || random.Port < 1 || random.Busy {
		t.Fatalf("random port: %+v %v", random, err)
	}
}
