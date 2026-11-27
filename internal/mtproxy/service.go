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
	"time"

	"github.com/backpack/backpack/internal/app"
	"github.com/backpack/backpack/internal/manage/core"
	"golang.org/x/sys/unix"
)

const serviceDir = app.ConfigDir + "/mtproxy"

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
		if err := withServiceLock(func() error { return setup.enable(ctx) }); err != nil {
			return err
		}
		fmt.Fprintf(out, "MTProxy Fake TLS is listening on 127.0.0.1:%d\n", setup.Port)
		return nil
	case len(args) == 2 && args[0] == "remove":
		id := args[1]
		if _, err := hex.DecodeString(id); err != nil || len(id) != 16 {
			return errors.New("invalid MTProxy instance ID")
		}
		if err := withServiceLock(func() error { return remove(id) }); err != nil {
			return err
		}
		fmt.Fprintln(out, "MTProxy instance removed")
		return nil
	default:
		return errors.New("use backpack mtproxy join or backpack mtproxy remove <id>")
	}
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
ExecStart=%s run %%d/proxy.toml
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
	previous, err := os.ReadFile(conf)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	exists := err == nil
	if exists && !bytes.Equal(body, previous) {
		return errors.New("this MTProxy instance already has a different destination port")
	}
	if core.IsActive(unit) {
		if exists {
			return nil
		}
		return errors.New("MTProxy unit is already running")
	}
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(s.Port))
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("destination port %d is unavailable: %w", s.Port, err)
	}
	listener.Close()
	binary, err := ensureBinary(ctx)
	if err != nil {
		return err
	}
	if err := app.WriteFileAtomic(conf, body, 0600); err != nil {
		return err
	}
	success := false
	defer func() {
		if !success && !exists {
			_ = remove(s.ID())
		}
	}()
	if err := app.WriteFileAtomic(filepath.Join(app.ServiceDir, unit), fmt.Appendf(nil, unitBody, conf, binary), 0644); err != nil {
		return err
	}
	if err := core.DaemonReload(); err != nil {
		return err
	}
	if err := core.StartService(unit); err != nil {
		return fmt.Errorf("start %s: %w", unit, err)
	}
	deadline, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	tick := time.NewTicker(150 * time.Millisecond)
	defer tick.Stop()
	for {
		connection, err := (&net.Dialer{Timeout: 150 * time.Millisecond}).DialContext(deadline, "tcp", addr)
		if err == nil {
			connection.Close()
			success = true
			return nil
		}
		select {
		case <-deadline.Done():
			return fmt.Errorf("%s did not open its loopback port", unit)
		case <-tick.C:
		}
	}
}

func remove(id string) error {
	unit, conf := serviceFiles(id)
	unitPath := filepath.Join(app.ServiceDir, unit)
	if _, err := os.Stat(unitPath); err == nil {
		if err := core.DisableService(unit); err != nil {
			return err
		}
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
