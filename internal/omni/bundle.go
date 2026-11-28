package omni

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"embed"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sync"
)

//go:embed payload/*
var payloadFS embed.FS

const helperRoot = "/usr/local/lib/backpack/helpers/omnitunnel"

var engines = []string{"omnitun", "omnistripe", "omniackless", "hysteria", "chisel"}

var embeddedID = sync.OnceValues(func() (string, error) {
	f, err := payloadFS.Open(bundleName())
	if err != nil {
		return "", err
	}
	defer f.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, f); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", hash.Sum(nil)[:12]), nil
})

func bundleName() string { return "payload/bundle-" + runtime.GOARCH + ".tar.gz" }

func Bundled() bool {
	if runtime.GOOS != "linux" {
		return false
	}
	_, err := fs.Stat(payloadFS, bundleName())
	return err == nil
}

// bundleManager materializes the embedded, versioned assets on first use.
// Referenced service paths stay valid across BackPack updates.
func bundleManager(root string) (string, error) {
	id, err := embeddedID()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(root, "backpack-"+runtime.GOARCH+"-"+id)
	path := filepath.Join(dir, "manager")
	if err := os.MkdirAll(root, 0755); err != nil {
		return "", err
	}
	if info, err := os.Lstat(root); err != nil || !info.IsDir() || info.Mode().Perm()&022 != 0 {
		return "", fmt.Errorf("invalid OmniTunnel asset directory: %s", root)
	}
	if _, err := os.Lstat(dir); !os.IsNotExist(err) {
		if verifiedBundleDir(dir) {
			return path, nil
		}
		return "", fmt.Errorf("embedded OmniTunnel directory is unavailable or incomplete: %s", dir)
	}
	stage, err := os.MkdirTemp(root, ".bundle-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(stage)
	asset, err := payloadFS.Open(bundleName())
	if err != nil {
		return "", err
	}
	defer asset.Close()
	if err := unpackBundle(stage, asset); err != nil {
		return "", err
	}
	if err := os.Chmod(stage, 0755); err != nil {
		return "", err
	}
	if err := os.Rename(stage, dir); err != nil && !verifiedBundleDir(dir) {
		return "", err
	}
	return path, nil
}

func verifiedBundleDir(dir string) bool {
	for _, part := range []string{dir, filepath.Join(dir, "bin")} {
		if info, err := os.Lstat(part); err != nil || !info.IsDir() || info.Mode().Perm()&022 != 0 {
			return false
		}
	}
	if !bundleExecutable(filepath.Join(dir, "manager")) {
		return false
	}
	for _, engine := range engines {
		if !bundleExecutable(filepath.Join(dir, "bin", engine)) {
			return false
		}
	}
	return true
}

func bundleExecutable(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.Mode().IsRegular() && info.Size() > 0 &&
		info.Mode().Perm()&0111 != 0 && info.Mode().Perm()&022 == 0
}

func unpackBundle(dir string, src io.Reader) error {
	z, err := gzip.NewReader(src)
	if err != nil {
		return err
	}
	defer z.Close()
	if err := os.Mkdir(filepath.Join(dir, "bin"), 0755); err != nil {
		return err
	}
	files := map[string]os.FileMode{"manager": 0755, "LICENSE": 0644}
	for _, engine := range engines {
		files["bin/"+engine] = 0755
	}
	archive := tar.NewReader(z)
	for {
		entry, err := archive.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		mode, expected := files[entry.Name]
		if !expected || entry.Typeflag != tar.TypeReg || entry.Size <= 0 || entry.Size > 64<<20 {
			return fmt.Errorf("invalid OmniTunnel bundle entry %q", entry.Name)
		}
		if err := writeBundleFile(filepath.Join(dir, entry.Name), archive, mode); err != nil {
			return err
		}
		delete(files, entry.Name)
	}
	if len(files) != 0 {
		return fmt.Errorf("embedded OmniTunnel bundle is incomplete")
	}
	_, err = io.Copy(io.Discard, z)
	return err
}

func writeBundleFile(path string, src io.Reader, mode os.FileMode) error {
	out, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, src)
	if closeErr := out.Close(); err == nil {
		err = closeErr
	}
	return err
}
