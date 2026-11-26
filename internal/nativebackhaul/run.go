package nativebackhaul

import (
	"context"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/backpack/backpack/config"
	"github.com/backpack/backpack/internal/backhaulpremium"
	"github.com/backpack/backpack/internal/metrics"
	"github.com/backpack/backpack/internal/tunnel/l3"
	"github.com/backpack/backpack/internal/tunnel/limits"
	"github.com/backpack/backpack/internal/tunnel/portmap"
	"github.com/backpack/backpack/internal/utils/handlers"
	"github.com/sirupsen/logrus"
	"github.com/xtaci/smux"
)

const laneCount = 4
const tunQueue = 64
const tunMTU = 1400

func Run(ctx context.Context, name string, c config.BackhaulConfig, log *logrus.Logger) error {
	creds, err := linkTLS(c)
	if err != nil {
		return err
	}
	if c.Mode == "tun-tcp" {
		return runTUN(ctx, name, c, creds, log)
	}
	if c.Role == "server" {
		return runMuxServer(ctx, c, creds, log)
	}
	addr := net.JoinHostPort(c.IranHost, strconv.Itoa(int(c.Port)))
	endpoints := []string{addr}
	if c.Mux != nil {
		backups, err := BackupAddresses(c.Mux.Backups, c.IranHost, c.Port)
		if err != nil {
			return err
		}
		endpoints = append(endpoints, backups...)
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var workers sync.WaitGroup
	peers := &lanePool{limit: c.Lanes}
	lanes := c.Lanes
	if lanes == 0 {
		lanes = laneCount
	}
	for range lanes {
		workers.Go(func() {
			retryLane(ctx, c, endpoints, creds, func(conn net.Conn) {
				session, err := smux.Server(conn, muxConfig(c.Mux))
				if err != nil {
					return
				}
				defer session.Close()
				remove, ok := peers.add(session, conn.RemoteAddr())
				if !ok {
					return
				}
				defer remove()
				serveStreams(ctx, session, log)
			})
		})
	}
	workers.Wait()
	return nil
}

type lanePool struct {
	mu    sync.Mutex
	live  []*smux.Session
	next  int
	limit int
}

func (p *lanePool) add(s *smux.Session, peer net.Addr) (func(), bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	limit := p.limit
	if limit == 0 {
		limit = laneCount
	}
	if len(p.live) >= limit*2 {
		return nil, false
	}
	p.live = append(p.live, s)
	metrics.ReportPeer(peer.String())
	return func() {
		p.mu.Lock()
		defer p.mu.Unlock()
		for i, lane := range p.live {
			if lane == s {
				p.live = append(p.live[:i], p.live[i+1:]...)
				break
			}
		}
		if len(p.live) == 0 {
			metrics.ClearPeer()
		}
	}, true
}

func (p *lanePool) candidates() []*smux.Session {
	p.mu.Lock()
	defer p.mu.Unlock()
	size := len(p.live)
	if size == 0 {
		return nil
	}
	i := p.next % size
	p.next = (i + 1) % size
	return append(append(make([]*smux.Session, 0, size), p.live[i:]...), p.live[:i]...)
}

func runMuxServer(ctx context.Context, c config.BackhaulConfig, t *tls.Config, log *logrus.Logger) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	pool := &lanePool{limit: c.Lanes}
	var caps *limits.Limiter
	proxy := false
	if c.Mux != nil {
		caps = limits.New(limits.Config{MaxConnections: c.Mux.MaxConnections, BandwidthMbps: c.Mux.BandwidthMbps})
		proxy = c.Mux.ProxyProtocol
	}
	maps, err := portmap.Expand(c.Ports, "127.0.0.1")
	if err != nil {
		return err
	}
	var listeners []net.Listener
	defer func() {
		for _, l := range listeners {
			l.Close()
		}
	}()
	for _, mapping := range maps {
		l, err := net.Listen("tcp", mapping.Listen)
		if err != nil {
			return err
		}
		listeners = append(listeners, l)
	}
	for i, l := range listeners {
		mapping := maps[i]
		go acceptUsers(l, func(conn net.Conn) {
			defer conn.Close()
			if !caps.Acquire() {
				return
			}
			defer caps.Release()
			for _, session := range pool.candidates() {
				for _, target := range mapping.Targets {
					stream, err := openTarget(session, target)
					if err == nil {
						front := caps.Wrap(ctx, metrics.CountedConn(conn))
						defer front.Close()
						handlers.TCPConnectionHandler(ctx, proxy, front, stream, log, nil, 0, false)
						return
					}
					if session.IsClosed() {
						break
					}
				}
			}
		})
	}
	return listenLanes(ctx, c, ":"+strconv.Itoa(int(c.Port)), t, func(conn net.Conn) {
		defer conn.Close()
		session, err := smux.Client(conn, muxConfig(c.Mux))
		if err != nil {
			return
		}
		defer session.Close()
		remove, ok := pool.add(session, conn.RemoteAddr())
		if !ok {
			return
		}
		defer remove()
		select {
		case <-ctx.Done():
		case <-session.CloseChan():
		}
	})
}

