package backhaulpremium

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

// Backhaul's raw TCP mode installs one RST rule per peer. Do not run it over
// an existing rule: the official engine replaces that rule at startup.
func protectIPXTCP(peer string) (func(), error) {
	const dir = "/run/backpack"
	if err := os.MkdirAll(dir, 0750); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "backhaul-ipx-"+peer+".lock")
	lock, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|unix.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		lock.Close()
		return nil, fmt.Errorf("Backhaul IPX/TCP for %s is already in use: %w", peer, err)
	}

	rule := []string{"OUTPUT", "-p", "tcp", "-d", peer, "--tcp-flags", "RST", "RST", "-j", "DROP"}
	firewall := func(action string) ([]byte, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		return exec.CommandContext(ctx, "iptables", append([]string{"-w", "3", action}, rule...)...).CombinedOutput()
	}
	out, err := firewall("-C")
	if err == nil {
		lock.Close()
		return nil, fmt.Errorf("an existing TCP reset firewall rule already owns the IPX path to %s", peer)
	}
	var noRule *exec.ExitError
	if !errors.As(err, &noRule) || noRule.ExitCode() != 1 {
		lock.Close()
		return nil, fmt.Errorf("could not check the IPX/TCP firewall rule: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return func() {
		defer lock.Close()
		if _, err := firewall("-C"); err == nil {
			if msg, err := firewall("-D"); err != nil {
				fmt.Fprintf(os.Stderr, "Backhaul IPX/TCP rule cleanup: %v: %s\n", err, strings.TrimSpace(string(msg)))
			}
		}
	}, nil
}
