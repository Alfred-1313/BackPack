package manage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/backpack/backpack/internal/gostdirect"
	"github.com/backpack/backpack/internal/omni"
	"github.com/backpack/backpack/internal/tunnel/portmap"
)

type RoutedPeerForm struct {
	Transport string `json:"transport"`
	Name      string `json:"name"`
	PeerAddr  string `json:"peerAddr"`
	Port      string `json:"port"`
	Secret    string `json:"secret"`
	Ports     string `json:"ports"`
	AcceptUDP bool   `json:"acceptUdp"`
	Ingress   string `json:"ingress"`
	Target    string `json:"target"`
}

var routedSetupName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,31}$`)

func StartRoutedPeerSetup(ctx context.Context, form RoutedPeerForm) (SetupLinkInfo, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	form.Name = strings.TrimSpace(form.Name)
	form.PeerAddr = strings.TrimSpace(form.PeerAddr)
	form.Port = strings.TrimSpace(form.Port)
	if !routedSetupName.MatchString(form.Name) {
		return SetupLinkInfo{}, errors.New("use 1–32 letters, digits, underscores or hyphens for the tunnel name")
	}
	if form.Port != "" {
		if !validPort(form.Port) {
			return SetupLinkInfo{}, errors.New("enter a tunnel port from 1 to 65535")
		}
		port, _ := strconv.Atoi(form.Port)
		form.Port = strconv.Itoa(port)
	}
	if err := validateRoutedSecret(form.Secret); err != nil {
		return SetupLinkInfo{}, err
	}
	if form.PeerAddr != "" {
		resolve, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		peer, err := ctResolveHost(resolve, form.PeerAddr, "IPv4")
		if err != nil {
			return SetupLinkInfo{}, err
		}
		form.PeerAddr = peer
	}
	if form.Transport == ctGostDirect && form.Ports == "" && form.Ingress != "" {
		if !validPort(form.Ingress) || !validPort(form.Target) {
			return SetupLinkInfo{}, errors.New("enter ports from 1 to 65535")
		}
		form.Ports = form.Ingress + "=" + form.Target
	}
	ports, err := routedForwardPorts(form.Ports)
	if err != nil {
		return SetupLinkInfo{}, err
	}
	if form.Transport == ctGostDirect {
		if form.Port == "" || len(ports) != 1 || form.AcceptUDP {
			return SetupLinkInfo{}, errors.New("GOST Direct needs a tunnel port and one TCP forward")
		}
		return beginPeerJoin(ctx, form.Name, "direct", false, func(ctx context.Context, _, peer string) (peerJoinResult, error) {
			ingress, target, _ := strings.Cut(ports[0], ":")
			peer = orDefault(form.PeerAddr, peer)
			result, err := gostdirect.Install(ctx, gostdirect.Form{Action: "setup", Name: form.Name, Peer: peer,
				Port: form.Port, Ingress: ingress, Target: target, Secret: routedKey(form.Secret)})
			return peerJoinResult{Driver: "gost", Token: result.Token, Mode: form.Transport, Name: form.Name}, err
		})
	}
	for _, option := range omni.Options() {
		if option["value"] != form.Transport {
			continue
		}
		if form.Secret != "" && option["secret"] == "" {
			return SetupLinkInfo{}, errors.New("this carrier has no secret token")
		}
		if (len(ports) != 0 || form.AcceptUDP) && option["forward"] == "" {
			return SetupLinkInfo{}, errors.New("configure forwards in the routed tunnel's management screen")
		}
		if form.AcceptUDP && option["udp"] == "" {
			return SetupLinkInfo{}, errors.New("this carrier forwards TCP only")
		}
		return beginPeerJoin(ctx, form.Name, option["direction"], false, func(ctx context.Context, host, peer string) (peerJoinResult, error) {
			return setupRoutedOmni(ctx, form, host, orDefault(form.PeerAddr, peer), ports)
		})
	}
	return SetupLinkInfo{}, errors.New("unknown routed transport")
}

func validateRoutedSecret(secret string) error {
	if len(secret) > 512 || strings.ContainsAny(secret, "\x00\r\n") {
		return errors.New("enter a secret of at most 512 bytes on one line")
	}
	return nil
}

func routedKey(secret string) string {
	if secret == "" {
		return ""
	}
	if decoded, err := hex.DecodeString(secret); err == nil && len(decoded) == sha256.Size {
		return strings.ToLower(secret)
	}
	key := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(key[:])
}

// Routed forwards use the peer address established by the carrier. Convert
// BackPack's port and range syntax to the manager's public:target format.
func routedForwardPorts(raw string) ([]string, error) {
	if strings.ContainsAny(raw, ":|") {
		return nil, errors.New("use port numbers such as 443 or 2053=8443; this carrier forwards to its peer")
	}
	mappings, err := portmap.Expand(parsePorts(raw), "127.0.0.1")
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(mappings))
	seen := make(map[string]bool)
	for _, mapping := range mappings {
		_, local, _ := net.SplitHostPort(mapping.Listen)
		_, remote, _ := net.SplitHostPort(mapping.Targets[0])
		if seen[local] {
			return nil, fmt.Errorf("use port %s only once", local)
		}
		seen[local] = true
		out = append(out, local+":"+remote)
	}
	return out, nil
}

func setupRoutedOmni(ctx context.Context, form RoutedPeerForm, host, peer string, ports []string) (peerJoinResult, error) {
	if busy := busyForwardPorts(parsePorts(form.Ports), "127.0.0.1"); len(busy) != 0 {
		return peerJoinResult{}, fmt.Errorf("forwarded ports already in use on Iran: %s", strings.Join(busy, ", "))
	}
	args := []string{"setup", form.Transport, form.Name, host, peer}
	if form.Port != "" {
		args = append(args, form.Port)
	}
	key := routedKey(form.Secret)
	var out bytes.Buffer
	if err := omni.RunConfiguredSetup(ctx, args, key, &out, &out); err != nil {
		if out.Len() != 0 {
			err = errors.New(ctExtraError(errors.New(out.String())))
		}
		return peerJoinResult{}, err
	}
	rollback := func(err error) (peerJoinResult, error) {
		cleanup, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		defer cancel()
		_, cleanupErr := ctOmniRun(cleanup, "", "remove", form.Name)
		return peerJoinResult{}, errors.Join(err, cleanupErr)
	}
	token, err := ctOmniToken(out.String())
	if err != nil {
		return rollback(err)
	}
	fields, err := ctOmniTokenFields(token)
	if err != nil {
		return rollback(err)
	}
	if form.Port != "" && fields[4] != form.Port {
		return rollback(errors.New("the manager could not use the requested tunnel port"))
	}
	if key != "" && fields[5] != key {
		return rollback(errors.New("the OmniTunnel helper needs the BackPack update for a custom secret"))
	}
	result := peerJoinResult{Driver: "omni", Token: token, Mode: form.Transport, Name: form.Name}
	if len(ports) == 0 {
		return result, nil
	}
	if form.Transport == "reverse-mux" {
		output, err := ctOmniRun(ctx, "", "mux-setup-forwards", form.Name, strings.Join(ports, ","))
		if err != nil {
			return rollback(err)
		}
		result.MuxToken, err = ctOmniToken(output)
		if err != nil {
			return rollback(err)
		}
		if len(result.MuxToken) >= 32<<10 {
			return rollback(errors.New("these Reverse Mux forwards exceed the Setup command's capacity"))
		}
	} else {
		protocol := "tcp"
		if form.AcceptUDP {
			protocol = "both"
		}
		if _, err := ctOmniRun(ctx, "", "forward", form.Name, protocol, strings.Join(ports, ",")); err != nil {
			return rollback(err)
		}
	}
	return result, nil
}
