package waterwall

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

const (
	Label             = "WaterWall"
	Version           = "v1.6.0"
	Protocol17Version = "v1.44.3"
	DefaultProtocol   = 136
	TestProtocol      = 17
	DefaultMTU        = 1500
	maxCoreSize       = 16 << 20
)

type build struct {
	version, name, archiveHash, binaryHash string
}

var compatible = build{Version, "Waterwall-linux-gcc-x64-old-cpu",
	"0121139392483e7308c8521ce37c4393afd28a394473fec22ebab776f720bab3",
	"865278836d49fa20d1764a6b1022a7ad195aeb153e50f8921ac7e13b93aaf4bc"}
var standard = build{Version, "Waterwall-linux-gcc-x64",
	"530c5305df44f5b2995e92fd6811125cb9e0543a3182c570561e53076a94c269",
	"561ef059d41cc272b2b73f90b628069df6327fc07ede26d181baa490ea26a1d9"}
var protocol17 = build{Protocol17Version, "Waterwall-linux-gcc-x64-old-cpu",
	"f8159c807712cecdd24014c549542e4ceddc5b9b2e341c9042b0711c402a19e2",
	"bf574f5f59dc2d3aa5de5b7eb358fe3e12265660ea2ab9d6a315cf9a827da9fa"}
var installMu sync.Mutex

func (b build) binary() string {
	return filepath.Join("/usr/local/lib/backpack/helpers/waterwall", b.version, b.name, "Waterwall")
}

func (b build) verified() (string, error) {
	file := b.binary()
	info, err := os.Lstat(file)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 || info.Mode().Perm()&022 != 0 || info.Size() > maxCoreSize {
		return "", fmt.Errorf("WaterWall %s must be a regular, protected executable", file)
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return "", err
	}
	if fmt.Sprintf("%x", sha256.Sum256(data)) != b.binaryHash {
		return "", fmt.Errorf("WaterWall %s checksum mismatch", file)
	}
	return file, nil
}

func selectedBuild(iran bool, protocol int) build {
	if protocol == TestProtocol {
		return protocol17
	}
	if iran || !standardCPU() {
		return compatible
	}
	return standard
}

func Binary(iran bool, protocol int) (string, error) {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		return "", fmt.Errorf("WaterWall Packet Tunnel requires Linux amd64")
	}
	b := selectedBuild(iran, protocol)
	file, err := b.verified()
	if os.IsNotExist(err) && b == standard {
		return compatible.verified()
	}
	return file, err
}

func InstalledVersions() string {
	var versions []string
	if _, err := compatible.verified(); err == nil {
		versions = append(versions, Version)
	} else if _, err := standard.verified(); err == nil {
		versions = append(versions, Version)
	}
	if _, err := protocol17.verified(); err == nil {
		versions = append(versions, Protocol17Version)
	}
	return strings.Join(versions, " · ")
}

func Ensure(ctx context.Context, iran bool, protocol int) error {
	installMu.Lock()
	defer installMu.Unlock()
	if _, err := Binary(iran, protocol); err == nil {
		return nil
	}
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		return fmt.Errorf("WaterWall Packet Tunnel requires Linux amd64")
	}
	b := selectedBuild(iran, protocol)
	url := "https://github.com/radkesvat/WaterWall/releases/download/" + b.version + "/" + b.name + ".zip"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	res, err := (&http.Client{Timeout: 60 * time.Second}).Do(req)
	if err != nil {
		return fmt.Errorf("download WaterWall: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("download WaterWall: HTTP %d", res.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, maxCoreSize))
	if err != nil {
		return err
	}
	if fmt.Sprintf("%x", sha256.Sum256(data)) != b.archiveHash {
		return fmt.Errorf("WaterWall release archive checksum mismatch")
	}
	archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return err
	}
	for _, entry := range archive.File {
		if path.Base(entry.Name) == "Waterwall" && entry.Mode().IsRegular() && entry.UncompressedSize64 <= maxCoreSize {
			return b.install(entry)
		}
	}
	return fmt.Errorf("WaterWall release archive has no core binary")
}

