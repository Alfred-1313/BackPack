//go:build linux

package matrix

import (
	"net"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

func Dialer(name string) *net.Dialer {
	iface := Iface(name)
	return &net.Dialer{Timeout: 10 * time.Second, Control: func(_, _ string, conn syscall.RawConn) error {
		var bind error
		if err := conn.Control(func(fd uintptr) { bind = unix.BindToDevice(int(fd), iface) }); err != nil {
			return err
		}
		return bind
	}}
}
