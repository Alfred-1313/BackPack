package mtproxy

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"time"
)

const randomStart, randomEnd = 11, 43

// CheckListener authenticates this proxy at its local or forwarded TCP address.
func (s Setup) CheckListener(ctx context.Context, addr string) error {
	if s.Mode == ModeWeb {
		return s.checkWebListener(ctx, addr)
	}
	_, err := s.CheckPath(ctx, addr)
	return err
}

// CheckPath authenticates the expected MTProxy through the forwarded port.
// It checks Fake TLS reachability and the secret; an actual Telegram session
// also depends on connectivity from the exit to Telegram.
func (s Setup) CheckPath(ctx context.Context, addr string) (time.Duration, error) {
	if err := s.Validate(); err != nil {
		return 0, err
	}
	if s.Mode == ModeWeb {
		return 0, errors.New("this setup requires the WEB probe")
	}
	ctx, cancel := context.WithTimeout(ctx, 7*time.Second)
	defer cancel()
	deadline, _ := ctx.Deadline()
	client, wire := net.Pipe()
	defer wire.Close()
	client.SetDeadline(deadline)
	wire.SetDeadline(deadline)
	go func() {
		defer client.Close()
		c := tls.Client(client, &tls.Config{ServerName: s.domain(), MinVersion: tls.VersionTLS13,
			CurvePreferences: []tls.CurveID{tls.X25519}})
		_ = c.HandshakeContext(ctx)
	}()
	hello, err := readTLSRecord(wire, 0x16)
	if err != nil {
		return 0, err
	}
	if len(hello) < randomEnd || hello[5] != 0x01 {
		return 0, errors.New("could not prepare a Fake TLS ClientHello")
	}
	key, _ := hex.DecodeString(s.Secret[2:34])
	clear(hello[randomStart:randomEnd])
	mac := hmac.New(sha256.New, key)
	mac.Write(hello)
	random := mac.Sum(nil)
	// Fake TLS puts its little-endian timestamp in the last four HMAC bytes.
	stamp := random[len(random)-4:]
	binary.LittleEndian.PutUint32(stamp, binary.LittleEndian.Uint32(stamp)^uint32(time.Now().Unix()))
	copy(hello[randomStart:randomEnd], random)

	start := time.Now()
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", addr)
	if err != nil {
		return 0, fmt.Errorf("could not connect to the TCP forward: %w", err)
	}
	defer conn.Close()
	defer context.AfterFunc(ctx, func() { conn.Close() })()
	conn.SetDeadline(deadline)
	if _, err := conn.Write(hello); err != nil {
		return 0, fmt.Errorf("could not send the Fake TLS handshake: %w", err)
	}
	reply, err := readTLSRecord(conn, 0x16)
	if err != nil {
		return 0, fmt.Errorf("the forward did not return a Fake TLS ServerHello: %w", err)
	}
	if len(reply) < randomEnd || reply[5] != 0x02 {
		return 0, errors.New("invalid Fake TLS ServerHello")
	}
	serverMAC := bytes.Clone(reply[randomStart:randomEnd])
	clear(reply[randomStart:randomEnd])
	mac.Reset()
	mac.Write(random)
	mac.Write(reply)
	for _, kind := range []byte{0x14, 0x17} {
		record, err := readTLSRecord(conn, kind)
		if err != nil {
			return 0, fmt.Errorf("the Fake TLS reply was incomplete: %w", err)
		}
		mac.Write(record)
	}
	if !hmac.Equal(mac.Sum(nil), serverMAC) {
		return 0, errors.New("the endpoint did not authenticate with this MTProxy secret")
	}
	return time.Since(start), nil
}

func readTLSRecord(r io.Reader, kind byte) ([]byte, error) {
	var header [5]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return nil, err
	}
	length := int(binary.BigEndian.Uint16(header[3:]))
	if header[0] != kind || header[1] != 0x03 || length < 1 || length > 18<<10 {
		return nil, errors.New("unexpected Fake TLS response")
	}
	record := make([]byte, len(header)+length)
	copy(record, header[:])
	_, err := io.ReadFull(r, record[len(header):])
	return record, err
}