func acceptUsers(l net.Listener, handle func(net.Conn)) {
	slots := make(chan struct{}, 256)
	for {
		c, err := l.Accept()
		if err != nil {
			return
		}
		select {
		case slots <- struct{}{}:
			go func() { defer func() { <-slots }(); handle(c) }()
		default:
			c.Close()
		}
	}
}

func openTarget(session *smux.Session, target string) (*smux.Stream, error) {
	if len(target) < 3 || len(target) > 255 {
		return nil, errors.New("invalid native tunnel target")
	}
	stream, err := session.OpenStream()
	if err != nil {
		return nil, err
	}
	_ = stream.SetDeadline(time.Now().Add(linkTimeout))
	header := append([]byte{byte(len(target))}, target...)
	if _, err = stream.Write(header); err == nil {
		var result [1]byte
		_, err = io.ReadFull(stream, result[:])
		if err == nil && result[0] != 1 {
			err = errors.New("peer could not open forwarded target")
		}
	}
	if err != nil {
		stream.Close()
		return nil, err
	}
	_ = stream.SetDeadline(time.Time{})
	return stream, nil
}

func serveStreams(ctx context.Context, s *smux.Session, log *logrus.Logger) {
	stop := context.AfterFunc(ctx, func() { s.Close() })
	defer stop()
	slots := make(chan struct{}, 256)
	for {
		stream, err := s.AcceptStream()
		if err != nil {
			return
		}
		select {
		case slots <- struct{}{}:
			go func() {
				defer func() { <-slots }()
				defer stream.Close()
				_ = stream.SetDeadline(time.Now().Add(linkTimeout))
				var frame [256]byte
				if _, err := io.ReadFull(stream, frame[:1]); err != nil || frame[0] == 0 {
					return
				}
				size := int(frame[0])
				if _, err := io.ReadFull(stream, frame[1:1+size]); err != nil {
					return
				}
				target := string(frame[1 : 1+size])
				dial := net.Dialer{Timeout: 5 * time.Second}
				conn, err := dial.DialContext(ctx, "tcp", target)
				if err != nil {
					stream.Write([]byte{0})
					return
				}
				defer conn.Close()
				if _, err := stream.Write([]byte{1}); err != nil {
					return
				}
				_ = stream.SetDeadline(time.Time{})
				handlers.TCPConnectionHandler(ctx, false, stream, metrics.CountedConn(conn), log, nil, 0, false)
			}()
		default:
			stream.Close()
		}
	}
}

func retryLane(ctx context.Context, c config.BackhaulConfig, endpoints []string, t *tls.Config, handle func(net.Conn)) {
	for ctx.Err() == nil {
		for _, addr := range endpoints {
			if ctx.Err() != nil {
				return
			}
			conn, err := dialLane(ctx, c, addr, t)
			if err == nil {
				handle(conn)
				conn.Close()
				break
			}
		}
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
		case <-timer.C:
		}
		timer.Stop()
	}
}

type packetLink interface {
	Read([][]byte, []int) (int, error)
	Write([][]byte) (int, error)
	BatchSize() int
	Close() error
}

