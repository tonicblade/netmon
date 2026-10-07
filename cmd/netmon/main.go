// Command netmon is a network monitoring CLI and TUI.
//
//	netmon                          live TUI dashboard
//	netmon ping <host> [flags]      continuous latency probe
//	netmon trace <host> [flags]     MTR-style traceroute
//	netmon dns <name> [flags]       DNS lookup + resolver benchmark
//	netmon interfaces [--json]      network interface inventory
//	netmon connections [flags]      active sockets
//	netmon routes [--json]          routing table
//	netmon stats [--watch D]        live aggregate stats
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"sort"
	"strings"
	"time"

	"netmon/internal/collector"
	"netmon/internal/config"
	"netmon/internal/fmtutil"
	"netmon/internal/network"
	"netmon/internal/platform"
	"netmon/pkg/types"
	"netmon/tui"
)

const version = "0.1.0"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "netmon: "+err.Error())
		os.Exit(1)
	}
}

func run() error {
	cfgPath, args := splitConfigFlag(os.Args[1:])

	if len(args) > 0 && (args[0] == "--config" || args[0] == "-c") {
		if len(args) < 2 {
			return fmt.Errorf("--config requires a path")
		}
		cfgPath = args[1]
		args = args[2:]
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}

	if len(args) == 0 {
		return runTUI(cfg)
	}

	switch args[0] {
	case "help", "-h", "--help":
		printHelp()
		return nil
	case "version", "-v", "--version":
		fmt.Println("netmon " + version)
		return nil
	case "ping":
		return cmdPing(cfg, args[1:])
	case "trace":
		return cmdTrace(cfg, args[1:])
	case "dns":
		return cmdDNS(cfg, args[1:])
	case "interfaces":
		return cmdInterfaces(cfg, args[1:])
	case "connections":
		return cmdConnections(cfg, args[1:])
	case "routes":
		return cmdRoutes(cfg, args[1:])
	case "stats":
		return cmdStats(cfg, args[1:])
	default:
		return fmt.Errorf("unknown command %q\n%s\nuse 'netmon help'", args[0], shortHelp())
	}
}

func splitConfigFlag(args []string) (string, []string) {
	for i, a := range args {
		if a == "--config" || a == "-c" {
			if i+1 < len(args) {
				rest := append(append([]string{}, args[:i]...), args[i+2:]...)
				return args[i+1], rest
			}
		}
	}
	return "", args
}

// ---- TUI ----

func runTUI(cfg *config.Config) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	mon, err := collector.New(cfg)
	if err != nil {
		return err
	}
	mon.Start(ctx)
	defer mon.Stop()

	return tui.Run(ctx, mon, cfg)
}

// ---- shared helpers ----

func mainCtx() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt)
}

// flagBools lists flags that take no value but consume errors we accept.
var flagBools = map[string]bool{"json": true, "bench": true}

// parseInterspersed parses flags that may appear before or after positional
// arguments (Go's flag package stops at the first positional otherwise).
func parseInterspersed(fs *flag.FlagSet, args []string) error {
	var flags, pos []string
	i := 0
	for i < len(args) {
		a := args[i]
		if strings.HasPrefix(a, "-") && a != "-" {
			name := strings.TrimLeft(a, "-")
			if eq := strings.IndexByte(name, '='); eq >= 0 {
				name = name[:eq]
			}
			flags = append(flags, a)
			if !flagBools[name] && !strings.Contains(a, "=") {
				if i+1 >= len(args) {
					return fmt.Errorf("flag needs an argument: -%s", name)
				}
				flags = append(flags, args[i+1])
				i++
			}
			i++
			continue
		}
		pos = append(pos, a)
		i++
	}
	return fs.Parse(append(flags, pos...))
}

func printTable(headers []string, rows [][]string) {
	widths := make([]int, len(headers))
	for i, h := range headers {
		widths[i] = len(h)
	}
	for _, r := range rows {
		for i, c := range r {
			if i < len(widths) && len(c) > widths[i] {
				widths[i] = len(c)
			}
		}
	}
	var b strings.Builder
	writeRow := func(cells []string, header bool) {
		var line []string
		for i, c := range cells {
			w := 0
			if i < len(widths) {
				w = widths[i]
			}
			if i == len(cells)-1 {
				line = append(line, c)
			} else if header {
				line = append(line, c+strings.Repeat(" ", w-len(c)+2))
			} else {
				line = append(line, c+strings.Repeat(" ", w-len(c)+2))
			}
		}
		b.WriteString(strings.Join(line, "") + "\n")
	}
	writeRow(headers, true)
	b.WriteString(strings.Repeat("-", sum(widths)+len(widths)*2) + "\n")
	for _, r := range rows {
		writeRow(r, false)
	}
	fmt.Print(b.String())
}

