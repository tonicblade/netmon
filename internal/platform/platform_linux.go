//go:build linux

package platform

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"netmon/pkg/types"
)

func newCollector() Collector { return &linuxCollector{} }

type linuxCollector struct{}

// ---- interfaces ----

func (c *linuxCollector) Interfaces() ([]types.InterfaceInfo, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	out := make([]types.InterfaceInfo, 0, len(ifaces))
	for _, iface := range ifaces {
		info := types.InterfaceInfo{
			Name:   iface.Name,
			Index:  iface.Index,
			MAC:    iface.HardwareAddr.String(),
			IsUp:   iface.Flags&net.FlagUp != 0,
			IsLoop: iface.Flags&net.FlagLoopback != 0,
		}
		if info.IsLoop {
			info.Kind = "loop"
		}
		if mtu := readIntFile("/sys/class/net/" + iface.Name + "/mtu"); mtu > 0 {
			info.MTU = mtu
		}
		if speed := readIntFile("/sys/class/net/" + iface.Name + "/speed"); speed > 0 {
			info.Speed = uint64(speed) * 1_000_000
		}
		if info.Kind == "" {
			info.Kind = classifyLinuxKind(iface.Name, iface.Flags)
		}
		addrs, err := iface.Addrs()
		if err == nil {
			for _, a := range addrs {
				ipnet, ok := a.(*net.IPNet)
				if !ok {
					continue
				}
				ip := ipnet.IP.String()
				if ipnet.IP.To4() != nil {
					info.Addr4 = append(info.Addr4, ip)
				} else {
					info.Addr6 = append(info.Addr6, ip)
				}
			}
		}
		out = append(out, info)
	}
	return out, nil
}

func classifyLinuxKind(name string, flags net.Flags) string {
	if flags&net.FlagLoopback != 0 {
		return "loop"
	}
	switch {
	case strings.HasPrefix(name, "lo"), strings.HasPrefix(name, "docker"),
		strings.HasPrefix(name, "br-"), strings.HasPrefix(name, "veth"),
		strings.HasPrefix(name, "virbr"), strings.HasPrefix(name, "vmnet"),
		strings.HasPrefix(name, "vnet"):
		return "virt"
	case strings.HasPrefix(name, "tun"), strings.HasPrefix(name, "tap"),
		strings.HasPrefix(name, "wg"), strings.HasPrefix(name, "ppp"):
		return "tunnel"
	case strings.HasPrefix(name, "wl"), strings.HasPrefix(name, "wlan"),
		strings.HasPrefix(name, "wlp"):
		return "wifi"
	default:
		return "eth"
	}
}

// ---- counters: /proc/net/dev ----

func (c *linuxCollector) InterfaceStats() ([]types.InterfaceStats, error) {
	f, err := os.Open("/proc/net/dev")
	if err != nil {
		return nil, err
	}
	defer f.Close()

	now := time.Now()
	var out []types.InterfaceStats
	sc := bufio.NewScanner(f)
	// first two lines are headers
	for i := 0; sc.Scan(); i++ {
		if i < 2 {
			continue
		}
		line := sc.Text()
		colon := strings.IndexByte(line, ':')
		if colon < 0 {
			continue
		}
		name := strings.TrimSpace(line[:colon])
		fields := strings.Fields(line[colon+1:])
		if len(fields) < 16 {
			continue
		}
		u := func(idx int) uint64 {
			v, _ := strconv.ParseUint(fields[idx], 10, 64)
			return v
		}
		out = append(out, types.InterfaceStats{
			Name:      name,
			RxBytes:   u(0),
			RxPackets: u(1),
			RxErrors:  u(2),
			RxDrops:   u(3),
			TxBytes:   u(8),
			TxPackets: u(9),
			TxErrors:  u(10),
			TxDrops:   u(11),
			Timestamp: now,
		})
	}
	return out, sc.Err()
}

// ---- routes ----

