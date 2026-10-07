//go:build windows

package platform

import (
	"encoding/binary"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"netmon/pkg/types"
)

func newCollector() Collector { return &winCollector{} }

type winCollector struct{}

func (c *winCollector) Interfaces() ([]types.InterfaceInfo, error) {
	rows, err := ifTable()
	if err != nil {
		return nil, err
	}
	addrsByIndex := addrsByInterfaceIndex()

	out := make([]types.InterfaceInfo, 0, len(rows))
	for _, r := range rows {
		info := types.InterfaceInfo{
			Name:   windows.UTF16ToString(r.Alias[:]),
			Index:  int(r.InterfaceIndex),
			MTU:    int(r.Mtu),
			IsUp:   r.OperStatus == windows.IfOperStatusUp,
			IsLoop: r.InterfaceIndex == 1 || strings.HasPrefix(strings.ToLower(windows.UTF16ToString(r.Alias[:])), "loopback"),
		}
		if info.Name == "" {
			info.Name = fmt.Sprintf("if%d", r.InterfaceIndex)
		}
		if r.PhysicalAddressLength > 0 && int(r.PhysicalAddressLength) <= len(r.PhysicalAddress) {
			info.MAC = fmtMac(r.PhysicalAddress[:r.PhysicalAddressLength])
		}
		info.Speed = r.TransmitLinkSpeed
		if r.ReceiveLinkSpeed > info.Speed {
			info.Speed = r.ReceiveLinkSpeed
		}
		info.Kind = classifyWinKind(r.Type, info.Name)
		if a, ok := addrsByIndex[info.Index]; ok {
			info.Addr4, info.Addr6 = a.v4, a.v6
		}
		out = append(out, info)
	}
	return out, nil
}

func (c *winCollector) InterfaceStats() ([]types.InterfaceStats, error) {
	rows, err := ifTable()
	if err != nil {
		return nil, err
	}
	now := time.Now()
	out := make([]types.InterfaceStats, 0, len(rows))
	for _, r := range rows {
		name := windows.UTF16ToString(r.Alias[:])
		if name == "" {
			name = fmt.Sprintf("if%d", r.InterfaceIndex)
		}
		out = append(out, types.InterfaceStats{
			Name:      name,
			RxBytes:   r.InOctets,
			TxBytes:   r.OutOctets,
			RxPackets: r.InUcastPkts + r.InNUcastPkts,
			TxPackets: r.OutUcastPkts + r.OutNUcastPkts,
			RxErrors:  r.InErrors,
			TxErrors:  r.OutErrors,
			RxDrops:   r.InDiscards,
			TxDrops:   r.OutDiscards,
			Timestamp: now,
		})
	}
	return out, nil
}

func ifTable() ([]windows.MibIfRow2, error) {
	var tbl *windows.MibIfTable2
	if err := windows.GetIfTable2Ex(windows.MibIfTableNormal, &tbl); err != nil {
		return nil, err
	}
	defer windows.FreeMibTable(unsafe.Pointer(tbl))
	n := int(tbl.NumEntries)
	if n <= 0 {
		return nil, nil
	}
	rows := unsafe.Slice(&tbl.Table[0], n)
	out := make([]windows.MibIfRow2, n)
	copy(out, rows)
	return out, nil
}

type ifAddrs struct{ v4, v6 []string }

func addrsByInterfaceIndex() map[int]ifAddrs {
	out := map[int]ifAddrs{}
	ifaces, err := net.Interfaces()
	if err != nil {
		return out
	}
	for _, iface := range ifaces {
		a, err := iface.Addrs()
		if err != nil {
			continue
		}
		e := out[iface.Index]
		for _, addr := range a {
			ipnet, ok := addr.(*net.IPNet)
			if !ok {
				continue
			}
			if ipnet.IP.To4() != nil {
				e.v4 = append(e.v4, ipnet.IP.String())
			} else {
				e.v6 = append(e.v6, ipnet.IP.String())
			}
		}
		out[iface.Index] = e
	}
	return out
}

