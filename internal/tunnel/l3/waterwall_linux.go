package l3

import (
	"context"
	"fmt"
	"math/rand/v2"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/backpack/backpack/internal/metrics"
	"github.com/backpack/backpack/internal/waterwall"
	"golang.org/x/sys/unix"
)

// Probe traffic has its own address (the tunnel IP) and never hits the public
// interface. A TCP handshake confirms the configured outer protocol carried it.
const waterWallProbePort = "49318"

// Reserve one DSCP from the experimental/local-use pool for protocol 17.
// The old WaterWall core swaps the protocol byte but keeps the TCP header.
// Marking only that core's raw socket keeps ordinary UDP out of its queue.
const waterWallDSCP = "0x2f"

func (t *Tunnel) runWaterWall(ctx context.Context) error {
	c := t.cfg
	path, version, err := waterwall.Binary(c.Mode == ModeDial, c.WaterWallProtocol)
	if err != nil {
		return fmt.Errorf("WaterWall core: %w", err)
	}
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
	if linkExists(c.Iface) {
		return fmt.Errorf("WaterWall interface %s is already in use", c.Iface)
	}

	// The core captures by peer and IP protocol, so two instances with the
	// same filter would race for the exact same packets.
	lockName := fmt.Sprintf("backpack-waterwall-%s-%d.lock", remote.String(), c.WaterWallProtocol)
	lock, err := os.OpenFile(filepath.Join("/run", lockName), os.O_CREATE|os.O_RDWR|unix.O_NOFOLLOW, 0600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		return fmt.Errorf("WaterWall already uses IP protocol %d with peer %s: %w", c.WaterWallProtocol, remote, err)
	}

	dir, err := os.MkdirTemp("", "backpack-waterwall-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	mtu := c.MTU
	if mtu == 0 {
		mtu = waterwall.DefaultMTU
	}
	if pathMTU := egressMTU(local); pathMTU > 0 && pathMTU < mtu {
		mtu = pathMTU
	}
	var testPort int
	var packetMark uint32
	var legacyEndpoint string
	if c.WaterWallProtocol == waterwall.TestProtocol {
		packetMark = (rand.Uint32() & 0x3fffffff) | 0x40000000
		legacyEndpoint = local.String()
		if value := os.Getenv("BACKPACK_WATERWALL_TEST_PORT"); value != "" {
			testPort, err = strconv.Atoi(value)
			if err != nil || testPort < 1024 || testPort > 65535 {
				return fmt.Errorf("invalid WaterWall Connection Test port")
			}
		}
	}
	firewallEnv, cleanup, err := waterWallFirewall(dir, testPort, legacyEndpoint)
	if err != nil {
		return err
	}
	defer cleanup()
	check := exec.CommandContext(ctx, filepath.Join(dir, "iptables"), "--version")
	check.Env = append(os.Environ(), firewallEnv...)
	if err := check.Run(); err != nil {
		return fmt.Errorf("WaterWall firewall wrapper is unavailable: %w", err)
	}
	if packetMark != 0 {
		match := []string{"-p", "17", "-s", local.String(), "-d", remote.String(),
			"-m", "mark", "--mark", fmt.Sprintf("%d", packetMark)}
		for _, rule := range []struct {
			table  string
			target []string
		}{
			{"mangle", []string{"DSCP", "--set-dscp", waterWallDSCP}},
			{"raw", []string{"CT", "--notrack"}},
		} {
			args := append([]string{"-t", rule.table, "-I", "OUTPUT"}, match...)
			args = append(append(args, "-j"), rule.target...)
			cmd := exec.CommandContext(ctx, filepath.Join(dir, "iptables"), args...)
			cmd.Env = append(os.Environ(), firewallEnv...)
			if output, err := cmd.CombinedOutput(); err != nil {
				return fmt.Errorf("set up WaterWall protocol 17 firewall: %w: %s", err, strings.TrimSpace(string(output)))
			}
		}
	}
	settings := waterwall.RuntimeConfig{
		Interface: c.Iface, LocalCIDR: c.LocalIP, PeerIP: hostPart(c.PeerIP),
		LocalEndpoint: local.String(), RemoteEndpoint: remote.String(),
		Protocol: c.WaterWallProtocol, Workers: c.WaterWallWorkers, MTU: mtu, SocketMark: packetMark,
	}
	if err := settings.Write(dir); err != nil {
		return err
	}

	child, cancel := context.WithCancel(ctx)
	// TunDevice sets the host-wide rp_filter by default. A private mount
	// namespace keeps /proc/sys read-only for the child and its commands.
	script := `mount --bind /proc/sys /proc/sys
mount -o remount,bind,ro /proc/sys
exec "$1"`
	cmd := exec.CommandContext(child, "unshare", "--mount", "--propagation", "private",
		"sh", "-ec", script, "waterwall", path)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), firewallEnv...)
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGTERM}
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = 4 * time.Second
	if err := cmd.Start(); err != nil {
		cancel()
		return fmt.Errorf("start WaterWall core: %w", err)
	}
	ended := make(chan struct{})
	var exitErr error
	go func() {
		exitErr = cmd.Wait()
		close(ended)
	}()
	defer func() { cancel(); <-ended }()
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	for !linkExists(c.Iface) {
		select {
		case <-ctx.Done():
			return nil
		case <-ended:
			return fmt.Errorf("WaterWall exited before creating %s: %v", c.Iface, exitErr)
		case <-deadline.C:
			return fmt.Errorf("WaterWall did not create %s", c.Iface)
		case <-time.After(100 * time.Millisecond):
		}
	}

	t.gre.iface.Store(&c.Iface)
	t.setCurrentMTU(mtu)
	defer func() {
		last, _ := readGRECounters(c.Iface)
		t.gre.retire(last)
		metrics.ClearPeer()
	}()
	inside := installIfaceAccept(c.Iface, t.log)
	defer inside.remove()
	if testPort != 0 {
		select {
		case <-ctx.Done():
			return nil
		case <-ended:
			return fmt.Errorf("WaterWall test core exited: %v", exitErr)
		}
	}
	probe, err := (&net.ListenConfig{}).Listen(ctx, "tcp4", net.JoinHostPort(hostPart(c.LocalIP), waterWallProbePort))
	if err == nil {
		var wg sync.WaitGroup
		wg.Go(func() {
			for {
				conn, err := probe.Accept()
				if err != nil {
					return
				}
				conn.Close()
			}
		})
		defer func() { probe.Close(); wg.Wait() }()
	} else {
		t.log.Warnf("WaterWall could not listen for peer checks: %v", err)
	}
	t.log.Infof("WaterWall %s up on %s with IP protocol %d and MTU %d", version, c.Iface, c.WaterWallProtocol, mtu)
	target := net.JoinHostPort(hostPart(c.PeerIP), waterWallProbePort)
	dial := net.Dialer{Timeout: 1200 * time.Millisecond, LocalAddr: &net.TCPAddr{IP: net.ParseIP(hostPart(c.LocalIP))}}
	tick := time.NewTicker(3 * time.Second)
	defer tick.Stop()
	for {
		conn, err := dial.DialContext(ctx, "tcp4", target)
		if err == nil {
			conn.Close()
			t.gre.lastAnswer.Store(time.Now().UnixNano())
			metrics.ReportPeer(remote.String())
		} else if !t.gre.up(time.Now()) {
			metrics.ClearPeer()
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ended:
			return fmt.Errorf("WaterWall core exited: %v", exitErr)
		case <-tick.C:
		}
	}
}