func (c *linuxCollector) Routes() ([]types.Route, error) {
	var routes []types.Route

	// IPv4: /proc/net/route
	if f, err := os.Open("/proc/net/route"); err == nil {
		sc := bufio.NewScanner(f)
		first := true
		for sc.Scan() {
			if first {
				first = false
				continue
			}
			fields := strings.Fields(sc.Text())
			if len(fields) < 11 {
				continue
			}
			dest := hexIP(fields[1])
			gw := hexIP(fields[2])
			mask := hexIP(fields[7])
			metric, _ := strconv.Atoi(fields[6])
			routes = append(routes, types.Route{
				Destination: dest,
				Gateway:     gw,
				Genmask:     mask,
				Metric:      metric,
				Iface:       fields[0],
				Family:      "IPv4",
				Default:     dest == "0.0.0.0",
			})
		}
		f.Close()
	}

	// IPv6: /proc/net/ipv6_route
	if f, err := os.Open("/proc/net/ipv6_route"); err == nil {
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			fields := strings.Fields(sc.Text())
			if len(fields) < 10 {
				continue
			}
			dst, dstLen := hexV6(fields[0]), hexInt(fields[1])
			gw := hexV6(fields[4])
			metric := hexInt(fields[5])
			iface := fields[9]
			if dst == "::" && dstLen == 0 {
				routes = append(routes, types.Route{
					Destination: "::/0", Gateway: gw, Genmask: "::/0",
					Metric: metric, Iface: iface, Family: "IPv6", Default: true,
				})
				continue
			}
			routes = append(routes, types.Route{
				Destination: fmt.Sprintf("%s/%d", dst, dstLen),
				Gateway:     gw,
				Genmask:     fmt.Sprintf("/%d", dstLen),
				Metric:      metric,
				Iface:       iface,
				Family:      "IPv6",
			})
		}
		f.Close()
	}
	return routes, nil
}

// hexIP parses /proc/net/route's little-endian hex IPv4 into dotted form.
func hexIP(h string) string {
	v, err := strconv.ParseUint(h, 16, 32)
	if err != nil {
		return h
	}
	return fmt.Sprintf("%d.%d.%d.%d", v&0xff, (v>>8)&0xff, (v>>16)&0xff, (v>>24)&0xff)
}

func hexV6(h string) string {
	if len(h) != 32 {
		return h
	}
	var groups []string
	for i := 0; i < 32; i += 4 {
		groups = append(groups, h[i:i+4])
	}
	// /proc/net/ipv6_route is already in network order nibble groups
	ip := net.ParseIP(strings.Join(groups, ":"))
	if ip == nil {
		return h
	}
	return ip.String()
}

func hexInt(h string) int {
	v, _ := strconv.ParseUint(h, 16, 32)
	return int(v)
}

// ---- connections ----

func (c *linuxCollector) Connections() ([]types.Connection, error) {
	var conns []types.Connection
	for _, spec := range []struct {
		path  string
		proto string
		tcp   bool
	}{
		{"/proc/net/tcp", "TCP", true},
		{"/proc/net/tcp6", "TCP6", true},
		{"/proc/net/udp", "UDP", false},
		{"/proc/net/udp6", "UDP6", false},
	} {
		part, err := parseProcNet(spec.path, spec.proto, spec.tcp)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return conns, err
		}
		conns = append(conns, part...)
	}
	attachProcessInfo(conns)
	return conns, nil
}

func parseProcNet(path, proto string, tcp bool) ([]types.Connection, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var out []types.Connection
	sc := bufio.NewScanner(f)
	first := true
	for sc.Scan() {
		if first {
			first = false
			continue
		}
		fields := strings.Fields(sc.Text())
		if len(fields) < 10 {
			continue
		}
		local := decodeAddr(fields[1], strings.Contains(proto, "6"))
		remote := decodeAddr(fields[2], strings.Contains(proto, "6"))
		state := tcpState(fields[3], tcp)
		out = append(out, types.Connection{
			Proto:  proto,
			Local:  local,
			Remote: remote,
			State:  state,
		})
	}
	return out, sc.Err()
}

