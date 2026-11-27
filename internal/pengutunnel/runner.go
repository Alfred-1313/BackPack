package pengutunnel

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/backpack/backpack/config"
)

const Version = "v6.3.1"
const LicenseExitCode = 78

var ErrLicenseRejected = errors.New("PenguTunnel license rejected: ask PenguCloud to activate this Iran origin IP")

const licenseFile = "/etc/backpack/pengutunnel.license"
const maxCoreSize = 32 << 20

var releases = map[string]string{
	"amd64": "212ae77f492ac2bef9c2160071d8be3b5ecff55e1bff2cac98d6598655780609",
	"arm64": "779c67ca5a7576dc0c17ce55fee85d33707b2161951be20231fc4b67d747cf10",
}

var installMu sync.Mutex

func binary() string {
	return filepath.Join("/usr/local/lib/backpack/helpers/pengutunnel", Version+"-"+runtime.GOARCH, "pengutunnel")
}

func verify(path string) error {
	expected := releases[runtime.GOARCH]
	if runtime.GOOS != "linux" || expected == "" {
		return errors.New("PenguTunnel requires Linux amd64 or arm64")
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 || info.Size() > maxCoreSize {
		return errors.New("invalid PenguTunnel executable")
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	sum := sha256.New()
	if _, err := io.Copy(sum, io.LimitReader(f, maxCoreSize+1)); err != nil {
		return err
	}
	if hex.EncodeToString(sum.Sum(nil)) != expected {
		return fmt.Errorf("PenguTunnel %s checksum mismatch", Version)
	}
	return nil
}

func BinaryStatus() error { return verify(binary()) }

func Ensure(ctx context.Context) error {
	installMu.Lock()
	defer installMu.Unlock()
	dst := binary()
	if err := verify(dst); !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}
	file := "pengutunnel"
	if runtime.GOARCH == "arm64" {
		file += "-arm64"
	}
	var err error
	for _, host := range []string{"pg.pengucloud.net", "pg-ir.pengucloud.net"} {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		err = fetch(ctx, "https://"+host+":2096/"+file, dst)
		if err == nil {
			return nil
		}
	}
	return fmt.Errorf("install PenguTunnel %s: %w", Version, err)
}

func fetch(ctx context.Context, url, dst string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("publisher returned HTTP %d", resp.StatusCode)
	}
	f, err := os.CreateTemp(filepath.Dir(dst), ".pengutunnel-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, copyErr := io.Copy(f, io.LimitReader(resp.Body, maxCoreSize+1))
	if err := errors.Join(copyErr, f.Close()); err != nil {
		return err
	}
	if err := os.Chmod(f.Name(), 0755); err != nil {
		return err
	}
	if err := verify(f.Name()); err != nil {
		return err
	}
	return os.Rename(f.Name(), dst)
}

func LocalInterface(address string) (string, error) {
	local, err := netip.ParseAddr(address)
	if err != nil || !local.Is4() {
		return "", errors.New("PenguTunnel IPX needs an assigned local IPv4 address")
	}
	ifaces, err := net.Interfaces()
	if err != nil {
		return "", err
	}
	for _, iface := range ifaces {
		addrs, _ := iface.Addrs()
		for _, addr := range addrs {
			prefix, err := netip.ParsePrefix(addr.String())
			if err == nil && prefix.Addr() == local {
				return iface.Name, nil
			}
		}
	}
	return "", fmt.Errorf("PenguTunnel IPX address %s is not assigned to a local interface", address)
}

func readLicense() (string, error) {
	fi, err := os.Lstat(licenseFile)
	if os.IsNotExist(err) {
		// This is the public default in the publisher's installer. The core
		// applies its normal activation checks to this key or the local key.
		return "pengucloud", nil
	}
	if err != nil {
		return "", err
	}
	if !fi.Mode().IsRegular() || fi.Mode().Perm()&0077 != 0 || fi.Size() > 4096 {
		return "", fmt.Errorf("%s must be a private regular file", licenseFile)
	}
	raw, err := os.ReadFile(licenseFile)
	if err != nil {
		return "", err
	}
	key := strings.TrimSpace(string(raw))
	if key == "" || strings.ContainsAny(key, "\r\n\x00") {
		return "", fmt.Errorf("%s must contain one PenguTunnel license key", licenseFile)
	}
	return key, nil
}

func Run(ctx context.Context, name string, c config.PenguConfig) error {
	if err := BinaryStatus(); err != nil {
		return fmt.Errorf("PenguTunnel core: %w", err)
	}
	if err := Validate(c); err != nil {
		return err
	}
	var iface, license string
	var err error
	if c.Side == "kharej" {
		license, err = readLicense()
		if err != nil {
			return err
		}
	}
	if mode, _ := Find(c.Mode); mode.IPX {
		ip := c.IranHost
		if c.Side == "kharej" {
			ip = c.KharejHost
		}
		iface, err = LocalInterface(ip)
		if err != nil {
			return err
		}
	}
	body, err := Build(name, c, iface, license)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp("", "backpack-pengu-*.toml")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, err = f.Write(body)
	if err := errors.Join(err, f.Close()); err != nil {
		return err
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(runCtx, binary(), "-config", f.Name())
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = 4 * time.Second
	log := &redactedLog{key: c.Key, license: license, cancel: cancel}
	cmd.Stdout, cmd.Stderr = log, log
	err = cmd.Run()
	log.flush()
	if ctx.Err() != nil {
		return nil
	}
	if log.refused {
		return ErrLicenseRejected
	}
	return err
}

type redactedLog struct {
	key, license string
	line         []byte
	refused      bool
	cancel       context.CancelFunc
}

func (log *redactedLog) Write(p []byte) (int, error) {
	n := len(p)
	for len(p) > 0 {
		part, rest, eol := bytes.Cut(p, []byte("\n"))
		if len(log.line) <= 8192 {
			log.line = append(log.line, part[:min(len(part), 8193-len(log.line))]...)
		}
		if eol {
			log.flush()
		}
		p = rest
	}
	return n, nil
}

func (log *redactedLog) flush() {
	if len(log.line) <= 8192 && len(log.line) != 0 {
		line := strings.ReplaceAll(string(log.line), log.key, "[private]")
		if log.license != "" {
			line = strings.ReplaceAll(line, log.license, "[private]")
		}
		lower := strings.ToLower(line)
		if strings.Contains(lower, "origin ip not licensed") || strings.Contains(lower, "invalid license key") {
			log.refused = true
			log.cancel()
		}
		fmt.Fprintln(os.Stderr, line)
	}
	log.line = log.line[:0]
}
