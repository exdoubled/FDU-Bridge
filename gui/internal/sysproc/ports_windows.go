package sysproc

import (
	"encoding/binary"
	"unsafe"

	"golang.org/x/sys/windows"
)

var procGetExtendedTcpTable = windows.NewLazySystemDLL("iphlpapi.dll").NewProc("GetExtendedTcpTable")

const tcpTableOwnerPIDListener = 3

func PortInUse(port int) bool { return portChecker()(port) }

// portChecker snapshots the IPv4 and IPv6 listener tables once. Binding a probe socket is not
// reliable here: Windows lets 127.0.0.1:p succeed while another process holds 0.0.0.0:p.
func portChecker() func(int) bool {
	seen := listeningPorts()
	return func(port int) bool { return seen[port] }
}

func listeningPorts() map[int]bool {
	seen := map[int]bool{}
	for _, family := range []struct {
		af       uint32
		rowBytes int
		portAt   int
	}{{windows.AF_INET, 24, 8}, {windows.AF_INET6, 56, 20}} {
		size := uint32(0)
		_, _, _ = procGetExtendedTcpTable.Call(0, uintptr(unsafe.Pointer(&size)), 0, uintptr(family.af), tcpTableOwnerPIDListener, 0)
		if size == 0 {
			continue
		}
		buffer := make([]byte, size+1024)
		size = uint32(len(buffer))
		r, _, _ := procGetExtendedTcpTable.Call(uintptr(unsafe.Pointer(&buffer[0])), uintptr(unsafe.Pointer(&size)), 0, uintptr(family.af), tcpTableOwnerPIDListener, 0)
		if r != 0 {
			continue
		}
		count := int(binary.LittleEndian.Uint32(buffer))
		for i := 0; i < count; i++ {
			offset := 4 + i*family.rowBytes + family.portAt
			if offset+2 > len(buffer) {
				break
			}
			// dwLocalPort keeps the port in network byte order in its low two bytes.
			seen[int(binary.BigEndian.Uint16(buffer[offset:]))] = true
		}
	}
	return seen
}
