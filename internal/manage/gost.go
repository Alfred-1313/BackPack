package manage

import "github.com/backpack/backpack/internal/tui"

func configureGost(next *TunnelSpec, previous TunnelSpec, public, host string) bool {
	if next.Role == "server" {
		g := previous.GostServer
		g.Binary = tui.PromptDefault("GOST v3 Binary", defaultString(g.Binary, managedHelperPath("gost")))
		g.Listen = public
		g.Username = tui.PromptDefault("GOST Username (Same On Kharej)", defaultString(g.Username, "backpack"))
		g.Password = managedSecret("GOST Password (Same On Kharej)", g.Password, randomToken(32))
		cert, key, ok := managedCertificate(*next, host, g.Certificate, g.Key)
		if !ok {
			return false
		}
		g.Certificate, g.Key = cert, key
		next.GostServer = g
		return true
	}
	g := previous.GostClient
	g.Binary = tui.PromptDefault("GOST v3 Binary", defaultString(g.Binary, managedHelperPath("gost")))
	g.Server = public
	g.Username = tui.PromptDefault("GOST Username (From Iran)", defaultString(g.Username, "backpack"))
	g.Password = managedSecret("GOST Password (From Iran)", g.Password, "")
	g.CAFile = tui.PromptDefault("CA File (Required For A Private Certificate; Empty For Public TLS)", g.CAFile)
	next.GostClient = g
	return true
}
