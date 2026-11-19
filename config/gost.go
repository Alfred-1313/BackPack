package config

// GostServerConfig carries the reverse TCP engine through a GOST v3 TLS relay.
type GostServerConfig struct {
	// Binary is the absolute path to the GOST v3 executable.
	Binary string `toml:"binary"`
	// Listen is the public TLS address, separate from the loopback reverse listener.
	Listen string `toml:"listen"`
	// Username is the account accepted by the GOST relay.
	Username string `toml:"username"`
	// Password is the GOST relay secret, separate from the reverse tunnel token.
	Password string `toml:"password"`
	// Certificate is the absolute path to the public PEM certificate chain.
	Certificate string `toml:"certificate"`
	// Key is the absolute path to the PEM private key.
	Key string `toml:"key"`
}

func (g GostServerConfig) Enabled() bool { return g != (GostServerConfig{}) }

// GostClientConfig reaches the Iran reverse engine through the GOST v3 relay.
type GostClientConfig struct {
	// Binary is the absolute path to the GOST v3 executable.
	Binary string `toml:"binary"`
	// Server is the public TLS endpoint the helper connects to and verifies.
	Server string `toml:"server"`
	// Username is the GOST relay account shared by both sides.
	Username string `toml:"username"`
	// Password is the GOST relay secret shared by both sides.
	Password string `toml:"password"`
	// CAFile is an optional absolute path to a private trust anchor in PEM format.
	CAFile string `toml:"ca_file"`
}

func (g GostClientConfig) Enabled() bool { return g != (GostClientConfig{}) }

func (c *Config) HasManagedHelper() bool {
	return c.Server.HasManagedHelper() || c.Client.HasManagedHelper()
}
func (c *ServerConfig) HasManagedHelper() bool {
	return c.Naive.Enabled() || c.Xray.Enabled() || c.Gost.Enabled()
}
func (c *ClientConfig) HasManagedHelper() bool {
	return c.Naive.Enabled() || c.Xray.Enabled() || c.Gost.Enabled()
}
