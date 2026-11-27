// Package xraycore installs official Xray releases for BackPack's managed helper.
package xraycore

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

const (
	helperRoot  = "/usr/local/lib/backpack/helpers/xray"
	pinnedTag   = "v26.3.27"
	releasesURL = "https://api.github.com/repos/XTLS/Xray-core/releases"
	downloadURL = "https://github.com/XTLS/Xray-core/releases/download/"
	maxArchive  = 64 << 20
)

var releaseTag = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+(?:[-+][0-9A-Za-z]+(?:[.-][0-9A-Za-z]+)*)?$`)

func NormalizeTag(version string) (string, error) {
	tag := "v" + strings.TrimPrefix(strings.TrimSpace(version), "v")
	if len(tag) > 64 || !releaseTag.MatchString(tag) {
		return "", errors.New("enter an Xray version such as v26.7.28")
	}
	return tag, nil
}

func DefaultPath() string { return defaultPath(helperRoot, runtime.GOARCH) }

func defaultPath(root, arch string) string {
	return filepath.Join(root, pinnedTag+"-"+arch, "xray")
}

func activePath(root, arch string) string {
	return filepath.Join(root, "current-"+arch)
}

// Resolve redirects the path stored in managed tunnel configurations. A
// caller's explicitly configured binary outside that path is kept as-is.
func Resolve(configured string) string {
	return resolve(helperRoot, runtime.GOARCH, configured)
}

func resolve(root, arch, configured string) string {
	if configured == defaultPath(root, arch) {
		path := activePath(root, arch)
		if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0111 != 0 {
			return path
		}
	}
	return configured
}

type Release struct {
	Tag        string `json:"tag"`
	Prerelease bool   `json:"prerelease"`
}

type asset struct {
	Name   string `json:"name"`
	Digest string `json:"digest"`
	Size   int64  `json:"size"`
}

type githubRelease struct {
	Tag        string  `json:"tag_name"`
	Prerelease bool    `json:"prerelease"`
	Draft      bool    `json:"draft"`
	Assets     []asset `json:"assets"`
}

func (r githubRelease) binaryAsset(name string) (asset, bool) {
	if r.Draft || len(r.Tag) > 64 || !releaseTag.MatchString(r.Tag) {
		return asset{}, false
	}
	for _, a := range r.Assets {
		if a.Name == name && a.Size > 0 && a.Size <= maxArchive {
			return a, true
		}
	}
	return asset{}, false
}

func releaseAsset() (string, error) {
	if runtime.GOOS == "linux" {
		switch runtime.GOARCH {
		case "amd64":
			return "Xray-linux-64.zip", nil
		case "arm64":
			return "Xray-linux-arm64-v8a.zip", nil
		}
	}
	return "", fmt.Errorf("Xray core updates are unavailable on %s/%s", runtime.GOOS, runtime.GOARCH)
}

// Releases reads the recent official releases that have a build for this host.
func Releases(ctx context.Context) ([]Release, error) {
	name, err := releaseAsset()
	if err != nil {
		return nil, err
	}
	var releases []githubRelease
	if err := readGitHub(ctx, releasesURL+"?per_page=12", &releases); err != nil {
		return nil, err
	}
	out := make([]Release, 0, len(releases))
	for _, r := range releases {
		if _, ok := r.binaryAsset(name); ok {
			out = append(out, Release{Tag: r.Tag, Prerelease: r.Prerelease})
		}
	}
	return out, nil
}

func readGitHub(ctx context.Context, url string, out any) error {
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	resp, err := get(ctx, url)
	if err != nil {
		return fmt.Errorf("fetch Xray releases: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return errors.New("Xray release not found on GitHub")
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GitHub release request returned %s", resp.Status)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(out)
}

func get(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "BackPack Xray core updater")
	return http.DefaultClient.Do(req)
}

// Install stages and verifies a release before atomically publishing it for
// future helper starts. Existing Xray processes keep their open executable.
func Install(ctx context.Context, tag string, step func(string)) error {
	tag, err := NormalizeTag(tag)
	if err != nil {
		return err
	}
	name, err := releaseAsset()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	return install(ctx, helperRoot, runtime.GOARCH, tag, name, step)
}

func install(ctx context.Context, root, arch, tag, name string, step func(string)) error {
	step("Looking up " + tag)
	var release githubRelease
	if err := readGitHub(ctx, releasesURL+"/tags/"+tag, &release); err != nil {
		return err
	}
	a, ok := release.binaryAsset(name)
	if !ok || release.Tag != tag {
		return fmt.Errorf("%s has no official Xray build for %s", tag, arch)
	}
	url := downloadURL + tag + "/" + name
	digest, err := expectedDigest(ctx, url, a)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(root, 0755); err != nil {
		return err
	}
	dir, err := os.MkdirTemp(root, ".update-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	archive := filepath.Join(dir, "release.zip")
	step("Downloading " + tag)
	if err := download(ctx, url, archive, a.Size, digest); err != nil {
		return err
	}
	step("Verifying " + tag)
	binary := filepath.Join(dir, "xray")
	if err := unpack(archive, binary); err != nil {
		return err
	}
	if err := checkVersion(ctx, binary, tag); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return os.Rename(binary, activePath(root, arch))
}

var sha256Line = regexp.MustCompile(`(?m)^SHA2?-256[ \t]*=[ \t]*([a-fA-F0-9]{64})[ \t\r]*$`)

func expectedDigest(ctx context.Context, url string, a asset) (string, error) {
	if digest, ok := strings.CutPrefix(a.Digest, "sha256:"); ok && len(digest) == 64 {
		if _, err := hex.DecodeString(digest); err == nil {
			return strings.ToLower(digest), nil
		}
	}
	resp, err := get(ctx, url+".dgst")
	if err != nil {
		return "", fmt.Errorf("download Xray checksum: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("Xray checksum request returned %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8192))
	if err != nil {
		return "", err
	}
	match := sha256Line.FindSubmatch(body)
	if len(match) != 2 {
		return "", errors.New("official Xray SHA-256 checksum is missing")
	}
	return strings.ToLower(string(match[1])), nil
}

func download(ctx context.Context, url, path string, size int64, digest string) error {
	resp, err := get(ctx, url)
	if err != nil {
		return fmt.Errorf("download Xray: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Xray download returned %s", resp.Status)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	hash := sha256.New()
	n, err := io.Copy(io.MultiWriter(file, hash), io.LimitReader(resp.Body, size+1))
	if err := errors.Join(err, file.Close()); err != nil {
		return err
	}
	if n != size || hex.EncodeToString(hash.Sum(nil)) != digest {
		return errors.New("Xray download failed its size or SHA-256 check")
	}
	return nil
}

func unpack(archive, binary string) error {
	reader, err := zip.OpenReader(archive)
	if err != nil {
		return err
	}
	defer reader.Close()
	for _, member := range reader.File {
		if member.Name != "xray" {
			continue
		}
		if !member.Mode().IsRegular() || member.UncompressedSize64 == 0 || member.UncompressedSize64 > 128<<20 {
			return errors.New("invalid Xray executable in release archive")
		}
		in, err := member.Open()
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(binary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0755)
		if err != nil {
			return err
		}
		n, err := io.Copy(out, io.LimitReader(in, int64(member.UncompressedSize64)+1))
		if err := errors.Join(err, out.Sync(), out.Close()); err != nil {
			return err
		}
		if n != int64(member.UncompressedSize64) {
			return errors.New("Xray archive has an incorrect executable size")
		}
		return nil
	}
	return errors.New("Xray executable missing from release archive")
}

func checkVersion(ctx context.Context, binary, tag string) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "version")
	cmd.WaitDelay = time.Second
	output, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("run downloaded Xray: %w", err)
	}
	fields := strings.Fields(string(output))
	if len(fields) < 2 || fields[0] != "Xray" || "v"+strings.TrimPrefix(fields[1], "v") != tag {
		return fmt.Errorf("the downloaded executable did not report Xray %s", tag)
	}
	return nil
}