// Record only rules installed by this child so BackPack can remove them if
// WaterWall exits without cleanup. Limit protocol 17's older capture rule to
// this endpoint's marked traffic, and the echo port when used in a test.
func waterWallFirewall(dir string, testPort int, legacyEndpoint string) ([]string, func(), error) {
	iptables, err := exec.LookPath("iptables")
	if err != nil {
		return nil, nil, err
	}
	filter := ""
	if testPort != 0 {
		// IPv4 fragment offset, header length, then source or destination port.
		filter = fmt.Sprintf("9,40 0 0 6,69 6 0 8191,177 0 0 0,72 0 0 0,21 2 0 %d,72 0 0 2,21 0 1 %d,6 0 0 4294967295,6 0 0 0", testPort, testPort)
	}
	wrapper := `#!/bin/sh
set -eu
record=
for arg in "$@"; do
	case "$arg" in
		-I|-A) record=1 ;;
		NFQUEUE)
			if [ -n "$BACKPACK_WW_LEGACY_ENDPOINT" ]; then
				[ "$#" -eq 8 ] && [ "$2" = INPUT ] && [ "$3" = -s ] &&
					[ "$5" = -j ] && [ "$6" = NFQUEUE ] && [ "$7" = --queue-num ] || exit 1
				set -- -t raw "$1" PREROUTING -p 17 -s "$4" -d "$BACKPACK_WW_LEGACY_ENDPOINT" \
					-m dscp --dscp "$BACKPACK_WW_DSCP" -j NFQUEUE --queue-num "$8" --queue-bypass
			fi
			if [ -n "$BACKPACK_WW_TEST_FILTER" ]; then
				set -- "$@" -m bpf --bytecode "$BACKPACK_WW_TEST_FILTER"
			fi
			;;
	esac
done
if [ -z "$record" ]; then
	exec "$BACKPACK_WW_IPTABLES" "$@"
fi
file=$(mktemp "$BACKPACK_WW_RULES/rule.XXXXXXXX")
printf '%s\0' "$@" > "$file"
if "$BACKPACK_WW_IPTABLES" "$@"; then
	exit 0
else
	status=$?
	rm -f -- "$file"
	exit "$status"
fi
`
	if err := os.WriteFile(filepath.Join(dir, "iptables"), []byte(wrapper), 0700); err != nil {
		return nil, nil, err
	}
	return []string{
		"PATH=" + dir + ":" + os.Getenv("PATH"),
		"BACKPACK_WW_IPTABLES=" + iptables,
		"BACKPACK_WW_TEST_FILTER=" + filter,
		"BACKPACK_WW_RULES=" + dir,
		"BACKPACK_WW_LEGACY_ENDPOINT=" + legacyEndpoint,
		"BACKPACK_WW_DSCP=" + waterWallDSCP,
	}, func() { cleanWaterWallRules(iptables, dir) }, nil
}

func cleanWaterWallRules(iptables, dir string) {
	entries, err := filepath.Glob(filepath.Join(dir, "rule.*"))
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	for _, name := range entries {
		data, err := os.ReadFile(name)
		if err != nil {
			continue
		}
		args := strings.Split(strings.TrimSuffix(string(data), "\x00"), "\x00")
		for i, arg := range args {
			if arg != "-A" && arg != "-I" {
				continue
			}
			if arg == "-I" && i+2 < len(args) {
				if _, err := strconv.Atoi(args[i+2]); err == nil {
					args = append(args[:i+2], args[i+3:]...)
				}
			}
			args[i] = "-D"
			_ = exec.CommandContext(ctx, iptables, args...).Run()
			break
		}
	}
}