func (b build) install(entry *zip.File) error {
	in, err := entry.Open()
	if err != nil {
		return err
	}
	defer in.Close()
	dest := b.binary()
	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		return err
	}
	out, err := os.CreateTemp(filepath.Dir(dest), ".core-*")
	if err != nil {
		return err
	}
	defer os.Remove(out.Name())
	defer out.Close()
	hash := sha256.New()
	if _, err := io.Copy(io.MultiWriter(out, hash), in); err != nil {
		return err
	}
	if fmt.Sprintf("%x", hash.Sum(nil)) != b.binaryHash {
		return fmt.Errorf("WaterWall binary checksum mismatch")
	}
	if err := out.Chmod(0755); err != nil {
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Rename(out.Name(), dest)
}

// RuntimeConfig renders the supplied packet chain. TCP/transport stays in
// WaterWall. BackPack handles service supervision and forwarded-port mappings.
type RuntimeConfig struct {
	Interface, LocalCIDR, PeerIP, LocalEndpoint, RemoteEndpoint string
	Protocol, Workers, MTU                                      int
	SocketMark                                                  uint32
}

func (c RuntimeConfig) Write(dir string) error {
	type fields = map[string]any
	node := func(name, kind, next string, settings fields) fields {
		result := fields{"name": name, "type": kind, "settings": settings}
		if next != "" {
			result["next"] = next
		}
		return result
	}
	overwrite := func(name, next, direction, key, value string) fields {
		return node(name, "IpOverrider", next, fields{"direction": direction, "mode": key, "ipv4": value})
	}
	rawSettings := fields{
		"capture-filter-mode": "source-ip", "capture-ip": c.RemoteEndpoint,
		"capture-protocols": []int{c.Protocol}, "skip-sysctl": true,
		"capture-device-name": c.Interface, "raw-device-name": c.Interface,
	}
	if c.SocketMark != 0 {
		rawSettings["mark"] = c.SocketMark
	}
	tunnel := fields{"name": "BackPack Packet Tunnel", "nodes": []fields{
		node("tun", "TunDevice", "wire-source", fields{
			"device-name": c.Interface, "device-ip": c.LocalCIDR, "device-mtu": c.MTU,
		}),
		overwrite("wire-source", "wire-destination", "up", "source-ip", c.LocalEndpoint),
		overwrite("wire-destination", "protocol", "up", "dest-ip", c.RemoteEndpoint),
		node("protocol", "IpManipulator", "inner-source", fields{"protoswap": c.Protocol}),
		overwrite("inner-source", "inner-destination", "down", "source-ip", c.PeerIP),
		overwrite("inner-destination", "raw", "down", "dest-ip", localIP(c.LocalCIDR)),
		node("raw", "RawSocket", "", rawSettings),
	}}
	core := fields{
		"configs": []string{"packet.json"},
		"misc":    fields{"workers": c.Workers, "mtu": c.MTU, "ram-profile": "client", "tcp-tune": false},
		"log": fields{
			"core":     fields{"loglevel": "WARN", "console": true},
			"network":  fields{"loglevel": "WARN", "console": true},
			"internal": fields{"loglevel": "WARN", "console": true},
		},
	}
	for name, body := range map[string]fields{"core.json": core, "packet.json": tunnel} {
		data, err := json.MarshalIndent(body, "", "  ")
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, 0600); err != nil {
			return err
		}
	}
	return nil
}

func Validate(protocol, workers int) error {
	if protocol < 1 || protocol > 254 {
		return fmt.Errorf("WaterWall IP protocol must be between 1 and 254")
	}
	if protocol == 6 {
		return fmt.Errorf("WaterWall IP protocol 6 would capture the peer's native TCP traffic")
	}
	if workers < 0 || workers > 64 {
		return fmt.Errorf("WaterWall workers must be between 0 (automatic) and 64")
	}
	return nil
}