func sum(v []int) int {
	s := 0
	for _, x := range v {
		s += x
	}
	return s
}

func printJSON(v interface{}) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// ---- ping ----

func cmdPing(cfg *config.Config, args []string) error {
	fs := flag.NewFlagSet("ping", flag.ContinueOnError)
	count := fs.Int("count", 0, "stop after N probes (default: run until interrupted)")
	interval := fs.Duration("interval", cfg.Refresh.Ping.D(), "time between probes")
	timeout := fs.Duration("timeout", cfg.Ping.Timeout.D(), "per-probe timeout")
	port := fs.Int("port", cfg.Ping.Port, "TCP fallback port (unprivileged ping)")
	asJSON := fs.Bool("json", false, "emit JSON lines")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: netmon ping <host> [--count N --interval D --timeout D --port P --json]")
	}
	if err := parseInterspersed(fs, args); err != nil {
		return err
	}
	target := fs.Arg(0)
	if target == "" {
		fs.Usage()
		return fmt.Errorf("missing target host")
	}

	ctx, cancel := mainCtx()
	defer cancel()

	type probe struct {
		seq int
		rtt float64
		ok  bool
		err string
		at  time.Time
	}
	var probes []probe
	seq := 0

	wrap := func(d time.Duration, e error) {
		seq++
		if *count > 0 && seq >= *count {
			cancel()
		}
		p := probe{seq: seq, at: time.Now(), rtt: float64(d.Microseconds()) / 1000.0, ok: e == nil}
		if e != nil {
			p.err = e.Error()
		}
		probes = append(probes, p)

		if *asJSON {
			rec := map[string]interface{}{
				"seq":    p.seq,
				"host":   target,
				"rtt_ms": p.rtt,
				"ok":     p.ok,
				"time":   p.at.Format(time.RFC3339),
			}
			if p.err != "" {
				rec["error"] = p.err
			}
			enc := json.NewEncoder(os.Stdout)
			_ = enc.Encode(rec)
			return
		}

		if p.ok {
			fmt.Printf("64 bytes from %s: icmp_seq=%d time=%.2f ms\n", target, p.seq, p.rtt)
		} else {
			fmt.Printf("Request timeout for %s seq=%d (%s)\n", target, p.seq, p.err)
		}
	}

	fmt.Printf("PING %s (interval %s, timeout %s)\n", target, *interval, *timeout)
	err := network.PingStream(ctx, target, *port, *interval, *timeout, wrap)
	if err != nil && err != context.Canceled {
		return err
	}

	// summary
	var rtts []float64
	lost := 0
	for _, p := range probes {
		if p.ok {
			rtts = append(rtts, p.rtt)
		} else {
			lost++
		}
	}
	sent := len(probes)
	lossPct := 0.0
	if sent > 0 {
		lossPct = float64(lost) / float64(sent) * 100
	}
	if *asJSON {
		sum := map[string]interface{}{
			"host": target, "sent": sent, "received": sent - lost, "lost": lost,
			"loss_pct": lossPct, "min_ms": types.Min(rtts), "avg_ms": types.Avg(rtts),
			"max_ms": types.Max(rtts), "jitter_ms": types.Jitter(rtts),
		}
		return printJSON(sum)
	}

	fmt.Printf("\n--- %s ping statistics ---\n", target)
	fmt.Printf("%d probes transmitted, %d received, %.1f%% packet loss\n",
		sent, sent-lost, lossPct)
	if len(rtts) > 0 {
		fmt.Printf("min/avg/max/jitter = %.2f/%.2f/%.2f/%.2f ms\n",
			types.Min(rtts), types.Avg(rtts), types.Max(rtts), types.Jitter(rtts))
	}
	return nil
}

// ---- trace ----

