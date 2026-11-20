// Package transportprobe checks a configured TCP forward with a one-time
// challenge and a reply that is known only to the temporary listener.
package transportprobe

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"time"
)

const Lifetime = 5 * time.Minute
const prefix = "bpt1."

func validateAddress(address string) error {
	ap, err := netip.ParseAddrPort(address)
	if err != nil || ap.Port() == 0 ||
		(!ap.Addr().IsLoopback() && !netip.MustParsePrefix("10.201.0.0/16").Contains(ap.Addr())) {
		return errors.New("enter a loopback or OmniTunnel IPv4 address and a port, for example 127.0.0.1:18080")
	}
	return nil
}

// Listen returns a test code and runs a single temporary proof listener.
func Listen(ctx context.Context, address string) (string, error) {
	if err := validateAddress(address); err != nil {
		return "", err
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return "", err
	}
	proof := make([]byte, 64)
	if _, err := rand.Read(proof); err != nil {
		listener.Close()
		return "", err
	}
	go func() {
		defer listener.Close()
		stop := context.AfterFunc(ctx, func() { listener.Close() })
		defer stop()
		end := time.Now().Add(Lifetime)
		_ = listener.(*net.TCPListener).SetDeadline(end)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
			challenge := make([]byte, 32)
			_, err = io.ReadFull(conn, challenge)
			accepted := err == nil && bytes.Equal(proof[:32], challenge)
			if accepted {
				_, err = conn.Write(proof[32:])
			}
			conn.Close()
			if accepted && err == nil {
				return
			}
		}
	}()
	return prefix + base64.RawURLEncoding.EncodeToString(proof), nil
}

func Probe(ctx context.Context, address, code string) (time.Duration, error) {
	if err := validateAddress(address); err != nil {
		return 0, err
	}
	data, ok := strings.CutPrefix(strings.TrimSpace(code), prefix)
	proof, err := base64.RawURLEncoding.DecodeString(data)
	if !ok || err != nil || len(proof) != 64 {
		return 0, errors.New("invalid configured tunnel test code")
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	start := time.Now()
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", address)
	if err != nil {
		return 0, err
	}
	defer conn.Close()
	cancelIO := context.AfterFunc(ctx, func() { conn.Close() })
	defer cancelIO()
	if _, err := conn.Write(proof[:32]); err != nil {
		return 0, err
	}
	reply := make([]byte, 32)
	if _, err := io.ReadFull(conn, reply); err != nil {
		return 0, fmt.Errorf("no valid reply from Kharej: %w", err)
	}
	if !bytes.Equal(reply, proof[32:]) {
		return 0, errors.New("wrong reply from Kharej")
	}
	return time.Since(start), nil
}

func Command(ctx context.Context, args []string, input io.Reader, output io.Writer) error {
	if len(args) == 2 && args[0] == "listen" {
		ctx, cancel := context.WithTimeout(ctx, Lifetime)
		defer cancel()
		code, err := Listen(ctx, args[1])
		if err != nil {
			return err
		}
		fmt.Fprintln(output, code)
		<-ctx.Done()
		return nil
	}
	if len(args) == 2 && args[0] == "probe" {
		code, err := io.ReadAll(io.LimitReader(input, 512))
		if err != nil {
			return err
		}
		elapsed, err := Probe(ctx, args[1], string(code))
		if err != nil {
			return err
		}
		fmt.Fprintln(output, "Kharej payload verified in "+strconv.FormatInt(elapsed.Milliseconds(), 10)+" ms")
		return nil
	}
	return errors.New("usage: backpack transport-test listen|probe <address:port>; probe reads the test code on stdin")
}
