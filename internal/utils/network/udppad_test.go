package network

import (
	"bytes"
	"encoding/binary"
	"net"
	"testing"
	"time"
)

func TestUDPPadderWireSize(t *testing.T) {
	receiver, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer receiver.Close()
	sender, err := net.DialUDP("udp4", nil, receiver.LocalAddr().(*net.UDPAddr))
	if err != nil {
		t.Fatal(err)
	}
	defer sender.Close()
	if err := receiver.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}

	wire := make([]byte, MaxDatagram)
	for _, size := range []int{0, 17, UDPPaddedSize - 1, 8192} {
		payload := bytes.Repeat([]byte{'X'}, size)
		if sent, err := WriteTunnelUDP(sender, nil, payload, true); err != nil || sent != size {
			t.Fatalf("write = %d, %v for %d bytes", sent, err, size)
		}
		n, _, err := receiver.ReadFromUDP(wire)
		if err != nil {
			t.Fatal(err)
		}
		if n != max(UDPPaddedSize, size+udpPadHeader) {
			t.Fatalf("wire size = %d for %d bytes", n, size)
		}
		if int(binary.BigEndian.Uint16(wire[:udpPadHeader])) != size ||
			!bytes.Equal(wire[udpPadHeader:udpPadHeader+size], payload) ||
			!bytes.Equal(wire[udpPadHeader+size:n], make([]byte, n-size-udpPadHeader)) {
			t.Fatalf("invalid frame for %d bytes", size)
		}
	}
}
