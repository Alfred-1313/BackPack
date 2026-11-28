package omni

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

func TestEmbeddedBundleFileLayout(t *testing.T) {
	names := []string{"manager", "LICENSE"}
	for _, engine := range engines {
		names = append(names, "bin/"+engine)
	}
	dir := t.TempDir()
	if err := unpackBundle(dir, bundleFixture(t, names)); err != nil {
		t.Fatal(err)
	}
	if !verifiedBundleDir(dir) {
		t.Fatal("unpacked bundle is missing a protected executable")
	}
	if err := os.Remove(filepath.Join(dir, "bin", engines[0])); err != nil {
		t.Fatal(err)
	}
	if verifiedBundleDir(dir) {
		t.Fatal("accepted an incomplete bundle directory")
	}
}

func TestEmbeddedBundleRejectsUnexpectedFiles(t *testing.T) {
	for _, names := range [][]string{{"../outside"}, {"manager", "manager"}, {"manager", "LICENSE"}} {
		if err := unpackBundle(t.TempDir(), bundleFixture(t, names)); err == nil {
			t.Errorf("accepted bundle containing %v", names)
		}
	}
}

func bundleFixture(t *testing.T, names []string) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	z := gzip.NewWriter(&buf)
	archive := tar.NewWriter(z)
	for _, name := range names {
		if err := archive.WriteHeader(&tar.Header{Name: name, Size: 1, Mode: 0755}); err != nil {
			t.Fatal(err)
		}
		if _, err := archive.Write([]byte{1}); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	return &buf
}
