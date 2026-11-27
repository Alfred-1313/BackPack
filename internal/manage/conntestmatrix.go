package manage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/backpack/backpack/config"
	"github.com/backpack/backpack/internal/matrix"
)

// This run's throwaway key stays inside the authenticated test control path.
type ctMatrixOffer struct {
	Side config.MatrixConfig
	Key  string
}

func ctMatrixSubnet(name string) netip.Prefix {
	sum := sha256.Sum256([]byte(name))
	return netip.PrefixFrom(netip.AddrFrom4([4]byte{10, 204 + sum[0]%8, sum[1], 0}), 24)
}

func (p *ctHelperProcess) startMatrix(name string, c config.MatrixConfig) error {
	if _, err := matrix.Binary(); err != nil {
		return err
	}
	if err := matrix.Validate(c); err != nil {
		return err
	}
	if err := matrixSubnetAvailable(ctMatrixSubnet(name)); err != nil {
		return err
	}
	p.label, p.secret = matrix.Label, c.PrivateKey
	var doc bytes.Buffer
	if err := toml.NewEncoder(&doc).Encode(struct {
		Matrix config.MatrixConfig `toml:"matrix"`
	}{c}); err != nil {
		return err
	}
	var err error
	p.engine, err = startCTEngine(p.dir, name, doc.String(), "TMPDIR="+p.dir)
	return err
}

func (s *ConnTestIran) setupMatrix(ctx context.Context, c *connTestCase, ctrl *ctExtraControl) error {
	job := ctExtraJob{Action: "port", Name: c.name, Mode: matrix.ID}
	outer, err := ctrl.exchange(ctx, c.extraWorker, job)
	if err != nil {
		return err
	}
	if outer.Port <= 0 || outer.Port > 65535 {
		return errors.New("invalid Matrix test port")
	}
	block := ctMatrixSubnet(c.name)
	mainKey, mainPublic, err := matrix.GenerateKey()
	if err != nil {
		return err
	}
	nodeKey, nodePublic, err := matrix.GenerateKey()
	if err != nil {
		return err
	}
	s.mu.Lock()
	nearPort := ctPickPort(s.helperPorts, true)
	s.mu.Unlock()
	if nearPort == 0 {
		return errors.New("no local Matrix test port")
	}
	main := matrixNode(block, 1, s.Kharej(), mainPublic, uint16(outer.Port))
	node := matrixNode(block, 2, s.link.Host, nodePublic, uint16(nearPort))
	local := config.MatrixConfig{Subnet: block.String(), Local: node, Peers: []config.MatrixHost{main}, PrivateKey: nodeKey}
	far := config.MatrixConfig{Subnet: block.String(), Local: main, Peers: []config.MatrixHost{node}}
	dir, err := os.MkdirTemp(s.dir, "matrix-")
	if err != nil {
		return err
	}
	c.helper, c.localStarted = &ctHelperProcess{dir: dir}, true
	if err := c.helper.startMatrix(c.name, local); err != nil {
		return err
	}
	body, err := json.Marshal(ctMatrixOffer{Side: far, Key: mainKey})
	if err != nil {
		return err
	}
	c.peerIP, c.peerStarted = main.IP, true
	job.Action, job.Token = "join", base64.RawURLEncoding.EncodeToString(body)
	_, err = ctrl.exchange(ctx, c.extraWorker, job)
	return err
}

func (w *ctExtraInstance) joinMatrix(ctx context.Context, link ConnTestLink, job ctExtraJob) error {
	var offer ctMatrixOffer
	data, err := base64.RawURLEncoding.DecodeString(job.Token)
	if err != nil || len(data) > 10000 || json.Unmarshal(data, &offer) != nil {
		return errors.New("invalid Matrix test setup")
	}
	c := offer.Side
	block := ctMatrixSubnet(job.Name)
	if c.Subnet != block.String() || c.Local.Name != matrix.Main || c.Local.IP != block.Addr().Next().String() ||
		len(c.Peers) != 1 || c.Peers[0].Address != link.Host || c.Peers[0].Name != "node_2" ||
		c.Peers[0].IP != block.Addr().Next().Next().String() || len(c.Ports) != 0 {
		return errors.New("unexpected Matrix test settings")
	}
	c.PrivateKey = offer.Key
	dir, err := os.MkdirTemp(w.dir, "matrix-")
	if err != nil {
		return err
	}
	w.name, w.mode, w.helper = job.Name, matrix.ID, &ctHelperProcess{dir: dir}
	if err := w.helper.startMatrix(job.Name, c); err != nil {
		return err
	}
	echoCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	addr := net.JoinHostPort(c.Local.IP, fmt.Sprint(link.L3))
	for echoCtx.Err() == nil {
		if listener, err := net.Listen("tcp4", addr); err == nil {
			w.echo = &ctEchoes{}
			w.echo.add(listener)
			go ctServeTCPEcho(listener, w.echo)
			return nil
		}
		if exited, detail := w.helper.startupFailure(); exited {
			return errors.New(detail)
		}
		ctSleep(echoCtx, 100*time.Millisecond)
	}
	return errors.New("Matrix did not bring up its test interface")
}
