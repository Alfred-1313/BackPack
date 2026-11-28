package waterwall

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/backpack/backpack/internal/app"
	"golang.org/x/net/html"
)

const releasePath = "/radkesvat/WaterWall/releases"
const updateTokenPrefix = "ww1."

var releaseTag = regexp.MustCompile(`^v[0-9]{1,6}\.[0-9]{1,6}\.[0-9]{1,6}$`)
var sha256Hex = regexp.MustCompile(`^[0-9a-f]{64}$`)
var releaseClient = &http.Client{Timeout: 12 * time.Second}

type UpdateCheck struct {
	Installed string `json:"installed"`
	Latest    string `json:"latest"`
	Asset     string `json:"asset"`
}

func CheckUpdate(ctx context.Context) (UpdateCheck, error) {
	res, err := releaseGET(ctx, releasesURL+"/latest")
	if err != nil {
		return UpdateCheck{}, err
	}
	defer res.Body.Close()
	u := res.Request.URL
	tag, ok := strings.CutPrefix(u.Path, releasePath+"/tag/")
	if u.Scheme != "https" || u.Host != "github.com" || !ok || !validUpdateTag(tag) {
		return UpdateCheck{}, fmt.Errorf("GitHub did not return an official WaterWall release tag")
	}
	return UpdateCheck{InstalledVersion(true), tag, compatible.name + ".zip"}, nil
}

func validUpdateTag(tag string) bool {
	return releaseTag.MatchString(tag) && tag != Protocol17Version
}

func UpdateCommand(tag string) string {
	if !validUpdateTag(tag) {
		return ""
	}
	return "sudo backpack waterwall update '" + updateTokenPrefix + tag + "'"
}

func UpdateTag(token string) (string, error) {
	tag, ok := strings.CutPrefix(token, updateTokenPrefix)
	if !ok || UpdateCommand(tag) == "" {
		return "", fmt.Errorf("invalid WaterWall update token")
	}
	return tag, nil
}

type installedRelease struct {
	Tag    string `json:"tag"`
	SHA256 string `json:"sha256"`
}

func (b build) activeFile() string {
	return filepath.Join(coreDir, "active-"+b.name+".json")
}

func installedBuild(fallback build) (file, version string, err error) {
	raw, err := os.ReadFile(fallback.activeFile())
	if err == nil {
		var active installedRelease
		if json.Unmarshal(raw, &active) == nil && validUpdateTag(active.Tag) && sha256Hex.MatchString(active.SHA256) {
			b := build{version: active.Tag, name: fallback.name, binaryHash: active.SHA256}
			if file, err := b.verified(); err == nil {
				return file, b.version, nil
			}
		}
	}
	file, err = fallback.verified()
	return file, fallback.version, err
}

// InstallUpdate activates a verified core for future tunnel starts.
func InstallUpdate(ctx context.Context, tag string, iran bool, step func(string)) (string, error) {
	if !validUpdateTag(tag) {
		return "", fmt.Errorf("invalid WaterWall release tag")
	}
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		return "", fmt.Errorf("WaterWall updates require Linux amd64")
	}
	installMu.Lock()
	defer installMu.Unlock()

	b := selectedBuild(iran, DefaultProtocol)
	if _, active, err := installedBuild(b); err == nil && active == tag {
		return b.name + ".zip", nil
	}
	if tag != b.version {
		b.version, b.binaryHash = tag, ""
		step("Checking the official archive checksum…")
		hash, err := releaseDigest(ctx, b)
		if err != nil {
			return "", err
		}
		b.archiveHash = hash
	}
	if _, err := b.verified(); err != nil {
		step("Downloading and verifying " + b.name + ".zip…")
		var downloadErr error
		b, downloadErr = b.download(ctx)
		if downloadErr != nil {
			return "", downloadErr
		}
	}
	if _, err := b.verified(); err != nil {
		return "", err
	}
	active, err := json.Marshal(installedRelease{Tag: b.version, SHA256: b.binaryHash})
	if err != nil {
		return "", err
	}
	step("Activating WaterWall " + b.version + "…")
	if err := app.WriteFileAtomic(b.activeFile(), active, 0644); err != nil {
		return "", err
	}
	return b.name + ".zip", nil
}

func releaseGET(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	res, err := releaseClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("could not check the official WaterWall releases: %w", err)
	}
	if res.StatusCode != http.StatusOK {
		res.Body.Close()
		return nil, fmt.Errorf("WaterWall releases returned HTTP %d", res.StatusCode)
	}
	return res, nil
}

func releaseDigest(ctx context.Context, b build) (string, error) {
	// The release page also exposes the digest when GitHub rate-limits its API.
	url := releasesURL + "/expanded_assets/" + b.version
	res, err := releaseGET(ctx, url)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.Request.URL.String() != url {
		return "", fmt.Errorf("unexpected redirect for WaterWall release assets")
	}
	assetPath := releasePath + "/download/" + b.version + "/" + b.name + ".zip"
	return assetDigest(io.LimitReader(res.Body, 1<<20), assetPath)
}

func assetDigest(r io.Reader, assetPath string) (string, error) {
	z := html.NewTokenizer(r)
	var found bool
	var digest string
	for {
		switch z.Next() {
		case html.ErrorToken:
			return "", fmt.Errorf("official release has no SHA-256 checksum for %s", filepath.Base(assetPath))
		case html.StartTagToken:
			token := z.Token()
			switch token.Data {
			case "li":
				found, digest = false, ""
			case "a":
				for _, attr := range token.Attr {
					found = found || (attr.Key == "href" && attr.Val == assetPath)
				}
			case "clipboard-copy":
				for _, attr := range token.Attr {
					value, ok := strings.CutPrefix(attr.Val, "sha256:")
					if attr.Key == "value" && ok && sha256Hex.MatchString(value) {
						digest = value
					}
				}
			}
		case html.EndTagToken:
			if tag, _ := z.TagName(); string(tag) == "li" {
				if found && digest != "" {
					return digest, nil
				}
				found, digest = false, ""
			}
		}
	}
}
