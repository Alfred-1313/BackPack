package mtproxy

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/backpack/backpack/internal/app"
	"github.com/backpack/backpack/internal/manage/core"
	"golang.org/x/sys/unix"
)

const serviceDir = app.ConfigDir + "/mtproxy"

var errDestinationBusy = errors.New("MTProxy destination port is in use")

func Command(ctx context.Context, args []string, in io.Reader, out io.Writer) error {
	if os.Geteuid() != 0 {
		return errors.New("run MTProxy setup with sudo")
	}
	switch {
	case len(args) == 1 && args[0] == "join":
		scan := bufio.NewScanner(in)
		scan.Buffer(make([]byte, 1024), 2048)
		if !scan.Scan() {
			return errors.New("provide the MTProxy setup command from the panel")
		}
		raw, err := base64.RawURLEncoding.DecodeString(scan.Text())
		if err != nil {
			return errors.New("invalid MTProxy setup token")
		}
		var setup Setup
		if err := json.Unmarshal(raw, &setup); err != nil {
			return errors.New("invalid MTProxy setup token")
		}
		if err := setup.Validate(); err != nil {
			return err
		}
		return setup.join(ctx, out)
	case len(args) >= 2 && len(args) <= 3 && args[0] == "port":
		check := PortCheck{Bind: args[1]}
		if len(args) == 3 {
			port, err := strconv.Atoi(args[2])
			if err != nil || port == 0 {
				return errors.New("enter a port from 1 to 65535, or omit it for a random port")
			}
			check.Port = port
		}
		status, err := check.Check()
		if err != nil {
			return err
		}
		fmt.Fprintln(out, check.ResultLine(status))
		return nil
	case len(args) == 2 && args[0] == "remove":
		if err := Remove(args[1]); err != nil {
			return err
		}
		fmt.Fprintln(out, "MTProxy instance removed")
		return nil
	default:
		return errors.New("use backpack mtproxy join, backpack mtproxy port <bind> [port], or backpack mtproxy remove <id>")
	}
}

func (s Setup) join(ctx context.Context, out io.Writer) error {
	rule := strings.Repeat("\u2500", 44)
	mode := "MTProto Fake TLS"
	if s.Mode == ModeWeb {
		mode = "MTProto WEB"
	}
	fmt.Fprintf(out, "\n BackPack \u00b7 MTProxy setup\n %s\n", rule)
	fmt.Fprintf(out, "   Mode          %s\n   Destination   %s\n", mode, s.Addr())
	fmt.Fprintln(out, " \u25cc Checking the destination port and starting the proxy\u2026")
	err := withServiceLock(func() error { return s.enable(ctx) })
	if errors.Is(err, errDestinationBusy) {
		return fmt.Errorf(" \u2717 Port %d (TCP) is already in use on Kharej.\n     Choose another Kharej port in the panel and copy its new setup command.\n %s", s.Port, rule)
	}
	if err != nil {
		return fmt.Errorf(" \u2717 MTProxy could not start: %w\n %s", err, rule)
	}
	fmt.Fprintf(out, " \u2713 MTProxy authenticated and is listening on %s.\n %s\n", s.Addr(), rule)
	return nil
}

