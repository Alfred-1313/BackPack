package manage

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/backpack/backpack/config"
	"github.com/backpack/backpack/internal/manage/core"
	"github.com/backpack/backpack/internal/matrix"
	"github.com/backpack/backpack/internal/tunnel/l3"
)

const removalScheme = "backpack-remove://1."

type removalLink struct {
	Name string `json:"n"`
	ID   string `json:"i"`
}

// RemovalLinkFor captures the matching identity before Iran's config is
// removed. The link carries a fingerprint and works only with a local root command.
func RemovalLinkFor(t Tunnel) (string, error) {
	if err := core.CheckName(t.Name); err != nil {
		return "", err
	}
	cfg, err := core.LoadTunnelConfig(t.Name)
	if err != nil {
		return "", err
	}
	id, side := peerRemovalIdentity(t, cfg)
	if side != "iran" || id == "" {
		return "", fmt.Errorf("this tunnel cannot identify a single Kharej end")
	}
	raw, err := json.Marshal(removalLink{Name: t.Name, ID: id})
	if err != nil {
		return "", err
	}
	return removalScheme + base64.RawURLEncoding.EncodeToString(raw), nil
}

func parseRemovalLink(raw string) (removalLink, error) {
	var link removalLink
	token, ok := strings.CutPrefix(strings.TrimSpace(raw), removalScheme)
	if !ok || len(token) > 512 {
		return link, fmt.Errorf("expected a removal link from the Iran panel")
	}
	data, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || json.Unmarshal(data, &link) != nil ||
		core.CheckName(link.Name) != nil || len(link.ID) != 2*sha256.Size {
		return removalLink{}, fmt.Errorf("invalid tunnel removal link")
	}
	if _, err := hex.DecodeString(link.ID); err != nil {
		return removalLink{}, fmt.Errorf("invalid tunnel removal fingerprint")
	}
	return link, nil
}

// RemoveLinkedPeer finds the Kharej end by its config, including tunnels
// renamed after setup. A supplied name narrows the match without bypassing it.
func RemoveLinkedPeer(raw, name string) (string, error) {
	link, err := parseRemovalLink(raw)
	if err != nil {
		return "", err
	}
	if name != "" {
		if err := core.CheckName(name); err != nil {
			return "", err
		}
	}
	match, err := findRemovalPeer(link, name, core.List(), core.LoadTunnelConfig)
	if err != nil {
		return "", err
	}
	cfg, err := core.LoadTunnelConfig(match.Name)
	if err != nil {
		return "", err
	}
	if id, side := peerRemovalIdentity(match, cfg); side != "kharej" || id != link.ID {
		return "", fmt.Errorf("the Kharej tunnel changed during removal")
	}
	if err := core.Delete(match.Name); err != nil {
		return "", err
	}
	if _, err := core.LoadTunnelConfig(match.Name); !os.IsNotExist(err) {
		return "", fmt.Errorf("the configuration for %q is still present", match.Name)
	}
	return match.Name, nil
}

func findRemovalPeer(link removalLink, name string, tunnels []Tunnel, load func(string) (config.Config, error)) (Tunnel, error) {
	return findLinkedPeer(link, "", name, tunnels, load)
}

func findLinkedPeer(link removalLink, nextID, name string, tunnels []Tunnel, load func(string) (config.Config, error)) (Tunnel, error) {
	var match Tunnel
	for _, t := range tunnels {
		if core.CheckName(t.Name) != nil || name != "" && t.Name != name {
			continue
		}
		cfg, err := load(t.Name)
		if err != nil {
			continue
		}
		id, side := peerRemovalIdentity(t, cfg)
		if side != "kharej" || id == "" || id != link.ID && id != nextID {
			continue
		}
		if match.Name != "" {
			return Tunnel{}, fmt.Errorf("several Kharej tunnels match %q; repeat with --name and the exact Kharej tunnel name", link.Name)
		}
		match = t
	}
	if match.Name == "" {
		return Tunnel{}, fmt.Errorf("no matching Kharej tunnel for %q was found on this server", link.Name)
	}
	return match, nil
}

