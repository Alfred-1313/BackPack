package config

// PenguConfig describes both ends of a PenguTunnel connection.
type PenguConfig struct {
	// Mode selects a port forward, PGprotocol, or IPX profile.
	Mode string `toml:"mode"`
	// Direction selects which server dials a stream transport.
	Direction string `toml:"direction"`
	// Side selects the Iran edge or Kharej exit.
	Side string `toml:"side"`
	// IranHost is the address of the Iran endpoint.
	IranHost string `toml:"iran_host"`
	// KharejHost is the address of the Kharej endpoint when required.
	KharejHost string `toml:"kharej_host"`
	// Family defaults to IPv4 and selects the listener address family.
	Family string `toml:"family"`
	// Port is the carrier listener port for stream transports.
	Port uint16 `toml:"port"`
	// HealthPort is the health port shared by both ends.
	HealthPort uint16 `toml:"health_port"`
	// Key is the shared 32-byte base64 tunnel secret.
	Key string `toml:"key"`
	// IranTunnel assigns the Iran IPv4 interface for TUN.
	IranTunnel string `toml:"iran_tunnel"`
	// KharejTunnel assigns the Kharej IPv4 interface for TUN.
	KharejTunnel string `toml:"kharej_tunnel"`
	// Ports are the edge's forwarding rules, with UDP rules prefixed by udp/.
	Ports []string `toml:"ports"`
	// Preset sets the core's tuning profile.
	Preset string `toml:"preset"`
	// Pool sets the number of parallel stream carriers.
	Pool int `toml:"pool"`
	// Workers caps worker CPU cores. Zero enables automatic tuning.
	Workers int `toml:"workers"`
	// MTU limits the TUN interface packet size.
	MTU int `toml:"mtu"`
	// Window sets the stream receive window for No-TUN. Zero uses the core default.
	Window int `toml:"window"`
	// Cipher selects AES-GCM or ChaCha20-Poly1305.
	Cipher string `toml:"cipher"`
	// Header selects an optional PenguTunnel header for tcpfwd.
	Header string `toml:"header"`
	// TLSName is the server name used when dialing a TLS transport.
	TLSName string `toml:"tls_name"`
	// ICMPType selects the type used for the ICMP profile.
	ICMPType *int `toml:"icmp_type"`
	// ICMPCode selects the code used for the ICMP profile.
	ICMPCode int `toml:"icmp_code"`
}

func (c PenguConfig) Present() bool { return c.Mode != "" }
