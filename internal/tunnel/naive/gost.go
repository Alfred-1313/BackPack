package naive

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"

	"github.com/backpack/backpack/config"
	"github.com/sirupsen/logrus"
)

// ValidateGost checks the executable, TLS material and the reverse target
// before the engine starts or replaces a running generation.
func ValidateGost(c *config.Config) error {
	gServer, gClient := c.Server.Gost.Enabled(), c.Client.Gost.Enabled()
	if !gServer && !gClient {
		return nil
	}
	if runtime.GOOS != "linux" {
		return errors.New("managed GOST requires Linux")
	}
	if c.Server.Naive.Enabled() || c.Client.Naive.Enabled() || c.Server.Xray.Enabled() || c.Client.Xray.Enabled() {
		return errors.New("GOST requires a tunnel with no other managed helper")
	}
	server, client := c.Server.BindAddr != "", c.Client.RemoteAddr != ""
	if c.Direct.Present() || c.L3.Enabled() || server == client || gServer != server || gClient != client {
		return errors.New("GOST requires exactly one reverse role and helper settings for that role")
	}
	if server {
		return validateGostServer(&c.Server)
	}
	return validateGostClient(&c.Client)
}

func validateGostServer(c *config.ServerConfig) error {
	g := c.Gost
	if c.Transport != config.TCP || len(c.FallbackTransports) != 0 || c.ForwardsUDP() {
		return errors.New("GOST requires reverse TCP forwarding without transport fallbacks")
	}
	_, target, err := hostPort(c.BindAddr, true)
	if err != nil {
		return fmt.Errorf("server.bind_addr: %w", err)
	}
	_, listen, err := hostPort(g.Listen, false)
	if err != nil {
		return fmt.Errorf("server.gost.listen: %w", err)
	}
	if target == listen {
		return errors.New("the public GOST port must differ from the loopback reverse port")
	}
	if err := gostCredentials(g.Binary, g.Username, g.Password); err != nil {
		return err
	}
	for _, path := range []string{g.Certificate, g.Key} {
		if err := readableFile(path); err != nil {
			return err
		}
	}
	_, _, err = validatedTLSMaterial(g.Certificate, g.Key, "", "GOST")
	return err
}

func validateGostClient(c *config.ClientConfig) error {
	g := c.Gost
	if c.Transport != config.TCP || len(c.FallbackTransports) != 0 || c.Proxy != "" ||
		c.LocalAddr != "" || c.Interface != "" || c.SOMark != 0 || len(c.FallbackAddrs) != 0 || c.HealthFailover || c.LoadBalance {
		return errors.New("GOST requires reverse TCP with a single endpoint and no other outbound settings")
	}
	if _, _, err := hostPort(c.RemoteAddr, true); err != nil {
		return fmt.Errorf("client.remote_addr: %w", err)
	}
	if _, _, err := hostPort(g.Server, false); err != nil {
		return fmt.Errorf("client.gost.server: %w", err)
	}
	if err := gostCredentials(g.Binary, g.Username, g.Password); err != nil {
		return err
	}
	if g.CAFile != "" {
		if err := readableFile(g.CAFile); err != nil {
			return fmt.Errorf("client.gost.ca_file: %w", err)
		}
		body, err := os.ReadFile(g.CAFile)
		if err != nil {
			return err
		}
		if !x509.NewCertPool().AppendCertsFromPEM(body) {
			return errors.New("client.gost.ca_file contains no PEM certificates")
		}
	}
	return nil
}

func gostCredentials(binary, user, password string) error {
	if err := credentials(binary, user, password); err != nil {
		return err
	}
	if len(user) > 255 || len(password) > 255 {
		return errors.New("GOST Relay allows up to 255 bytes per credential")
	}
	info, err := os.Stat(binary)
	if err != nil {
		return err
	}
	if info.Mode()&0111 == 0 {
		return errors.New("the GOST helper binary must be executable")
	}
	return nil
}