func classifyWinKind(ifType uint32, alias string) string {
	low := strings.ToLower(alias)
	switch {
	case strings.Contains(low, "loopback"):
		return "loop"
	case strings.Contains(low, "vethernet"), strings.Contains(low, "hyper-v"),
		strings.Contains(low, "vmware"), strings.Contains(low, "virtual"),
		strings.Contains(low, "docker"), strings.Contains(low, "wsl"):
		return "virt"
	case strings.Contains(low, "vpn"), strings.Contains(low, "wireguard"),
		strings.Contains(low, "tun"), strings.Contains(low, "ppp"):
		return "tunnel"
	}
	switch ifType {
	case 6:
		return "eth"
	case 71:
		return "wifi"
	case 24:
		return "loop"
	case 131, 134:
		return "tunnel"
	}
	return "other"
}

func fmtMac(b []byte) string {
	parts := make([]string, len(b))
	for i, v := range b {
		parts[i] = fmt.Sprintf("%02x", v)
	}
	return strings.Join(parts, ":")
}

func (c *winCollector) Routes() ([]types.Route, error) {
	nameByID := map[int]string{}
	if infos, err := c.Interfaces(); err == nil {
		for _, i := range infos {
			nameByID[i.Index] = i.Name
		}
	}

	var routes []types.Route
	for _, family := range []uint16{windows.AF_INET, windows.AF_INET6} {
		var tbl *windows.MibIpForwardTable2
		if err := windows.GetIpForwardTable2(family, &tbl); err != nil {
			continue
		}
		for _, r := range tbl.Rows() {
			dst := sockaddrInetString(r.DestinationPrefix.Prefix)
			gw := sockaddrInetString(r.NextHop)
			prefix := int(r.DestinationPrefix.PrefixLength)
			fam := "IPv4"
			if family == windows.AF_INET6 {
				fam = "IPv6"
			}
			mask := prefix
			_ = mask
			routes = append(routes, types.Route{
				Destination: formatDest(dst, prefix, fam),
				Gateway:     gw,
				Genmask:     formatMask(prefix, fam),
				Metric:      int(r.Metric),
				Iface:       nameByID[int(r.InterfaceIndex)],
				Family:      fam,
				Default:     isDefault(dst, prefix),
			})
		}
		windows.FreeMibTable(unsafe.Pointer(tbl))
	}
	return routes, nil
}

func isDefault(dst string, prefix int) bool {
	if prefix == 0 {
		return true
	}
	return dst == "0.0.0.0" || dst == "::"
}

func formatDest(dst string, prefix int, fam string) string {
	if prefix == 0 {
		if fam == "IPv6" {
			return "::/0"
		}
		return "0.0.0.0/0"
	}
	return fmt.Sprintf("%s/%d", dst, prefix)
}

func formatMask(prefix int, fam string) string {
	if fam == "IPv6" {
		return fmt.Sprintf("/%d", prefix)
	}
	if prefix == 0 || prefix > 32 {
		if prefix == 0 {
			return "0.0.0.0"
		}
		prefix = 32
	}
	var mask uint32 = 0xffffffff << (32 - prefix)
	return fmt.Sprintf("%d.%d.%d.%d", mask&0xff, (mask>>8)&0xff, (mask>>16)&0xff, (mask>>24)&0xff)
}

func sockaddrInetString(sa windows.RawSockaddrInet) string {
	switch sa.Family {
	case windows.AF_INET:
		v := binary.LittleEndian.Uint32(unsafe.Slice((*byte)(unsafe.Pointer(&sa.Data[0])), 4))
		return fmt.Sprintf("%d.%d.%d.%d", v&0xff, (v>>8)&0xff, (v>>16)&0xff, (v>>24)&0xff)
	case windows.AF_INET6:
		sa6 := (*windows.RawSockaddrInet6)(unsafe.Pointer(&sa))
		return net.IP(sa6.Addr[:]).String()
	}
	return ""
}

