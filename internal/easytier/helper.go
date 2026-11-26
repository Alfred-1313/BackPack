package easytier

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"
)

const release = "v2.6.4"

// EncryptedMTUOverhead is what the pinned helper reserves from --mtu when
// creating its encrypted TUN interface.
const EncryptedMTUOverhead = 20

var installMu sync.Mutex

func releaseAsset() (dir, digest string, err error) {
	if runtime.GOOS == "linux" {
		switch runtime.GOARCH {
		case "amd64":
			return "easytier-linux-x86_64", "61b659eaedba658fa66fe47d17e1426cdd77e5d02fa15fed447bb4357c09dfd6", nil
		case "arm64":
			return "easytier-linux-aarch64", "f533ec25a7ea714e09f645615012200278058525795cc3bb690ff011aec1a70f", nil
		}
	}
	return "", "", fmt.Errorf("EasyTier requires Linux amd64 or arm64")
}

func binaryPath() string {
	return filepath.Join("/usr/local/lib/backpack/helpers/easytier", release+"-"+runtime.GOARCH, "easytier-core")
}

func Binary() (string, error) {
	path := binaryPath()
	if st, err := os.Lstat(path); err == nil && st.Mode().IsRegular() && st.Mode().Perm()&0111 != 0 {
		return path, nil
	}
	return "", fmt.Errorf("the verified EasyTier helper is missing")
}

// Ensure installs only the pinned core, after verifying the release archive.
// Tunnel startup itself can then run without contacting the release server.
func Ensure(ctx context.Context) error {
	installMu.Lock()
	defer installMu.Unlock()
	if _, err := Binary(); err == nil {
		return nil
	}
	dir, expected, err := releaseAsset()
	if err != nil {
		return err
	}
	dest := binaryPath()
	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dest), ".download-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	url := "https://github.com/EasyTier/EasyTier/releases/download/" + release + "/" + dir + "-" + release + ".zip"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	res, err := (&http.Client{Timeout: 75 * time.Second}).Do(req)
	if err != nil {
		return fmt.Errorf("download EasyTier helper: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("download EasyTier helper: HTTP %d", res.StatusCode)
	}
	hash := sha256.New()
	size, err := io.Copy(io.MultiWriter(tmp, hash), io.LimitReader(res.Body, 32<<20))
	if err != nil {
		return err
	}
	if hex.EncodeToString(hash.Sum(nil)) != expected {
		return fmt.Errorf("EasyTier release archive checksum mismatch")
	}
	z, err := zip.NewReader(tmp, size)
	if err != nil {
		return err
	}
	for _, entry := range z.File {
		if entry.Name == dir+"/easytier-core" && entry.Mode().IsRegular() && entry.UncompressedSize64 < 128<<20 {
			return installCore(dest, entry)
		}
	}
	return fmt.Errorf("the EasyTier release archive has no expected core binary")
}

func installCore(dest string, entry *zip.File) error {
	in, err := entry.Open()
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.CreateTemp(filepath.Dir(dest), ".core-*")
	if err != nil {
		return err
	}
	defer os.Remove(out.Name())
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	if err := out.Chmod(0755); err != nil {
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Rename(out.Name(), dest)
}