func withServiceLock(work func() error) error {
	if err := os.MkdirAll(serviceDir, 0700); err != nil {
		return err
	}
	lock, err := os.OpenFile(filepath.Join(serviceDir, ".lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		return fmt.Errorf("MTProxy setup is busy: %w", err)
	}
	return work()
}

const unitBody = `[Unit]
Description=BackPack MTProxy (Fake TLS)
After=network-online.target

[Service]
Type=exec
DynamicUser=yes
NoNewPrivileges=yes
AmbientCapabilities=CAP_NET_BIND_SERVICE
LoadCredential=proxy.toml:%s
StateDirectory=%s
StateDirectoryMode=0700
WorkingDirectory=%%S/%s
ExecStart=%s %s%%d/proxy.toml
Restart=on-failure
RestartSec=3

[Install]
WantedBy=multi-user.target
`

func serviceFiles(id string) (unit, config string) {
	return "backpack-mtproxy-" + id + ".service", filepath.Join(serviceDir, id+".toml")
}

func (s Setup) enable(ctx context.Context) error {
	unit, conf := serviceFiles(s.ID())
	body := []byte(s.Config())
	unitPath := filepath.Join(app.ServiceDir, unit)
	previous, err := os.ReadFile(conf)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	exists := err == nil
	oldUnit, err := os.ReadFile(unitPath)
	if err != nil && (!os.IsNotExist(err) || exists) {
		return err
	}
	active := core.IsActive(unit)
	if active && exists && bytes.Equal(body, previous) {
		return s.waitReady(ctx)
	}
	if exists && !strings.Contains(string(previous), fmt.Sprintf("bind-to = %q", s.Addr())) &&
		!strings.Contains(string(previous), fmt.Sprintf("ip = %q\nport = %d\n", s.BindIP(), s.Port)) {
		return errors.New("this MTProxy instance already has a different destination address")
	}
	if active && !exists {
		return errors.New("MTProxy unit is already running")
	}
	addr := s.Addr()
	if !active {
		listener, err := net.Listen("tcp", addr)
		if errors.Is(err, unix.EADDRINUSE) {
			return errDestinationBusy
		}
		if err != nil {
			return fmt.Errorf("destination %s is unavailable: %w", addr, err)
		}
		listener.Close()
	}
	binary, err := ensureBinary(ctx, s.Mode)
	if err != nil {
		return err
	}
	if s.Mode == ModeWeb {
		if err := app.WriteFileAtomic(filepath.Join(webSite, "index.html"),
			[]byte("<!doctype html><meta charset=utf-8><title>Welcome</title><p>Welcome.</p>\n"), 0644); err != nil {
			return err
		}
	}
	if err := app.WriteFileAtomic(conf, body, 0600); err != nil {
		return err
	}
	wasEnabled := exists && !active && core.IsEnabled(unit)
	success := false
	defer func() {
		if success {
			return
		}
		if !exists {
			_ = remove(s.ID())
			return
		}
		if !active {
			if wasEnabled {
				_ = core.StopService(unit)
			} else {
				_ = core.DisableService(unit)
			}
		}
		_ = app.WriteFileAtomic(conf, previous, 0600)
		_ = app.WriteFileAtomic(unitPath, oldUnit, 0644)
		_ = core.DaemonReload()
		if active {
			_ = core.RestartService(unit)
		}
	}()
	arg := ""
	if s.Mode == "" {
		arg = "run "
	}
	state := "backpack-mtproxy-" + s.ID()
	if err := app.WriteFileAtomic(unitPath, fmt.Appendf(nil, unitBody, conf, state, state, binary, arg), 0644); err != nil {
		return err
	}
	if err := core.DaemonReload(); err != nil {
		return err
	}
	start := core.StartService
	if active {
		start = core.RestartService
	}
	if err := start(unit); err != nil {
		return fmt.Errorf("start %s: %w", unit, err)
	}
	if err := s.waitReady(ctx); err != nil {
		return err
	}
	success = true
	return nil
}

func (s Setup) waitReady(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	for {
		probeCtx, stop := context.WithTimeout(ctx, 3*time.Second)
		err := s.CheckListener(probeCtx, s.Addr())
		stop()
		if err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("MTProxy on %s did not authenticate: %w", s.Addr(), err)
		case <-tick.C:
		}
	}
}

func Remove(id string) error {
	if _, err := hex.DecodeString(id); err != nil || len(id) != 16 {
		return errors.New("invalid MTProxy instance ID")
	}
	return withServiceLock(func() error { return remove(id) })
}

func remove(id string) error {
	unit, conf := serviceFiles(id)
	unitPath := filepath.Join(app.ServiceDir, unit)
	if _, err := os.Stat(unitPath); err == nil {
		if err := core.DisableService(unit); err != nil {
			return err
		}
		_, _ = core.Systemctl("clean", "--what=state", unit)
		if err := os.Remove(unitPath); err != nil {
			return err
		}
		if err := core.DaemonReload(); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.Remove(conf); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
