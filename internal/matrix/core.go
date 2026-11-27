package matrix

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"sync"

	"github.com/backpack/backpack/config"
	"github.com/backpack/backpack/internal/tunnel/portmap"
)

const ID = "matrix"
const Label = "Matrix (Tinc)"
const Main = "main"
const DefaultPort = 2097

var aptMu sync.Mutex
var versionPattern = regexp.MustCompile(`\btinc version ([0-9][a-zA-Z0-9.+-]*)`)
var nodePattern = regexp.MustCompile(`^(main|node_[0-9]{1,3})$`)

func Binary() (string, error) {
	if runtime.GOOS == "linux" {
		if path, err := exec.LookPath("tincd"); err == nil {
			return path, nil
		}
		for _, p := range []string{"/usr/sbin/tincd", "/usr/local/sbin/tincd"} {
			if st, err := os.Stat(p); err == nil && st.Mode().IsRegular() && st.Mode().Perm()&0111 != 0 {
				return p, nil
			}
		}
	}
	return "", errors.New("tincd is not installed")
}

func Version(ctx context.Context) (string, error) {
	path, err := Binary()
	if err != nil {
		return "", err
	}
	out, err := exec.CommandContext(ctx, path, "--version").Output()
	if m := versionPattern.FindSubmatch(out); err == nil && len(m) > 1 {
		return "v" + string(m[1]), nil
	}
	return "", errors.New("tincd version could not be read")
}

// Ensure uses the system's signed package repository. It never runs an
// upstream provisioning script or writes a system-wide Tinc network.
func Ensure(ctx context.Context) error {
	aptMu.Lock()
	defer aptMu.Unlock()
	if _, err := Binary(); err == nil {
		return nil
	}
	if runtime.GOOS != "linux" || os.Geteuid() != 0 {
		return errors.New("Matrix requires root on Linux")
	}
	apt, err := exec.LookPath("apt-get")
	if err != nil {
		return errors.New("install the tinc package on this server before setting up Matrix")
	}
	install := func(args ...string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, apt, args...)
		cmd.Env = append(os.Environ(), "DEBIAN_FRONTEND=noninteractive", "NEEDRESTART_MODE=l")
		return cmd.CombinedOutput()
	}
	args := []string{"-o", "DPkg::Lock::Timeout=15", "-o", "Acquire::Retries=0", "-o", "Acquire::http::Timeout=12",
		"-o", "Acquire::https::Timeout=12", "install", "-y", "--no-install-recommends", "tinc"}
	out, err := install(args...)
	if err != nil {
		if _, updateErr := install("-o", "Acquire::Retries=0", "-o", "Acquire::http::Timeout=12", "-o", "Acquire::https::Timeout=12", "update"); updateErr == nil {
			out, err = install(args...)
		}
	}
	if err != nil {
		line := strings.TrimSpace(string(out))
		if i := strings.LastIndexByte(line, '\n'); i >= 0 {
			line = line[i+1:]
		}
		return fmt.Errorf("install tinc: %w: %s", err, line)
	}
	_, err = Binary()
	return err
}

func GenerateKey() (private, public string, err error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return "", "", err
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})),
		string(pem.EncodeToMemory(&pem.Block{Type: "RSA PUBLIC KEY", Bytes: x509.MarshalPKCS1PublicKey(&key.PublicKey)})), nil
}

func publicKey(text string) (*rsa.PublicKey, error) {
	block, rest := pem.Decode([]byte(text))
	if len(text) > 1500 || block == nil || block.Type != "RSA PUBLIC KEY" || len(block.Headers) != 0 ||
		strings.TrimSpace(string(rest)) != "" || strings.TrimSpace(text) != strings.TrimSpace(string(pem.EncodeToMemory(block))) {
		return nil, errors.New("invalid Matrix node public key")
	}
	key, err := x509.ParsePKCS1PublicKey(block.Bytes)
	if err != nil || key.N.BitLen() < 2048 || key.N.BitLen() > 4096 || key.E != 65537 {
		return nil, errors.New("invalid Matrix node RSA key")
	}
	return key, nil
}

func Iface(name string) string {
	return fmt.Sprintf("bm%x", sha256.Sum256([]byte(name)))[:14]
}

func Subnet(text string) (netip.Prefix, error) {
	block, err := netip.ParsePrefix(text)
	if err != nil || !block.Addr().Is4() || !block.Addr().IsPrivate() || block.Bits() != 24 || block != block.Masked() {
		return netip.Prefix{}, errors.New("enter a private IPv4 network in /24 form, such as 10.203.42.0/24")
	}
	return block, nil
}

func Validate(c config.MatrixConfig) error {
	block, err := Subnet(c.Subnet)
	if err != nil {
		return err
	}
	if len(c.Peers) > 64 || len(c.Peers) == 0 && c.Local.Name != Main {
		return errors.New("a Matrix network supports up to 64 peers and every node needs Main")
	}
	if c.MTU != 0 && (c.MTU < 576 || c.MTU > 1500) || c.TxQueueLen < 0 || c.TxQueueLen > 16384 {
		return errors.New("invalid Matrix MTU or transmit queue")
	}
	names, addresses := map[string]bool{}, map[string]bool{}
	for _, h := range append([]config.MatrixHost{c.Local}, c.Peers...) {
		ip, ipErr := netip.ParseAddr(h.IP)
		outer, outerErr := netip.ParseAddr(h.Address)
		if !nodePattern.MatchString(h.Name) || h.Port == 0 || ipErr != nil || !block.Contains(ip) ||
			ip.As4()[3] == 0 || ip.As4()[3] == 255 || outerErr != nil || outer.Zone() != "" ||
			!outer.IsGlobalUnicast() && !outer.IsLoopback() || block.Contains(outer) || names[h.Name] || addresses[h.IP] {
			return errors.New("invalid or duplicate Matrix node name, address or port")
		}
		if _, err := publicKey(h.Key); err != nil {
			return err
		}
		names[h.Name], addresses[h.IP] = true, true
	}
	if !names[Main] {
		return errors.New("the Matrix Main host is required")
	}
	key, err := publicKey(c.Local.Key)
	if err != nil {
		return err
	}
	pemKey, rest := pem.Decode([]byte(c.PrivateKey))
	if len(c.PrivateKey) > 4500 || pemKey == nil || pemKey.Type != "RSA PRIVATE KEY" || strings.TrimSpace(string(rest)) != "" {
		return errors.New("the Matrix node private key is missing")
	}
	priv, err := x509.ParsePKCS1PrivateKey(pemKey.Bytes)
	if err != nil || !priv.PublicKey.Equal(key) {
		return errors.New("the Matrix node keys do not match")
	}
	return ValidatePorts(c.Ports, c.Local.IP, c.Peers)
}

func ValidatePorts(ports []string, local string, peers []config.MatrixHost) error {
	if len(ports) == 0 {
		return nil
	}
	if len(peers) == 0 {
		return errors.New("add a Matrix peer before forwarding ports")
	}
	mappings, err := portmap.Expand(ports, peers[0].IP)
	if err != nil {
		return err
	}
	allowed := make(map[string]bool, len(peers))
	for _, peer := range peers {
		allowed[peer.IP] = peer.IP != local
	}
	for _, m := range mappings {
		for _, target := range m.Targets {
			host, _, _ := net.SplitHostPort(target)
			if !allowed[host] {
				return fmt.Errorf("forwarding target %q must be an enrolled Matrix peer's private address", host)
			}
		}
	}
	return nil
}
