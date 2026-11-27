package backhaulpremium

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
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

const Version = "v2.0.3"
const LicenseExitCode = 78

var ErrLicenseRejected = errors.New("Backhaul license validation failed")

type release struct {
	size   int64
	digest string
}

var releases = map[string]release{
	"amd64": {4279920, "0ffe0afcda76e4ce16a91027befa7c43df62c66463e33e0fb8115d6ee947f671"},
	"arm64": {3898136, "7cfa1ab04b090c9a2ff3d90aa5cd537511b4fc5801452bc8915e0ed9c3f8fd51"},
}

func binaryPath() string {
	return "/usr/local/lib/backpack/helpers/backhaul/" + Version + "-" + runtime.GOARCH + "/backhaul_premium"
}

var installMu sync.Mutex

// BinaryStatus checks the pinned official binary without downloading it.
func BinaryStatus() error {
	rel, ok := releases[runtime.GOARCH]
	if runtime.GOOS != "linux" || !ok {
		return fmt.Errorf("Backhaul Premium %s requires Linux amd64 or arm64", Version)
	}
	if err := checkBinary(binaryPath(), rel); err != nil {
		return fmt.Errorf("verified Backhaul Premium %s is unavailable: %w", Version, err)
	}
	return nil
}

func checkBinary(path string, rel release) error {
	st, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !st.Mode().IsRegular() || st.Mode().Perm()&0111 == 0 || st.Size() != rel.size {
		return errors.New("unexpected Backhaul Premium executable")
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	h := sha256.New()
	if _, err := io.Copy(h, file); err != nil {
		return err
	}
	if fmt.Sprintf("%x", h.Sum(nil)) != rel.digest {
		return errors.New("Backhaul Premium checksum mismatch")
	}
	return nil
}

func Ensure(ctx context.Context) error {
	rel, ok := releases[runtime.GOARCH]
	if runtime.GOOS != "linux" || !ok {
		return fmt.Errorf("Backhaul Premium %s requires Linux amd64 or arm64", Version)
	}
	installMu.Lock()
	defer installMu.Unlock()
	path := binaryPath()
	if err := checkBinary(path, rel); err == nil || !os.IsNotExist(err) {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	// The official launcher publishes a plain HTTP archive. The executable
	// must match the exact reviewed release before it can become executable.
	url := "http://en.backhaul-dev.com/backhaul_premium_linux_" + runtime.GOARCH + ".tar.gz"
	if err := download(ctx, url, path, rel); err == nil {
		return nil
	} else if ctx.Err() != nil {
		return ctx.Err()
	}
	url = "http://194.9.6.93/backhaul_premium_linux_" + runtime.GOARCH + ".tar.gz"
	return download(ctx, url, path, rel)
}

func download(ctx context.Context, url, path string, rel release) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")
	res, err := (&http.Client{Timeout: 12 * time.Second}).Do(req)
	if err != nil {
		return fmt.Errorf("download official Backhaul Premium: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("download official Backhaul Premium: HTTP %d", res.StatusCode)
	}
	gzipBody, err := gzip.NewReader(io.LimitReader(res.Body, 12<<20))
	if err != nil {
		return err
	}
	defer gzipBody.Close()
	archive := tar.NewReader(io.LimitReader(gzipBody, 14<<20))
	for {
		entry, err := archive.Next()
		if err != nil {
			return fmt.Errorf("read official Backhaul archive: %w", err)
		}
		if filepath.Base(entry.Name) != "backhaul_premium" {
			continue
		}
		if entry.Typeflag != tar.TypeReg || entry.Size != rel.size {
			return errors.New("unexpected executable in official Backhaul archive")
		}
		dst, err := os.CreateTemp(filepath.Dir(path), ".backhaul-*")
		if err != nil {
			return err
		}
		defer os.Remove(dst.Name())
		_, copyErr := io.Copy(dst, archive)
		closeErr := dst.Close()
		if err := errors.Join(copyErr, closeErr); err != nil {
			return err
		}
		if err := os.Chmod(dst.Name(), 0755); err != nil {
			return err
		}
		if err := checkBinary(dst.Name(), rel); err != nil {
			return err
		}
		return os.Rename(dst.Name(), path)
	}
}

func LocalInterface(address string) (string, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return "", err
	}
	for _, iface := range ifaces {
		addrs, _ := iface.Addrs()
		for _, raw := range addrs {
			prefix, err := netip.ParsePrefix(raw.String())
			if err == nil && prefix.Addr().String() == address {
				return iface.Name, nil
			}
		}
	}
	return "", fmt.Errorf("IPX endpoint %s is not assigned to a local interface", address)
}

func Run(ctx context.Context, name string, c config.BackhaulConfig) error {
	var iface string
	if c.Mode == "tun-ipx" {
		local := c.IranHost
		if c.Role == "client" {
			local = c.KharejIP
		}
		var err error
		iface, err = LocalInterface(local)
		if err != nil {
			return err
		}
	}
	body, err := Build(name, c, iface)
	if err != nil {
		return err
	}
	if err := BinaryStatus(); err != nil {
		return err
	}
	if c.Mode == "tun-ipx" && c.IPXProfile == "tcp" {
		peer := c.KharejIP
		if c.Role == "client" {
			peer = c.IranHost
		}
		cleanup, err := protectIPXTCP(peer)
		if err != nil {
			return err
		}
		defer cleanup()
	}
	file, err := os.CreateTemp("", "backpack-backhaul-*.toml")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	_, writeErr := file.Write(body)
	if err := errors.Join(writeErr, file.Close()); err != nil {
		return err
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(runCtx, binaryPath(), "-c", file.Name())
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = 5 * time.Second
	stream := &tokenRedactor{secret: c.Token, reject: cancel}
	cmd.Stdout, cmd.Stderr = stream, stream
	err = cmd.Run()
	if ctx.Err() != nil {
		return nil
	}
	if stream.licenseFailure != "" {
		return fmt.Errorf("%w: %s", ErrLicenseRejected, stream.licenseFailure)
	}
	if err != nil && stream.failure != "" {
		return fmt.Errorf("%w: %s", err, stream.failure)
	}
	return err
}

type tokenRedactor struct {
	secret, failure, licenseFailure string
	reject                          context.CancelFunc
}

func (w *tokenRedactor) Write(p []byte) (int, error) {
	message := strings.ReplaceAll(string(p), w.secret, "[private]")
	for _, line := range strings.Split(message, "\n") {
		if strings.Contains(line, "FATAL") || strings.Contains(line, "ERROR") {
			w.failure = strings.TrimSpace(line)
		}
		if w.licenseFailure == "" && (strings.Contains(line, "ERROR") || strings.Contains(line, "FATAL")) {
			w.licenseFailure = LicenseProblem(line)
			if w.licenseFailure != "" {
				w.reject()
			}
		}
	}
	_, err := io.WriteString(os.Stderr, message)
	return len(p), err
}

// LicenseProblem recognizes the messages emitted by the official engine.
// Console output may include ANSI styles around those messages.
func LicenseProblem(message string) string {
	lower := strings.ToLower(message)
	switch {
	case strings.Contains(lower, "license does not exist"):
		return "License does not exist"
	case strings.Contains(lower, "license rejected"):
		return "License rejected"
	default:
		return ""
	}
}