func cmdTrace(cfg *config.Config, args []string) error {
	fs := flag.NewFlagSet("trace", flag.ContinueOnError)
	maxHops := fs.Int("max-hops", 30, "max TTL to try")
	probes := fs.Int("probes", 3, "probes per hop")
	timeout := fs.Duration("timeout", time.Second, "per-probe timeout")
	intervalD := fs.Duration("interval", 200*time.Millisecond, "between probes")
	asJSON := fs.Bool("json", false, "emit JSON result")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: netmon trace <host> [--max-hops N --probes N --json]")
	}
	if err := parseInterspersed(fs, args); err != nil {
		return err
	}
	target := fs.Arg(0)
	if target == "" {
		fs.Usage()
		return fmt.Errorf("missing target host")
	}

	ctx, cancel := mainCtx()
	defer cancel()

	if !*asJSON {
		fmt.Printf("trace to %s (%d hops max, %d probes/hop)...\n", target, *maxHops, *probes)
	}

	opts := network.TraceOptions{
		Target:       target,
		MaxHops:      *maxHops,
		ProbesPerHop: *probes,
		Timeout:      *timeout,
		Interval:     *intervalD,
	}

	printed := map[int]bool{}
	var last []types.Hop
	var reached bool
	dataLines := 0
	printRow := func(h types.Hop) {
		host := h.Host
		if host == "" {
			host = "-"
		}
		addr := h.Addr
		if addr == "" {
			addr = "*"
		}
		fmt.Printf("%2d  %-34s %-20s loss %5.0f%%  last %-8s avg %-8s\n",
			h.Num, host, addr, h.LossPct, fmtutil.Ms(h.Last), fmtutil.Ms(h.Avg))
	}
	_, err := network.Trace(ctx, opts, func(hs []types.Hop, r bool) {
		last = hs
		reached = r
		if *asJSON {
			return
		}
		for _, h := range hs {
			if h.Sent == 0 || printed[h.Num] {
				continue
			}
			printed[h.Num] = true
			dataLines++
			printRow(h)
		}
	})
	if err != nil && err != context.Canceled {
		return err
	}

	if !*asJSON && dataLines > 0 {
		// redraw the whole table so reverse-DNS hostnames from the final
		// update are visible (mtr-style in-place refresh).
		for i := 0; i < dataLines; i++ {
			fmt.Print("\x1b[1A\x1b[2K")
		}
		for _, h := range last {
			printRow(h)
		}
	}

	if *asJSON {
		return printJSON(map[string]interface{}{
			"target": target, "hops": last, "reached": reached,
		})
	}
	return nil
}

// ---- dns ----

func cmdDNS(cfg *config.Config, args []string) error {
	fs := flag.NewFlagSet("dns", flag.ContinueOnError)
	qtype := fs.String("type", "A", "record type: A AAAA CNAME MX TXT NS")
	server := fs.String("server", "", "resolver IP (default: OS resolver)")
	timeout := fs.Duration("timeout", 3*time.Second, "resolver timeout")
	bench := fs.Bool("bench", false, "benchmark configured resolvers")
	asJSON := fs.Bool("json", false, "emit JSON result")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: netmon dns <name> [--type T --server IP --bench --timeout D --json]")
	}
	if err := parseInterspersed(fs, args); err != nil {
		return err
	}
	name := fs.Arg(0)
	if name == "" {
		fs.Usage()
		return fmt.Errorf("missing domain name")
	}

	ctx, cancel := mainCtx()
	defer cancel()

	res := network.Query(ctx, network.DNSQuery{
		Name: name, Type: *qtype, Server: *server, Timeout: *timeout,
	})

	if *asJSON {
		return printJSON(res)
	}

	if res.Err != "" {
		return fmt.Errorf("lookup failed: %s", res.Err)
	}
	fmt.Printf("%s %s -> %s  in %.1f ms\n", name, res.Type, res.Server, res.RTT)
	printTable([]string{"TTL", "TYPE", "VALUE"}, func() [][]string {
		var rows [][]string
		for _, r := range res.Records {
			rows = append(rows, []string{fmt.Sprintf("%d", r.TTL), r.Type, r.Value})
		}
		return rows
	}())

	if *bench {
		fmt.Println("\nbenchmarking resolvers...")
		rows := network.Benchmark(ctx, name, cfg.DNS.Resolvers, *timeout)
		tbl := make([][]string, 0, len(rows))
		for _, r := range rows {
			ok := "ok"
			if !r.OK {
				ok = "error: " + r.Error
			}
			tbl = append(tbl, []string{r.Server, fmtutil.Ms(r.RTT), ok})
		}
		printTable([]string{"RESOLVER", "RTT", "STATUS"}, tbl)
	}
	return nil
}

