package config

// BackhaulConfig describes one end of a native or legacy Backhaul-derived tunnel.
type BackhaulConfig struct {
	// Native selects the built-in BackPack implementation.
	Native bool `toml:"native,omitempty"`
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
	// Preset records which performance preset supplied the settings.
	Preset string `toml:"preset,omitempty"`
	// Lanes sets the number of parallel native stream connections.
	Lanes int `toml:"lanes,omitempty"`
	// PacketQueue sets the native TUN packet buffer size.
	PacketQueue int `toml:"packet_queue,omitempty"`
	// MTU sets the native TUN packet size.
	MTU int `toml:"mtu,omitempty"`
	// Mux controls the native XTcpMux and XWsMux options.
	Mux *BackhaulMuxOptions `toml:"mux,omitempty"`
	// HTTPPath is the request path for the native Dagger HTTP transports.
	HTTPPath string `toml:"http_path,omitempty"`
	// HTTPHost sets the host name for a native Dagger HTTP upgrade.
	HTTPHost string `toml:"http_host,omitempty"`
}

func (c BackhaulConfig) Present() bool { return c.Role != "" || c.Mode != "" }

// BackhaulMuxOptions applies to the built-in XTcpMux and XWsMux engines.
// The server-only listener and forwarding settings stay on Iran.
type BackhaulMuxOptions struct {
	// Backups lists alternative endpoints to dial.
	Backups []string `toml:"backups,omitempty"`
	// HeartbeatSeconds sets the SMUX keepalive interval.
	HeartbeatSeconds int `toml:"heartbeat_seconds,omitempty"`
	// TimeoutSeconds sets the SMUX keepalive timeout.
	TimeoutSeconds int `toml:"timeout_seconds,omitempty"`
	// IPv6 controls whether the listener also accepts IPv6.
	IPv6 *bool `toml:"ipv6,omitempty"`
	// ProxyProtocol sends the original client address to the forwarded service.
	ProxyProtocol bool `toml:"proxy_protocol,omitempty"`
	// MaxConnections bounds concurrent forwarded connections. Zero is unlimited.
	MaxConnections int `toml:"max_connections,omitempty"`
	// BandwidthMbps sets the forwarding bandwidth cap. Zero is unlimited.
	BandwidthMbps int `toml:"bandwidth_mbps,omitempty"`
}
