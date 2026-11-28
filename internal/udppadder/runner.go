package udppadder

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

const (
	Label = "UDP Padder"
	Dir   = "/usr/local/lib/backpack-udp-padder"
)

var helpers = []struct {
	name, hash string
	options    []string
}{
	{"udp_padder2", "599a814f88081a344845a219ba9973caf61e9e991bcf61b66b1b987fc7541363", []string{"-t", "1505", "-n", "8", "-q", "42", "-m", "0x42", "-r"}},
	{"udp_depadder2", "9f5f3de6e0d62892a6b3d44e5aecf56d8747c95d3c8de1744fecdd359f22de23", []string{"-n", "8", "-q", "50", "-m", "0x43", "-r"}},
}

var tableNames = map[string]bool{"udp_padder": true, "udp_depadder": true, "udp_padder_raw": true, "udp_depadder_raw": true}

type table struct{ Family, Name string }

func BinaryStatus() error {
	for _, helper := range helpers {
		path := filepath.Join(Dir, helper.name)
		fi, err := os.Lstat(path)
		if err != nil {
			return fmt.Errorf("%s: %w", helper.name, err)
		}
		owner, ok := fi.Sys().(*syscall.Stat_t)
		if !fi.Mode().IsRegular() || fi.Mode().Perm()&0o111 == 0 || fi.Mode().Perm()&0o022 != 0 || !ok || owner.Uid != 0 {
			return fmt.Errorf("%s must be a root-owned executable", helper.name)
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if fmt.Sprintf("%x", sha256.Sum256(body)) != helper.hash {
			return fmt.Errorf("%s does not match the supplied UDP Padder binary", helper.name)
		}
	}
	return nil
}

func listTables(ctx context.Context) ([]table, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "nft", "-j", "list", "tables").Output()
	if err != nil {
		return nil, fmt.Errorf("UDP Padder needs access to nftables: %w", err)
	}
	var got struct{ NFTables []struct{ Table table } }
	if err := json.Unmarshal(out, &got); err != nil {
		return nil, err
	}
	var tables []table
	for _, item := range got.NFTables {
		if tableNames[item.Table.Name] && (item.Table.Family == "ip" || item.Table.Family == "ip6") {
			tables = append(tables, item.Table)
		}
	}
	return tables, nil
}

// Available rejects a second instance because the supplied binaries use fixed
// firewall table names and NFQUEUE numbers on this network namespace.
func Available(ctx context.Context) error {
	if err := BinaryStatus(); err != nil {
		return err
	}
	tables, err := listTables(ctx)
	if err != nil {
		return err
	}
	if len(tables) > 0 {
		return fmt.Errorf("UDP Padder is already using nftables on this server")
	}
	return nil
}

func interfaceFor(ctx context.Context, host string, dial bool) (string, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return "", err
	}
	var candidates []string
	for _, i := range ifaces {
		if i.Flags&net.FlagUp == 0 || i.Flags&net.FlagLoopback != 0 {
			continue
		}
		if !validInterface(i.Name) {
			continue
		}
		if !dial {
			addrs, _ := i.Addrs()
			for _, a := range addrs {
				address, _, _ := strings.Cut(a.String(), "/")
				if address == host {
					return i.Name, nil
				}
			}
		}
		candidates = append(candidates, i.Name)
	}
	args := []string{"-j", "route", "show", "default"}
	if dial {
		ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil || len(ips) == 0 {
			return "", fmt.Errorf("could not resolve UDP Padder peer %s", host)
		}
		ip := ips[0].IP
		args = []string{"-j", "route", "get", ip.String()}
		if ip.To4() == nil {
			args = append([]string{"-6"}, args...)
		}
	} else if ip := net.ParseIP(host); ip != nil && ip.To4() == nil {
		args = append([]string{"-6"}, args...)
	}
	var routes []struct{ Dev string }
	if data, err := exec.CommandContext(ctx, "ip", args...).Output(); err == nil &&
		json.Unmarshal(data, &routes) == nil {
		for _, r := range routes {
			if validInterface(r.Dev) && r.Dev != "lo" {
				return r.Dev, nil
			}
		}
	}
	if len(candidates) == 1 {
		return candidates[0], nil
	}
	return "", fmt.Errorf("could not determine the UDP Padder network interface")
}

func validInterface(name string) bool {
	return name != "" && len(name) < 16 && strings.IndexFunc(name, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-' || r == '.')
	}) < 0
}

