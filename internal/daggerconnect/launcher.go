package daggerconnect

import (
	"bufio"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/backpack/backpack/config"
)

const (
	launcherURL  = "https://github.com/itsFLoKi/daggerConnect/releases/download/v1.4/DaggerLauncher"
	launcherSHA  = "aaf8cb90088313c64adffa0bc21578557f37ae00548af1dbd0b47fe4e9018dcf"
	launcher     = "/usr/local/lib/backpack/helpers/daggerconnect/v1.4/DaggerLauncher"
	launcherSize = 6988005
)

func checkLauncher(path string) error {
	st, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !st.Mode().IsRegular() || st.Mode().Perm()&0111 == 0 || st.Size() != launcherSize {
		return fmt.Errorf("unexpected DaggerLauncher file")
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	digest := sha256.New()
	if _, err := io.Copy(digest, f); err != nil {
		return err
	}
	if fmt.Sprintf("%x", digest.Sum(nil)) != launcherSHA {
		return fmt.Errorf("DaggerLauncher checksum mismatch")
	}
	return nil
}

func Ensure(ctx context.Context) error {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		return fmt.Errorf("DaggerLauncher v1.4 requires Linux amd64")
	}
	if err := checkLauncher(launcher); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	dir := filepath.Dir(launcher)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, launcherURL, nil)
	if err != nil {
		return err
	}
	res, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		return fmt.Errorf("download official DaggerLauncher: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("download official DaggerLauncher: HTTP %d", res.StatusCode)
	}
	f, err := os.CreateTemp(dir, ".launcher-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if _, err := io.Copy(f, io.LimitReader(res.Body, launcherSize+1)); err != nil {
		return err
	}
	if err := f.Chmod(0755); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := checkLauncher(f.Name()); err != nil {
		return err
	}
	return os.Rename(f.Name(), launcher)
}

func Run(ctx context.Context, name string, c config.DaggerConfig) error {
	body, err := Build(name, c)
	if err != nil {
		return err
	}
	if err := checkLauncher(launcher); err != nil {
		return fmt.Errorf("official DaggerLauncher is unavailable: %w", err)
	}
	f, err := os.CreateTemp("", "backpack-dagger-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if _, err := f.Write(body); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}

	cmd := exec.CommandContext(ctx, launcher, "-c", f.Name())
	cmd.WaitDelay = 5 * time.Second
	cmd.Env = append(os.Environ(), "DC_CHANNEL=release", "DC_VERSION="+CoreVersion,
		"DC_SERVER_PUBLIC_IP="+c.IranIP)
	rd, wr := io.Pipe()
	defer rd.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		reader := bufio.NewReader(rd)
		for {
			line, err := reader.ReadString('\n')
			if line != "" {
				fmt.Fprint(os.Stderr, strings.ReplaceAll(line, c.License, "[redacted]"))
			}
			if err != nil {
				return
			}
		}
	}()
	cmd.Stdout, cmd.Stderr = wr, wr
	err = cmd.Run()
	_ = wr.Close()
	<-done
	if ctx.Err() != nil {
		return nil
	}
	return err
}
