package manage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/backpack/backpack/config"
	"github.com/backpack/backpack/internal/app"
	"github.com/backpack/backpack/internal/manage/core"
	"github.com/backpack/backpack/internal/matrix"
)

type MatrixForm struct {
	Direction   string      `json:"direction"`
	Name        string      `json:"name"`
	Main        string      `json:"main"`
	Subnet      string      `json:"subnet"`
	Port        uint16      `json:"port"`
	Ports       string      `json:"ports"`
	AcceptUDP   bool        `json:"acceptUdp"`
	SetupFamily string      `json:"setupFamily"`
	Tuning      SetupTuning `json:"tuning"`
}

type MatrixPeerStatus struct {
	Name, IP, Address string
	Online            bool
}

type MatrixStatus struct {
	Role, Subnet, IP, Direction string
	Peers                       []MatrixPeerStatus
	Ports                       []string
	AcceptUDP                   bool
}

type MatrixEdit struct {
	Name       string `json:"name"`
	RemovePeer string `json:"removePeer"`
	Ports      string `json:"ports"`
	AcceptUDP  bool   `json:"acceptUdp"`
}

var matrixMu sync.Mutex

func matrixHost(family string) (string, error) {
	if err := validateSetupFamily(family, dualSetupFamilies); err != nil {
		return "", err
	}
	return setupPublicAddress(orDefault(family, "ipv4"))
}

func matrixSubnetAvailable(block netip.Prefix) error {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return err
	}
	for _, addr := range addrs {
		if other, err := netip.ParsePrefix(addr.String()); err == nil && block.Overlaps(other) {
			return fmt.Errorf("Matrix subnet %s overlaps a local interface", block)
		}
	}
	for _, t := range core.List() {
		if t.Transport != matrix.ID {
			continue
		}
		if cfg, err := core.LoadTunnelConfig(t.Name); err == nil {
			if other, err := matrix.Subnet(cfg.Matrix.Subnet); err == nil && block.Overlaps(other) {
				return fmt.Errorf("Matrix subnet %s is assigned to %s", block, t.Name)
			}
		}
	}
	return nil
}

// SuggestMatrixSubnet finds a private /24 available on this server.
func SuggestMatrixSubnet(previous string) (netip.Prefix, error) {
	exclude, _ := netip.ParsePrefix(strings.TrimSpace(previous))
	offset := rand.IntN(256)
	for i := range 256 {
		ip := netip.AddrFrom4([4]byte{10, 203, byte(offset + i), 0})
		block := netip.PrefixFrom(ip, 24)
		if block != exclude.Masked() && matrixSubnetAvailable(block) == nil {
			return block, nil
		}
	}
	return netip.Prefix{}, errors.New("no free Matrix subnet could be detected")
}

func matrixNode(block netip.Prefix, id int, addr, key string, port uint16) config.MatrixHost {
	ip := block.Addr().As4()
	ip[3] = byte(id)
	name := matrix.Main
	if id > 1 {
		name = "node_" + strconv.Itoa(id)
	}
	return config.MatrixHost{Name: name, IP: netip.AddrFrom4(ip).String(), Address: addr, Key: key, Port: port}
}

func (f MatrixForm) connectionDirection() (string, error) {
	direction := f.Direction
	// Older clients set Main directly. Translate that into the same direction
	// instead of changing which end listens when those clients are upgraded.
	if direction == "" {
		direction = matrix.Direct
		if f.Main == "iran" {
			direction = matrix.Reverse
		}
	}
	var main string
	switch direction {
	case matrix.Direct:
		main = "kharej"
	case matrix.Reverse:
		main = "iran"
	default:
		return "", errors.New("choose Direct or Reverse for Matrix")
	}
	if f.Main != "" && f.Main != main {
		return "", errors.New("the Matrix Main server must match the selected direction")
	}
	return direction, nil
}

