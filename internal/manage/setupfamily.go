package manage

import (
	"fmt"
	"net"
	"slices"
	"strings"
)

var dualSetupFamilies = []string{"ipv4", "ipv6"}
var ipv4SetupFamily = []string{"ipv4"}
var ipv6SetupFamily = []string{"ipv6"}

func DaggerSetupFamilies(mode string) []string {
	if mode == "dc6" {
		return ipv6SetupFamily
	}
	return ipv4SetupFamily
}

func DirectSetupFamilies(mode string) []string {
	switch mode {
	case "ip6gre":
		return ipv6SetupFamily
	case "gre", "xdi", "sni", "easytier":
		return dualSetupFamilies
	default:
		return ipv4SetupFamily
	}
}

func ReverseSetupFamilies(mode string) []string {
	if mode == "pck" {
		return ipv4SetupFamily
	}
	return dualSetupFamilies
}

func validateSetupFamily(family string, supported []string) error {
	if family == "" || slices.Contains(supported, family) {
		return nil
	}
	return fmt.Errorf("this transport does not support the selected IP family")
}

func setupPublicAddress(family string) (string, error) {
	var host string
	switch family {
	case "ipv6":
		host = PublicIPv6()
		if ip := net.ParseIP(host); ip != nil && ip.To4() == nil {
			return host, nil
		}
	case "ipv4":
		host = linkHost()
		if net.ParseIP(host).To4() != nil {
			return host, nil
		}
	}
	return "", fmt.Errorf("could not detect a public %s address on Iran", strings.ToUpper(family))
}

func prepareReverseAddress(n *NewTunnel) error {
	if n.SetupFamily == "" || n.Role != "server" {
		return nil
	}
	if err := validateSetupFamily(n.SetupFamily, ReverseSetupFamilies(n.Transport)); err != nil {
		return err
	}
	if n.SetupFamily != "ipv6" {
		return nil
	}
	bind, err := parseTunnelBind(n.TunnelPort)
	if err != nil {
		return err
	}
	if bind.HasHost() && net.ParseIP(bind.Host).To4() != nil {
		return fmt.Errorf("the tunnel port must be unpinned or bound to IPv6 for the IPv6 setup")
	}
	n.IPv6 = true
	if strings.TrimSpace(n.KharejDial) == "" {
		if bind.HasHost() && !net.ParseIP(bind.Host).IsUnspecified() {
			n.KharejDial, n.Host = bind.Host, bind.Host
			return nil
		}
		host, err := setupPublicAddress(n.SetupFamily)
		if err != nil {
			return err
		}
		n.KharejDial = host
	} else if ip := net.ParseIP(strings.Trim(strings.TrimSpace(n.KharejDial), "[]")); ip != nil && ip.To4() != nil {
		return fmt.Errorf("the Kharej dial address must be IPv6 or a DNS name for the IPv6 setup")
	}
	n.Host = n.KharejDial
	return nil
}