func gostServerJSON(c *config.ServerConfig, dir string) ([]byte, error) {
	g := c.Gost
	certificate, key, err := validatedTLSMaterial(g.Certificate, g.Key, "", "GOST")
	if err != nil {
		return nil, err
	}
	certFile, keyFile := filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	if err := os.WriteFile(certFile, certificate, 0600); err != nil {
		return nil, err
	}
	if err := os.WriteFile(keyFile, key, 0600); err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{
		"log": map[string]any{"level": "warn"},
		"services": []any{map[string]any{
			"name": "reverse", "addr": g.Listen,
			"listener": map[string]any{"type": "tls", "tls": map[string]any{
				"certFile": certFile, "keyFile": keyFile, "options": map[string]any{"minVersion": "1.2"},
			}},
			"handler": map[string]any{"type": "relay",
				"auth":     map[string]string{"username": g.Username, "password": g.Password},
				"metadata": map[string]any{"nodelay": true, "bind": false}},
			"forwarder": map[string]any{"nodes": []any{map[string]string{
				"name": "backpack", "addr": c.BindAddr,
			}}},
		}},
	})
}

func gostClientJSON(g config.GostClientConfig, addr, dir string) ([]byte, error) {
	tls := map[string]any{"secure": true, "options": map[string]any{"minVersion": "1.2"}}
	if g.CAFile != "" {
		body, err := os.ReadFile(g.CAFile)
		if err != nil {
			return nil, err
		}
		file := filepath.Join(dir, "ca.pem")
		if err := os.WriteFile(file, body, 0600); err != nil {
			return nil, err
		}
		tls["caFile"] = file
	}
	return json.Marshal(map[string]any{
		"log": map[string]any{"level": "warn"},
		"services": []any{map[string]any{
			"name": "local", "addr": addr, "listener": map[string]string{"type": "tcp"},
			"handler": map[string]any{"type": "socks5", "chain": "reverse"},
		}},
		"chains": []any{map[string]any{"name": "reverse", "hops": []any{map[string]any{
			"name": "iran", "nodes": []any{map[string]any{
				"name": "relay", "addr": g.Server,
				"dialer": map[string]any{"type": "tls", "tls": tls},
				"connector": map[string]any{"type": "relay",
					"auth":     map[string]string{"username": g.Username, "password": g.Password},
					"metadata": map[string]any{"nodelay": true}},
			}},
		}}}},
	})
}

func StartGostServer(ctx context.Context, c *config.ServerConfig, log *logrus.Logger) (*Helper, error) {
	if err := validateGostServer(c); err != nil {
		return nil, err
	}
	return startGost(ctx, c.Gost.Binary, "gost-server", c.Gost.Listen, log, func(addr, dir string) ([]byte, error) {
		return gostServerJSON(c, dir)
	})
}

func StartGostClient(ctx context.Context, c *config.ClientConfig, log *logrus.Logger) (*Helper, error) {
	if err := validateGostClient(c); err != nil {
		return nil, err
	}
	return startGost(ctx, c.Gost.Binary, "gost-client", "127.0.0.1:0", log, func(addr, dir string) ([]byte, error) {
		return gostClientJSON(c.Gost, addr, dir)
	})
}

func startGost(ctx context.Context, binary, mode, addr string, log *logrus.Logger, build func(addr, dir string) ([]byte, error)) (*Helper, error) {
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("reserve GOST listener: %w", err)
	}
	defer listener.Close()
	// An IPv4 wildcard reservation can report an IPv6 wildcard on
	// dual-stack Linux. Keep the configured host for the readiness probe.
	host, _, _ := net.SplitHostPort(addr)
	_, port, _ := net.SplitHostPort(listener.Addr().String())
	addr = net.JoinHostPort(host, port)
	return startManagedWithConfig(ctx, binary, mode, addr, os.Environ(), log, listener.Close, func(dir string) ([]byte, error) {
		return build(addr, dir)
	})
}
