package mtproxy

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/backpack/backpack/internal/app"
)

const maxArchive = 10 << 20

var releaseSHA = map[string]string{
	"amd64": "11c038ea5eeebd5d49148048975c24f7b0c4fa4c1cf4f15a2410203e2da01812",
	"arm64": "86e0fc6f50bfb453b7a6e8b91497c6661a7d64a3f607828e0e42f534d795ac5a",
}

func ensureBinary(ctx context.Context, mode string) (string, error) {
	version, binary := LegacyVersion, "mtg"
	asset := fmt.Sprintf("mtg-%s-linux-%s", version[1:], runtime.GOARCH)
	member := asset + "/mtg"
	source := "https://github.com/9seconds/mtg/releases/download/" + version + "/" + asset + ".tar.gz"
	checksum := releaseSHA[runtime.GOARCH]
	if mode != "" {
		version, binary, member = Version, "telemt", "telemt"
		arch := map[string]string{"amd64": "x86_64", "arm64": "aarch64"}[runtime.GOARCH]
		checksum = map[string]string{
			"amd64": "f1591635849e0aad31f147692b21726da2f49499aae3ece79f8760c92925133a",
			"arm64": "c80b0159b41cf410b2a87cf62676d67a16a537bd7207602b60dd9ce56e14d1f6",
		}[runtime.GOARCH]
		source = "https://github.com/telemt/telemt/releases/download/" + version + "/telemt-" + arch + "-linux-musl.tar.gz"
	}
	if runtime.GOOS != "linux" || checksum == "" {
		return "", errors.New("MTProxy requires Linux amd64 or arm64")
	}
	// The shared BackPack helpers directory is normally root-only. The
	// unprivileged proxy service needs to traverse this separate directory.
	dir := filepath.Join(helperRoot, version+"-"+runtime.GOARCH)
	dst := filepath.Join(dir, binary)
	if info, err := os.Lstat(dst); err == nil {
		if !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
			return "", errors.New("invalid installed mtg executable")
		}
		return dst, nil
	} else if !os.IsNotExist(err) {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, source, nil)
	if err != nil {
		return "", err
	}
	res, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%s release download returned HTTP %d", binary, res.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, maxArchive+1))
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	if len(data) > maxArchive || hex.EncodeToString(sum[:]) != checksum {
		return "", fmt.Errorf("%s release SHA-256 check failed", binary)
	}
	file, err := extractBinary(data, member)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	return dst, app.WriteFileAtomic(dst, file, 0755)
}

func extractBinary(data []byte, member string) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer gz.Close()
	reader := tar.NewReader(gz)
	for {
		entry, err := reader.Next()
		if err == io.EOF {
			return nil, errors.New("mtg executable missing from archive")
		}
		if err != nil {
			return nil, err
		}
		if entry.Name == member {
			if entry.Typeflag != tar.TypeReg || entry.Size <= 0 || entry.Size > 24<<20 {
				return nil, errors.New("invalid executable in mtg archive")
			}
			return io.ReadAll(reader)
		}
	}
}
