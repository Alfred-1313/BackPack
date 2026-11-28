package network

import (
	"encoding/binary"
	"errors"
	"io"
	"net"
)

// UDPPaddedSize is the minimum UDP payload on the wire. With IP and UDP headers
// it fits within the IPv6 minimum MTU. Large packets receive only the length.
const UDPPaddedSize = 1200
const udpPadHeader = 2
const maxUDPWire = 65507 // maximum IPv4 UDP payload
const maxPaddedPayload = maxUDPWire - udpPadHeader

var ErrPaddedUDPTooLarge = errors.New("UDP payload too large for padding")

// UDPControlToken keeps plain and padded UDP sessions distinct on their TCP control channel.
func UDPControlToken(token string, padded bool) string {
	if padded {
		return "udppad-v1:" + token
	}
	return token
}

// ReadTunnelUDP ignores invalid padded envelopes. The returned payload shares buf.
func ReadTunnelUDP(conn *net.UDPConn, buf []byte, padded bool) ([]byte, *net.UDPAddr, error) {
	for {
		n, from, err := conn.ReadFromUDP(buf)
		if err != nil {
			return nil, nil, err
		}
		if !padded {
			return buf[:n], from, nil
		}
		if n < UDPPaddedSize || n > maxUDPWire || (n == len(buf) && n < maxUDPWire) {
			continue
		}
		size := int(binary.BigEndian.Uint16(buf[:udpPadHeader]))
		if n != max(UDPPaddedSize, size+udpPadHeader) {
			continue
		}
		return buf[udpPadHeader : udpPadHeader+size], from, nil
	}
}

// WriteTunnelUDP sends one datagram and returns the original payload byte count.
// A nil destination is used for connected UDP sockets.
func WriteTunnelUDP(conn *net.UDPConn, to *net.UDPAddr, payload []byte, padded bool) (int, error) {
	wire := payload
	if padded {
		if len(payload) > maxPaddedPayload {
			return 0, ErrPaddedUDPTooLarge
		}
		wire = GetDatagramBuffer(max(UDPPaddedSize, len(payload)+udpPadHeader))
		defer PutDatagramBuffer(wire)
		binary.BigEndian.PutUint16(wire[:udpPadHeader], uint16(len(payload)))
		copy(wire[udpPadHeader:], payload)
		clear(wire[udpPadHeader+len(payload):])
	}
	var n int
	var err error
	if to == nil {
		n, err = conn.Write(wire)
	} else {
		n, err = conn.WriteToUDP(wire, to)
	}
	if err != nil {
		return 0, err
	}
	if n != len(wire) {
		return 0, io.ErrShortWrite
	}
	return len(payload), nil
}