const (
	afInet              = 2
	afInet6             = 23
	tcpTableOwnerPidAll = 5
	udpTableOwnerPid    = 1

	procQueryLimitedInfo = 0x1000
)

var (
	iphlpapi           = syscall.NewLazyDLL("iphlpapi.dll")
	procGetExtTcpTable = iphlpapi.NewProc("GetExtendedTcpTable")
	procGetExtUdpTable = iphlpapi.NewProc("GetExtendedUdpTable")
	kernel32           = syscall.NewLazyDLL("kernel32.dll")
	procOpenProcess    = kernel32.NewProc("OpenProcess")
	procCloseHandle    = kernel32.NewProc("CloseHandle")
	procQueryImageName = kernel32.NewProc("QueryFullProcessImageNameW")
)

func getExtendedTable(proc *syscall.LazyProc, af uint32, class uint32) ([]byte, error) {
	var size uint32

	r, _, _ := proc.Call(0, uintptr(unsafe.Pointer(&size)), 1, uintptr(af), uintptr(class), 0)
	if size == 0 {
		return nil, fmt.Errorf("getextendedtable: size query failed (%d)", r)
	}
	buf := make([]byte, size)
	r, _, _ = proc.Call(uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)), 1, uintptr(af), uintptr(class), 0)
	if r != 0 {

		buf = make([]byte, size)
		r, _, _ = proc.Call(uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)), 1, uintptr(af), uintptr(class), 0)
		if r != 0 {
			return nil, fmt.Errorf("getextendedtable: %w", syscall.Errno(r))
		}
	}
	return buf[:size], nil
}

func (c *winCollector) Connections() ([]types.Connection, error) {
	var out []types.Connection

	for _, af := range []uint32{afInet, afInet6} {
		if buf, err := getExtendedTable(procGetExtTcpTable, af, tcpTableOwnerPidAll); err == nil {
			v6 := af == afInet6
			num := binary.LittleEndian.Uint32(buf)
			rowSize := 24
			if v6 {
				rowSize = 56
			}
			for i := uint32(0); i < num; i++ {
				off := 4 + int(i)*rowSize
				if off+rowSize > len(buf) {
					break
				}
				row := buf[off : off+rowSize]
				var state uint32
				var localAddr, remoteAddr string
				var localPort, remotePort uint32
				var pid uint32
				if v6 {
					localAddr = fmtV6(row[0:16])
					localPort = binary.LittleEndian.Uint32(row[20:24])
					remoteAddr = fmtV6(row[24:40])
					remotePort = binary.LittleEndian.Uint32(row[44:48])
					state = binary.LittleEndian.Uint32(row[48:52])
					pid = binary.LittleEndian.Uint32(row[52:56])
				} else {
					state = binary.LittleEndian.Uint32(row[0:4])
					localAddr = fmtV4(binary.LittleEndian.Uint32(row[4:8]))
					localPort = binary.LittleEndian.Uint32(row[8:12])
					remoteAddr = fmtV4(binary.LittleEndian.Uint32(row[12:16]))
					remotePort = binary.LittleEndian.Uint32(row[16:20])
					pid = binary.LittleEndian.Uint32(row[20:24])
				}
				proto := "TCP"
				if v6 {
					proto = "TCP6"
				}
				out = append(out, types.Connection{
					Proto:   proto,
					Local:   net.JoinHostPort(localAddr, strconv.Itoa(int(ntohs(localPort)))),
					Remote:  net.JoinHostPort(remoteAddr, strconv.Itoa(int(ntohs(remotePort)))),
					State:   tcpStateName(state),
					PID:     int(pid),
					Process: processName(int(pid)),
				})
			}
		}

		if buf, err := getExtendedTable(procGetExtUdpTable, af, udpTableOwnerPid); err == nil {
			v6 := af == afInet6
			num := binary.LittleEndian.Uint32(buf)
			rowSize := 12
			if v6 {
				rowSize = 28
			}
			for i := uint32(0); i < num; i++ {
				off := 4 + int(i)*rowSize
				if off+rowSize > len(buf) {
					break
				}
				row := buf[off : off+rowSize]
				var localAddr, localPort, pid uint32
				var addr string
				if v6 {
					addr = fmtV6(row[0:16])
					localPort = binary.LittleEndian.Uint32(row[20:24])
					pid = binary.LittleEndian.Uint32(row[24:28])
				} else {
					localAddr = binary.LittleEndian.Uint32(row[0:4])
					localPort = binary.LittleEndian.Uint32(row[4:8])
					pid = binary.LittleEndian.Uint32(row[8:12])
					addr = fmtV4(localAddr)
				}
				proto := "UDP"
				if v6 {
					proto = "UDP6"
				}
				out = append(out, types.Connection{
					Proto:   proto,
					Local:   net.JoinHostPort(addr, strconv.Itoa(int(ntohs(localPort)))),
					Remote:  "",
					State:   "",
					PID:     int(pid),
					Process: processName(int(pid)),
				})
			}
		}
	}
	return out, nil
}

