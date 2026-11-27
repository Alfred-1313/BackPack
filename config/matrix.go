package config

// Matrix is one node of a BackPack-managed Tinc network. The private key
// belongs to this node alone. Peers carry host records with public keys.
type MatrixConfig struct {
	// Subnet is the private IPv4 /24 shared by this Matrix network.
	Subnet string `toml:"subnet" json:"subnet"`
	// Local contains this node's identity and addresses.
	Local MatrixHost `toml:"local" json:"local"`
	// Peers lists enrolled public host records. A node needs a record for Main.
	Peers []MatrixHost `toml:"peers" json:"peers"`
	// PrivateKey is this node's local RSA private key in PEM format.
	PrivateKey string `toml:"private_key" json:"-"`
	// Ports maps local ports to ports on enrolled private addresses.
	Ports []string `toml:"ports" json:"ports,omitempty"`
	// AcceptUDP also enables UDP for the configured port mappings.
	AcceptUDP bool `toml:"accept_udp" json:"acceptUdp,omitempty"`
	// MTU sets the Tinc interface's packet size. Zero selects 1350 bytes.
	MTU int `toml:"mtu" json:"mtu,omitempty"`
	// TxQueueLen sets the interface transmit queue. Zero selects 1000 packets.
	TxQueueLen int `toml:"txqueuelen" json:"txQueueLen,omitempty"`
}

type MatrixHost struct {
	// Name is the Tinc identity, either main or node_N.
	Name string `toml:"name" json:"name"`
	// Address is this host's reachable outer IPv4 or IPv6 address.
	Address string `toml:"address" json:"address"`
	// IP is the node's private IPv4 address inside Subnet.
	IP string `toml:"ip" json:"ip"`
	// Port is the daemon's TCP and UDP listening port.
	Port uint16 `toml:"port" json:"port"`
	// Key is the host's public RSA key in PEM format.
	Key string `toml:"key" json:"key"`
}

func (c MatrixConfig) Present() bool { return c.Local.Name != "" }
