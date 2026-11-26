package nativebackhaul

import (
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/backpack/backpack/config"
	"github.com/backpack/backpack/internal/backhaulpremium"
	"github.com/xtaci/smux"
)

func HasMuxOptions(mode string) bool {
	return mode == "xtcpmux" || mode == "xwsmux"
}

func BackupAddresses(values []string, primary string, port uint16) ([]string, error) {
	if len(values) > 64 {
		return nil, fmt.Errorf("enter at most 64 backup addresses")
	}
	defaultPort := strconv.Itoa(int(port))
	seen := map[string]bool{net.JoinHostPort(strings.ToLower(primary), defaultPort): true}
	var out []string
	for _, value := range values {
		host, remotePort := value, defaultPort
		if h, p, err := net.SplitHostPort(value); err == nil {
			host, remotePort = h, p
		}
		host = strings.TrimPrefix(strings.TrimSuffix(host, "]"), "[")
		n, err := strconv.ParseUint(remotePort, 10, 16)
		if !backhaulpremium.ValidEndpointHost(host) || err != nil || n == 0 {
			return nil, fmt.Errorf("invalid backup address %q", value)
		}
		endpoint := net.JoinHostPort(strings.ToLower(host), strconv.FormatUint(n, 10))
		if !seen[endpoint] {
			out = append(out, endpoint)
			seen[endpoint] = true
		}
	}
	return out, nil
}

func validateMuxOptions(c config.BackhaulConfig) error {
	o := c.Mux
	if o == nil {
		return nil
	}
	if !HasMuxOptions(c.Mode) {
		return fmt.Errorf("these options apply to XTcpMux and XWsMux")
	}
	if _, err := BackupAddresses(o.Backups, c.IranHost, c.Port); err != nil {
		return err
	}
	if o.MaxConnections < 0 || o.MaxConnections > 65535 || o.BandwidthMbps < 0 || o.BandwidthMbps > 100000 {
		return fmt.Errorf("limits must be between 0 and 65535 connections or 0 and 100000 Mbit/s")
	}
	if c.Role == "client" && (o.IPv6 != nil || o.ProxyProtocol || o.MaxConnections != 0 || o.BandwidthMbps != 0) {
		return fmt.Errorf("listener and forwarding settings belong on Iran")
	}
	mux := muxConfig(o)
	if o.HeartbeatSeconds < 0 || o.HeartbeatSeconds > 300 || o.TimeoutSeconds < 0 || o.TimeoutSeconds > 900 ||
		mux.KeepAliveTimeout <= mux.KeepAliveInterval {
		return fmt.Errorf("set heartbeat to 1–300 seconds and timeout to 1–900 seconds, with timeout longer than heartbeat; 0 uses defaults")
	}
	return nil
}

func muxConfig(o *config.BackhaulMuxOptions) *smux.Config {
	cfg := smux.DefaultConfig()
	cfg.Version = 2
	cfg.KeepAliveInterval = 5 * time.Second
	cfg.KeepAliveTimeout = 16 * time.Second
	if o != nil {
		if o.HeartbeatSeconds != 0 {
			cfg.KeepAliveInterval = time.Duration(o.HeartbeatSeconds) * time.Second
		}
		if o.TimeoutSeconds != 0 {
			cfg.KeepAliveTimeout = time.Duration(o.TimeoutSeconds) * time.Second
		}
	}
	return cfg
}