func ntohs(v uint32) uint16 {
	b := v & 0xffff
	return uint16(((b >> 8) & 0xff) | ((b & 0xff) << 8))
}

func fmtV4(v uint32) string {
	return fmt.Sprintf("%d.%d.%d.%d", v&0xff, (v>>8)&0xff, (v>>16)&0xff, (v>>24)&0xff)
}

func fmtV6(b []byte) string {
	if len(b) < 16 {
		return ""
	}
	return net.IP(b).String()
}

var tcpStateNames = map[uint32]string{
	1: "CLOSED", 2: "LISTEN", 3: "SYN_SENT", 4: "SYN_RECV",
	5: "ESTABLISHED", 6: "FIN_WAIT1", 7: "FIN_WAIT2", 8: "CLOSE_WAIT",
	9: "CLOSING", 10: "LAST_ACK", 11: "TIME_WAIT", 12: "DELETE_TCB",
}

func tcpStateName(v uint32) string {
	if s, ok := tcpStateNames[v]; ok {
		return s
	}
	return "UNKNOWN"
}

var (
	procCacheMu sync.Mutex
	procCache   = map[int]string{}
)

func processName(pid int) string {
	if pid <= 0 {
		return ""
	}
	procCacheMu.Lock()
	if n, ok := procCache[pid]; ok {
		procCacheMu.Unlock()
		return n
	}
	procCacheMu.Unlock()

	name := ""
	h, _, _ := procOpenProcess.Call(procQueryLimitedInfo, 0, uintptr(pid))
	if h != 0 {
		buf := make([]uint16, 1024)
		size := uint32(len(buf))

		for _, dwFlags := range []uint32{1, 0} {
			r, _, _ := procQueryImageName.Call(h, uintptr(dwFlags),
				uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)))
			if r != 0 && size > 0 {
				name = filepath.Base(strings.ReplaceAll(windows.UTF16ToString(buf[:size]), `\`, `/`))
				break
			}
		}
		procCloseHandle.Call(h)
	}
	if name == "" {
		name = strconv.Itoa(pid)
	}
	procCacheMu.Lock()
	if len(procCache) > 4096 {
		procCache = map[int]string{}
	}
	procCache[pid] = name
	procCacheMu.Unlock()
	return name
}

func hostname() string {
	h, err := os.Hostname()
	if err != nil {
		return "windows"
	}
	return h
}

var procGetTickCount64 = kernel32.NewProc("GetTickCount64")

func uptimeString() string {
	ms, _, _ := procGetTickCount64.Call()
	return formatUptime(time.Duration(ms) * time.Millisecond)
}

func formatUptime(d time.Duration) string {
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	mins := int(d.Minutes()) % 60
	if days > 0 {
		return fmt.Sprintf("%dd %dh %dm", days, hours, mins)
	}
	if hours > 0 {
		return fmt.Sprintf("%dh %dm", hours, mins)
	}
	return fmt.Sprintf("%dm", mins)
}
