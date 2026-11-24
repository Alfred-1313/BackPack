//go:build linux

package l3

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/backpack/backpack/internal/easytier"
	"github.com/backpack/backpack/internal/tunnel/mssclamp"
)

func (t *Tunnel) runRoutedLink(ctx context.Context) error {
	switch t.cfg.Carrier {
	case CarrierL2TPv3:
		return t.runL2TPv3(ctx)
	case CarrierEasyTier:
		return t.runEasyTier(ctx)
	default:
		return t.runKernelGRE(ctx)
	}
}

func l2tpID(id uint32) string { return strconv.FormatUint(uint64(id), 10) }

func l2tpRule(iface string, remote net.IP) []string {
	return []string{"INPUT", "-p", "115", "-s", remote.String(),
		"-m", "comment", "--comment", "backpack-l2tpv3 " + iface, "-j", "ACCEPT"}
}

// removeOwnedL2TP cleans a session left after a forced exit. Matching the
// interface as well as the IDs prevents taking over another L2TP service.
func removeOwnedL2TP(c Config) {
	tunnelID, sessionID := l2tpID(c.L2TPTunnelID), l2tpID(c.L2TPSessionID)
	out, err := exec.Command("ip", "l2tp", "show", "session", "tunnel_id", tunnelID, "session_id", sessionID).Output()
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(out), "\n") {
		key, value, hasValue := strings.Cut(strings.TrimSpace(line), ":")
		if hasValue && strings.EqualFold(key, "interface name") && strings.TrimSpace(value) == c.Iface {
			_ = exec.Command("ip", "l2tp", "del", "session", "tunnel_id", tunnelID, "session_id", sessionID).Run()
			deleteEmptyL2TPTunnel(tunnelID)
			return
		}
	}
}

func deleteEmptyL2TPTunnel(id string) {
	out, err := exec.Command("ip", "l2tp", "show", "session").Output()
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 5 && fields[0] == "Session" && fields[2] == "in" && fields[3] == "tunnel" && fields[4] == id {
			return
		}
	}
	_ = exec.Command("ip", "l2tp", "del", "tunnel", "tunnel_id", id).Run()
}

func CleanupL2TP(c Config) {
	if c.Iface == "" {
		c.Iface = defaultIfaceName
	}
	removeOwnedL2TP(c)
	if ip := net.ParseIP(c.Addr); ip != nil {
		rule := l2tpRule(c.Iface, ip)
		for exec.Command("iptables", append([]string{"-D"}, rule...)...).Run() == nil {
		}
	}
}

func (t *Tunnel) runL2TPv3(ctx context.Context) error {
	c := t.cfg
	remote, err := resolveGREEndpoint(ctx, c.Addr, c.Carrier)
	if err != nil {
		return err
	}
	local := net.ParseIP(c.GRELocal)
	if local == nil {
		local, err = GRELocalToward(remote)
		if err != nil {
			return err
		}
	}
	for _, module := range []string{"l2tp_netlink", "l2tp_ip", "l2tp_eth"} {
		_ = exec.Command("modprobe", module).Run()
	}
	removeOwnedL2TP(c)
	if linkExists(c.Iface) {
		return fmt.Errorf("l3: interface %s is already in use", c.Iface)
	}
	tunnelID, sessionID := l2tpID(c.L2TPTunnelID), l2tpID(c.L2TPSessionID)
	if err := run("ip", "l2tp", "add", "tunnel", "tunnel_id", tunnelID,
		"peer_tunnel_id", l2tpID(c.L2TPPeerTunnelID), "encap", "ip", "local", local.String(), "remote", remote.String()); err != nil {
		return fmt.Errorf("l3: L2TPv3 tunnel creation failed: %w (requires the running kernel's l2tp_netlink and l2tp_ip modules)", err)
	}
	defer deleteEmptyL2TPTunnel(tunnelID)
	if err := run("ip", "l2tp", "add", "session", "tunnel_id", tunnelID, "session_id", sessionID,
		"peer_session_id", l2tpID(c.L2TPPeerSessionID), "name", c.Iface); err != nil {
		return fmt.Errorf("l3: L2TPv3 Ethernet session creation failed: %w", err)
	}
	defer exec.Command("ip", "l2tp", "del", "session", "tunnel_id", tunnelID, "session_id", sessionID).Run()
	mtu := c.MTU
	if mtu == 0 {
		mtu = egressMTU(local)
		if mtu == 0 {
			mtu = 1500
		}
		// IPv4, the L2TPv3 session ID, the default L2-specific
		// sublayer, and the inner Ethernet header.
		mtu = max(minMTU, min(maxMTU, mtu-42))
	}
	steps, err := interfaceSteps(c.Iface, c.LocalIP, c.PeerIP, mtu, c.TxQueueLen)
	if err != nil {
		return err
	}
	if err := runIPSteps(steps); err != nil {
		return err
	}
	accept := installOuterAccept("iptables", l2tpRule(c.Iface, remote), t.log)
	defer accept.remove()
	mssclamp.Apply("l3", c.Iface, mtu, c.MSSClamp, t.log)
	link := &greLink{name: c.Iface, mssClamp: c.MSSClamp, mtu: mtu, log: t.log,
		cleanup: func() {}} // the kernel removes the interface with its session
	t.log.Infof("l3: L2TPv3 up on %s, mtu %d (IP protocol 115)", c.Iface, mtu)
	return t.watchRoutedLink(ctx, link, remote.String(), "IP protocol 115", nil)
}