// ---- interfaces ----

func cmdInterfaces(cfg *config.Config, args []string) error {
	fs := flag.NewFlagSet("interfaces", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "emit JSON")
	if err := parseInterspersed(fs, args); err != nil {
		return err
	}
	plat := platform.New()
	infos, err := plat.Interfaces()
	if err != nil {
		return err
	}
	stats, err := plat.InterfaceStats()
	if err != nil {
		return err
	}
	byName := map[string]types.InterfaceStats{}
	for _, s := range stats {
		byName[s.Name] = s
	}
	if *asJSON {
		return printJSON(map[string]interface{}{"interfaces": infos, "stats": stats})
	}
	rows := make([][]string, 0, len(infos))
	for _, inf := range infos {
		up := "down"
		if inf.IsUp {
			up = "up"
		}
		rs := byName[inf.Name]
		rows = append(rows, []string{
			inf.Name, inf.Kind, up,
			fmt.Sprintf("%d", inf.MTU),
			fmtutil.Bytes(float64(rs.RxBytes)), fmtutil.Bytes(float64(rs.TxBytes)),
			inf.MAC, strings.Join(append(inf.Addr4, inf.Addr6...), ", "),
		})
	}
	printTable([]string{"NAME", "KIND", "STATE", "MTU", "RX TOTAL", "TX TOTAL", "MAC", "ADDRESSES"}, rows)
	return nil
}

// ---- connections ----

func cmdConnections(cfg *config.Config, args []string) error {
	fs := flag.NewFlagSet("connections", flag.ContinueOnError)
	proto := fs.String("proto", "", "filter by protocol: TCP TCP6 UDP UDP6")
	state := fs.String("state", "", "filter by state (e.g. ESTABLISHED)")
	process := fs.String("process", "", "filter by process name substring")
	sortKey := fs.String("sort", "proto", "sort: proto state process remote port")
	asJSON := fs.Bool("json", false, "emit JSON")
	if err := parseInterspersed(fs, args); err != nil {
		return err
	}
	plat := platform.New()
	conns, err := plat.Connections()
	if err != nil {
		return err
	}

	filtered := make([]types.Connection, 0, len(conns))
	for _, c := range conns {
		if *proto != "" && !strings.EqualFold(c.Proto, *proto) {
			continue
		}
		if *state != "" && !strings.EqualFold(c.State, *state) {
			continue
		}
		if *process != "" && !strings.Contains(strings.ToLower(c.Process), strings.ToLower(*process)) {
			continue
		}
		filtered = append(filtered, c)
	}
	collector.SortConnections(filtered, *sortKey, false)

	if *asJSON {
		return printJSON(filtered)
	}
	rows := make([][]string, 0, len(filtered))
	for _, c := range filtered {
		proc := c.Process
		if proc == "" {
			proc = "-"
		}
		rows = append(rows, []string{c.Proto, c.State, c.Local, c.Remote,
			fmt.Sprintf("%d", c.PID), proc})
	}
	printTable([]string{"PROTO", "STATE", "LOCAL", "REMOTE", "PID", "PROCESS"}, rows)
	fmt.Printf("%d connections\n", len(filtered))
	return nil
}

// ---- routes ----

func cmdRoutes(cfg *config.Config, args []string) error {
	fs := flag.NewFlagSet("routes", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "emit JSON")
	if err := parseInterspersed(fs, args); err != nil {
		return err
	}
	plat := platform.New()
	routes, err := plat.Routes()
	if err != nil {
		return err
	}
	if *asJSON {
		return printJSON(routes)
	}
	sort.Slice(routes, func(i, j int) bool {
		if routes[i].Default != routes[j].Default {
			return routes[i].Default
		}
		return routes[i].Destination < routes[j].Destination
	})
	rows := make([][]string, 0, len(routes))
	for _, r := range routes {
		def := ""
		if r.Default {
			def = "*"
		}
		rows = append(rows, []string{
			r.Family, r.Destination, r.Genmask, r.Gateway,
			fmt.Sprintf("%d", r.Metric), r.Iface, r.Flags, def,
		})
	}
	printTable([]string{"FAMILY", "DESTINATION", "GENMASK", "GATEWAY", "METRIC", "IFACE", "FLAGS", "DEFAULT"}, rows)
	return nil
}