func removalFingerprint(parts ...string) string {
	// The length of each part is encoded by JSON so separate identities
	// cannot collide by joining their fields differently.
	raw, _ := json.Marshal(parts)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func removalSide(role string) string {
	switch role {
	case "server", "iran":
		return "iran"
	case "client", "kharej":
		return "kharej"
	default:
		return ""
	}
}

func peerRemovalIdentity(t Tunnel, cfg config.Config) (string, string) {
	side := removalSide(t.Role)
	switch {
	case cfg.Matrix.Present():
		c := cfg.Matrix
		if len(c.Peers) != 1 || c.Local.Key == "" || c.Peers[0].Key == "" || c.Subnet == "" {
			return "", ""
		}
		local, peer := c.Local.Key, c.Peers[0].Key
		if (c.Direction == matrix.Reverse) == (c.Local.Name == matrix.Main) {
			side = "iran"
		} else {
			side, local, peer = "kharej", peer, local
		}
		return removalFingerprint("matrix", c.Subnet, local, peer), side
	case cfg.Pengu.Present() && cfg.Pengu.Key != "":
		c := cfg.Pengu
		return removalFingerprint("pengu", c.Direction, c.Mode, c.Key, linkPort(c.Port)), c.Side
	case cfg.Backhaul.Present() && cfg.Backhaul.Token != "":
		c := cfg.Backhaul
		return removalFingerprint("backhaul", strconv.FormatBool(c.Native), c.Mode, c.Token, linkPort(c.Port)), removalSide(c.Role)
	case cfg.Dagger.Present() && cfg.Dagger.License != "":
		c := cfg.Dagger
		// The license can be shared by several tunnels, so identify the
		// actual Dagger listener as well as the license.
		return removalFingerprint("dagger", c.Flavor, c.License, c.IranIP, c.IranIPv6,
			linkPort(c.Port), c.IranTunnelIP, c.KharejTunnelIP), removalSide(c.Role)
	case cfg.Server.BindAddr != "" && cfg.Server.Token != "":
		c := cfg.Server
		bind := orDefault(c.Xray.Listen, orDefault(c.Naive.Listen, orDefault(c.Gost.Listen, c.BindAddr)))
		return removalFingerprint("reverse", c.Token, addrPort(bind)), "iran"
	case cfg.Client.RemoteAddr != "" && cfg.Client.Token != "":
		c := cfg.Client
		peer := orDefault(c.Xray.Server, orDefault(c.Naive.Server, orDefault(c.Gost.Server, c.RemoteAddr)))
		return removalFingerprint("reverse", c.Token, addrPort(peer)), "kharej"
	case cfg.L3.Enabled():
		c := cfg.L3
		if l3.PortlessLinkCarrier(c.Carrier) {
			local, peer := hostOf(c.LocalIP), hostOf(c.PeerIP)
			localTunnelID, peerTunnelID := c.L2TPTunnelID, c.L2TPPeerTunnelID
			localSessionID, peerSessionID := c.L2TPSessionID, c.L2TPPeerSessionID
			if side == "kharej" {
				local, peer = peer, local
				localTunnelID, peerTunnelID = peerTunnelID, localTunnelID
				localSessionID, peerSessionID = peerSessionID, localSessionID
			}
			if local == "" || peer == "" || local == peer {
				return "", side
			}
			return removalFingerprint("l3", c.Carrier, local, peer, uintText(c.GREKey),
				uintText(localTunnelID), uintText(peerTunnelID), uintText(localSessionID), uintText(peerSessionID)), side
		}
		if c.Token != "" {
			return removalFingerprint("l3", c.Carrier, c.Token, addrPort(c.Addr), c.EasyTierNetwork), side
		}
	}
	return "", side
}

func uintText(n uint32) string { return strconv.FormatUint(uint64(n), 10) }