func (t *Tunnel) runEasyTier(ctx context.Context) error {
	path, err := easytier.Binary()
	if err != nil {
		return err
	}
	c := t.cfg
	if linkExists(c.Iface) {
		return fmt.Errorf("l3: interface %s is already in use", c.Iface)
	}
	args := []string{"--instance-name", c.Iface, "--dev-name", c.Iface, "--ipv4", c.LocalIP,
		"--network-name", c.EasyTierNetwork, "--private-mode", "true",
		"--rpc-portal", "127.0.0.1:0", "--disable-ipv6",
		"--mtu", strconv.Itoa(c.MTU), "--manual-routes", c.PeerIP + "/32"}
	peer := c.PeerIP
	if c.Mode == ModeDial {
		args = append(args, "--no-listener", "--peers", "tcp://"+c.Addr)
		if host, _, err := net.SplitHostPort(c.Addr); err == nil {
			peer = host
		}
	} else {
		args = append(args, "--listeners", "tcp://"+c.Addr)
	}
	child, cancel := context.WithCancel(ctx)
	cmd := exec.CommandContext(child, path, args...)
	cmd.Env = append(os.Environ(), "ET_NETWORK_SECRET="+c.Token)
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = 3 * time.Second
	if err := cmd.Start(); err != nil {
		cancel()
		return fmt.Errorf("l3: start EasyTier: %w", err)
	}
	ended := make(chan struct{})
	var exitErr error
	go func() {
		exitErr = cmd.Wait()
		close(ended)
	}()
	defer func() { cancel(); <-ended }()
	timeout := time.NewTimer(12 * time.Second)
	defer timeout.Stop()
	for !linkExists(c.Iface) {
		select {
		case <-ctx.Done():
			return nil
		case <-ended:
			return fmt.Errorf("l3: EasyTier exited before creating %s: %v", c.Iface, exitErr)
		case <-timeout.C:
			return fmt.Errorf("l3: EasyTier did not create %s within 12 seconds", c.Iface)
		case <-time.After(100 * time.Millisecond):
		}
	}
	mssclamp.Apply("l3", c.Iface, c.MTU, c.MSSClamp, t.log)
	link := &greLink{name: c.Iface, mssClamp: c.MSSClamp, mtu: c.MTU, log: t.log,
		cleanup: func() {}} // EasyTier owns the TUN device
	t.log.Infof("l3: EasyTier up on %s, mtu %d", c.Iface, c.MTU)
	return t.watchRoutedLink(ctx, link, peer, "EasyTier TCP", ended)
}