func decodeAddr(s string, v6 bool) string {
	parts := strings.Split(s, ":")
	if len(parts) != 2 {
		return s
	}
	ipHex, portHex := parts[0], parts[1]
	port, _ := strconv.ParseUint(portHex, 16, 16)

	if !v6 {
		v, err := strconv.ParseUint(ipHex, 16, 32)
		if err != nil {
			return s
		}
		return fmt.Sprintf("%d.%d.%d.%d:%d", v&0xff, (v>>8)&0xff, (v>>16)&0xff, (v>>24)&0xff, port)
	}
	b := make([]byte, 16)
	if len(ipHex) != 32 {
		return s
	}
	for i := 0; i < 16; i++ {
		byteVal, err := strconv.ParseUint(ipHex[i*2:i*2+2], 16, 8)
		if err != nil {
			return s
		}
		// kernel stores 32-bit words little-endian
		b[(i/4)*4+3-(i%4)] = byte(byteVal)
	}
	ip := net.IP(b)
	return fmt.Sprintf("[%s]:%d", ip.String(), port)
}

var tcpStates = map[string]string{
	"01": "ESTABLISHED",
	"02": "SYN_SENT",
	"03": "SYN_RECV",
	"04": "FIN_WAIT1",
	"05": "FIN_WAIT2",
	"06": "TIME_WAIT",
	"07": "CLOSE",
	"08": "CLOSE_WAIT",
	"09": "LAST_ACK",
	"0A": "LISTEN",
	"0B": "CLOSING",
}

func tcpState(hexState string, tcp bool) string {
	if !tcp {
		return ""
	}
	if s, ok := tcpStates[strings.ToUpper(hexState)]; ok {
		return s
	}
	return "UNKNOWN"
}

// attachProcessInfo maps sockets to owning processes via /proc/<pid>/fd.
func attachProcessInfo(conns []types.Connection) {
	pids, err := os.ReadDir("/proc")
	if err != nil {
		return
	}
	type sockRef struct {
		pid  int
		name string
	}
	sockToProc := make(map[string]sockRef)

	for _, de := range pids {
		if !de.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(de.Name())
		if err != nil {
			continue
		}
		fdDir := filepath.Join("/proc", de.Name(), "fd")
		fds, err := os.ReadDir(fdDir)
		if err != nil {
			continue
		}
		procName := readProcName(pid)
		for _, fd := range fds {
			link, err := os.Readlink(filepath.Join(fdDir, fd.Name()))
			if err != nil {
				continue
			}
			if strings.HasPrefix(link, "socket:[") {
				inode := strings.TrimSuffix(strings.TrimPrefix(link, "socket:["), "]")
				sockToProc[inode] = sockRef{pid: pid, name: procName}
			}
		}
	}

	// The connection list built earlier did not keep inodes, so re-read
	// /proc/net files including the inode column and match by address pair.
	inodeMap := make(map[string]sockRef)
	for inode, ref := range sockToProc {
		inodeMap[inode] = ref
	}

	// Re-derive: match by socket address pairs is unreliable; instead redo the
	// parse including inode for TCP only.
	for _, spec := range []struct {
		path  string
		proto string
	}{
		{"/proc/net/tcp", "TCP"},
		{"/proc/net/tcp6", "TCP6"},
		{"/proc/net/udp", "UDP"},
		{"/proc/net/udp6", "UDP6"},
	} {
		f, err := os.Open(spec.path)
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(f)
		first := true
		for sc.Scan() {
			if first {
				first = false
				continue
			}
			fields := strings.Fields(sc.Text())
			if len(fields) < 10 {
				continue
			}
			inode := fields[9]
			ref, ok := inodeMap[inode]
			if !ok {
				continue
			}
			local := decodeAddr(fields[1], strings.HasSuffix(spec.proto, "6"))
			remote := decodeAddr(fields[2], strings.HasSuffix(spec.proto, "6"))
			for i := range conns {
				if conns[i].Local == local && conns[i].Remote == remote && conns[i].Proto == spec.proto && conns[i].PID == 0 {
					conns[i].PID = ref.pid
					conns[i].Process = ref.name
					break
				}
			}
		}
		f.Close()
	}
}

func readProcName(pid int) string {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/comm", pid))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// ---- helpers ----

func readIntFile(path string) int {
	b, err := os.ReadFile(path)
	if err != nil {
		return -1
	}
	v, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		return -1
	}
	return v
}

func hostname() string {
	h, err := os.Hostname()
	if err != nil {
		return "unknown"
	}
	return h
}

func uptimeString() string {
	b, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return ""
	}
	fields := strings.Fields(string(b))
	if len(fields) == 0 {
		return ""
	}
	secs, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return ""
	}
	return formatUptime(time.Duration(secs) * time.Second)
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
