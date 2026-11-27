package matrix

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/backpack/backpack/config"
)

func writeConfig(dir, iface string, c config.MatrixConfig) error {
	hosts := filepath.Join(dir, "hosts")
	if err := os.Mkdir(hosts, 0700); err != nil {
		return err
	}
	conf := fmt.Sprintf("Name = %s\nInterface = %s\nDevice = /dev/net/tun\nMode = router\nAddressFamily = any\nBroadcast = no\nPingInterval = 10\n", c.Local.Name, iface)
	if c.Local.Name != Main {
		conf += "ConnectTo = main\n"
	} else {
		conf += "StrictSubnets = yes\n"
	}
	for _, h := range append([]config.MatrixHost{c.Local}, c.Peers...) {
		record := fmt.Sprintf("Address = %s\nPort = %d\nSubnet = %s/32\nCipher = aes-256-cbc\nDigest = sha256\nMACLength = 16\n\n%s\n",
			h.Address, h.Port, h.IP, strings.TrimSpace(h.Key))
		if err := os.WriteFile(filepath.Join(hosts, h.Name), []byte(record), 0600); err != nil {
			return err
		}
		if h.Name != c.Local.Name {
			up := "#!/bin/sh\n: > \"$BACKPACK_MATRIX_STATE/" + h.Name + "\"\n"
			down := "#!/bin/sh\nrm -f -- \"$BACKPACK_MATRIX_STATE/" + h.Name + "\"\n"
			if err := os.WriteFile(filepath.Join(hosts, h.Name+"-up"), []byte(up), 0700); err != nil {
				return err
			}
			if err := os.WriteFile(filepath.Join(hosts, h.Name+"-down"), []byte(down), 0700); err != nil {
				return err
			}
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "rsa_key.priv"), []byte(c.PrivateKey), 0600); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "tinc.conf"), []byte(conf), 0600)
}

// Run supervises tincd in the foreground. It owns only the dedicated
// interface and private config directory for this tunnel generation.
func Run(ctx context.Context, name string, c config.MatrixConfig, log io.Writer) error {
	if err := Validate(c); err != nil {
		return err
	}
	path, err := Binary()
	if err != nil {
		return err
	}
	iface := Iface(name)
	if _, err := net.InterfaceByName(iface); err == nil {
		return fmt.Errorf("Matrix interface %s is already in use", iface)
	}
	dir, err := os.MkdirTemp("", "backpack-matrix-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	if err := writeConfig(dir, iface, c); err != nil {
		return err
	}
	state := statePath(name)
	// The supervisor owns its hash-named, root-only runtime directory.
	if err := os.RemoveAll(state); err != nil {
		return err
	}
	if err := os.MkdirAll(state, 0700); err != nil {
		return err
	}
	defer os.RemoveAll(state)
	child, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(child, path, "-D", "-d1", "-c", dir, "--pidfile="+filepath.Join(dir, "tinc.pid"))
	cmd.Env = append(os.Environ(), "BACKPACK_MATRIX_STATE="+state)
	cmd.Stdout, cmd.Stderr = log, log
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = 3 * time.Second
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan struct{})
	var exitErr error
	go func() { exitErr = cmd.Wait(); close(done) }()
	defer func() { cancel(); <-done }()
	start, stop := context.WithTimeout(ctx, 12*time.Second)
	defer stop()
	poll := time.NewTicker(100 * time.Millisecond)
	defer poll.Stop()
	for {
		if _, err := net.InterfaceByName(iface); err == nil {
			break
		}
		select {
		case <-ctx.Done():
			return nil
		case <-done:
			return fmt.Errorf("tincd stopped before creating its interface: %v", exitErr)
		case <-start.Done():
			return errors.New("tincd did not create its network interface")
		case <-poll.C:
		}
	}
	mtu := c.MTU
	if mtu == 0 {
		mtu = 1350
	}
	queue := c.TxQueueLen
	if queue == 0 {
		queue = 1000
	}
	for _, args := range [][]string{
		{"link", "set", "dev", iface, "mtu", strconv.Itoa(mtu), "txqueuelen", strconv.Itoa(queue), "up"},
		{"addr", "add", c.Local.IP + "/24", "dev", iface},
	} {
		if out, err := exec.CommandContext(start, "ip", args...).CombinedOutput(); err != nil {
			return fmt.Errorf("configure Matrix interface: %w: %s", err, strings.TrimSpace(string(out)))
		}
	}
	select {
	case <-ctx.Done():
		return nil
	case <-done:
		return fmt.Errorf("tincd stopped: %v", exitErr)
	}
}

func statePath(name string) string { return filepath.Join("/run/backpack/matrix", Iface(name)) }

// Online reports Tinc's authenticated reachability, including paths where
// ICMP to the private address is filtered by the remote host firewall.
func Online(name, peer string) bool {
	if !nodePattern.MatchString(peer) {
		return false
	}
	if _, err := net.InterfaceByName(Iface(name)); err != nil {
		return false
	}
	_, err := os.Stat(filepath.Join(statePath(name), peer))
	return err == nil
}

func Bytes(name, direction string) uint64 {
	out, _ := os.ReadFile(filepath.Join("/sys/class/net", Iface(name), "statistics", direction+"_bytes"))
	n, _ := strconv.ParseUint(strings.TrimSpace(string(out)), 10, 64)
	return n
}