func runTUN(ctx context.Context, name string, c config.BackhaulConfig, t *tls.Config, log *logrus.Logger) error {
	mtu, queue := c.MTU, c.PacketQueue
	if mtu == 0 {
		mtu = tunMTU
	}
	if queue == 0 {
		queue = tunQueue
	}
	local, peer := c.IranTunnel, c.KharejTunnel
	if c.Role == "client" {
		local, peer = peer, local
	}
	localIP, _, _ := net.ParseCIDR(local)
	peerIP, _, _ := net.ParseCIDR(peer)
	dev, err := l3.OpenStreamTUN(backhaulpremium.DeviceName(name), local, peerIP.String(), mtu, log)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var closing sync.Once
	closeDevice := func() { closing.Do(func() { _ = dev.Close() }) }
	stopDevice := context.AfterFunc(ctx, closeDevice)
	defer stopDevice()
	defer closeDevice()
	outgoing := make(chan []byte, queue)
	go readPackets(ctx, dev, outgoing)
	var one sync.Mutex
	handle := func(conn net.Conn) {
		defer conn.Close()
		if !one.TryLock() {
			return
		}
		defer one.Unlock()
		metrics.ReportPeer(conn.RemoteAddr().String())
		defer metrics.ClearPeer()
		exchangePackets(ctx, conn, dev, outgoing, localIP, peerIP, mtu)
	}
	addr := strconv.Itoa(int(c.Port))
	if c.Role == "client" {
		retryLane(ctx, c, []string{net.JoinHostPort(c.IranHost, addr)}, t, handle)
		return nil
	}
	forward, err := l3.NewForwarder(l3.Config{PeerIP: peerIP.String(), Ports: c.Ports}, log)
	if err != nil {
		return err
	}
	if forward != nil {
		go func() {
			if err := forward.Run(ctx); err != nil && ctx.Err() == nil {
				log.Error(err)
				cancel()
			}
		}()
	}
	return listenLanes(ctx, c, ":"+addr, t, handle)
}

func readPackets(ctx context.Context, dev packetLink, output chan<- []byte) {
	buf := make([][]byte, dev.BatchSize())
	for i := range buf {
		buf[i] = make([]byte, 65536)
	}
	sizes := make([]int, len(buf))
	for ctx.Err() == nil {
		n, err := dev.Read(buf, sizes)
		if err != nil {
			return
		}
		for i := range n {
			if len(output) == cap(output) {
				break
			}
			packet := append([]byte(nil), buf[i][:sizes[i]]...)
			select {
			case output <- packet:
			default:
			}
		}
	}
}

// A zero-length frame is a heartbeat. Packets have a fixed upper bound even
// during a remote protocol error.
func exchangePackets(ctx context.Context, conn net.Conn, dev packetLink, output <-chan []byte, local, peer net.IP, mtu int) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer cancel()
		defer conn.Close()
		interval := time.NewTicker(5 * time.Second)
		defer interval.Stop()
		frame := make([]byte, mtu+2)
		for ctx.Err() == nil {
			var packet []byte
			select {
			case <-ctx.Done():
				return
			case packet = <-output:
			case <-interval.C:
			}
			if len(packet) > mtu {
				continue
			}
			binary.BigEndian.PutUint16(frame, uint16(len(packet)))
			copy(frame[2:], packet)
			conn.SetWriteDeadline(time.Now().Add(8 * time.Second))
			if _, err := conn.Write(frame[:2+len(packet)]); err != nil {
				return
			}
			metrics.AddBytes(0, uint64(len(packet)))
		}
	}()
	defer func() { conn.Close(); <-done }()
	frame := make([]byte, mtu)
	for ctx.Err() == nil {
		conn.SetReadDeadline(time.Now().Add(16 * time.Second))
		if _, err := io.ReadFull(conn, frame[:2]); err != nil {
			return
		}
		n := int(binary.BigEndian.Uint16(frame))
		if n == 0 {
			continue
		}
		if n < 20 || n > mtu {
			return
		}
		if _, err := io.ReadFull(conn, frame[:n]); err != nil {
			return
		}
		if frame[0]>>4 != 4 || !net.IP(frame[12:16]).Equal(peer) || !net.IP(frame[16:20]).Equal(local) {
			continue
		}
		if _, err := dev.Write([][]byte{frame[:n]}); err != nil {
			return
		}
		metrics.AddBytes(uint64(n), 0)
	}
}
