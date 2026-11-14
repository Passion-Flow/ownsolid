package egress

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

// Linux TUN interface constants from <linux/if_tun.h>.
const (
	tunSetInterface = 0x400454ca // TUNSETIFF
	tunModeTUN      = 0x0001     // IFF_TUN: layer 3 packets
	tunNoInfo       = 0x1000     // IFF_NO_PI: no packet information prefix
	interfaceNameSz = 16         // IFNAMSIZ
)

// interfaceRequest is struct ifreq with the flags member of its union.
type interfaceRequest struct {
	name  [interfaceNameSz]byte
	flags uint16
	_     [22]byte
}

// OpenTUN creates the TUN interface name and returns its device. The interface exists
// while the device is open; the caller brings it up and routes traffic to it.
func OpenTUN(name string) (*os.File, error) {
	if len(name) == 0 || len(name) >= interfaceNameSz {
		return nil, fmt.Errorf("interface name %q must be 1 to %d bytes", name, interfaceNameSz-1)
	}
	// The interface must be attached before Go's poller sees the descriptor: polling an
	// unattached TUN descriptor never registers a wakeup, so reads would hang forever.
	fd, err := syscall.Open("/dev/net/tun", syscall.O_RDWR|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("open /dev/net/tun: %w", err)
	}
	var request interfaceRequest
	copy(request.name[:], name)
	request.flags = tunModeTUN | tunNoInfo
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), tunSetInterface, uintptr(unsafe.Pointer(&request))); errno != 0 {
		syscall.Close(fd)
		return nil, fmt.Errorf("create interface %s: %w", name, errno)
	}
	if err = syscall.SetNonblock(fd, true); err != nil {
		syscall.Close(fd)
		return nil, fmt.Errorf("set interface %s non-blocking: %w", name, err)
	}
	// A non-blocking descriptor joins the poller, so Close interrupts a pending Read.
	return os.NewFile(uintptr(fd), "/dev/net/tun"), nil
}