// ---- stats ----

func cmdStats(cfg *config.Config, args []string) error {
	fs := flag.NewFlagSet("stats", flag.ContinueOnError)
	watch := fs.Duration("watch", 0, "refresh every interval until interrupted (e.g. 2s)")
	asJSON := fs.Bool("json", false, "emit JSON")
	if err := parseInterspersed(fs, args); err != nil {
		return err
	}
	ctx, cancel := mainCtx()
	defer cancel()

	mon, err := collector.New(cfg)
	if err != nil {
		return err
	}
	mon.Start(ctx)
	defer mon.Stop()

	emit := func() {
		snap := mon.Snapshot()
		if *asJSON {
			enc := json.NewEncoder(os.Stdout)
			_ = enc.Encode(map[string]interface{}{
				"time":   snap.Generated.Format(time.RFC3339),
				"rx_bps": snap.TotalRx, "tx_bps": snap.TotalTx,
				"interfaces":  len(snap.Interfaces),
				"connections": len(snap.Conns),
				"routes":      len(snap.Routes),
				"health":      snap.Health,
			})
			return
		}
		var lat string
		if len(snap.Pings) > 0 {
			var avg, worst float64
			for _, st := range snap.Pings {
				avg += st.Avg
				if st.LossPct > worst {
					worst = st.LossPct
				}
			}
			avg /= float64(len(snap.Pings))
			lat = fmt.Sprintf("%s avg, %.0f%% worst loss", fmtutil.Ms(avg), worst)
		} else {
			lat = "waiting..."
		}
		fmt.Printf("%s  rx %s  tx %s  ifaces %d  conns %d  nqi %d  lat %s\n",
			snap.Generated.Format("15:04:05"),
			fmtutil.Bps(snap.TotalRx), fmtutil.Bps(snap.TotalTx),
			len(snap.Interfaces), len(snap.Conns), snap.Health.Score, lat)
	}

	if *watch <= 0 {
		time.Sleep(2 * time.Second) // let collectors get one sample
		emit()
		return nil
	}

	t := time.NewTicker(*watch)
	defer t.Stop()
	emit()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			emit()
		}
	}
}

// ---- help ----

func shortHelp() string {
	return `usage:
  netmon                       run the interactive TUI dashboard
  netmon ping <host> [flags]   continuous latency probe
  netmon trace <host> [flags]  MTR-style traceroute
  netmon dns <name> [flags]    DNS lookup + resolver benchmark
  netmon interfaces [--json]   network interface inventory
  netmon connections [flags]   active sockets
  netmon routes [--json]       routing table
  netmon stats [--watch D]     live aggregate stats
  netmon help                  this help
  netmon version               print version`
}

func printHelp() {
	fmt.Println(`netmon ` + version + ` — network monitoring TUI + CLI

USAGE
  netmon                            interactive dashboard (TUI)
  netmon ping <host> [--count N] [--interval D] [--timeout D] [--port P] [--json]
  netmon trace <host> [--max-hops N] [--probes N] [--hops D] [--json]
  netmon dns <name> [--type T] [--server IP] [--bench] [--timeout D] [--json]
  netmon interfaces [--json]
  netmon connections [--proto P] [--state S] [--process X] [--sort K] [--json]
  netmon routes [--json]
  netmon stats [--watch D] [--json]
  netmon help | version

GLOBAL
  --config path  YAML config file (default: netmon.yaml, ~/.config/netmon/config.yaml)

TUI KEYS
  1-8 tabs | j/k move | g/G top/bottom | / filter | h/l graph range
  x graph mode | p pause | enter/q query/trace | r re-run | ? help | q quit

PING METHODS (auto)
  1 raw ICMP    2 unprivileged ICMP (dgram)    3 TCP connect timing

NOTES
  traceroute needs raw ICMP — run elevated/admin on Windows if it fails.
  ` + shortHelp())
}