func StartMatrixSetup(ctx, lifetime context.Context, f MatrixForm) (SetupLinkInfo, error) {
	f.Name = strings.TrimSpace(f.Name)
	if err := core.CheckName(f.Name); err != nil {
		return SetupLinkInfo{}, err
	}
	if _, err := os.Stat(app.ConfigPath(f.Name)); !os.IsNotExist(err) {
		return SetupLinkInfo{}, errors.New("a tunnel with this name already exists")
	}
	direction, err := f.connectionDirection()
	if err != nil {
		return SetupLinkInfo{}, err
	}
	host, err := matrixHost(f.SetupFamily)
	if err != nil {
		return SetupLinkInfo{}, err
	}
	var block netip.Prefix
	if strings.TrimSpace(f.Subnet) == "" {
		block, err = SuggestMatrixSubnet("")
	} else {
		block, err = matrix.Subnet(strings.TrimSpace(f.Subnet))
		if err == nil {
			err = matrixSubnetAvailable(block)
		}
	}
	if err != nil {
		return SetupLinkInfo{}, err
	}
	if f.Port == 0 {
		f.Port = matrix.DefaultPort
	}
	queue, err := setupTuningValue(matrix.ID, f.Tuning)
	if err != nil {
		return SetupLinkInfo{}, err
	}
	mtu := 0
	if f.Tuning.MTU != nil {
		mtu = *f.Tuning.MTU
	}
	private, public, err := matrix.GenerateKey()
	if err != nil {
		return SetupLinkInfo{}, err
	}
	localID, remoteID := matrix.EndpointIDs(direction)
	local := matrixNode(block, localID, host, public, f.Port)
	ports := parsePorts(f.Ports)
	if err := matrix.ValidatePorts(ports, local.IP, []config.MatrixHost{matrixNode(block, remoteID, host, "", f.Port)}); err != nil {
		return SetupLinkInfo{}, err
	}
	if err := matrix.Ensure(ctx); err != nil {
		return SetupLinkInfo{}, err
	}
	return beginPeerJoinProtocol(lifetime, host, f.Name, direction, true, func(ctx context.Context, _, addr, key string) (peerJoinResult, error) {
		peer := matrixNode(block, remoteID, addr, key, f.Port)
		near := config.MatrixConfig{Direction: direction, Subnet: block.String(), Local: local, Peers: []config.MatrixHost{peer},
			PrivateKey: private, Ports: ports, AcceptUDP: f.AcceptUDP, MTU: mtu, TxQueueLen: queue}
		if _, _, err := installMatrix(ctx, f.Name, near); err != nil {
			return peerJoinResult{}, err
		}
		far := config.MatrixConfig{Direction: direction, Subnet: near.Subnet, Local: peer, Peers: []config.MatrixHost{local}, MTU: mtu, TxQueueLen: queue}
		return peerJoinResult{Driver: matrix.ID, Name: f.Name, Mode: matrix.ID, Matrix: &far}, nil
	})
}

func installMatrix(ctx context.Context, name string, c config.MatrixConfig) (string, bool, error) {
	if err := core.CheckName(name); err != nil {
		return "", false, err
	}
	if err := matrix.Validate(c); err != nil {
		return "", false, err
	}
	matrixMu.Lock()
	defer matrixMu.Unlock()
	if previous, err := core.LoadTunnelConfig(name); err == nil && previous.Matrix.Local.Key == c.Local.Key {
		service := app.ServiceName(name)
		return service, core.IsActive(service), nil
	}
	block, _ := matrix.Subnet(c.Subnet)
	if err := matrixSubnetAvailable(block); err != nil {
		return "", false, err
	}
	return installHelperTunnel(ctx, name, struct {
		Matrix config.MatrixConfig `toml:"matrix"`
	}{c}, matrix.Ensure)
}

func matrixWaitPeer(name string, peer config.MatrixHost) func(time.Duration) (bool, string) {
	return func(within time.Duration) (bool, string) {
		ctx, cancel := context.WithTimeout(context.Background(), within)
		defer cancel()
		ticker := time.NewTicker(300 * time.Millisecond)
		defer ticker.Stop()
		for {
			if matrix.Online(name, peer.Name) {
				return true, "Matrix peer " + peer.IP + " authenticated by Tinc"
			}
			select {
			case <-ctx.Done():
				return false, "Tinc has not connected to " + peer.IP
			case <-ticker.C:
			}
		}
	}
}

