package l3

import "github.com/sirupsen/logrus"

// OpenStreamTUN gives the authenticated stream tunnel the same Linux TUN
// device setup and packet batching as the other BackPack routed tunnels.
func OpenStreamTUN(name, local, peer string, mtu int, log *logrus.Logger) (interface {
	Read([][]byte, []int) (int, error)
	Write([][]byte) (int, error)
	BatchSize() int
	Close() error
}, error) {
	return openTUNTuned(name, local, peer, mtu, 0, 128, "", log)
}
