package config

// DaggerConfig describes one endpoint of a DaggerConnect tunnel.
type DaggerConfig struct {
	// Role is server on Iran and client on Kharej.
	Role string `toml:"role"`
	// Flavor selects the DaggerConnect transport or Quantum Gaming profile.
	Flavor string `toml:"flavor"`
	// IranIP is the Iran endpoint's public IPv4.
	IranIP string `toml:"iran_ip"`
	// KharejIP is the other endpoint's IPv4 for the TUN carrier.
	KharejIP string `toml:"kharej_ip"`
	// IranIPv6 is the Iran endpoint's IPv6 for DC6.
	IranIPv6 string `toml:"iran_ipv6"`
	// Port is the DaggerConnect tunnel port.
	Port uint16 `toml:"port"`
	// License is the DaggerConnect PSK issued for the two endpoints.
	License string `toml:"license"`
	// Ports are comma-separated mappings, optionally prefixed by tcp: or udp:.
	Ports string `toml:"ports"`
	// LoopbackOnly keeps temporary forwarded listeners private.
	LoopbackOnly bool `toml:"loopback_only,omitempty"`
	// HTTPHost is the host name sent by the HTTP or HTTPS transport.
	HTTPHost string `toml:"http_host"`
	// HTTPPath is the path sent by the HTTP or HTTPS transport.
	HTTPPath string `toml:"http_path"`
	// CertFile is the HTTPS listener's certificate file.
	CertFile string `toml:"cert_file"`
	// KeyFile is the HTTPS listener's private key file.
	KeyFile string `toml:"key_file"`
	// TUNProfile is the encapsulation of the TUN transport.
	TUNProfile string `toml:"tun_profile"`
	// TUNPort is the service port for the TCP and UDP TUN profiles.
	TUNPort uint16 `toml:"tun_port"`
	// IranTunnelIP is the Iran endpoint's IPv4 inside the TUN transport.
	IranTunnelIP string `toml:"iran_tunnel_ip"`
	// KharejTunnelIP is the Kharej endpoint's IPv4 inside the TUN transport.
	KharejTunnelIP string `toml:"kharej_tunnel_ip"`
}

func (c DaggerConfig) Present() bool { return c.Role != "" || c.Flavor != "" }
