package mtproxy

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"syscall"
)

type PortCheck struct {
	Bind string `json:"bind"`
	Port int    `json:"port"`
}

type PortStatus struct {
	Port int  `json:"port"`
	Busy bool `json:"busy"`
}

// Check runs on the machine that will host the proxy. Port zero asks the
// kernel for a free TCP port on the exact bind address used for the proxy.
func (p PortCheck) Check() (PortStatus, error) {
	ip, err := netip.ParseAddr(p.Bind)
	if err != nil || ip.Zone() != "" || !(ip.IsLoopback() || ip.IsPrivate()) || p.Port < 0 || p.Port > 65535 {
		return PortStatus{}, errors.New("enter a valid private MTProxy bind address and port")
	}
	listener, err := net.Listen("tcp", net.JoinHostPort(p.Bind, strconv.Itoa(p.Port)))
	if errors.Is(err, syscall.EADDRINUSE) && p.Port != 0 {
		return PortStatus{Port: p.Port, Busy: true}, nil
	}
	if err != nil {
		return PortStatus{}, fmt.Errorf("cannot check the Kharej listener: %w", err)
	}
	defer listener.Close()
	return PortStatus{Port: listener.Addr().(*net.TCPAddr).Port}, nil
}

func (p PortCheck) ResultLine(s PortStatus) string {
	status := "FREE"
	if s.Busy {
		status = "BUSY"
	}
	return fmt.Sprintf("MTPORT %s %s", net.JoinHostPort(p.Bind, strconv.Itoa(s.Port)), status)
}

func (p PortCheck) ManualResult(result string) bool {
	return p.Port > 0 && strings.TrimSpace(result) == p.ResultLine(PortStatus{Port: p.Port})
}
