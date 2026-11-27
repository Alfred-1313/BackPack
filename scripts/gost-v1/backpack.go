package main

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"net"
	"net/netip"
	"os"
	"time"

	"github.com/ginuerzh/gosocks5"
)

var backpackConfig string

type backpackForward struct {
	Listen      string
	Server      string
	Target      string
	Password    string
	Certificate string
	Key         string
}

// runBackpack runs the v1 SOCKS5 data path with a fixed forwarding destination
// and authenticated TLS, using a private file instead of credentials in argv.
func runBackpack(path string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var form backpackForward
	if err := json.Unmarshal(raw, &form); err != nil {
		return err
	}
	target, err := netip.ParseAddrPort(form.Target)
	if err != nil || target.Addr() != netip.MustParseAddr("127.0.0.1") || target.Port() == 0 ||
		len(form.Password) < 32 || len(form.Password) > 96 {
		return errors.New("invalid BackPack GOST v1 destination or credential")
	}
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12}
	var listener net.Listener
	var handle func(net.Conn)
	if form.Server == "" {
		cert, err := tls.LoadX509KeyPair(form.Certificate, form.Key)
		if err != nil {
			return err
		}
		tlsConfig.Certificates = []tls.Certificate{cert}
		listener, err = tls.Listen("tcp4", form.Listen, tlsConfig)
		if err != nil {
			return err
		}
		handle = func(conn net.Conn) { form.accept(conn) }
	} else {
		host, _, err := net.SplitHostPort(form.Server)
		if err != nil {
			return err
		}
		pem, err := os.ReadFile(form.Certificate)
		if err != nil {
			return err
		}
		roots := x509.NewCertPool()
		if !roots.AppendCertsFromPEM(pem) {
			return errors.New("invalid BackPack GOST v1 trust certificate")
		}
		tlsConfig.ServerName, tlsConfig.RootCAs = host, roots
		listener, err = net.Listen("tcp4", form.Listen)
		if err != nil {
			return err
		}
		handle = func(conn net.Conn) { form.forward(conn, target, tlsConfig) }
	}
	defer listener.Close()
	for {
		conn, err := listener.Accept()
		if err != nil {
			return err
		}
		go func() {
			defer conn.Close()
			handle(conn)
		}()
	}
}

func (f backpackForward) accept(conn net.Conn) {
	conn.SetDeadline(time.Now().Add(10 * time.Second))
	wrapped := gosocks5.ServerConn(conn, &gosocks5.Config{
		SelectMethod: func(methods ...uint8) uint8 {
			for _, method := range methods {
				if method == gosocks5.MethodUserPass {
					return method
				}
			}
			return gosocks5.MethodNoAcceptable
		},
		MethodSelected: func(method uint8, conn net.Conn) (net.Conn, error) {
			if method != gosocks5.MethodUserPass {
				return nil, gosocks5.ErrBadMethod
			}
			return conn, serverSocksAuth(conn, "backpack", f.Password)
		},
	})
	request, err := gosocks5.ReadRequest(wrapped)
	if err != nil {
		return
	}
	if request.Cmd != gosocks5.CmdConnect || request.Addr.String() != f.Target {
		_ = gosocks5.NewReply(gosocks5.Failure, nil).Write(wrapped)
		return
	}
	backend, err := net.DialTimeout("tcp4", f.Target, 5*time.Second)
	if err != nil {
		_ = gosocks5.NewReply(gosocks5.Failure, nil).Write(wrapped)
		return
	}
	defer backend.Close()
	if err := gosocks5.NewReply(gosocks5.Succeeded, nil).Write(wrapped); err != nil {
		return
	}
	conn.SetDeadline(time.Time{})
	_ = Transport(wrapped, backend)
}

func (f backpackForward) forward(ingress net.Conn, target netip.AddrPort, config *tls.Config) {
	outer, err := tls.DialWithDialer(&net.Dialer{Timeout: 10 * time.Second}, "tcp4", f.Server, config)
	if err != nil {
		return
	}
	defer outer.Close()
	outer.SetDeadline(time.Now().Add(10 * time.Second))
	stream := gosocks5.ClientConn(outer, &gosocks5.Config{
		Methods: []uint8{gosocks5.MethodUserPass},
		MethodSelected: func(method uint8, conn net.Conn) (net.Conn, error) {
			if method != gosocks5.MethodUserPass {
				return nil, gosocks5.ErrBadMethod
			}
			return conn, clientSocksAuth(conn, "backpack", f.Password)
		},
	})
	request := gosocks5.NewRequest(gosocks5.CmdConnect, &gosocks5.Addr{
		Type: gosocks5.AddrIPv4, Host: target.Addr().String(), Port: target.Port(),
	})
	if err := request.Write(stream); err != nil {
		return
	}
	reply, err := gosocks5.ReadReply(stream)
	if err != nil || reply.Rep != gosocks5.Succeeded {
		return
	}
	outer.SetDeadline(time.Time{})
	_ = Transport(ingress, stream)
}