type worker struct {
	cmd   *exec.Cmd
	ready chan struct{}
	done  chan struct{}
	err   error
}

type Pair struct {
	lock    *os.File
	workers []*worker
	closed  sync.Once
}

func (p *Pair) Stop() {
	p.closed.Do(func() {
		defer p.lock.Close()
		for _, w := range p.workers {
			_ = w.cmd.Process.Signal(syscall.SIGTERM)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		for _, w := range p.workers {
			select {
			case <-w.done:
			case <-ctx.Done():
				_ = w.cmd.Process.Kill()
				<-w.done
			}
		}
		if len(p.workers) == 0 {
			return
		}
		cleanup, done := context.WithTimeout(context.Background(), 3*time.Second)
		defer done()
		tables, err := listTables(cleanup)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return
		}
		for _, table := range tables {
			if out, err := exec.CommandContext(cleanup, "nft", "delete", "table", table.Family, table.Name).CombinedOutput(); err != nil {
				fmt.Fprintf(os.Stderr, "UDP Padder cleanup: %v: %s\n", err, out)
			}
		}
	})
}

// Wait returns when a worker is lost or its tunnel has stopped.
func (p *Pair) Wait(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return nil
	case <-p.workers[0].done:
		return fmt.Errorf("UDP padder stopped: %v", p.workers[0].err)
	case <-p.workers[1].done:
		return fmt.Errorf("UDP depadder stopped: %v", p.workers[1].err)
	}
}

func Start(ctx context.Context, endpoint string, dial bool) (_ *Pair, err error) {
	host, rawPort, err := net.SplitHostPort(endpoint)
	if err != nil {
		return nil, err
	}
	port, err := strconv.Atoi(rawPort)
	if err != nil || port < 1 || port > 65535 {
		return nil, fmt.Errorf("UDP Padder needs a valid tunnel port")
	}
	if err := os.MkdirAll("/run/backpack", 0700); err != nil {
		return nil, err
	}
	ns, err := os.Stat("/proc/self/ns/net")
	if err != nil {
		return nil, err
	}
	path := fmt.Sprintf("/run/backpack/udp-padder-%d.lock", ns.Sys().(*syscall.Stat_t).Ino)
	lock, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|unix.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, err
	}
	p := &Pair{lock: lock}
	defer func() {
		if err != nil {
			p.Stop()
		}
	}()
	if err := unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		return nil, fmt.Errorf("another UDP Padder is active on this server: %w", err)
	}
	if err := Available(ctx); err != nil {
		return nil, err
	}
	startup, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	iface, err := interfaceFor(startup, host, dial)
	if err != nil {
		return nil, err
	}
	if dial {
		out, err := exec.CommandContext(startup, "ss", "-Huan", "sport", "=", ":"+rawPort).Output()
		if err != nil {
			return nil, fmt.Errorf("could not check UDP Padder port %d: %w", port, err)
		}
		if len(out) > 0 {
			return nil, fmt.Errorf("UDP port %d is already used on this server", port)
		}
	}

	for _, helper := range helpers {
		args := []string{"-oL", "-eL", filepath.Join(Dir, helper.name), "-i", iface, "-p", rawPort}
		args = append(args, helper.options...)
		cmd := exec.Command("stdbuf", args...)
		cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGTERM}
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			return nil, err
		}
		cmd.Stderr = cmd.Stdout
		if err := cmd.Start(); err != nil {
			return nil, err
		}
		w := &worker{cmd: cmd, ready: make(chan struct{}), done: make(chan struct{})}
		p.workers = append(p.workers, w)
		go func() {
			s := bufio.NewScanner(stdout)
			ready, failed, installed := 0, false, false
			for s.Scan() {
				line := s.Text()
				fmt.Fprintf(os.Stderr, "%s: %s\n", helper.name, line)
				failed = failed || strings.Contains(line, "[nft] FAILED")
				installed = installed || strings.Contains(line, "[nft] Rules added.")
				if strings.Contains(line, "queue ") && strings.HasSuffix(line, " ready") {
					ready++
				}
				if !failed && installed && ready == 8 {
					close(w.ready)
					ready++
				}
			}
			w.err = cmd.Wait()
			close(w.done)
		}()
		select {
		case <-startup.Done():
			return nil, fmt.Errorf("%s did not start: %w", helper.name, startup.Err())
		case <-w.done:
			return nil, fmt.Errorf("%s exited during setup: %v", helper.name, w.err)
		case <-w.ready:
		}
	}
	return p, nil
}
