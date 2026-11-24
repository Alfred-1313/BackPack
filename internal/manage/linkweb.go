package manage

import (
	"fmt"
	"strings"

	"github.com/backpack/backpack/internal/app"
	"github.com/backpack/backpack/internal/tunnel/l3"
)

// greHostPlaceholder stands in the kharej's command for the Iran server's
// address when the GRE link could not name it.
const greHostPlaceholder = "IRAN_SERVER_IP"

// The setup link, as the web panel shows it.
//
// The menu prints a tunnel's link with the two lines that build the other end
// (printShareLink); the panel needs the same facts as data. This is that, and
// nothing else: which side the link is for, the link, and the commands.

// SetupLinkInfo is one tunnel's setup link and how to use it.
type SetupLinkInfo struct {
	Name string `json:"name"`
	Link string `json:"link"`
	// For is the side the link builds: "kharej" or "iran".
	For  string `json:"for"`
	Kind string `json:"kind"` // reverse | direct
	// Apply and Install are the two commands for a kharej: with Backpack
	// already on it, and without. Empty when the link is for the Iran side,
	// which is built from its menu.
	Apply   string `json:"apply,omitempty"`
	Install string `json:"install,omitempty"`
	// Where is the menu path on the other server that takes the link.
	Where string `json:"where"`
	// Host is the address of this server the link names; empty when none was
	// known, and then NeedsAddress says the other side will ask for it.
	Host         string `json:"host,omitempty"`
	NeedsAddress bool   `json:"needsAddress,omitempty"`
	// GRE is a kernel GRE tunnel's link, which carries a GRE key and no
	// token, so the screen's warning names what it does hold.
	GRE bool `json:"gre,omitempty"`
	// GRETAP is configured from the other server's Add tunnel form.
	GRETAPPeer      *PeerForm `json:"gretapPeer,omitempty"`
	PanelDirectPeer *PeerForm `json:"panelDirectPeer,omitempty"`
}

// SetupLinkFor builds a tunnel's setup link. host overrides the address the
// link names; empty uses this server's public address, as the menu does.
func SetupLinkFor(name, host string) (SetupLinkInfo, error) {
	if cfg, err := LoadTunnelConfig(name); err == nil && cfg.Dagger.Present() {
		if cfg.Dagger.Role != "server" {
			return SetupLinkInfo{}, fmt.Errorf("open Setup link on Iran to get the DaggerConnect join command")
		}
		token := daggerPeerToken(name, cfg.Dagger)
		return SetupLinkInfo{Name: name, For: "kharej", Kind: "reverse", Apply: DaggerJoinCommand(token),
			Install: "printf '%s\\n' '" + token + "' | " + app.SourceInstallCommand("dagger", "join"), Where: "Kharej terminal"}, nil
	}
	host = strings.Trim(strings.TrimSpace(host), "[]")
	if dial := LinkDial(name); dial != "" {
		host = dial
	}
	if host == "" {
		host = linkHost()
	}
	raw, err := shareLinkFor(name, host)
	if err != nil {
		return SetupLinkInfo{}, err
	}
	l, err := DecodeShareLink(raw)
	if err != nil {
		return SetupLinkInfo{}, fmt.Errorf("the link does not read back: %w", err)
	}
	info := SetupLinkInfo{Name: name, Link: raw, For: l.PeerSide(), Kind: l.Kind, Host: host,
		NeedsAddress: host == "" && l.PeerNeedsAddress()}
	// A GRE link's kharej names this server, so the address the link holds is
	// the one that counts, and without it `link apply` refuses: the line has to
	// carry --host, and the screen has to say which address goes there.
	gre := l3.PortlessLinkCarrier(l.Tr) && l.Kind == "direct"
	if gre {
		info.Host, info.NeedsAddress, info.GRE = l.Host, l.Host == "", true
	}
	if l.Kind == "direct" && l.Tr == l3.CarrierGRETAP {
		peer := MirrorForPeer(l)
		if _, prefix, ok := strings.Cut(l.LocalIP, "/"); ok && peer.LocalIP != "" && !strings.Contains(peer.LocalIP, "/") {
			peer.LocalIP += "/" + prefix
		}
		info.GRETAPPeer, info.Where = &peer, "Add a tunnel → Direct → GRETAP"
		return info, nil
	}
	if l.Kind == "direct" && (l.Tr == l3.CarrierL2TPv3 || l.Tr == l3.CarrierEasyTier) {
		peer := MirrorForPeer(l)
		if _, prefix, ok := strings.Cut(l.LocalIP, "/"); ok && peer.LocalIP != "" && !strings.Contains(peer.LocalIP, "/") {
			peer.LocalIP += "/" + prefix
		}
		info.PanelDirectPeer, info.Where = &peer, "Add a tunnel → Direct → "+carrierLabel(l.Tr)
		return info, nil
	}
	if info.For == "kharej" {
		info.Apply = "sudo backpack link apply '" + raw + "'"
		if gre && l.Host == "" {
			info.Apply = "sudo backpack link apply --host " + greHostPlaceholder + " '" + raw + "'"
		}
		info.Install = InstallCommand(raw)
		info.Where = "sudo backpack → Setup Kharej → Setup Link"
		if managedTransport(l.Tr) {
			info.Where = managedPeerMenu(l.Tr)
		}
	} else {
		info.Where = "sudo backpack → Manage → Set Up From A Link"
	}
	return info, nil
}
