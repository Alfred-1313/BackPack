package cmd

import (
	"context"
	"net"
	"os"
	"time"

	"github.com/backpack/backpack/config"
	"github.com/backpack/backpack/internal/matrix"
	"github.com/backpack/backpack/internal/metrics"
	"github.com/backpack/backpack/internal/tunnel/l3"
)

func runMatrix(ctx context.Context, path string, c config.MatrixConfig) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	name := tunnelNameFromPath(path)
	role := "node"
	if c.Local.Name == matrix.Main {
		role = matrix.Main
	}
	metrics.ClearPeer()
	stop := startMetricsWithTraffic(ctx, path, matrix.ID, role,
		func() uint64 { return matrix.Bytes(name, "rx") },
		func() uint64 { return matrix.Bytes(name, "tx") })
	defer stop()
	var peerIP string
	if len(c.Peers) > 0 {
		peerIP = c.Peers[0].IP
	}
	forward, err := l3.NewForwarder(l3.Config{Ports: c.Ports, PeerIP: peerIP, AcceptUDP: c.AcceptUDP,
		MaxConnections: 2048}, logger)
	if err != nil {
		return err
	}
	if forward != nil {
		dialer := matrix.Dialer(name)
		forward.SetDialer(func(ctx context.Context, network, addr string, _ func(error)) (net.Conn, error) {
			return dialer.DialContext(ctx, network, addr)
		})
	}
	go monitorMatrix(ctx, name, c.Peers)
	run := make(chan error, 1)
	go func() { run <- matrix.Run(ctx, name, c, os.Stderr) }()
	if forward == nil {
		return <-run
	}
	ports := make(chan error, 1)
	go func() { ports <- forward.Run(ctx) }()
	select {
	case err := <-run:
		cancel()
		<-ports
		return err
	case err := <-ports:
		cancel()
		<-run
		return err
	}
}

func monitorMatrix(ctx context.Context, name string, peers []config.MatrixHost) {
	timer := time.NewTicker(5 * time.Second)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			var address string
			for _, peer := range peers {
				if matrix.Online(name, peer.Name) {
					address = peer.IP
					break
				}
			}
			if address == "" {
				metrics.ClearPeer()
			} else {
				metrics.ReportPeer(address)
			}
		}
	}
}
