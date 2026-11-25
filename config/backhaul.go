package config

// BackhaulConfig describes one end of a Backhaul Premium tunnel.
type BackhaulConfig struct {
	// Role selects the Iran server or the Kharej client.
	Role string `toml:"role"`
	// Mode selects XTcpMux, XWsMux, TUN over TCP or TUN over IPX.
	Mode string `toml:"mode"`
	// IranHost is the listener's address, or its local IPv4 address for IPX.
	IranHost string `toml:"iran_host"`
	// KharejIP is the Kharej IPv4 endpoint for IPX.
	KharejIP string `toml:"kharej_ip,omitempty"`
	// Port is the Iran listener port for TCP-based modes.
	Port uint16 `toml:"port,omitempty"`
	// Token is the shared tunnel secret. IPX uses a base64-encoded 32-byte key.
	Token string `toml:"token"`
	// Ports holds optional TCP forwarding rules on Iran.
	Ports []string `toml:"ports,omitempty"`
	// IranTunnel is the IPv4/CIDR address inside the tunnel on Iran.
	IranTunnel string `toml:"iran_tunnel,omitempty"`
	// KharejTunnel is the IPv4/CIDR address inside the tunnel on Kharej.
	KharejTunnel string `toml:"kharej_tunnel,omitempty"`
	// HealthPort is the Backhaul TUN health-check port shared by the endpoints.
	HealthPort uint16 `toml:"health_port,omitempty"`
	// IPXProfile selects the IPX outer protocol.
	IPXProfile string `toml:"ipx_profile,omitempty"`
}

func (c BackhaulConfig) Present() bool { return c.Role != "" || c.Mode != "" }
