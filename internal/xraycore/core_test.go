package xraycore

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"strings"
	"testing"
)

type releaseFixture func(*http.Request) (*http.Response, error)

func (f releaseFixture) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestInstallPublishesOnlyVerifiedXray(t *testing.T) {
	name, err := releaseAsset()
	if err != nil {
		t.Skip(err)
	}
	root, arch := t.TempDir(), runtime.GOARCH
	const tag = "v26.7.28"
	var body bytes.Buffer
	archive := zip.NewWriter(&body)
	member, err := archive.Create("xray")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(member, "#!/bin/sh\nprintf 'Xray 26.7.28\\n'\n"); err != nil {
		t.Fatal(err)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	release := githubRelease{
		Tag: tag, Assets: []asset{{
			Name: name, Size: int64(body.Len()), Digest: fmt.Sprintf("sha256:%x", sha256.Sum256(body.Bytes())),
		}},
	}
	client := http.DefaultClient
	t.Cleanup(func() { http.DefaultClient = client })
	http.DefaultClient = &http.Client{Transport: releaseFixture(func(req *http.Request) (*http.Response, error) {
		var payload io.Reader
		switch req.URL.String() {
		case releasesURL + "/tags/" + tag:
			metadata, err := json.Marshal(release)
			if err != nil {
				return nil, err
			}
			payload = bytes.NewReader(metadata)
		case downloadURL + tag + "/" + name:
			payload = bytes.NewReader(body.Bytes())
		default:
			return nil, fmt.Errorf("unexpected request to %s", req.URL)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(payload)}, nil
	})}

	managed := defaultPath(root, arch)
	if got := resolve(root, arch, managed); got != managed {
		t.Fatalf("default path before install = %q", got)
	}
	if err := install(t.Context(), root, arch, tag, name, func(string) {}); err != nil {
		t.Fatal(err)
	}
	selected := resolve(root, arch, managed)
	if selected != activePath(root, arch) {
		t.Fatalf("managed path after install = %q", selected)
	}
	if got := resolve(root, arch, "/custom/xray"); got != "/custom/xray" {
		t.Fatalf("explicit binary redirected to %q", got)
	}
	before, err := os.Stat(selected)
	if err != nil {
		t.Fatal(err)
	}
	release.Assets[0].Digest = "sha256:" + strings.Repeat("0", 64)
	if err := install(t.Context(), root, arch, tag, name, func(string) {}); err == nil {
		t.Fatal("installed a release with the wrong checksum")
	}
	after, err := os.Stat(selected)
	if err != nil || !os.SameFile(before, after) {
		t.Fatalf("failed install replaced the active binary: %v", err)
	}
	if entries, err := os.ReadDir(root); err != nil || len(entries) != 1 {
		t.Fatalf("installation left staging files: %v, %v", entries, err)
	}
}