// StartMatrixInvite makes one expiring enrolment for an existing Main.
func StartMatrixInvite(ctx context.Context, name string) (SetupLinkInfo, error) {
	cfg, err := core.LoadTunnelConfig(name)
	if err != nil || cfg.Matrix.Local.Name != matrix.Main {
		return SetupLinkInfo{}, errors.New("open node invitations on the Matrix Main server")
	}
	host := cfg.Matrix.Local.Address
	return beginPeerJoinProtocol(ctx, host, name, orDefault(cfg.Matrix.Direction, matrix.Direct), true, func(_ context.Context, _, addr, key string) (peerJoinResult, error) {
		matrixMu.Lock()
		defer matrixMu.Unlock()
		cfg, err := core.LoadTunnelConfig(name)
		if err != nil || cfg.Matrix.Local.Name != matrix.Main {
			return peerJoinResult{}, errors.New("the Matrix Main was removed")
		}
		c := cfg.Matrix
		if len(c.Peers) >= 64 {
			return peerJoinResult{}, errors.New("this Matrix network has 64 nodes")
		}
		block, err := matrix.Subnet(c.Subnet)
		if err != nil {
			return peerJoinResult{}, err
		}
		var peer config.MatrixHost
		for id := 2; id < 255; id++ {
			peer = matrixNode(block, id, addr, key, c.Local.Port)
			if !slices.ContainsFunc(c.Peers, func(other config.MatrixHost) bool { return other.IP == peer.IP }) {
				break
			}
		}
		c.Peers = append(c.Peers, peer)
		if err := saveMatrix(name, c); err != nil {
			return peerJoinResult{}, err
		}
		far := config.MatrixConfig{Direction: c.Direction, Subnet: c.Subnet, Local: peer, Peers: []config.MatrixHost{c.Local}, MTU: c.MTU, TxQueueLen: c.TxQueueLen}
		return peerJoinResult{Driver: matrix.ID, Name: name, Mode: matrix.ID, Matrix: &far}, nil
	})
}

func ReadMatrixStatus(name string) (MatrixStatus, error) {
	cfg, err := core.LoadTunnelConfig(name)
	if err != nil || !cfg.Matrix.Present() {
		return MatrixStatus{}, errors.New("Matrix network not found")
	}
	c := cfg.Matrix
	role := "node"
	if c.Local.Name == matrix.Main {
		role = matrix.Main
	}
	out := MatrixStatus{Role: role, Subnet: c.Subnet, IP: c.Local.IP, Direction: c.Direction, Ports: c.Ports,
		AcceptUDP: c.AcceptUDP, Peers: make([]MatrixPeerStatus, len(c.Peers))}
	for i, peer := range c.Peers {
		out.Peers[i] = MatrixPeerStatus{Name: peer.Name, IP: peer.IP, Address: peer.Address, Online: matrix.Online(name, peer.Name)}
	}
	return out, nil
}

func EditMatrix(f MatrixEdit) error {
	matrixMu.Lock()
	defer matrixMu.Unlock()
	cfg, err := core.LoadTunnelConfig(f.Name)
	if err != nil || !cfg.Matrix.Present() {
		return errors.New("Matrix network not found")
	}
	c := cfg.Matrix
	if f.RemovePeer != "" {
		if c.Local.Name != matrix.Main {
			return errors.New("remove nodes from Matrix Main")
		}
		index := slices.IndexFunc(c.Peers, func(h config.MatrixHost) bool { return h.Name == f.RemovePeer })
		if index < 0 {
			return errors.New("Matrix peer not found")
		}
		// Keep an implicit mapping pointed to the original node. Deleting it
		// requires clearing/repointing the mapping first.
		if index == 0 && len(c.Ports) > 0 {
			return errors.New("clear existing forwarded ports before removing the first Matrix node")
		}
		c.Peers = slices.Delete(c.Peers, index, index+1)
	} else {
		c.Ports, c.AcceptUDP = parsePorts(f.Ports), f.AcceptUDP
	}
	return saveMatrix(f.Name, c)
}

func saveMatrix(name string, c config.MatrixConfig) error {
	if err := matrix.Validate(c); err != nil {
		return err
	}
	var body bytes.Buffer
	if err := toml.NewEncoder(&body).Encode(struct {
		Matrix config.MatrixConfig `toml:"matrix"`
	}{c}); err != nil {
		return err
	}
	path := app.ConfigPath(name)
	previous, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := writeMatrixConfig(path, body.Bytes()); err != nil {
		return err
	}
	service := app.ServiceName(name)
	if !core.IsActive(service) {
		return nil
	}
	if err := core.RestartService(service); err != nil {
		if restore := writeMatrixConfig(path, previous); restore != nil {
			return errors.Join(err, restore)
		}
		return errors.Join(err, core.RestartService(service))
	}
	return nil
}

func writeMatrixConfig(path string, body []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".matrix-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	_, err = tmp.Write(body)
	if err := errors.Join(err, tmp.Close()); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
